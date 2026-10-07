package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lipalpha/examples/tutorial/mixedflow"
	"lipalpha/runtime"
)

func TestMixedLibraryComposition(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	ctx := context.Background()
	worker, err := runtime.NewPythonWorker(ctx, runtime.PythonWorkerConfig{Python: "python3"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	host := runtime.NewPythonHost(worker)
	host.RegisterReadOnly("load_text", loadText)
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("1 4 9\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]runtime.Value{"path": path}
	for _, run := range []func(context.Context, runtime.Host, map[string]runtime.Value) (runtime.Value, []runtime.TraceEvent, error){mixedflow.Run, mixedflow.RunSequential, func(c context.Context, h runtime.Host, v map[string]runtime.Value) (runtime.Value, []runtime.TraceEvent, error) {
		return mixedflow.RunParallel(c, h, v, 2)
	}} {
		value, _, err := run(ctx, host, inputs)
		if err != nil {
			t.Fatal(err)
		}
		text, _ := runtime.FormatValue(value)
		if text != `{"label":"1, 2, 3","roots":[1,2,3],"total":6}` {
			t.Fatal(text)
		}
	}
	for _, tc := range []struct{ input, want string }{{"", `"roots":[]`}, {"1 nope", "string.parse_number"}, {"1 -4", "math domain error"}} {
		if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		value, _, err := mixedflow.Run(ctx, host, inputs)
		if tc.input == "" {
			text, _ := runtime.FormatValue(value)
			if err != nil || !strings.Contains(text, tc.want) {
				t.Fatalf("%s %v", text, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatal(err)
		}
	}
}
