package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedCoreOperations(t *testing.T) {
	// Huge repetition must fail before allocation, with the same boundary
	// whether written as an operator or a standard library call.
	for _, args := range [][]Value{{"x", 1e12}, {1e12, "x"}} {
		if _, err := Binary("*", args[0], args[1]); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatal(err)
		}
	}
	if _, err := Binary("+", strings.Repeat("x", MaxStringBytes), "x"); err == nil {
		t.Fatal("unbounded concatenation")
	}
	invalid := string([]byte{255})
	for _, check := range []func() error{
		func() error { return CheckType(invalid, "string") },
		func() error { _, err := Length(invalid); return err },
		func() error { _, err := Index(invalid, 0); return err },
		func() error { _, err := Binary("+", invalid, ""); return err },
		func() error { _, err := Binary("*", invalid, 0); return err },
	} {
		if err := check(); err == nil {
			t.Fatal("accepted invalid UTF-8")
		}
	}
	items := make([]Value, MaxListLength+1)
	called := false
	if _, err := Fold(context.Background(), items, 0, func(context.Context, []Value) Result { called = true; return Ready(0) }); err == nil || called {
		t.Fatalf("fold: called=%v err=%v", called, err)
	}
	result := evaluateMap(context.Background(), &MapSpec{Source: "items", Eval: func(context.Context, Value, map[string]Value) Result { called = true; return Ready(0) }}, map[string]Value{"items": items}, 2)
	if result.Err == nil || called {
		t.Fatalf("Map: %v", result)
	}
}

func TestPythonFailedConversionReclaimsHandles(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	module := "def bad():\n    return [object(), float('nan')]\n"
	if err := os.WriteFile(filepath.Join(dir, "conversion_probe.py"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3", Dir: dir, MaxPythonHandles: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	for attempt := 0; attempt < 3; attempt++ {
		result := worker.Call(context.Background(), "conversion_probe.bad", nil)
		if result.Err == nil || !strings.Contains(result.Err.Error(), "finite") {
			t.Fatal(result)
		}
	}
	result := worker.Call(context.Background(), "fractions.Fraction", []Value{1, 3})
	if result.Err != nil {
		t.Fatalf("failed conversion leaked handle quota: %v", result.Err)
	}
	if result := worker.Call(context.Background(), "python.release", []Value{result.Value}); result.Err != nil {
		t.Fatal(result.Err)
	}
}

func TestPythonDataBoundaryIsLossless(t *testing.T) {
	worker := newTestPythonWorker(t)
	ctx := context.Background()
	for _, name := range []string{"nan", "inf", "-inf"} {
		if result := worker.Call(ctx, "builtins.float", []Value{name}); result.Err == nil || !strings.Contains(result.Err.Error(), "finite") {
			t.Fatalf("%s: %+v", name, result)
		}
	}
	result := worker.Call(ctx, "builtins.set", []Value{[]Value{3, 1, 2}})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	handle, ok := result.Value.(map[string]any)
	if !ok || handle["$python_handle"] == nil {
		t.Fatalf("unordered set became list: %v", result)
	}
	if result := worker.Call(ctx, "python.to_json", []Value{handle}); result.Err == nil || !strings.Contains(result.Err.Error(), "order") {
		t.Fatal(result)
	}
	ordered := worker.Call(ctx, "builtins.sorted", []Value{handle})
	if ordered.Err != nil {
		t.Fatal(ordered.Err)
	}
	text, _ := FormatValue(ordered.Value)
	if text != "[1,2,3]" {
		t.Fatal(text)
	}
	if result := worker.Call(ctx, "python.release", []Value{handle}); result.Err != nil {
		t.Fatal(result.Err)
	}
	// Both keys would stringify to "1"; silently overwriting one loses data.
	result = worker.Call(ctx, "builtins.dict", []Value{[]Value{[]Value{1, "number"}, []Value{"1", "text"}}})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "collide") {
		t.Fatal(result)
	}
	if result := worker.Call(ctx, "math.sqrt", []Value{9}); result.Err != nil || result.Value != float64(3) {
		t.Fatalf("worker failed after rejected data: %+v", result)
	}
}
