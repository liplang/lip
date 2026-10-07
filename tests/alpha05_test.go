package tests

import (
	"encoding/json"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestAlpha05Diagnostics(t *testing.T) {
	cases := []struct{ name, source, diagnostic string }{
		{"missing input type", `flow Bad(x) -> any { return x }`, "explicit type"},
		{"missing output type", `flow Bad() { return 1 }`, "explicit output type"},
		{"missing return", `flow Bad(x: number) -> number { doubled = x * 2 }`, "no return"},
		{"undefined", `flow Bad() -> any { return missing }`, "undefined or forward"},
		{"forward", "flow Bad() -> any { b = a\n a = 1\n return b }", "forward reference"},
		{"duplicate", "flow Bad() -> any { a = 1\n a = 2\n return a }", "duplicate binding"},
		{"scope", "flow Bad() -> any { when true { a = 1 }\n return a }", "scoped to a when"},
		{"untyped function", `fn f(x) { return x } flow Bad() -> number { return f(1) }`, "explicit type"},
		{"wrong dynamic branch", `flow Bad(x: any) -> string { return if true then x else 1 }`, "declared output"},
		{"wrong output", `flow Bad() -> string { return 1 }`, "declared output"},
		{"wrong function output", `fn f(x: number) -> string { return x } flow Bad() -> any { return f(1) }`, "declared string"},
		{"gated output", `flow Bad(x: bool) -> number { when x { return 1 } }`, "may produce no value"},
		{"multiple outputs", `flow Bad(x: bool) -> number? { when x { return 1 } return 2 }`, "exactly one return"},
		{"impure function", `require host "fetch" fn f(x: string) { return fetch(x) } flow Bad() -> any { return f("x") }`, "must be pure"},
		{"nested external", `require host "fetch" flow Bad() -> string { return str(fetch()) }`, "Flow binding first"},
		{"recursive function", `fn f(x: number) { return f(x) } flow Bad() -> number { return f(1) }`, "recursive"},
		{"control in map", `require host "fetch" flow Bad(values: any) -> any { return [retry(fetch(x), 2) for x in values] }`, "not Map elements"},
		{"numeric map source", `flow Bad(values: number) -> any { return [x * 2 for x in values] }`, "must be a list"},
		{"wrong feedback verifier", `fn initial() -> number { return 1 } fn step(x: number) -> number { return x } fn verify(x: number) -> number { return x } flow Bad() -> number { return feedback(initial(), step, verify, 3) }`, "must return bool"},
		{"wrong feedback arity", `fn initial() -> number { return 1 } fn step(x: number, y: number) -> number { return x + y } fn verify(x: number) -> bool { return x > 0 } flow Bad() -> number { return feedback(initial(), step, verify, 3) }`, "supplies one candidate"},
		{"reserved function", `fn str(x: number) { return x } flow Bad() -> number { return str(1) }`, "reserved"},
		{"str arity", `flow Bad() -> string { return str(1, 2) }`, "1 argument"},
		{"effect arity", `flow Bad() -> string { str(1, 2) return "x" }`, "1 argument"},
		{"incomplete require", `require python flow Bad() -> number { return 1 }`, "expected string"},
		{"malformed require", `require python ">=1.0" flow Bad() -> number { return 1 }`, "invalid python dependency"},
		{"distribution name", `require python "scikit-learn" flow Bad() -> number { return 1 }`, "invalid python dependency"},
		{"duplicate require", `require python "math" require python "math" flow Bad() -> number { return 1 }`, "duplicate python"},
		{"requires alias", `requires python "math" flow Bad() -> number { return 1 }`, "use singular \"require\""},
		{"import alias", `import python "math" flow Bad() -> number { return 1 }`, "use singular \"require\""},
		{"late alias", `require python "math" requires python "numpy" flow Bad() -> number { return 1 }`, "use singular \"require\""},
		{"two flows", `flow One() -> number { return 1 } flow Two() -> number { return 2 }`, "end of file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compiler.ParseAndBuild(tc.source)
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("got %v, want %q", err, tc.diagnostic)
			}
			if !strings.Contains(err.Error(), ":") {
				t.Fatalf("diagnostic has no source position: %v", err)
			}
		})
	}
}

func TestAlpha05Migration(t *testing.T) {
	source := "# 保留注释\nrequires host \"fetch\"\nfn twice(x) { return x * 2 }\nflow Older(input) {\n gate = true\n when gate { return twice(input) }\n}\n"
	updated, report, err := compiler.MigrateSource(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 保留注释", `require host "fetch"`, "twice(x: any)", "Older(input: any) -> any?"} {
		if !strings.Contains(updated, want) {
			t.Fatalf("migration lost %q: %s", want, updated)
		}
	}
	if len(report) < 3 {
		t.Fatalf("report=%v", report)
	}
	second, _, err := compiler.MigrateSource(updated)
	if err != nil || updated != second {
		t.Fatalf("migration is not idempotent: %v\n%s", err, second)
	}
	constant, _, err := compiler.MigrateSource("flow Constant{ return 2 }\n")
	if err != nil || !strings.Contains(constant, "Constant() -> number") {
		t.Fatalf("empty input migration: %q %v", constant, err)
	}
	if _, _, err := compiler.MigrateSource(`flow Incomplete(input) { x = input }`); err == nil {
		t.Fatal("migration invented a return")
	}
}

func writeTestFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func generatedSource(t *testing.T, source string, main bool) string {
	t.Helper()
	graph, err := compiler.ParseAndBuild(source)
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "main", IncludeMain: main})
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		t.Fatalf("invalid generated Go: %v\n%s", err, code)
	}
	return string(formatted)
}

func TestAlpha05GeneratedLibrary(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `require host "fetch"
fn twice(x: number) -> number { return x * 2 }
fn choose(x: number) -> string { return if x > 0 then str(twice(x)) else "nonpositive" }
flow Checked(x: number) -> string {
 count = state(1)
 raw = fetch(x)
 return choose(raw + count)
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import ("context"; "math"; "reflect"; "sync"; "testing"; "lipalpha/runtime")
func TestContract(t *testing.T) {
 ctx := context.Background()
 host := runtime.DefaultHost()
 host.RegisterPure("fetch", func(_ context.Context,args []runtime.Value) runtime.Result { return runtime.Ready(args[0]) })
 host.Register("str", func(_ context.Context,_ []runtime.Value) runtime.Result { return runtime.Ready("overridden") })
 host.RegisterPure("twice", func(_ context.Context,_ []runtime.Value) runtime.Result { return runtime.Ready("caller") })
 for _, run := range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}} {
  value, _, err := run(ctx,host,map[string]runtime.Value{"x":2})
  if err != nil || value != "6" { t.Fatalf("value=%v err=%v",value,err) }
  for _, inputs := range []map[string]runtime.Value{nil,{"x":"2"},{"x":math.Inf(1)},{"x":2,"hidden":99}} { if _,_,err:=run(ctx,host,inputs);err==nil{t.Fatalf("accepted invalid inputs: %v",inputs)} }
 }
 if result := host.Call(ctx,"twice",nil);result.Value!="caller"{t.Fatal("Flow overwrote caller's operation")}
 var wg sync.WaitGroup
 for n:=0;n<6;n++{wg.Add(1);go func(){defer wg.Done();if _,_,err:=Run(ctx,host,map[string]runtime.Value{"x":2});err!=nil{t.Error(err)}}()};wg.Wait()
 instance,err:=NewInstance(host,map[string]runtime.Value{"x":2});if err!=nil{t.Fatal(err)}
 if _,_,err:=instance.Tick(ctx,nil);err!=nil{t.Fatal(err)}
 tick:=instance.TickCount()
 for _,updates:=range []map[string]runtime.Value{{"x":"bad"},{"count":99},{"unknown":1}}{if _,_,err:=instance.Tick(ctx,updates);err==nil{t.Fatal("invalid Tick accepted")}}
 if instance.TickCount()!=tick{t.Fatal("invalid Tick mutated instance")}
 if err:=instance.SetState("count","bad");err==nil{t.Fatal("invalid state accepted")}
 if err:=instance.SetState("count",3);err!=nil{t.Fatal(err)}
 value,_,err:=instance.Tick(ctx,map[string]runtime.Value{"x":4});if err!=nil||value!="14"{t.Fatalf("value=%v err=%v",value,err)}
 if _,_,err:=Run(ctx,runtime.DefaultHost(),map[string]runtime.Value{"x":2});err==nil{t.Fatal("missing adapter accepted")}
 if got:=RequiredDependencies();!reflect.DeepEqual(got,[]Dependency{{Kind:"host",Spec:"fetch"}}){t.Fatal(got)}
}
`)
	cmd := exec.Command("go", "test", "-race", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated library failed: %v\n%s", err, out)
	}
}

func TestAlpha05EndToEnd(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	lipc := filepath.Join(dir, "lipc")
	command := func(args ...string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "LIP_INPUT=\"World\"")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := command("go", "build", "-o", lipc, "./cmd/lipc"); err != nil {
		t.Fatalf("build lipc: %v\n%s", err, out)
	}
	t.Run("hello", func(t *testing.T) {
		source := filepath.Join(root, "examples", "hello.lip")
		binary := filepath.Join(dir, "hello")
		goSource := filepath.Join(dir, "hello.go")
		for _, args := range [][]string{{lipc, "build", "-o", binary, source}, {lipc, "build", "-emit-go", "-o", goSource, source}} {
			if out, err := command(args...); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
		}
		for _, prefix := range [][]string{{binary}, {lipc, "run", source, "--"}, {"go", "run", goSource}} {
			out, err := command(append(append([]string{}, prefix...), "Alice")...)
			if err != nil || out != "Hello, Alice\n" {
				t.Fatalf("hello %v: %q %v", prefix, out, err)
			}
			for _, extra := range [][]string{nil, {"Alice", "Bob"}} {
				out, err := command(append(append([]string{}, prefix...), extra...)...)
				if err == nil || !strings.Contains(out, "usage:") || strings.Contains(out, "World") {
					t.Fatalf("invalid input %v: %q %v", prefix, out, err)
				}
			}
		}
	})
	t.Run("cli types", func(t *testing.T) {
		source := filepath.Join(dir, "typed.lip")
		writeTestFile(t, source, `flow Typed(text: string, n: number, enabled: bool, data: any) -> any { return [text, n, enabled, data] }`)
		binary, goSource := filepath.Join(dir, "typed"), filepath.Join(dir, "typed.go")
		for _, args := range [][]string{{lipc, "build", "-o", binary, source}, {lipc, "build", "-emit-go", "-o", goSource, source}} {
			if out, err := command(args...); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
		}
		for _, prefix := range [][]string{{binary}, {lipc, "run", source, "--"}, {"go", "run", goSource}} {
			args := []string{"Alice", "-1.25e2", "true", `{"items":[1,2]}`}
			out, err := command(append(append([]string{}, prefix...), args...)...)
			if err != nil || out != "[\"Alice\",-125,true,{\"items\":[1,2]}]\n" {
				t.Fatalf("types %v: %q %v", prefix, out, err)
			}
			for _, bad := range []struct {
				index int
				value string
			}{{1, "NaN"}, {1, "0x1p2"}, {1, "1_000"}, {2, "TRUE"}, {3, "Alice"}} {
				invalid := append([]string(nil), args...)
				invalid[bad.index] = bad.value
				out, err := command(append(append([]string{}, prefix...), invalid...)...)
				if err == nil || !strings.Contains(out, "usage:") {
					t.Fatalf("bad types %v %v: %q %v", prefix, invalid, out, err)
				}
			}
		}
	})
	t.Run("python", func(t *testing.T) {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("python3 unavailable")
		}
		for _, tc := range []struct {
			name, source, want string
			failure            bool
		}{
			{"standard library", `require python "math" flow SquareRoot(x: number) -> number { return math.sqrt(x) }`, "3\n", false},
			{"missing module", `require python "lip_missing_module_alpha05" flow Missing(x: number) -> number { return lip_missing_module_alpha05.sqrt(x) }`, "lip_missing_module_alpha05", true},
			{"real attribute path", `require python "datetime" flow Date(x: string) -> any { value = datetime.datetime.fromisoformat(x) return python.to_json(value) }`, "2026-10-07 00:00:00\n", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				source := filepath.Join(dir, "python.lip")
				writeTestFile(t, source, tc.source)
				input := "9"
				if tc.name == "real attribute path" {
					input = "2026-10-07"
				}
				out, err := command(lipc, "run", source, "--", input)
				if tc.failure {
					if err == nil || !strings.Contains(out, tc.want) {
						t.Fatalf("missing module: %q %v", out, err)
					}
				} else if err != nil || out != tc.want {
					t.Fatalf("python: %q %v", out, err)
				}
			})
		}
		if out, err := command("python3", "-c", "import numpy, pandas"); err != nil {
			t.Logf("scientific packages unavailable: %s", out)
			return
		}
		source := filepath.Join(root, "examples", "python", "flow.lip")
		out, err := command(lipc, "run", source, "--", "[1,2,3,4,5]")
		var actual []any
		decodeErr := json.Unmarshal([]byte(out), &actual)
		if err != nil || decodeErr != nil || len(actual) != 3 || actual[0] != float64(15) || actual[1] != float64(3) {
			t.Fatalf("scientific Flow: %q %v", out, err)
		}
	})
	t.Run("metadata", func(t *testing.T) {
		source := filepath.Join(dir, "deps.lip")
		writeTestFile(t, source, `require python "math" require go "example.com/adapter" require host "fetch" flow Deps() -> number { return 1 }`)
		out, err := command(lipc, "check", source)
		if err != nil {
			t.Fatal(err)
		}
		for _, dep := range []string{"python:math", "go:example.com/adapter", "host:fetch"} {
			if !strings.Contains(out, "require: "+dep) {
				t.Fatal(out)
			}
		}
		library := filepath.Join(dir, "deps.go")
		writeTestFile(t, library, generatedSource(t, `require python "math" require go "example.com/adapter" require host "fetch" flow Deps() -> number { return 1 }`, false))
		metadataMain := filepath.Join(dir, "deps_main.go")
		writeTestFile(t, metadataMain, `package main
import "fmt"
func main(){for _,dependency:=range RequiredDependencies(){fmt.Printf("require: %s:%s\n",dependency.Kind,dependency.Spec)}}`)
		queried, queryErr := command("go", "run", library, metadataMain)
		if queryErr != nil || strings.Join(strings.Split(out, "\n")[1:], "\n") != queried {
			t.Fatalf("check/library metadata differ: %q vs %q (%v)", out, queried, queryErr)
		}
		out, err = command(lipc, "run", source)
		if err == nil || !strings.Contains(out, "use library mode") {
			t.Fatalf("standalone adapters: %q %v", out, err)
		}
	})
}

func TestAlpha05DynamicOutputFailure(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `require host "fetch" flow Dynamic() -> number { return fetch() }`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"strings";"testing";"lipalpha/runtime")
func TestDynamic(t *testing.T){
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}}{
  host:=runtime.DefaultHost()
  host.RegisterPure("fetch",func(_ context.Context,_ []runtime.Value)runtime.Result{ch:=make(chan runtime.Result,1);ch<-runtime.Ready("wrong");close(ch);return runtime.Await(ch)})
  value,trace,err:=run(context.Background(),host,nil)
  if value!=nil||err==nil||!strings.Contains(err.Error(),"expected number")||len(trace)<2||trace[len(trace)-1].Status!=runtime.Error{t.Fatalf("value=%v trace=%v err=%v",value,trace,err)}
 }
}
`)
	cmd := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dynamic output: %v\n%s", err, out)
	}
}

func TestAlpha05OptionalAndExpressions(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	for _, tc := range []struct{ name, source, want string }{
		{"optional", `flow Optional() -> number? { when false { return 2 } }`, "null\n"},
		{"lazy if", `flow Lazy() -> number { return if true then -2 * 3 else 1 / 0 }`, "-6\n"},
		{"short circuit and", `flow And() -> bool { return false && (1 / 0 > 0) }`, "false\n"},
		{"short circuit or", `flow Or() -> bool { return true || (1 / 0 > 0) }`, "true\n"},
		{"large number", `flow Large() -> number { return 1e100 }`, "1e+100\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "main.go")
			writeTestFile(t, path, generatedSource(t, tc.source, true))
			cmd := exec.Command("go", "run", path)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != tc.want {
				t.Fatalf("%q %v", out, err)
			}
		})
	}
}
