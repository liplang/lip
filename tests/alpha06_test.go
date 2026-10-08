package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestAlpha06Diagnostics(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`flow Bad() -> object { return {x: 1, "x": 2} }`, "duplicate object key"},
		{`flow Bad() -> object { return {true: 1} }`, "object key"},
		{`flow Bad() -> bool { return !1 }`, "expects bool"},
		{`flow Bad() -> number { return len(1) }`, "len expects"},
		{`flow Bad() -> number { return len([], []) }`, "1 argument"},
		{`flow Bad() -> list { return range() }`, "1 to 3 arguments"},
		{`flow Bad() -> list { return range("0", 5) }`, "expects numbers"},
		{`flow Bad() -> number { return fold([], 0, missing) }`, "local pure function"},
		{`fn add(a: number) -> number { return a } flow Bad() -> number { return fold([], 0, add) }`, "2 parameters"},
		{`fn add(a: number, b: number) -> number { return a+b } flow Bad() -> number { return fold(1, 0, add) }`, "must be a list"},
		{`fn add(a: number, b: number) -> number { return a+b } flow Bad() -> number { return fold([], "bad", add) }`, "seed"},
		{`fn add(a: number, b: number) -> string { return str(a+b) } flow Bad() -> any { return fold([], 0, add) }`, "accumulator requires"},
		{`fn add(a: number, b: number) -> number { return a+b } flow Bad() -> number { return fold([], 0, add()) }`, "local pure function"},
		{`fn recurse(n: number) { return if n==0 { 0 } else { recurse(n-1) } } flow Bad() -> number { return recurse(1) }`, "explicit return type"},
		{`fn a(n: number) -> number { return b(n) } fn b(n: number) { return a(n) } flow Bad() -> number { return a(1) }`, "explicit return type"},
		{`flow Bad() -> list { return {} }`, "declared output"},
		{`flow Bad() -> number { return null }`, "declared output"},
		{`flow Bad() -> number { return if true { null } else { 1 } }`, "declared output"},
	} {
		_, err := compiler.ParseAndBuild(tc.source)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s\ngot %v, want %s", tc.source, err, tc.want)
		}
	}
}

func TestAlpha06LibraryComposition(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	source := `
fn add(a: number, b: number) -> number { return a+b }
fn fact(n: number) -> number { return if n<=1 { 1 } else { n*fact(n-1) } }
fn even(n: number) -> bool { return if n==0 { true } else { odd(n-1) } }
fn odd(n: number) -> bool { return if n==0 { false } else { even(n-1) } }
fn forever(n: number) -> number { return forever(n) }
fn compose(values: list) -> number { return fold(values, 0, add) }
flow Core(values: list, recurse: bool) -> object {
 seed = state(0)
 numbers = range(0, len(values))
 squares = [x*x for x in numbers]
 result = if recurse { forever(0) } else { compose(values)+seed }
 facts = [fact(x) for x in numbers]
 return {sum: result, squares: squares, facts: facts, even: even(4), unicode: "你好🌱"[2], missing: null}
}`
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, source, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"reflect";"strings";"testing";"lipalpha/runtime")
func TestComposition(t *testing.T){
 host:=runtime.DefaultHost()
 // Core builtins and reducers have fixed meaning, including inside local fn.
 for _,name:=range []string{"len","range","fold","str","add"}{host.Register(name,func(context.Context,[]runtime.Value)runtime.Result{return runtime.Failed(errors.New("host override invoked"))})}
 inputs:=map[string]runtime.Value{"values":[]int{1,2,3},"recurse":false}
 want:=map[string]runtime.Value{"sum":float64(6),"squares":[]runtime.Value{float64(0),float64(1),float64(4)},"facts":[]runtime.Value{float64(1),float64(1),float64(2)},"even":true,"unicode":"🌱","missing":nil}
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}} {
  got,_,err:=run(context.Background(),host,inputs);if err!=nil||!reflect.DeepEqual(got,want){t.Fatalf("value=%v err=%v",got,err)}
  ctx,cancel:=context.WithCancel(context.Background());cancel();if _,_,err:=run(ctx,host,inputs);!errors.Is(err,context.Canceled){t.Fatal(err)}
  if _,_,err:=run(context.Background(),host,map[string]runtime.Value{"values":[]runtime.Value{1,"bad"},"recurse":false});err==nil||!strings.Contains(err.Error(),"fold element 1"){t.Fatalf("dynamic fold: %v",err)}
  if _,_,err:=run(context.Background(),host,map[string]runtime.Value{"values":[]int{},"recurse":true});err==nil||!strings.Contains(err.Error(),"call depth exceeds 256"){t.Fatalf("recursion bound: %v",err)}
 }
 instance,err:=NewInstance(host,inputs);if err!=nil{t.Fatal(err)}
 if _,_,err:=instance.Tick(context.Background(),nil);err!=nil{t.Fatal(err)}
 _,trace,err:=instance.Tick(context.Background(),nil);if err!=nil{t.Fatal(err)}
 reused:=false;for _,event:=range trace{if event.Node=="result"&&strings.Contains(event.Reason,"reuse"){reused=true}};if !reused{t.Fatal(trace)}
 if err:=instance.SetState("seed",2);err!=nil{t.Fatal(err)}
 value,_,err:=instance.Tick(context.Background(),nil);if err!=nil||value.(map[string]runtime.Value)["sum"]!=float64(8){t.Fatalf("state: %v %v",value,err)}
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("library conformance: %v\n%s", err, out)
	}
}

func TestAlpha06InspectDependencies(t *testing.T) {
	graph, err := compiler.ParseAndBuild(`fn add(a: number,b: number)->number{return a+b}
flow Inspect(values: list, flag: bool) -> object {
 mapped = [{key: x, flag: !flag} for x in values]
 total = fold(values, 0, add)
 return {items: mapped, total: total}
}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(graph.Nodes[0].Deps, []string{"values", "flag"}) || !reflect.DeepEqual(graph.Nodes[1].Deps, []string{"values"}) {
		t.Fatal(graph.Nodes)
	}
	first, err := compiler.InspectJSON(graph)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiler.InspectJSON(graph)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("unstable inspection")
	}
	var inspected struct {
		Schema string
		Nodes  []struct {
			Name, Kind string
			Calls      []string
		}
	}
	if err := json.Unmarshal(first, &inspected); err != nil || inspected.Schema != "lip.graph.v1" || inspected.Nodes[0].Kind != "map" || !reflect.DeepEqual(inspected.Nodes[1].Calls, []string{"fold", "add"}) {
		t.Fatalf("inspection: %s %v", first, err)
	}
}

func TestAlpha06CLIAndTrace(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	lipc := filepath.Join(dir, "lipc")
	command := func(args ...string) (string, int) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), 0
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return string(out), exit.ExitCode()
		}
		t.Fatal(err)
		return "", -1
	}
	if out, code := command("go", "build", "-buildvcs=false", "-o", lipc, "./cmd/lipc"); code != 0 {
		t.Fatal(out)
	}
	core := filepath.Join(root, "examples", "core.lip")
	binary, emitted := filepath.Join(dir, "report"), filepath.Join(dir, "report.go")
	for _, args := range [][]string{{lipc, "build", "--output", binary, core}, {lipc, "build", "--emit-go", "--output", emitted, core}} {
		if out, code := command(args...); code != 0 {
			t.Fatal(out)
		}
	}
	for _, prefix := range [][]string{{binary}, {"go", "run", emitted}, {lipc, "run", core}} {
		for _, tc := range []struct{ input, want string }{{"[1,2,3]", "{\"average\":4,\"count\":3,\"total\":12,\"values\":[2,4,6]}\n"}, {"[]", "{\"average\":null,\"count\":0,\"total\":0,\"values\":[]}\n"}} {
			out, code := command(append(append([]string{}, prefix...), tc.input)...)
			if code != 0 || out != tc.want {
				t.Fatalf("%v: %q (%d)", prefix, out, code)
			}
		}
	}
	tracePath := filepath.Join(dir, "trace.json")
	for _, args := range [][]string{
		{"run", "--trace", tracePath, core, "[1,2,3]"},
		{"run", "--trace=" + tracePath, core, "[1,2,3]"},
	} {
		if out, code := command(append([]string{lipc}, args...)...); code != 0 || !strings.HasPrefix(out, "{\"average\"") {
			t.Fatalf("trace run %v: %q (%d)", args, out, code)
		}
	}
	// A tool option value can end in .lip without becoming the entry file.
	if out, code := command(lipc, "run", "--trace", filepath.Join(dir, "trace.lip"), core, "[]"); code != 0 || !strings.HasPrefix(out, "{\"average\":null") {
		t.Fatalf("option value mistaken for entry: %q (%d)", out, code)
	}
	for _, args := range [][]string{{"run", "--unknown"}, {"run"}} {
		if out, code := command(append([]string{lipc}, args...)...); code != 2 {
			t.Fatalf("tool usage %v: %q (%d)", args, out, code)
		}
	}
	if out, code := command(lipc, "run", "--help"); code != 0 || !strings.Contains(out, "Tool options go before the file") {
		t.Fatalf("run help: %q (%d)", out, code)
	}
	echo := filepath.Join(dir, "echo with spaces.lip")
	writeTestFile(t, echo, `flow Echo(value: string) -> string { return value }`)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", echo, "小林"}, "小林\n"},
		{[]string{"run", echo, "-1"}, "-1\n"},
		{[]string{"run", echo, "another.lip"}, "another.lip\n"},
		{[]string{"run", echo, "name=小林"}, "name=小林\n"},
		{[]string{"run", echo, "--trace"}, "--trace\n"},
		{[]string{"run", echo, "--help"}, "--help\n"},
		{[]string{"run", echo, "--"}, "--\n"},
	} {
		if out, code := command(append([]string{lipc}, tc.args...)...); code != 0 || out != tc.want {
			t.Fatalf("program arguments %v: %q (%d)", tc.args, out, code)
		}
	}
	pair := filepath.Join(dir, "pair.lip")
	writeTestFile(t, pair, `flow Pair(first: string, second: string) -> list { return [first, second] }`)
	if out, code := command(lipc, "run", pair, "小林", "--"); code != 0 || out != "[\"小林\",\"--\"]\n" {
		t.Fatalf("literal double hyphen input: %q (%d)", out, code)
	}
	if out, code := command(lipc, "run", core, "--trace", tracePath, "[1,2,3]"); code != 2 || !strings.Contains(out, "expected 1 argument, got 3") {
		t.Fatalf("tool option after entry must belong to program: %q (%d)", out, code)
	}
	readTrace := func(wantError bool) {
		t.Helper()
		data, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		var trace struct {
			Schema, Error string
			Events        []struct{ Node, Status string }
		}
		if err := json.Unmarshal(data, &trace); err != nil || trace.Schema != "lip.trace.v1" || len(trace.Events) == 0 || (trace.Error != "") != wantError {
			t.Fatalf("trace=%s err=%v", data, err)
		}
		if wantError {
			found := false
			for _, event := range trace.Events {
				if event.Status == "Error" {
					found = true
				}
			}
			if !found {
				t.Fatal("no failed lifecycle event")
			}
		}
	}
	readTrace(false)
	if out, code := command(lipc, "run", "--trace", tracePath, core, `[1,"bad"]`); code != 1 || !strings.Contains(out, "error:") {
		t.Fatalf("failed trace: %q (%d)", out, code)
	}
	readTrace(true)
	before, _ := os.ReadFile(tracePath)
	if out, code := command(lipc, "run", "--trace", tracePath, core, "{}"); code != 2 || !strings.Contains(out, "expected list") {
		t.Fatalf("input failure: %q (%d)", out, code)
	}
	after, _ := os.ReadFile(tracePath)
	if !bytes.Equal(before, after) {
		t.Fatal("input failure overwrote trace")
	}
	if out, code := command(lipc, "run", "--trace", dir, core, "[]"); code != 1 || !strings.Contains(out, "trace:") {
		t.Fatalf("trace write failure: %q (%d)", out, code)
	}
	if out, code := command(lipc, "run", "--trace", dir, core, `[1,"bad"]`); code != 1 || !strings.Contains(out, "trace:") || !strings.Contains(out, "error:") {
		t.Fatalf("trace failure hid execution failure: %q (%d)", out, code)
	}
	if out, code := command(lipc, "inspect", core); code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("CLI inspect: %q (%d)", out, code)
	}
	// Optional explicit null, Unicode indexing and len/fold composition in fn.
	for _, tc := range []struct{ source, want string }{
		{`flow Nil() -> number? { return null }`, "null\n"},
		{`flow Choice(flag: bool) -> number? { return if flag { 3 } else { null } }`, "null\n"},
		{`flow Data() -> object { return {text: "你好🌱"[1], size: len("你好🌱"), enabled: !false, "two words": [null, {}]} }`, "{\"enabled\":true,\"size\":3,\"text\":\"好\",\"two words\":[null,{}]}\n"},
	} {
		file := filepath.Join(dir, "small.lip")
		writeTestFile(t, file, tc.source)
		args := []string{lipc, "run", file}
		if strings.Contains(tc.source, "flag: bool") {
			args = append(args, "false")
		}
		out, code := command(args...)
		if code != 0 || out != tc.want {
			t.Fatalf("%s: %q (%d)", tc.source, out, code)
		}
	}
}

func TestLipcCommandExperience(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	lipc := filepath.Join(dir, "lipc")
	if runtime.GOOS == "windows" {
		lipc += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", lipc, "./cmd/lipc")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lipc: %v\n%s", err, out)
	}
	command := func(env []string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(lipc, args...)
		cmd.Dir, cmd.Env = dir, env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return stdout.String(), stderr.String(), code
	}
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		if stdout, stderr, code := command(nil, args...); code != 0 || stderr != "" || !strings.Contains(stdout, "usage: lipc") {
			t.Fatalf("overview %v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	for _, name := range []string{"run", "build", "check", "inspect", "version", "help"} {
		stdout, stderr, code := command(nil, "help", name)
		flagHelp, flagErr, flagCode := command(nil, name, "--help")
		if code != 0 || flagCode != 0 || stderr != "" || flagErr != "" || stdout != flagHelp || !strings.Contains(stdout, "usage: lipc "+name) {
			t.Fatalf("help %s: direct=%q (%d, %q), flag=%q (%d, %q)", name, stdout, code, stderr, flagHelp, flagCode, flagErr)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"rnu", "hello.lip"}, "unknown command"},
		{[]string{"hello.lip"}, "unknown command"},
		{[]string{"-h"}, "unknown option"},
		{[]string{"-v"}, "unknown option"},
		{[]string{"build", "-o", "hello", "hello.lip"}, "unknown option"},
		{[]string{"build", "-emit-go", "hello.lip"}, "unknown option"},
		{[]string{"run", "-trace", "trace.json", "hello.lip"}, "unknown option"},
		{[]string{"check", "-json", "hello.lip"}, "unknown option"},
		{[]string{"check", "hello.lip", "--json"}, "one source file"},
		{[]string{"help", "rnu"}, "unknown command"},
		{[]string{"version", "extra"}, "does not accept arguments"},
		{[]string{"build"}, "one source file"},
		{[]string{"inspect"}, "one source file"},
		{[]string{"migrate"}, "unknown command"},
		{[]string{"check", "--json", "--wat"}, "unknown option --wat"},
		{[]string{"build", "--output"}, "option --output requires a value"},
		{[]string{"build", "hello.lip", "--output"}, "one source file"},
		{[]string{"run", "--trace"}, "option --trace requires a value"},
	} {
		if stdout, stderr, code := command(nil, tc.args...); code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "lipc help") {
			t.Fatalf("usage %v: exit=%d stdout=%q stderr=%q", tc.args, code, stdout, stderr)
		}
	}
	source := `flow Hello(name: string) -> string { return name }`
	writeTestFile(t, filepath.Join(dir, "hello.lip"), source)
	writeTestFile(t, filepath.Join(dir, "without extension"), source)
	writeTestFile(t, filepath.Join(dir, "-hello.lip"), source)
	noGoEnv := append(os.Environ(), "PATH="+t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "hello.lip"}, "missing name:string"},
		{[]string{"run", "--trace", "rejected/trace.json", "hello.lip"}, "missing name:string"},
		{[]string{"run", "hello.lip", "a", "b"}, "expected 1 argument, got 2"},
	} {
		stdout, stderr, code := command(noGoEnv, tc.args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: lipc run hello.lip <name:string>") || strings.Contains(stderr, "lipc-run-") {
			t.Fatalf("input validation without Go %v: exit=%d stdout=%q stderr=%q", tc.args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "rejected")); !os.IsNotExist(err) {
		t.Fatalf("input error created a trace directory: %v", err)
	}
	for _, args := range [][]string{
		{"check", "without extension"},
		{"check", "--json", "without extension"},
		{"inspect", "without extension"},
		{"check", "--", "-hello.lip"},
		{"build", "--emit-go", "--output=generated source.lip", "hello.lip"},
		{"build", "--output=second.lip", "--emit-go", "hello.lip"},
		{"build", "--no-main", "--package", "hostflow", "hello.lip"},
	} {
		if stdout, stderr, code := command(noGoEnv, args...); code != 0 {
			t.Fatalf("source operations %v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if stdout, stderr, code := command(noGoEnv, "build", "--output", "program.go", "hello.lip"); code != 1 || stdout != "" || !strings.Contains(stderr, "requires Go") {
		t.Fatalf("output suffix changed build mode: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if err := os.MkdirAll(filepath.Join(dir, "existing directory.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := command(nil, "build", "--output", "existing directory.go", "hello.lip"); code != 0 || stderr != "" || !strings.Contains(stdout, "Hello") {
		t.Fatalf("directory suffix selected source mode: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	library, err := os.ReadFile(filepath.Join(dir, "Hello_generated.go"))
	if err != nil || !bytes.Contains(library, []byte("package hostflow")) || bytes.Contains(library, []byte("func main()")) {
		t.Fatalf("implicit library generation: %s, %v", library, err)
	}
	for _, alias := range []struct {
		name string
		link func(string, string) error
	}{{"hardlink.lip", os.Link}, {"symlink.lip", os.Symlink}} {
		if err := alias.link(filepath.Join(dir, "hello.lip"), filepath.Join(dir, alias.name)); err != nil {
			t.Logf("source alias %s unavailable: %v", alias.name, err)
			continue
		}
	}
	for _, output := range []string{"hello.lip", "hardlink.lip", "symlink.lip"} {
		if _, err := os.Stat(filepath.Join(dir, output)); err != nil {
			continue
		}
		for _, args := range [][]string{
			{"build", "--emit-go", "--output", output, "hello.lip"},
			{"run", "--trace", output, "hello.lip", "小林"},
		} {
			if stdout, stderr, code := command(noGoEnv, args...); code != 2 || stdout != "" || !strings.Contains(stderr, "would overwrite the source") {
				t.Fatalf("source preservation %v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "hello.lip")); err != nil || string(data) != source {
		t.Fatalf("source was modified: %s, %v", data, err)
	}
	writeTestFile(t, filepath.Join(dir, "bad.lip"), "flow Bad() -> string {\n    return missing\n}\n")
	if stdout, stderr, code := command(nil, "check", "bad.lip"); code != 1 || stdout != "" || !strings.Contains(stderr, "bad.lip:2:5:") || !strings.Contains(stderr, "2 |     return missing") || !strings.Contains(stderr, "hint:") {
		t.Fatalf("source diagnostic: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code := command(nil, "check", "--json", "bad.lip"); code != 1 || stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("structured diagnostic: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"build", "--no-main", "--output", "new source directory/", "hello.lip"},
		{"build", "--output", "new binary directory/", "hello.lip"},
		{"run", "--trace", "new traces/hello.json", "without extension", "小林"},
	} {
		if stdout, stderr, code := command(nil, args...); code != 0 || stderr != "" {
			t.Fatalf("paths %v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	for _, file := range []string{"new source directory/Hello_generated.go", "new traces/hello.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(dir, "new binary directory", "Hello")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command(binary, "小林")
	cmd.Env = noGoEnv
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "小林\n" {
		t.Fatalf("directory output executable: %q, %v", out, err)
	}
}

func TestLipcOutsideCheckout(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	installGopath := filepath.Join(t.TempDir(), "custom workspace")
	otherGopath := filepath.Join(t.TempDir(), "other workspace")
	lipc := filepath.Join(installGopath, "bin", "lipc")
	if runtime.GOOS == "windows" {
		lipc += ".exe"
	}
	build := exec.Command("go", "install", "./cmd/lipc")
	build.Dir = root
	build.Env = append(os.Environ(), "GOENV=off", "GOBIN=", "GOPATH="+installGopath+string(os.PathListSeparator)+otherGopath)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("install lipc into configured GOPATH: %v\n%s", err, out)
	}
	for _, foreignModule := range []bool{false, true} {
		name := "without-go-module"
		if foreignModule {
			name = "unrelated-go-module"
		}
		t.Run(name, func(t *testing.T) {
			project := filepath.Join(t.TempDir(), "project with spaces")
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, example := range []string{"core.lip", "strings.lip"} {
				data, err := os.ReadFile(filepath.Join(root, "examples", example))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(project, example), data, 0o444); err != nil {
					t.Fatal(err)
				}
			}
			if foreignModule {
				writeTestFile(t, filepath.Join(project, "go.mod"), "module unrelated.example\n\ngo 1.27\n\nrequire lipalpha v0.0.0\nreplace lipalpha => ./missing-runtime\n")
			}
			// Neither an enclosing workspace nor legacy module mode may change
			// which runtime an installed compiler builds against.
			workspace := filepath.Join(project, "go.work")
			writeTestFile(t, workspace, "go 1.27\nuse ./missing-module\n")
			buildTemp := filepath.Join(t.TempDir(), "build temp with spaces")
			if err := os.MkdirAll(buildTemp, 0o755); err != nil {
				t.Fatal(err)
			}
			env := append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GO111MODULE=off", "GOWORK="+workspace, "TMPDIR="+buildTemp)
			noGoEnv := append(append([]string{}, env...), "PATH="+t.TempDir())
			command := func(environment []string, binary string, args ...string) (string, string, int) {
				t.Helper()
				cmd := exec.Command(binary, args...)
				cmd.Dir, cmd.Env = project, environment
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				code := 0
				if err != nil {
					exit, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatal(err)
					}
					code = exit.ExitCode()
				}
				return stdout.String(), stderr.String(), code
			}
			for _, args := range [][]string{
				{"check", "core.lip"},
				{"inspect", "core.lip"},
				{"build", "--emit-go", "--no-main", "--package", "report", "--output", "generated/report.go", "core.lip"},
			} {
				if _, stderr, code := command(noGoEnv, lipc, args...); code != 0 || stderr != "" {
					t.Fatalf("without Go %v: exit=%d stderr=%s", args, code, stderr)
				}
			}
			wantCore := "{\"average\":4,\"count\":3,\"total\":12,\"values\":[2,4,6]}\n"
			for _, tc := range []struct {
				args []string
				want string
			}{
				{[]string{"run", "--trace", "trace.json", "core.lip", "[1,2,3]"}, wantCore},
				{[]string{"run", "strings.lip", " Rust, LIP, rust, ,你好 "}, "{\"count\":3,\"label\":\"rust / lip / 你好\",\"tags\":[\"rust\",\"lip\",\"你好\"]}\n"},
			} {
				stdout, stderr, code := command(env, lipc, tc.args...)
				if code != 0 || stdout != tc.want || stderr != "" {
					t.Fatalf("%v: exit=%d stdout=%q stderr=%s", tc.args, code, stdout, stderr)
				}
			}
			trace, err := os.ReadFile(filepath.Join(project, "trace.json"))
			if err != nil || !json.Valid(trace) {
				t.Fatalf("trace in caller directory: %s, %v", trace, err)
			}
			binary := filepath.Join(project, "output with spaces", "report")
			if _, stderr, code := command(env, lipc, "build", "--output", binary, "core.lip"); code != 0 || stderr != "" {
				t.Fatalf("build outside checkout: exit=%d stderr=%s", code, stderr)
			}
			if stdout, stderr, code := command(noGoEnv, binary, "[1,2,3]"); code != 0 || stdout != wantCore || stderr != "" {
				t.Fatalf("executable without Go: exit=%d stdout=%q stderr=%s", code, stdout, stderr)
			}
			if _, stderr, code := command(noGoEnv, lipc, "run", "core.lip", "[1,2,3]"); code == 0 || !strings.Contains(stderr, "requires Go 1.27 or newer") {
				t.Fatalf("missing toolchain diagnostic: exit=%d stderr=%s", code, stderr)
			}
			if _, stderr, code := command(env, lipc, "run", "core.lip", "invalid-json"); code != 2 || !strings.Contains(stderr, "argument 1 (values)") {
				t.Fatalf("program failure: exit=%d stderr=%s", code, stderr)
			}
			entries, err := os.ReadDir(buildTemp)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary builds were not cleaned up: %v, %v", entries, err)
			}
			t.Run("python-caller-directory", func(t *testing.T) {
				if _, err := exec.LookPath("python3"); err != nil {
					t.Skip("python3 unavailable")
				}
				// A project-local module and its relative file must remain
				// accessible even though Go compilation happens elsewhere.
				writeTestFile(t, filepath.Join(project, "local_helper.py"), "def read():\n    with open('message.txt', encoding='utf-8') as source:\n        return source.read()\n")
				writeTestFile(t, filepath.Join(project, "message.txt"), "来自调用目录")
				writeTestFile(t, filepath.Join(project, "python.lip"), "import python \"local_helper\"\nflow Local() -> string {\n message = local_helper.read()\n return message\n}\n")
				stdout, stderr, code := command(env, lipc, "run", "python.lip")
				if code != 0 || stdout != "来自调用目录\n" || stderr != "" {
					t.Fatalf("local Python module: exit=%d stdout=%q stderr=%s", code, stdout, stderr)
				}
			})
		})
	}
}
