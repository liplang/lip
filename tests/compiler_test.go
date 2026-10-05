package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"lipalpha/compiler"
	"lipalpha/runtime"
)

func TestHelloGraph(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request: string) {
        greeting = "Hello, " + request
        return greeting
    }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(graph.Nodes))
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) == 0 {
		t.Fatal("empty generated code")
	}
}

func TestEffectCallStatement(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Log(value: string) {
        print(value)
        return value
    }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 || graph.Nodes[0].Expr == nil {
		t.Fatalf("nodes = %#v, want effect and return nodes", graph.Nodes)
	}
	if _, err := compiler.ParseAndBuild(`flow Bad() { 1 + 2; return 0 }`); err == nil {
		t.Fatal("expected arbitrary unused expression to be rejected")
	}
}

func TestFlowReturn(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request) {
        greeting = "Hello, " + request
        return greeting
    }`)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Nodes[len(graph.Nodes)-1].Output {
		t.Fatal("flow return did not create an output node")
	}
}

func TestGatedGraph(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Gated(input: number) {
        valid = input > 0
        when valid {
            doubled = input * 2
            return doubled
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) < 100 {
		t.Fatal("generated code unexpectedly short")
	}

	// Exercise the same runtime semantics directly with a tiny graph.
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "valid", Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result { return runtime.Ready(false) }})
	rt.Add(runtime.NodeSpec{Name: "sink", Deps: []string{"valid"}, Gates: []string{"valid"}, Gate: func(values map[string]runtime.Value) (bool, error) { return runtime.Bool(values["valid"]) }, Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready("should not run")
	}})
	value, trace, err := rt.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		t.Fatalf("value = %v, want nil", value)
	}
	if len(trace) != 3 || trace[2].Status != runtime.Skipped {
		t.Fatalf("trace = %#v", trace)
	}
}

func TestRejectsForwardReference(t *testing.T) {
	_, err := compiler.ParseAndBuild(`flow Bad() { b = a; a = 1; return b }`)
	if err == nil {
		t.Fatal("expected forward-reference error")
	}
}

func TestSmallLanguageCore(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`fn twice(x: number) { return x * 2 }
        flow Language(input: any) {
            values = [1, 2, 3]
            selected = values[1]
            result = twice(selected)
            return result
        }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Functions) != 1 || len(graph.Nodes) != 4 {
		t.Fatalf("functions=%d nodes=%d", len(graph.Functions), len(graph.Nodes))
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "host.RegisterPure(\"twice\"") {
		t.Fatal("generated code did not register local function")
	}
}

func TestBoundedParallelRuntime(t *testing.T) {
	rt := runtime.NewGraph()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	worker := func(value int) func(context.Context, map[string]runtime.Value) runtime.Result {
		return func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
			started <- struct{}{}
			<-release
			return runtime.Ready(value)
		}
	}
	rt.Add(runtime.NodeSpec{Name: "a", Pure: true, Eval: worker(1)})
	rt.Add(runtime.NodeSpec{Name: "b", Pure: true, Eval: worker(2)})
	rt.Add(runtime.NodeSpec{Name: "sum", Pure: true, Deps: []string{"a", "b"}, Output: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		value, err := runtime.Binary("+", values["a"], values["b"])
		if err != nil {
			return runtime.Failed(err)
		}
		return runtime.Ready(value)
	}})
	resultCh := make(chan struct {
		value runtime.Value
		err   error
	}, 1)
	go func() {
		value, _, err := rt.RunParallel(context.Background(), runtime.NewHost(), nil, 2)
		resultCh <- struct {
			value runtime.Value
			err   error
		}{value, err}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("parallel nodes did not start")
		}
	}
	close(release)
	result := <-resultCh
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.value != float64(3) {
		t.Fatalf("result = %v, want 3", result.value)
	}
}

func TestAwaitResultIsPropagated(t *testing.T) {
	rt := runtime.NewGraph()
	result := make(chan runtime.Result, 1)
	rt.Add(runtime.NodeSpec{Name: "async", Pure: true, Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Await(result)
	}})
	go func() {
		time.Sleep(5 * time.Millisecond)
		result <- runtime.Ready("done")
	}()
	value, _, err := rt.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if value != "done" {
		t.Fatalf("value = %v, want done", value)
	}
}

func TestParallelPreservesSourceOrderForOutputs(t *testing.T) {
	rt := runtime.NewGraph()
	started := make(chan struct{})
	release := make(chan struct{})
	rt.Add(runtime.NodeSpec{Name: "first", Pure: true, Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		close(started)
		<-release
		return runtime.Ready("first")
	}})
	rt.Add(runtime.NodeSpec{Name: "second", Pure: true, Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready("second")
	}})
	resultCh := make(chan runtime.Value, 1)
	go func() {
		value, _, err := rt.RunParallel(context.Background(), runtime.NewHost(), nil, 2)
		if err != nil {
			t.Errorf("parallel run: %v", err)
		}
		resultCh <- value
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first output did not start")
	}
	close(release)
	if value := <-resultCh; value != "second" {
		t.Fatalf("value = %v, want source-order last output", value)
	}
}

func TestOperatorsRejectKnownMixedTypes(t *testing.T) {
	if _, err := compiler.ParseAndBuild(`flow Bad() { value = 1 + "x"; return value }`); err == nil {
		t.Fatal("expected compile-time mixed-type operator error")
	}
	if _, err := compiler.ParseAndBuild(`flow Bad() { value = 1 == "1"; return value }`); err == nil {
		t.Fatal("expected compile-time incompatible equality error")
	}
	if _, err := runtime.Binary("+", "x", 1); err == nil {
		t.Fatal("expected runtime mixed-type operator error")
	}
	if value, err := runtime.Binary("*", "ha", 3); err != nil || value != "hahaha" {
		t.Fatalf("string repetition = %v, %v", value, err)
	}
	if _, err := runtime.Binary("*", "ha", -1); err == nil {
		t.Fatal("expected negative string repetition error")
	}
	if _, err := compiler.ParseAndBuild(`fn twice(x: number) { return x * 2 }
        flow Bad() { value = twice("x"); return value }`); err == nil {
		t.Fatal("expected local function argument type error")
	}
}

func TestIndexRequiresAnInteger(t *testing.T) {
	if _, err := runtime.Index([]runtime.Value{"a", "b"}, 1.5); err == nil {
		t.Fatal("expected a non-integer index to be rejected")
	}
	value, err := runtime.Index([]runtime.Value{"a", "b"}, 1.0)
	if err != nil || value != "b" {
		t.Fatalf("integer-valued index = %v, %v; want b, nil", value, err)
	}
}

func TestFunctionTypesAreOrderIndependent(t *testing.T) {
	_, err := compiler.ParseAndBuild(`fn caller(x: number) { return later(x) }
		fn later(x: number) { return x * 2 }
		flow Bad() {
			value = caller(1)
			return value + "!"
		}`)
	if err == nil {
		t.Fatal("expected the return type of a later function to be checked")
	}
}

func TestGeneratedNodeNamesAreReserved(t *testing.T) {
	if _, err := compiler.ParseAndBuild(`flow Bad() {
		__return_0 = 1
		return __return_0
	}`); err == nil {
		t.Fatal("expected generated-node name collision to be rejected")
	}
}

func TestTypedFlowInputIsChecked(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request: string) { return "Hello, " + request }`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, `runtime.CheckType(inputs["request"], "string")`) {
		t.Fatal("generated flow does not check annotated input")
	}
}

func TestGeneratedNestedExpressionErrorsReturnResults(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Fields(input: any) {
        value = input.missing
        result = [value, 1 + 2]
        return result[0]
    }`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "mustField") || strings.Contains(code, "mustIndex") || strings.Contains(code, "panic(") {
		t.Fatal("generated expression code still contains panic-based helpers")
	}
	if !strings.Contains(code, "runtime.Field") || !strings.Contains(code, "runtime.Index") {
		t.Fatal("generated expression code does not propagate field/index operations")
	}
}
