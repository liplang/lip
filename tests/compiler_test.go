package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lipalpha/compiler"
	"lipalpha/runtime"
)

func TestHelloGraph(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request: string) -> any {
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

func TestGeneratedMainUsesOnlyDeclaredInputs(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request: string) -> any {
		greeting = "Hello, " + request
		return greeting
	}`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sampleInput", "LIP_INPUT", `runtime.Value("World")`, "float64(1)"} {
		if strings.Contains(code, forbidden) {
			t.Fatalf("generated main contains implicit input %q", forbidden)
		}
	}
	for _, required := range []string{"parseCLIInputs", `usage: %s <request:string>`, "os.Args[1:]"} {
		if !strings.Contains(code, required) {
			t.Fatalf("generated main is missing strict input parser fragment %q", required)
		}
	}
}

func TestDependencyDeclarationsArePreserved(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`require python "numpy>=1.26"
		require go "github.com/acme/adapter"
		require host "load_profile"
		flow Scientific(values: any) -> any { return values }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Dependencies) != 3 || graph.Dependencies[0].Kind != "python" || graph.Dependencies[0].Spec != "numpy>=1.26" {
		t.Fatalf("dependencies = %#v", graph.Dependencies)
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "scientific", IncludeMain: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"func RequiredDependencies()", `Kind: "python", Spec: "numpy>=1.26"`, `Kind: "go", Spec: "github.com/acme/adapter"`, `Kind: "host", Spec: "load_profile"`} {
		if !strings.Contains(code, fragment) {
			t.Fatalf("generated library is missing dependency metadata %q", fragment)
		}
	}
	mainCode, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mainCode, "runtime.NewProcessHost") || !strings.Contains(mainCode, "runtime.NewPythonHost") || !strings.Contains(mainCode, "host/go dependencies require a Go host program") {
		t.Fatal("python dependency did not activate the standalone worker")
	}
	if _, err := compiler.ParseAndBuild(`require python "numpy"
		require python "numpy"
		flow Duplicate() -> any { return 1 }`); err == nil {
		t.Fatal("expected duplicate dependency error")
	}
	if _, err := compiler.ParseAndBuild(`requires python "numpy"
		flow Legacy() -> any { return 1 }`); err == nil || !strings.Contains(err.Error(), "require") {
		t.Fatalf("expected singular require diagnostic, got %v", err)
	}
}

func TestBindingErrorsAreCompileErrors(t *testing.T) {
	for _, source := range []string{
		`flow Missing() -> any { return missing }`,
		"flow Duplicate() -> any { value = 1\n value = 2\n return value }",
		"flow Escaped() -> any { when true { value = 1\n }\n return value }",
	} {
		if _, err := compiler.ParseAndBuild(source); err == nil {
			t.Fatalf("expected compile error for %q", source)
		}
	}
	if _, err := compiler.ParseAndBuild("flow NoOutput(value: number) -> any { doubled = value * 2 }"); err == nil {
		t.Fatal("expected a Flow without return to be rejected")
	}
}

func TestDottedHostCallBuilds(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`require python "math"
	flow Python(input: number) -> any {
		root = math.sqrt(input)
		return root
	}`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, `host.Call(ctx, "math.sqrt"`) {
		t.Fatalf("generated code did not preserve dotted operation: %s", code)
	}
}

func TestExternalCallsNeedDependencyDeclarations(t *testing.T) {
	if _, err := compiler.ParseAndBuild(`flow MissingPython(input: number) -> any {
		return math.sqrt(input)
	}`); err == nil || !strings.Contains(err.Error(), `require python "math"`) {
		t.Fatalf("expected missing Python dependency diagnostic, got %v", err)
	}
	if _, err := compiler.ParseAndBuild(`flow MissingHost(input: string) -> any {
		return fetch(input)
	}`); err == nil || !strings.Contains(err.Error(), `require host "fetch"`) {
		t.Fatalf("expected missing Host dependency diagnostic, got %v", err)
	}
}

func TestEffectCallStatement(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`require host "print"
	flow Log(value: string) -> any {
        print(value)
        return value
    }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 || graph.Nodes[0].Expr == nil {
		t.Fatalf("nodes = %#v, want effect and return nodes", graph.Nodes)
	}
	if _, err := compiler.ParseAndBuild(`flow Bad() -> any { 1 + 2
 return 0 }`); err == nil {
		t.Fatal("expected arbitrary unused expression to be rejected")
	}
}

func TestFlowReturn(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request: any) -> any {
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
	graph, err := compiler.ParseAndBuild(`flow Gated(input: number) -> any? {
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
	_, err := compiler.ParseAndBuild(`flow Bad() -> any { b = a
 a = 1
 return b }`)
	if err == nil {
		t.Fatal("expected forward-reference error")
	}
}

func TestSmallLanguageCore(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`fn twice(x: number) { return x * 2 }
        flow Language(input: any) -> any {
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

func TestComprehensionBuildsOneDynamicMapNode(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`fn twice(x: number) { return x * 2 }
		flow MapNumbers(input: any) -> any {
			values = [1, 2, 3]
			doubled = [twice(x) for x in values]
			return doubled
		}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(graph.Nodes))
	}
	if got := graph.Nodes[1].Deps; len(got) != 1 || got[0] != "values" {
		t.Fatalf("map deps = %#v, want [values]", got)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "runtime.MapSpec") || !strings.Contains(code, `Source: "values"`) {
		t.Fatal("generated code did not contain a dynamic map spec")
	}
}

func TestStateBuildsPersistentNodeAndInstanceAPI(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Counter(input: number) -> any {
		count = state(0)
		double = count * 2
		return double + input
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 3 || !graph.Nodes[0].State {
		t.Fatalf("nodes = %#v, want a state node followed by two derived nodes", graph.Nodes)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "func NewInstance") || !strings.Contains(code, "State: true") {
		t.Fatal("generated code did not expose the persistent instance API")
	}
}

func TestStatefulInstanceReusesUnchangedNodes(t *testing.T) {
	rt := runtime.NewGraph()
	var stateRuns, derivedRuns, inputRuns atomic.Int64
	rt.Add(runtime.NodeSpec{Name: "count", State: true, Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		stateRuns.Add(1)
		return runtime.Ready(0)
	}})
	rt.Add(runtime.NodeSpec{Name: "double", Deps: []string{"count"}, Pure: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		derivedRuns.Add(1)
		return runtime.Ready(values["count"].(int) * 2)
	}, Output: true})
	rt.Add(runtime.NodeSpec{Name: "input_value", Deps: []string{"input"}, Pure: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		inputRuns.Add(1)
		return runtime.Ready(values["input"])
	}})
	instance := rt.NewInstance(runtime.NewHost(), map[string]runtime.Value{"input": 1})
	value, trace, err := instance.Tick(context.Background(), nil)
	if err != nil || value != 0 {
		t.Fatalf("first tick value=%v trace=%#v err=%v", value, trace, err)
	}
	if stateRuns.Load() != 1 || derivedRuns.Load() != 1 || inputRuns.Load() != 1 {
		t.Fatalf("first runs state=%d derived=%d input=%d", stateRuns.Load(), derivedRuns.Load(), inputRuns.Load())
	}
	value, trace, err = instance.Tick(context.Background(), nil)
	if err != nil || value != 0 || instance.TickCount() != 2 {
		t.Fatalf("second tick value=%v trace=%#v err=%v tick=%d", value, trace, err, instance.TickCount())
	}
	if stateRuns.Load() != 1 || derivedRuns.Load() != 1 || inputRuns.Load() != 1 {
		t.Fatalf("unchanged tick reran nodes state=%d derived=%d input=%d", stateRuns.Load(), derivedRuns.Load(), inputRuns.Load())
	}
	if len(trace) != 3 || trace[0].Tick != 2 || trace[0].Reason != "reused" {
		t.Fatalf("reuse trace = %#v", trace)
	}
	if err := instance.SetState("count", 3); err != nil {
		t.Fatal(err)
	}
	value, _, err = instance.Tick(context.Background(), nil)
	if err != nil || value != 6 {
		t.Fatalf("state update value=%v err=%v, want 6", value, err)
	}
	if stateRuns.Load() != 1 || derivedRuns.Load() != 2 || inputRuns.Load() != 1 {
		t.Fatalf("state update runs state=%d derived=%d input=%d", stateRuns.Load(), derivedRuns.Load(), inputRuns.Load())
	}
}

func TestStateInitializerIsCommittedAcrossTicks(t *testing.T) {
	rt := runtime.NewGraph()
	var initRuns atomic.Int64
	rt.Add(runtime.NodeSpec{Name: "state", State: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		initRuns.Add(1)
		return runtime.Ready(values["seed"])
	}})
	rt.Add(runtime.NodeSpec{Name: "out", Deps: []string{"state"}, Output: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		return runtime.Ready(values["state"])
	}})
	instance := rt.NewInstance(runtime.NewHost(), map[string]runtime.Value{"seed": 1})
	value, _, err := instance.Tick(context.Background(), nil)
	if err != nil || value != 1 {
		t.Fatalf("first tick value=%v err=%v", value, err)
	}
	value, _, err = instance.Tick(context.Background(), map[string]runtime.Value{"seed": 9})
	if err != nil || value != 1 || initRuns.Load() != 1 {
		t.Fatalf("state reset on input change: value=%v runs=%d err=%v", value, initRuns.Load(), err)
	}
}

func TestReadOnlyHostOperationsMayRunConcurrently(t *testing.T) {
	host := runtime.NewHost()
	var active, maxActive atomic.Int64
	read := func(_ context.Context, _ []runtime.Value) runtime.Result {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return runtime.Ready("ok")
	}
	host.RegisterReadOnly("read_a", read)
	host.RegisterReadOnly("read_b", read)
	rt := runtime.NewGraph()
	for _, name := range []string{"a", "b"} {
		op := "read_" + name
		rt.Add(runtime.NodeSpec{Name: name, Op: op, Eval: func(ctx context.Context, _ map[string]runtime.Value) runtime.Result {
			return host.Call(ctx, op, nil)
		}})
	}
	rt.Add(runtime.NodeSpec{Name: "out", Deps: []string{"a", "b"}, Output: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		return runtime.Ready(values["a"].(string) + values["b"].(string))
	}})
	if _, _, err := rt.RunParallel(context.Background(), host, nil, 2); err != nil {
		t.Fatal(err)
	}
	if maxActive.Load() != 2 {
		t.Fatalf("max concurrent read-only calls = %d, want 2", maxActive.Load())
	}
}

func TestExternalWritesRemainInSourceOrder(t *testing.T) {
	host := runtime.NewHost()
	var seen []string
	var mu sync.Mutex
	for _, name := range []string{"write_a", "write_b"} {
		value := name
		host.Register(name, func(_ context.Context, _ []runtime.Value) runtime.Result {
			mu.Lock()
			seen = append(seen, value)
			mu.Unlock()
			return runtime.Ready(nil)
		})
	}
	rt := runtime.NewGraph()
	for _, name := range []string{"a", "b"} {
		op := "write_" + name
		rt.Add(runtime.NodeSpec{Name: name, Op: op, Eval: func(ctx context.Context, _ map[string]runtime.Value) runtime.Result {
			return host.Call(ctx, op, nil)
		}})
	}
	rt.Add(runtime.NodeSpec{Name: "out", Deps: []string{"a", "b"}, Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready("done")
	}})
	if _, _, err := rt.RunParallel(context.Background(), host, nil, 2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(seen, ","); got != "write_a,write_b" {
		t.Fatalf("write order = %s, want write_a,write_b", got)
	}
}

func TestParallelRunsLatePurePredecessorOfWrite(t *testing.T) {
	host := runtime.NewHost()
	var order []string
	var mu sync.Mutex
	host.Register("write", func(_ context.Context, _ []runtime.Value) runtime.Result {
		mu.Lock()
		order = append(order, "write")
		mu.Unlock()
		return runtime.Ready("done")
	})
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "write", Op: "write", Deps: []string{"late"}, Output: true, Eval: func(ctx context.Context, values map[string]runtime.Value) runtime.Result {
		return host.Call(ctx, "write", []runtime.Value{values["late"]})
	}})
	rt.Add(runtime.NodeSpec{Name: "late", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		mu.Lock()
		order = append(order, "late")
		mu.Unlock()
		return runtime.Ready("value")
	}})
	if _, _, err := rt.RunParallel(context.Background(), host, nil, 2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "late,write" {
		t.Fatalf("late predecessor order = %s, want late,write", got)
	}
}

func TestAfterAddsOrderingWithoutDataDependency(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	rt := runtime.NewGraph()
	appendSeen := func(name string) func(context.Context, map[string]runtime.Value) runtime.Result {
		return func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
			return runtime.Ready(name)
		}
	}
	rt.Add(runtime.NodeSpec{Name: "first", Pure: true, Eval: appendSeen("first")})
	rt.Add(runtime.NodeSpec{Name: "second", Pure: true, After: []string{"first"}, Output: true, Eval: appendSeen("second")})
	if _, _, err := rt.RunParallel(context.Background(), runtime.NewHost(), nil, 2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(seen, ","); got != "first,second" {
		t.Fatalf("After order = %s, want first,second", got)
	}
}

func TestRetryNodeRetriesUntilSuccess(t *testing.T) {
	rt := runtime.NewGraph()
	var attempts atomic.Int64
	rt.Add(runtime.NodeSpec{Name: "result", Output: true, Retry: &runtime.RetrySpec{Attempts: 3, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		if attempts.Add(1) < 3 {
			return runtime.Failed(fmt.Errorf("transient"))
		}
		return runtime.Ready("ok")
	}}})
	value, _, err := rt.Run(context.Background(), nil)
	if err != nil || value != "ok" || attempts.Load() != 3 {
		t.Fatalf("value=%v attempts=%d err=%v, want ok after three attempts", value, attempts.Load(), err)
	}
}

func TestFeedbackNodeConvergesWithBoundedAttempts(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "result", Output: true, Feedback: &runtime.FeedbackSpec{
		Attempts: 3,
		Init:     func(_ context.Context, _ map[string]runtime.Value) runtime.Result { return runtime.Ready(0) },
		Step: func(_ context.Context, value runtime.Value) runtime.Result {
			number, _ := runtime.Number(value)
			return runtime.Ready(number + 1)
		},
		Verify: func(_ context.Context, value runtime.Value) runtime.Result {
			number, _ := runtime.Number(value)
			return runtime.Ready(number >= 2)
		},
	}})
	value, _, err := rt.Run(context.Background(), nil)
	if err != nil || value != float64(2) {
		t.Fatalf("value=%v err=%v, want 2", value, err)
	}
}

func TestCancellationIsReportedAsCancelled(t *testing.T) {
	rt := runtime.NewGraph()
	ctx, cancel := context.WithCancel(context.Background())
	rt.Add(runtime.NodeSpec{Name: "cancelled", Output: true, Eval: func(ctx context.Context, _ map[string]runtime.Value) runtime.Result {
		cancel()
		return runtime.Failed(ctx.Err())
	}})
	_, trace, err := rt.Run(ctx, nil)
	if err == nil || len(trace) == 0 || trace[len(trace)-1].Status != runtime.Cancelled {
		t.Fatalf("trace=%#v err=%v, want Cancelled status", trace, err)
	}
}

func TestCancellationSkipsDependentNodes(t *testing.T) {
	rt := runtime.NewGraph()
	ctx, cancel := context.WithCancel(context.Background())
	rt.Add(runtime.NodeSpec{Name: "source", Output: false, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		cancel()
		return runtime.Failed(context.Canceled)
	}})
	rt.Add(runtime.NodeSpec{Name: "dependent", Deps: []string{"source"}, Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		t.Fatal("dependent node ran after cancellation")
		return runtime.Ready(nil)
	}})
	_, trace, err := rt.Run(ctx, nil)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	var got runtime.Status
	for _, event := range trace {
		if event.Node == "dependent" {
			got = event.Status
		}
	}
	if got != runtime.Skipped {
		t.Fatalf("dependent trace = %#v, want Skipped", trace)
	}
}

func TestRuntimeRejectsNilContext(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "value", Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready(1)
	}})
	if _, _, err := rt.Run(nil, nil); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("Run(nil) error = %v, want nil-context error", err)
	}
	if result := runtime.NewHost().Call(nil, "missing", nil); result.Err == nil || !strings.Contains(result.Err.Error(), "nil context") {
		t.Fatalf("Host.Call(nil) result = %#v, want nil-context error", result)
	}
}

func TestPersistentInstanceSerializesConcurrentTicks(t *testing.T) {
	rt := runtime.NewGraph()
	var active, maxActive atomic.Int64
	rt.Add(runtime.NodeSpec{Name: "value", Output: true, Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return runtime.Ready(1)
	}})
	instance := rt.NewInstance(runtime.NewHost(), nil)
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := instance.Tick(context.Background(), nil); err != nil {
				t.Errorf("concurrent Tick: %v", err)
			}
		}()
	}
	wg.Wait()
	if instance.TickCount() != 2 || maxActive.Load() != 1 {
		t.Fatalf("ticks=%d maxActive=%d, want two serialized ticks", instance.TickCount(), maxActive.Load())
	}
}

func TestCancelledTickAdvancesLogicalClock(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "value", Output: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready(1)
	}})
	instance := rt.NewInstance(runtime.NewHost(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, trace, err := instance.Tick(ctx, nil); err == nil || instance.TickCount() != 1 || len(trace) != 1 || trace[0].Status != runtime.Cancelled {
		t.Fatalf("cancelled Tick count=%d trace=%#v err=%v", instance.TickCount(), trace, err)
	}
}

func TestGraphSerializesConcurrentRuns(t *testing.T) {
	rt := runtime.NewGraph()
	var active, maxActive atomic.Int64
	rt.Add(runtime.NodeSpec{Name: "value", Output: true, Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return runtime.Ready(1)
	}})
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := rt.RunParallel(context.Background(), runtime.NewHost(), nil, 1); err != nil {
				t.Errorf("concurrent RunParallel: %v", err)
			}
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent Graph runs=%d, want 1", maxActive.Load())
	}
}

func TestDynamicMapPreservesOrderAndHonorsLimit(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "values", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready([]runtime.Value{1, 2, 3, 4})
	}})
	var active, maxActive atomic.Int64
	rt.Add(runtime.NodeSpec{
		Name: "doubled", Deps: []string{"values"}, Pure: true,
		Map: &runtime.MapSpec{Source: "values", Pure: true, Eval: func(ctx context.Context, item runtime.Value, _ map[string]runtime.Value) runtime.Result {
			current := active.Add(1)
			for {
				old := maxActive.Load()
				if current <= old || maxActive.CompareAndSwap(old, current) {
					break
				}
			}
			defer active.Add(-1)
			select {
			case <-ctx.Done():
				return runtime.Failed(ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
			value, err := runtime.Binary("*", item, 2)
			if err != nil {
				return runtime.Failed(err)
			}
			return runtime.Ready(value)
		}},
	})
	rt.Add(runtime.NodeSpec{Name: "out", Deps: []string{"doubled"}, Output: true, Eval: func(_ context.Context, values map[string]runtime.Value) runtime.Result {
		return runtime.Ready(values["doubled"])
	}})
	value, _, err := rt.RunParallel(context.Background(), runtime.NewHost(), nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := value.([]runtime.Value)
	if !ok {
		t.Fatalf("value = %T %v, want []runtime.Value", value, value)
	}
	if got := fmt.Sprint(items); got != "[2 4 6 8]" {
		t.Fatalf("mapped values = %s, want [2 4 6 8]", got)
	}
	if maxActive.Load() > 2 {
		t.Fatalf("max active = %d, want <= 2", maxActive.Load())
	}
}

func TestDynamicMapEmptySourceReturnsEmptyList(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "values", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready([]runtime.Value{})
	}})
	rt.Add(runtime.NodeSpec{Name: "mapped", Deps: []string{"values"}, Pure: true, Output: true, Map: &runtime.MapSpec{Source: "values", Pure: true, Eval: func(_ context.Context, item runtime.Value, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready(item)
	}}})
	value, _, err := rt.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := value.([]runtime.Value)
	if !ok || len(items) != 0 {
		t.Fatalf("value = %#v, want empty []runtime.Value", value)
	}
}

func TestEffectfulDynamicMapRunsSequentially(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "values", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready([]runtime.Value{1, 2, 3})
	}})
	var active, maxActive atomic.Int64
	rt.Add(runtime.NodeSpec{Name: "mapped", Deps: []string{"values"}, Map: &runtime.MapSpec{Source: "values", Ops: []string{"effect"}, Eval: func(_ context.Context, item runtime.Value, _ map[string]runtime.Value) runtime.Result {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return runtime.Ready(item)
	}}})
	host := runtime.NewHost()
	host.Register("effect", func(_ context.Context, args []runtime.Value) runtime.Result { return runtime.Ready(args) })
	if _, _, err := rt.RunParallel(context.Background(), host, nil, 4); err != nil {
		t.Fatal(err)
	}
	if maxActive.Load() != 1 {
		t.Fatalf("max active = %d, want 1 for an effectful map", maxActive.Load())
	}
}

func TestDynamicMapRejectsNonSequenceSource(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "value", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready(1)
	}})
	rt.Add(runtime.NodeSpec{Name: "mapped", Deps: []string{"value"}, Pure: true, Map: &runtime.MapSpec{Source: "value", Pure: true, Eval: func(_ context.Context, item runtime.Value, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready(item)
	}}})
	if _, _, err := rt.Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "map source must be a list or slice") {
		t.Fatalf("error = %v, want sequence-source error", err)
	}
}

func TestDynamicMapElementErrorStopsDownstream(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "values", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready([]runtime.Value{1, 2, 3})
	}})
	rt.Add(runtime.NodeSpec{Name: "mapped", Deps: []string{"values"}, Pure: true, Map: &runtime.MapSpec{Source: "values", Pure: true, Eval: func(_ context.Context, item runtime.Value, _ map[string]runtime.Value) runtime.Result {
		if number, _ := runtime.Number(item); number == 2 {
			return runtime.Failed(fmt.Errorf("boom"))
		}
		return runtime.Ready(item)
	}}})
	value, trace, err := rt.RunParallel(context.Background(), runtime.NewHost(), nil, 2)
	if err == nil || !strings.Contains(err.Error(), "map element 1") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("value=%v trace=%#v err=%v, want indexed map error", value, trace, err)
	}
	for _, event := range trace {
		if event.Node == "mapped" && event.Status == runtime.Completed {
			t.Fatal("mapped node completed after an element error")
		}
	}
}

func TestDynamicMapHonorsContextCancellation(t *testing.T) {
	rt := runtime.NewGraph()
	rt.Add(runtime.NodeSpec{Name: "values", Pure: true, Eval: func(_ context.Context, _ map[string]runtime.Value) runtime.Result {
		return runtime.Ready([]runtime.Value{1, 2, 3, 4})
	}})
	rt.Add(runtime.NodeSpec{Name: "mapped", Deps: []string{"values"}, Pure: true, Map: &runtime.MapSpec{Source: "values", Pure: true, Eval: func(ctx context.Context, item runtime.Value, _ map[string]runtime.Value) runtime.Result {
		select {
		case <-ctx.Done():
			return runtime.Failed(ctx.Err())
		case <-time.After(time.Second):
			return runtime.Ready(item)
		}
	}}})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := rt.RunParallel(ctx, runtime.NewHost(), nil, 2)
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("err = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("map did not stop after context cancellation")
	}
}

func TestConformanceSourcesCompile(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("conformance", "*.lip"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no conformance sources found")
	}
	for _, path := range paths {
		if _, err := compiler.CompileFile(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join("conformance", "map.lip")); err != nil {
		t.Fatal(err)
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
	if _, err := compiler.ParseAndBuild(`flow Bad() -> any { value = 1 + "x"
 return value }`); err == nil {
		t.Fatal("expected compile-time mixed-type operator error")
	}
	if _, err := compiler.ParseAndBuild(`flow Bad() -> any { value = 1 == "1"
 return value }`); err == nil {
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
        flow Bad() -> any { value = twice("x")
 return value }`); err == nil {
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
		flow Bad() -> any {
			value = caller(1)
			return value + "!"
		}`)
	if err == nil {
		t.Fatal("expected the return type of a later function to be checked")
	}
}

func TestGeneratedNodeNamesAreReserved(t *testing.T) {
	if _, err := compiler.ParseAndBuild(`flow Bad() -> any {
		__return_0 = 1
		return __return_0
	}`); err == nil {
		t.Fatal("expected generated-node name collision to be rejected")
	}
}

func TestTypedFlowInputIsChecked(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Hello(request: string) -> any { return "Hello, " + request }`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, `runtime.CheckType(value, "string")`) {
		t.Fatal("generated flow does not check annotated input")
	}
}

func TestGeneratedNestedExpressionErrorsReturnResults(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`flow Fields(input: any) -> any {
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
