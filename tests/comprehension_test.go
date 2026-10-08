package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestComprehensionCompositionChecks(t *testing.T) {
	for _, source := range []string{
		`import python "numpy" as np; np.mean([x for x in range(1, 19)])`,
		`print([x * x for x in range(1, 19)])`,
		`fn squares(n: number) { return [x * x for x in range(0, n)] }; print(squares(3))`,
		`print([[x + y for y in range(0, x)] for x in range(1, 4)])`,
		`print([x for x in [y * y for y in range(0, 3)]])`,
		`when len([x for x in range(0, 3)]) == 3 { print(true) }`,
		`import host "fetch"; items = [fetch(x) for x in range(0, 3)]; print(items)`,
	} {
		graph, err := compiler.ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if _, err := compiler.GenerateGo(graph); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, tc := range []struct{ source, want string }{
		{`print([x for x in 3])`, "source must be a list"},
		{`print([missing for x in range(0, 3)])`, `reference "missing"`},
		{`a = [x for x in range(0, 3)]; print(x)`, `reference "x"`},
		{`import host "fetch"; print([fetch(x) for x in range(0, 3)])`, "nested external"},
		{`import host "fetch"; a = [x for x in fetch()]`, "nested external"},
		{`import host "fetch"; fn f() { return [fetch(x) for x in range(0, 3)] }; print(f())`, "must be pure"},
		{`print([state(x) for x in range(0, 3)])`, "Flow binding"},
		{`a = [retry(str(x), 2) for x in range(0, 3)]`, "not Map elements"},
	} {
		if _, err := compiler.ParseAndBuild(tc.source); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %q", tc.source, err, tc.want)
		}
	}
}

func TestGeneratedComposableComprehensions(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `import python "numpy" as np
fn add(a: number, b: number) -> number { return a + b }
fn squares(n: number) -> list { return [x * x for x in range(1, n)] }
fn shadow(x: number) { return [x for x in range(0, x)] }
flow Compose(mode: number, input: any) -> object {
    x = 100
    mapped = [n * n for n in range(1, 4)]
    dynamic = [x * x for x in range(0, len(input))]
    shadow_source = [x for x in range(x - 2, x)]
    reversed = [x for x in range(3, 0, -1)]
    mean = np.mean([x for x in range(1, 19)])
    when false { hidden = [fail("hidden element") for x in range(0, 3, 0)] }
    return if mode == 1 {
        {first: fail("first field"), later: [fail("later field") for x in range(0, 3)]}
    } else { if mode == 2 {
        {items: [1 / (x - 2) for x in range(0, 4)]}
    } else { if mode == 3 {
        {items: [x for x in input]}
    } else {
        {
            mapped: mapped, dynamic: dynamic, source: shadow_source, reversed: reversed, mean: mean,
            captured: [x + y for y in range(1, 3)],
            nested: [[x + y for y in range(0, x)] for x in range(1, 4)],
            shadowed: [[x for x in range(0, x)] for x in range(1, 4)],
            chained: [x + 1 for x in [y * y for y in range(0, 3)]],
            function: squares(4), fn_shadow: shadow(3),
            total: fold([x * x for x in range(1, 4)], 0, add),
            indexed: [x * x for x in range(1, 4)][1],
            count: len([x for x in input]), outer: x,
            lazy: if false { [fail("unused branch") for x in range(0, 3, 0)] } else { [] },
            short_and: false && len([fail("unused and") for x in range(0, 3)]) > 0,
            short_or: true || len([fail("unused or") for x in range(0, 3)]) > 0,
            empty: [fail("empty element") for x in []]
        }
    } } }
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import ("context"; "reflect"; "strings"; "testing"; "lipalpha/runtime")
func TestComposition(t *testing.T) {
    host := runtime.DefaultHost()
    host.RegisterPure("numpy.mean", func(ctx context.Context, args []runtime.Value) runtime.Result {
        sum, err := runtime.ListCall(ctx, "list.sum", args, nil)
        if err != nil { return runtime.Failed(err) }
        n, _ := runtime.Length(args[0])
        value, err := runtime.Binary("/", sum, n)
        if err != nil { return runtime.Failed(err) }
        return runtime.Ready(value)
    })
    want := "{\"captured\":[101,102],\"chained\":[1,2,5],\"count\":2,\"dynamic\":[0,1],\"empty\":[],\"fn_shadow\":[0,1,2],\"function\":[1,4,9],\"indexed\":4,\"lazy\":[],\"mapped\":[1,4,9],\"mean\":9.5,\"nested\":[[1],[2,3],[3,4,5]],\"outer\":100,\"reversed\":[3,2,1],\"shadowed\":[[0],[0,1],[0,1,2]],\"short_and\":false,\"short_or\":true,\"source\":[98,99],\"total\":14}"
    for _, run := range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}} {
        value, trace, err := run(context.Background(), host, map[string]runtime.Value{"mode":0,"input":[]int{4,5}})
        if err != nil { t.Fatal(err) }
        got, err := runtime.FormatValue(value)
        if err != nil || got != want { t.Fatalf("got %s, err=%v",got,err) }
        for _, event := range trace { if event.Node=="hidden" && event.Status==runtime.Running { t.Fatal("false gate evaluated source") } }
        for _, tc := range []struct{mode int; input runtime.Value; want string}{
            {1, []int{}, "first field"}, {2, []int{}, "map element 2"}, {3, "bad", "comprehension source"},
        } {
            _, _, err := run(context.Background(), host, map[string]runtime.Value{"mode":tc.mode,"input":tc.input})
            if err==nil || !strings.Contains(err.Error(),tc.want) || strings.Contains(err.Error(),"later field") { t.Fatalf("mode %d: %v",tc.mode,err) }
        }
    }
    instance, err := NewInstance(host, map[string]runtime.Value{"mode":0,"input":[]int{4,5}})
    if err != nil { t.Fatal(err) }
    first, _, err := instance.Tick(context.Background(),nil)
    if err != nil { t.Fatal(err) }
    second, _, err := instance.Tick(context.Background(),nil)
    if err != nil || !reflect.DeepEqual(first,second) { t.Fatalf("repeat tick: %v",err) }
    third, _, err := instance.Tick(context.Background(),map[string]runtime.Value{"input":[]int{4,5,6}})
    if err != nil || third.(map[string]runtime.Value)["count"]!=float64(3) || !reflect.DeepEqual(third.(map[string]runtime.Value)["dynamic"],[]runtime.Value{float64(0),float64(1),float64(4)}) { t.Fatalf("changed dependency: %v %v",third,err) }
}
`)
	cmd := exec.Command("go", "test", "-race", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated comprehensions failed: %v\n%s", err, out)
	}
}
