package tests

import (
	"bytes"
	"context"
	"fmt"
	. "lip/script"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type exampleCase struct {
	path, input, want string
	entry             string
	demo, snapshot    bool
}

var examples = []exampleCase{
	{path: "basics/01_hello.lip", want: `"Hello, LIP!"`},
	{path: "basics/02_bindings.lip", want: `{"subtotal":36,"total":31}`},
	{path: "basics/03_arithmetic.lip", want: `{"division":3.5,"grouped":20,"inverse_power":0.125,"logarithm":3,"logic":true,"negative_base":9,"negative_power":-9,"power":8,"power_chain":512,"quotient":-2,"remainder":1,"sum":14}`},
	{path: "basics/04_containers.lip", want: `{"every_other":[1,3,5,7],"fields":["name","role"],"first":"Ada","has_age":false,"last":"Lin","names":["Ada","Lin","Sam"],"original_role":"reader","reversed":["Lin","Ada"],"selected":["Lin","Ada","Lin"],"updated":{"name":"Ada","role":"writer"}}`},
	{path: "basics/05_strings_numbers.lip", want: `{"display":"score=0.8","length":3,"lexical_order":true,"middle":"𐀀","score":0.8,"words":["red","","blue"]}`},
	{path: "basics/22_multiline_strings.lip", want: `{"html":"\u000a<main>\u000a  <h1 title=\"Welcome\">Hello, LIP!</h1>\u000a</main>\u000a","prompt":"Read \"carefully\".\u000a# This is text, not a comment.\u000a","quotes":"Embedded triple quotes: \"\"\""}`},
	{path: "structures/06_conditions.lip", want: `{"grade":"ready","safe":false}`},
	{path: "structures/07_match.lip", want: `["empty","nonnegative","other","note","other"]`},
	{path: "structures/08_functions_closures.lip", want: `{"mapped":[11,12,13],"product":20}`},
	{path: "structures/09_map_fold.lip", want: `{"squares":[1,4,9,16],"total":30}`},
	{path: "structures/10_comprehensions.lip", want: `{"evens":[0,2,4,6],"reciprocals":[0.5,0.25]}`},
	{path: "structures/11_loops.lip", want: `[1,3]`},
	{path: "structures/12_carry.lip", want: `{"complete":6,"early":6}`},
	{path: "advanced/13_tail_calls.lip", want: `{"even":true,"indirect":0,"sum":5000050000}`},
	{path: "advanced/14_parallel_claim.lip", want: `{"count":3,"results":[{"input":3,"square":9},{"input":1,"square":1},{"input":2,"square":4}]}`},
	{path: "advanced/15_inputs.lip", input: `{"name":"Ada","scores":[0.6,0.9]}`, want: `{"accepted":[0.9],"message":"Hello, Ada"}`},
	{path: "advanced/16_snapshots.lip", snapshot: true},
	{path: "ecosystem/17_go_python.lip", demo: true, want: `{"digest":"a615eeaee21de5179de080de8c3052c8da901138406ba71c38c032845f7d54f4","mean":2}`},
	{path: "ecosystem/18_proposals.lip", demo: true, want: `{"average":2,"first":"HELLO","second":"HELLO, LIP"}`},
	{path: "ecosystem/19_parallel_read_write.lip", demo: true, want: `["ADA","LIN","SAM"]`},
	{path: "ecosystem/20_recorded_iteration.lip", demo: true, want: `{"rounds":4,"score":0.8}`},
	{path: "advanced/21_business_failures.lip", want: `{"failures":3,"visited":4}`},
	{path: "advanced/26_flows.lip", input: `{"xs":[3,1,2]}`, want: `{"count":3,"values":[5,3,4]}`},
	{path: "advanced/27_linear_model.lip", want: `{"intercept":1,"mse":0,"predictions":[9,11],"singular_rejected":true,"slope":2}`},
	{path: "refine.lip", want: `{"rounds":4,"score":0.8}`},
	{path: "evidence.lip", want: `{"missing":["b","c"]}`},
}

// Every educational example is verified against its documented result through
// both paths. Only live snapshots intentionally have a range of legal results.
func TestExamples(t *testing.T) {
	for _, example := range examples {
		t.Run(example.path, func(t *testing.T) {
			host := Host{}
			options := GoOptions{}
			if example.demo {
				if _, err := exec.LookPath("python3"); err != nil {
					t.Skip("demo Host needs Python 3")
				}
				var err error
				host, err = DemoHost()
				if err != nil {
					t.Fatal(err)
				}
				options.HostSource, err = DemoHostGoSource()
				if err != nil {
					t.Fatal(err)
				}
			}
			source, err := os.ReadFile(filepath.Join("..", "examples", example.path))
			if err != nil {
				t.Fatal(err)
			}
			program, err := CompileWithOptions(string(source), host, CompileOptions{Entry: example.entry})
			if err != nil {
				t.Fatal(err)
			}
			input := example.input
			if input == "" {
				input = "{}"
			}
			value, err := DecodeData([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := value.Fields()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			report, err := program.Run(ctx, fields, Options{Parallelism: 4, MaxSteps: 10000000, RunID: "example"})
			if err != nil {
				t.Fatal(err)
			}
			checkExample(t, example, display(t, report.Output))
			executable, _ := buildNative(t, program, options)
			for _, parallel := range []string{"1", "4"} {
				stdout, stderr, err := runNative(t, executable, "-input", input, "-parallel", parallel)
				if err != nil {
					t.Fatalf("compiled run: %v\n%s", err, stderr)
				}
				checkExample(t, example, strings.TrimSpace(stdout))
			}
		})
	}
}
func checkExample(t *testing.T, example exampleCase, actual string) {
	t.Helper()
	if !example.snapshot {
		if actual != example.want {
			t.Fatalf("got %s\nwant %s", actual, example.want)
		}
		return
	}
	value, err := DecodeData([]byte(actual))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := value.Fields()
	if num(fields["complete_count"]) != 3 || num(fields["observed_count"]) < 0 || num(fields["observed_count"]) > 3 {
		t.Fatal("invalid snapshot", actual)
	}
}
func buildNative(t *testing.T, program *Program, options GoOptions) (string, string) {
	t.Helper()
	source, err := program.EmitGo(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"func parse(", "func lex(", "switch e.tag", "switch s.tag {\n\tcase \"bind\""} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("compiled artifact retains interpreter code: %s", forbidden)
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, source, 0600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "program")
	args := []string{"build", "-o", executable, file}
	if os.Getenv("LIP_NATIVE_RACE") == "1" {
		args = append([]string{"build", "-race"}, args[1:]...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", args...).CombinedOutput(); err != nil {
		t.Fatalf("Go build: %v\n%s", err, out)
	}
	return executable, dir
}
func runNative(t *testing.T, executable string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func TestMainDefaultEntryInterpreterAndGo(t *testing.T) {
	source := `flow Other(x:number)->number{return x+10}
flow main()->number{return fail("lowercase entry must not run")}
flow Main(x:number)->number{return x+1}`
	for _, test := range []struct{ entry, want string }{{"", "3"}, {"Other", "12"}} {
		t.Run("entry="+test.entry, func(t *testing.T) {
			p, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: test.entry})
			if err != nil {
				t.Fatal(err)
			}
			if test.entry == "" {
				explicit, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: "Main"})
				if err != nil || display(t, p.BuildInfo()) != display(t, explicit.BuildInfo()) {
					t.Fatal("default Main changed build identity", err)
				}
			}
			report, err := p.Run(context.Background(), map[string]Value{"x": number(2)}, Options{})
			if err != nil || display(t, report.Output) != test.want {
				t.Fatal(report, err)
			}
			executable, _ := buildNative(t, p, GoOptions{})
			stdout, stderr, err := runNative(t, executable, "-input", `{"x":2}`)
			if err != nil || strings.TrimSpace(stdout) != test.want || stderr != "" {
				t.Fatal(stdout, stderr, err)
			}
		})
	}
}

func TestGoBackendDeclarationsAndEntryProtocol(t *testing.T) {
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "touch"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect,
		Invoke: func(context.Context, Request) Response { return Result(NullValue()) },
	}}}
	source := `import host "app"
flow First(CONFIG:number,y:string)->object {perform effect app.touch();return {local:CONFIG,y:y,static:read_config()}}
fn read_config()->object {CONFIG}
flow Other()->null {return null}
CONFIG:object={values:[-0,+2,null,true]}`
	program, err := CompileWithOptions(source, host, CompileOptions{Entry: "First"})
	if err != nil {
		t.Fatal(err)
	}
	options := GoOptions{HostSource: `package main
import("context";"fmt";"os")
func generatedHost()(Host,error){return Host{Contracts:[]Contract{{Operation:Operation{Language:"host",Path:"app",Member:"touch"},Version:"1",Result:Schema{Type:"null"},Effect:WriteEffect,Invoke:func(context.Context,Request)Response{fmt.Fprintln(os.Stderr,"provider called");return Result(NullValue())}}}},nil}
`}
	executable, _ := buildNative(t, program, options)
	valid := `{"positional":[7],"named":{"y":"a"}}`
	value, err := DecodeEntryInput([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	report, err := program.RunEntry(context.Background(), value, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runNative(t, executable, "-entry-input", valid, "-entry-output")
	if err != nil || strings.TrimSpace(stdout) != display(t, *report.EntryOutput) || stderr != "provider called\n" {
		t.Fatal(stdout, stderr, err)
	}
	for _, input := range []string{
		`{"positional":[7],"named":{}}`,
		`{"positional":[7],"named":{"CONFIG":8,"y":"a"}}`,
		`{"positional":[7],"named":{"y":"a","y":"b"}}`,
		`{"positional":[7],"named":{"y":1}}`,
		`{"positional":[7],"named":{"y":"a"},"extra":1}`,
	} {
		stdout, stderr, err := runNative(t, executable, "-entry-input", input, "-entry-output")
		if err == nil || stdout != "" || !strings.Contains(stderr, "E-SCHEMA") || strings.Contains(stderr, "provider called") {
			t.Fatalf("%s: stdout=%q stderr=%q err=%v", input, stdout, stderr, err)
		}
	}
	if stdout, stderr, err := runNative(t, executable, "-input", "{}", "-entry-input", valid); err == nil || stdout != "" || strings.Contains(stderr, "provider called") {
		t.Fatal("ambiguous input format executed", stdout, stderr, err)
	}
	for _, input := range []string{`{"CONFIG":7,"y":"a","y":"b"}`, `{"CONFIG":7,"y":"\ud800"}`, `{"CONFIG":7,"y":`, `[]`, `{},"positional":[]`} {
		stdout, stderr, err := runNative(t, executable, "-input", input)
		if err == nil || stdout != "" || !strings.Contains(stderr, "E-SCHEMA") || strings.Contains(stderr, "provider called") {
			t.Fatalf("named input %s: stdout=%q stderr=%q err=%v", input, stdout, stderr, err)
		}
	}
	for _, test := range []struct{ source, want string }{
		{`flow Main()->null {return null}`, `{"kind":"value","value":null}`},
		{"", `{"kind":"void"}`},
	} {
		program, err := Compile(test.source, Host{})
		if err != nil {
			t.Fatal(err)
		}
		executable, _ := buildNative(t, program, GoOptions{})
		stdout, stderr, err := runNative(t, executable, "-entry-output")
		if err != nil || strings.TrimSpace(stdout) != test.want || stderr != "" {
			t.Fatal(stdout, stderr, err)
		}
	}
}

func TestWebExamplesInterpreterAndGo(t *testing.T) {
	input := `{"request":{"params":{"name":"Ada"},"query":{"tag":["one","two"]},"json":{"value":3}}}`
	value, err := DecodeData([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := value.Fields()
	for _, name := range []string{"23_home", "24_echo", "25_greeting"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "examples", "web", name+".lip"))
			if err != nil {
				t.Fatal(err)
			}
			p, err := Compile(string(source), Host{})
			if err != nil {
				t.Fatal(err)
			}
			report, err := p.Run(context.Background(), fields, Options{})
			if err != nil {
				t.Fatal(err)
			}
			executable, _ := buildNative(t, p, GoOptions{})
			stdout, stderr, err := runNative(t, executable, "-input", input)
			if err != nil || strings.TrimSpace(stdout) != display(t, report.Output) {
				t.Fatal(stdout, stderr, err)
			}
		})
	}
}

func TestGoBackendNumericErrorsAndBudget(t *testing.T) {
	cases := []struct {
		source string
		flags  []string
	}{
		{`flow Main()->object{return {q:1//0.1,r:1%0.1,negative:-1e-17%1,subnormal:parse_number("3e-324")}}`, nil},
		{`flow Main()->number{return if false {1/0} else {7}}`, nil},
		{`flow Main()->number{x=0;return 1/x}`, nil},
		{`flow Main()->number{return parse_number("01")}`, nil},
		{`flow Main()->number{return {for {}; 1}}`, []string{"-steps", "20"}},
		{`fn identity(x:any?)->any?{x} flow Main()->any?{return map([null,3,"hello"],identity)}`, nil},
		{`flow Main()->number{f=[fn(x:any)->any{x}][0];return f("wrong")}`, nil},
		{`flow Main()->any{f=[fn(x:number)->number{x}][0];return f(1,2)}`, nil},
		{`flow Main()->any{f=[fn(x:number)->number{x}][0];return f("wrong")}`, nil},
		{`flow Main()->any{f=[fn(x:any)->number{x}][0];return f("wrong")}`, nil},
		{`fn outer(f:function,x:any)->number{f(x)} fn inner(x:any)->string{x} flow Main()->any{return outer(inner,true)}`, nil},
	}
	for i, tt := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p, err := Compile(tt.source, Host{})
			if err != nil {
				t.Fatal(err)
			}
			options := Options{MaxSteps: 10000000}
			if len(tt.flags) > 0 {
				options.MaxSteps = 20
			}
			report, wantError := p.Run(context.Background(), nil, options)
			executable, _ := buildNative(t, p, GoOptions{})
			stdout, stderr, err := runNative(t, executable, tt.flags...)
			if wantError == nil {
				if err != nil || strings.TrimSpace(stdout) != display(t, report.Output) {
					t.Fatalf("native result: %v %s %s", err, stdout, stderr)
				}
			} else {
				if err == nil || stdout != "" || !strings.Contains(stderr, wantError.Error()) {
					t.Fatalf("native error: %v %q; want %v", err, stderr, wantError)
				}
			}
		})
	}
}

func TestGoBackendNonTailFrameBudget(t *testing.T) {
	p, err := Compile(frameBudgetSource, Host{})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, tt := range frameBudgetCases {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"n":%d,"mode":%q}`, tt.n, tt.mode)
			flags := []string{"-input", input, "-entry-output"}
			if tt.frames != 0 {
				flags = append(flags, "-frames", fmt.Sprint(tt.frames))
			}
			stdout, stderr, err := runNative(t, executable, flags...)
			if tt.code == "" {
				if err != nil || strings.TrimSpace(stdout) != `{"kind":"value","value":`+tt.want+`}` || stderr != "" {
					t.Fatalf("native result: %v %q %q", err, stdout, stderr)
				}
				return
			}
			if tt.frames < 0 {
				if err == nil || stdout != "" || !strings.Contains(stderr, "-frames must be nonnegative") {
					t.Fatalf("invalid native configuration: %v %q %q", err, stdout, stderr)
				}
				return
			}
			value, decodeErr := DecodeData([]byte(input))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			fields, _ := value.Fields()
			_, wantError := p.Run(context.Background(), fields, Options{MaxFrames: tt.frames, MaxSteps: 2000000})
			if err == nil || stdout != "" || wantError == nil || strings.TrimSpace(stderr) != wantError.Error() {
				t.Fatalf("native error: %v %q %q; want %v", err, stdout, stderr, wantError)
			}
		})
	}
}

func TestGoBackendArbitraryParallelism(t *testing.T) {
	host := Host{Contracts: []Contract{{
		Operation: Operation{Language: "host", Path: "probe", Member: "read"}, Version: "1",
		Params: []Parameter{{Name: "x", Schema: Schema{Type: "number"}}, {Name: "target", Schema: Schema{Type: "number"}}, {Name: "limit", Schema: Schema{Type: "number"}}},
		Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true,
		Invoke: func(_ context.Context, request Request) Response { return Result(request.Positional[0]) },
	}}}
	p, err := Compile(`import host "probe"
flow Main(n:number,target:number,limit:number)->number {
 facts=claim()
 for x in range(n) parallel {value=perform effect probe.read(x,target,limit); facts <+ value}
 return fold([row.value for row in facts],0,fn(acc:number,x:number)->number{acc+x})
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{HostSource: `package main
import("context";"sync/atomic")
func generatedHost()(Host,error){
 var active,arrivals atomic.Int64
 release:=make(chan struct{})
 return Host{Contracts:[]Contract{{
  Operation:Operation{Language:"host",Path:"probe",Member:"read"},Version:"1",
  Params:[]Parameter{{Name:"x",Schema:Schema{Type:"number"}},{Name:"target",Schema:Schema{Type:"number"}},{Name:"limit",Schema:Schema{Type:"number"}}},
  Result:Schema{Type:"number"},Effect:ReadOnlyEffect,Concurrent:true,
  Invoke:func(ctx context.Context,request Request)Response{
   current:=active.Add(1);defer active.Add(-1)
   if current>int64(request.Positional[2].n){return Rejected("limit","active provider limit exceeded")}
   if arrivals.Add(1)==int64(request.Positional[1].n){close(release)}
   select{case <-release:return Result(request.Positional[0]);case <-ctx.Done():return Uncertain("overlap target not reached")}
  },
 }}},nil
}
`})
	for _, limit := range []int{3, 7, 17, 1238, 73295, int(^uint(0) >> 1)} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			items := min(limit, 1301)
			if limit < 100 {
				items = limit + 5
			}
			target := min(items, limit)
			input := fmt.Sprintf(`{"n":%d,"target":%d,"limit":%d}`, items, target, target)
			stdout, stderr, err := runNative(t, executable, "-parallel", fmt.Sprint(limit), "-input", input)
			want := fmt.Sprint(items * (items - 1) / 2)
			if err != nil || stderr != "" || strings.TrimSpace(stdout) != want {
				t.Fatalf("limit %d: %q %q %v; want %s", limit, stdout, stderr, err, want)
			}
		})
	}
	if stdout, stderr, err := runNative(t, executable, "-parallel", "-1"); err == nil || stdout != "" || !strings.Contains(stderr, "-parallel must be nonnegative") {
		t.Fatalf("invalid parallelism: %q %q %v", stdout, stderr, err)
	}
}

func TestGoBackendPowerLogarithm(t *testing.T) {
	p, err := Compile(`flow Main(a:any,b:any,kind:string)->number {
 return if kind == "power" { a ** b } else { a */ b }
}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, input := range []string{
		`{"a":2,"b":3,"kind":"power"}`, `{"a":8,"b":2,"kind":"log"}`,
		`{"a":2,"b":0.5,"kind":"power"}`, `{"a":27,"b":0.3333333333333333,"kind":"power"}`,
		`{"a":2,"b":-1074,"kind":"power"}`, `{"a":2,"b":-1075,"kind":"power"}`,
		`{"a":5e-324,"b":0.5,"kind":"power"}`, `{"a":0,"b":0,"kind":"power"}`,
		`{"a":-2,"b":-3,"kind":"power"}`, `{"a":-1,"b":1e30,"kind":"power"}`,
		`{"a":0.5,"b":1e30,"kind":"power"}`, `{"a":2,"b":1e30,"kind":"power"}`,
		`{"a":2,"b":1024,"kind":"power"}`, `{"a":0,"b":-1,"kind":"power"}`,
		`{"a":-2,"b":0.5,"kind":"power"}`, `{"a":"2","b":3,"kind":"power"}`,
		`{"a":2,"b":1,"kind":"log"}`, `{"a":2,"b":0,"kind":"log"}`,
		`{"a":0,"b":2,"kind":"log"}`, `{"a":2,"b":3,"kind":"log"}`,
		`{"a":10,"b":2,"kind":"log"}`, `{"a":1.0000000000000002,"b":2,"kind":"log"}`,
		`{"a":5e-324,"b":1.0000000000000002,"kind":"log"}`, `{"a":8,"b":true,"kind":"log"}`,
	} {
		value, err := DecodeData([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := value.Fields()
		r, wantError := p.Run(context.Background(), fields, Options{})
		stdout, stderr, err := runNative(t, executable, "-input", input)
		if wantError == nil {
			if err != nil || strings.TrimSpace(stdout) != display(t, r.Output) {
				t.Fatalf("%s: native %q %s %v", input, stdout, stderr, err)
			}
		} else if err == nil || stdout != "" || !strings.Contains(stderr, wantError.Error()) {
			t.Fatalf("%s: native error %v %q, want %v", input, err, stderr, wantError)
		}
	}
}

func TestDynamicFlowOutputErrorBelongsToReturnNode(t *testing.T) {
	p, err := Compile(`flow Main()->number { f=[fn(x:any)->any{x}][0]; return f("wrong") }`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 4})
	if err == nil || report.Error.Code != "E-SCHEMA" || len(report.Error.Position) == 0 {
		t.Fatal("output error lost its node identity", err)
	}
	found := false
	for _, node := range report.Nodes {
		if node.Status == Error && node.Error != nil && node.Error.Code == "E-SCHEMA" {
			found = true
		}
	}
	if !found {
		t.Fatal("return node incorrectly completed")
	}
}

// The Claim conflict fixture needs redelivery of an internal JoinID, which a
// valid LIP source program cannot generate twice. Compile a tiny standalone
// runtime and test the delivery boundary there, keeping all tests under tests/.
func TestNativeClaimConflictFixture(t *testing.T) {
	p, err := CompileWithOptions(`BASE:object={value:2}
flow Child()->number{value=BASE.value;return value}
flow Items()->list{return [1,2]}
flow Main()->number{first=Child();later=3;for x in Items(){unused=x};return first+later}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	_, dir := buildNative(t, p, GoOptions{})
	fixture, err := os.ReadFile("fixtures/claim_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "main_test.go"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module nativefixture\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"test", "."}
	if os.Getenv("LIP_NATIVE_RACE") == "1" {
		args = []string{"test", "-race", "."}
	}
	command := exec.Command("go", args...)
	command.Dir = dir
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native conflict fixture: %v\n%s", err, out)
	}
}

func TestGoHostContractDriftRejectedBeforeCalls(t *testing.T) {
	host, err := DemoHost()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(`import go "strings" as text
flow Main()->string{return perform effect text.ToUpper("hello")}`, host)
	if err != nil {
		t.Fatal(err)
	}
	hostSource, err := DemoHostGoSource()
	if err != nil {
		t.Fatal(err)
	}
	hostSource = regexp.MustCompile(`Version:\s*"1"`).ReplaceAllString(hostSource, `Version: "changed"`)
	executable, _ := buildNative(t, p, GoOptions{HostSource: hostSource})
	stdout, stderr, err := runNative(t, executable)
	if err == nil || stdout != "" || !strings.Contains(stderr, "compiled operation contract changed") {
		t.Fatalf("contract drift: %v %s %s", err, stdout, stderr)
	}
}

func TestGoBackendStructuredFlows(t *testing.T) {
	source := `fn plus(x:number,delta:number)->number{x+delta}
fn down(n:number)->number{if n==0 {0} else {n-1 |>down}}
flow Read(x:number)->number {facts=claim();for n in [x,x+1] parallel {facts <+ n};return len(facts)+x}
flow Source()->list{return [1,2]}
flow Main()->object {
 values=[x |>Read |>plus(delta=3) for x in Source()]
 nested={a=Read(4);Read(a)}
 return {values:values,nested:nested,tail:100000 |>down}
}`
	p, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, parallel := range []string{"1", "4"} {
		stdout, stderr, err := runNative(t, executable, "-parallel", parallel)
		if err != nil || strings.TrimSpace(stdout) != display(t, report.Output) {
			t.Fatal(stdout, stderr, err)
		}
	}
}

func TestGoBackendStaticModules(t *testing.T) {
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "local", Text: `import lip "./lib.lip" as lib
flow Main()->object{return {value:3 |>lib.Adjust,closure:lib.make(2)(3)}}`},
		"lib.lip": {Version: "1", Text: `BASE:number=7
export fn make(x:number)->function{fn(y:number)->number{x+y+BASE}}
export flow Adjust(x:number)->number{return x+BASE}`},
	}}
	p, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != display(t, report.Output) {
		t.Fatal(stdout, stderr, err)
	}
}

// One frozen diamond dependency exercises the same dynamic contexts on both
// execution paths; modes include gated calls and runtime Data boundary faults.
func TestSnapshotProposalComposition(t *testing.T) {
	source := `flow Collect(value:number)->list {
 facts=claim()
 facts <+ value
 complete=facts
 request={len(complete);effect claim.snapshot("facts")}
 reader=fn()->effect{request}
 boxed=map([reader()],fn(p:effect)->effect{p})
 selected=for x in [0] with p=boxed[::-1][[0,0]][-1] {next p}
 observed=perform selected
 return [r.value for r in observed]
}
flow Main()->list {return [Collect(1),Collect(2)]}`
	p, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, parallel := range []int{1, 7} {
		journal := NewMemoryJournal()
		for replay := 0; replay < 2; replay++ {
			report, err := p.Run(context.Background(), nil, Options{RunID: "snapshot-composition", Parallelism: parallel, Journal: journal})
			if err != nil || display(t, report.Output) != `[[1],[2]]` || len(journal.Records()) != 2 {
				t.Fatal("snapshot identity or replay changed", report, err, journal.Records())
			}
		}
		stdout, stderr, err := runNative(t, executable, "-parallel", fmt.Sprint(parallel))
		if err != nil || strings.TrimSpace(stdout) != `[[1],[2]]` {
			t.Fatal(stdout, stderr, err)
		}
	}
}

func TestCoreCompositionConformance(t *testing.T) {
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "local", Text: `import lip "./left.lip" as left
import lip "./right.lip" as right
BASE:number=100
fn decorate(x:number,delta:number)->number{x+delta}
flow Main(mode:string)->any {
 facts=claim()
 values=for x in [1,2] with total=[] {value=x |>left.Work |>decorate(delta=1);next concat(total,[value])}
 for x in right.Items() parallel {value=left.Work(x);facts <+ value}
 if mode=="void" {0 |>left.Empty}
 return match mode {
  "ok" => {values:values,facts:[row.value for row in facts],closure:right.make(3)(4),own:BASE,checked:decorate(left.Work(1),0),shadow:{Work=fn(x:number)->number{x+10};Work(1)}},
  "empty" => [x |>left.Work for x in []],
  "skip" => if false {left.Fail()} else {null},
  "void" => null,
  "alias" => [{n:1}] |>left.Keep,
  "closure_pipeline" => {direct:(fn(xs:list)->number{xs |>len})([1,2]),captured:right.make_pipeline(3)(4),nested:map([1,2],fn(x:number)->number{x |>decorate(delta=x |>decorate(delta=1))})},
  "input" => left.Accept({f:fn()->number{1}}),
  "output" => left.BadReturn(),
  "stage_input" => 1 |>right.take(config={f:fn()->number{1}}),
  _ => left.Fail()
 }
}`},
		"left.lip": {Version: "1", Text: `import lip "./shared.lip" as base
export flow Work(x:number)->number {facts=claim();facts <+ base.echo(x);return (x |>base.scale)+len(facts)}
export flow Empty(x:number)->void{}
export flow Keep(x:list)->object {data=x;capture=fn()->list{data};facts=claim();for i in [0] {facts <+ capture()};return {data:data,snapshot:[row.value for row in facts]}}
export flow Accept(x:object)->object{return x}
export flow BadReturn()->object{return {f:fn()->number{1}}}
export flow Fail()->number{return fail("module child")}`},
		"right.lip": {Version: "1", Text: `import lip "./shared.lip" as base
export flow Items()->list{return [1,2]}
export fn make(x:number)->function{fn(y:number)->number{base.scale(x+y)}}
export fn make_pipeline(x:number)->function{fn(y:number)->number{x+y |>base.scale}}
export fn take(x:number,config:object)->number{fail("body must not run")}`},
		"shared.lip": {Version: "1", Text: `export BASE:number=2
export fn echo(facts:number)->number{facts}
fn double(x:number)->number{x*BASE}
export fn scale(x:number)->number{x |>double}`},
	}}
	p, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, test := range []struct{ mode, want, code string }{
		{"ok", `{"checked":3,"closure":14,"facts":[3,5],"own":100,"shadow":11,"values":[4,6]}`, ""},
		{"empty", "[]", ""}, {"skip", "null", ""}, {"void", "null", ""},
		{"alias", `{"data":[{"n":1}],"snapshot":[[{"n":1}]]}`, ""},
		{"closure_pipeline", `{"captured":14,"direct":2,"nested":[3,5]}`, ""},
		{"input", "", "E-SCHEMA"}, {"output", "", "E-SCHEMA"}, {"stage_input", "", "E-SCHEMA"}, {"error", "", "E-FAIL"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			input := `{"mode":"` + test.mode + `"}`
			value, _ := DecodeData([]byte(input))
			fields, _ := value.Fields()
			for _, parallel := range []int{1, 2, 4, 7, 1238, 73295} {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				report, runErr := p.Run(ctx, fields, Options{Parallelism: parallel, RunID: "composition"})
				cancel()
				stdout, stderr, nativeErr := runNative(t, executable, "-input", input, "-parallel", fmt.Sprint(parallel))
				if test.code == "" {
					if runErr != nil || nativeErr != nil || display(t, report.Output) != test.want || strings.TrimSpace(stdout) != test.want {
						t.Fatal(report, runErr, stdout, stderr, nativeErr)
					}
					seen := map[string]bool{}
					for _, n := range report.Nodes {
						key := display(t, n.Position.Wire())
						if seen[key] {
							t.Fatal("position collision", key)
						}
						seen[key] = true
					}
				} else if runErr == nil || report.Error == nil || report.Error.Code != test.code || nativeErr == nil || stdout != "" || !strings.Contains(stderr, runErr.Error()) {
					t.Fatal("interpreter/generated boundary mismatch", report, runErr, stdout, stderr, nativeErr)
				}
			}
		})
	}
}

// A complete decision must wait for every child check before publishing. Equal
// payloads from separate iterations remain separate facts, including on replay.
func TestEvidenceDecisionComposition(t *testing.T) {
	var mu sync.Mutex
	completed := 0
	var saved []string
	host := Host{Contracts: []Contract{
		{
			Operation: Operation{Language: "host", Path: "audit", Member: "read"}, Version: "1",
			Params: []Parameter{{Name: "item", Schema: Schema{Type: "number"}}, {Name: "mode", Schema: Schema{Type: "string"}}},
			Result: Schema{Type: "object", Fields: map[string]Schema{"ok": {Type: "bool"}, "score": {Type: "number"}}, Required: []string{"ok", "score"}},
			Effect: ReadOnlyEffect, Concurrent: true,
			Invoke: func(ctx context.Context, r Request) Response {
				item, _ := r.Positional[0].Float()
				mode, _ := r.Positional[1].Text()
				// Perturb completion order without requiring another worker to run.
				select {
				case <-time.After(time.Duration(4-item) * time.Millisecond):
				case <-ctx.Done():
					return Uncertain("check cancelled")
				}
				mu.Lock()
				completed++
				mu.Unlock()
				if mode == "fault" {
					return Result(textValue("malformed result"))
				}
				return Result(object(map[string]Value{"ok": BoolValue(mode != "business" || item != 2), "score": number(item / 10)}))
			},
		},
		{
			Operation: Operation{Language: "host", Path: "audit", Member: "save"}, Version: "1",
			Params: []Parameter{{Name: "item", Schema: Schema{Type: "number"}}, {Name: "expected", Schema: Schema{Type: "number"}}},
			Result: Schema{Type: "null"}, Effect: WriteEffect,
			Invoke: func(_ context.Context, r Request) Response {
				item, _ := r.Positional[0].Float()
				expected, _ := r.Positional[1].Float()
				mu.Lock()
				defer mu.Unlock()
				if completed != int(expected) {
					return Rejected("E-EARLY", "publish preceded complete evidence")
				}
				saved = append(saved, fmt.Sprintf("saved %g\n", item))
				return Result(NullValue())
			},
		},
	}}
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "1", Text: `import host "audit"
import lip "./checks.lip" as checks
fn summarize(rows:list)->object {
 results=[row.value for row in rows]
 rejected=[result.item for result in results if !result.accepted]
 {allowed:len(results)>0 && len(rejected)==0, rejected:rejected, values:[result.item for result in results]}
}
flow Main(items:list,minimum:number,mode:string,publish:bool)->object {
 facts=claim()
 for item in items parallel {
  checked=item |>checks.Inspect(minimum=minimum,mode=mode)
  facts <+ checked
 }
 snapshot=facts
 decision=snapshot |>summarize
 if publish && decision.allowed {
  for item in decision.values {perform effect audit.save(item,len(snapshot))}
 }
 return decision
}`},
		"checks.lip": {Version: "1", Text: `import host "audit"
fn read_request(item:number,mode:string)->effect {effect audit.read(item,mode)}
fn accepts(minimum:number)->function {fn(raw:object)->bool{raw.ok && raw.score>=minimum}}
fn classify(raw:object,item:number,minimum:number)->object {
 predicate=accepts(minimum)
 {item:item,accepted:predicate(raw)}
}
export flow Inspect(item:number,minimum:number,mode:string)->object {
 facts=claim()
 request=read_request(item,mode)
 raw=perform request
 checked=raw |>classify(item=item,minimum=minimum)
 facts <+ checked
 snapshot=facts
 return snapshot[0].value
}`},
	}}
	for _, test := range []struct{ name, before, after string }{
		{"child_write", "raw=perform request", "raw=perform request;perform effect audit.save(item,1)"},
		{"child_unknown_request", "request=read_request(item,mode)", "request=[effect audit.read(item,mode)][0]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			child := set.Sources["checks.lip"]
			child.Text = strings.Replace(child.Text, test.before, test.after, 1)
			invalid := SourceSet{Root: set.Root, Sources: map[string]Source{"main.lip": set.Sources["main.lip"], "checks.lip": child}}
			_, err := CompileSourceSet(invalid, host, CompileOptions{})
			if d, ok := err.(*Diagnostic); !ok || d.Code != "E-PARALLEL" {
				t.Fatal("child effects bypassed parallel admission", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if completed != 0 || len(saved) != 0 {
				t.Fatal("rejected composition reached the Host", completed, saved)
			}
		})
	}
	p, err := CompileSourceSet(set, host, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{HostSource: `package main
import("context";"fmt";"os";"sync";"time")
func generatedHost()(Host,error){
 var mu sync.Mutex
 completed:=0
 return Host{Contracts:[]Contract{
  {Operation:Operation{Language:"host",Path:"audit",Member:"read"},Version:"1",
   Params:[]Parameter{{Name:"item",Schema:Schema{Type:"number"}},{Name:"mode",Schema:Schema{Type:"string"}}},
   Result:Schema{Type:"object",Fields:map[string]Schema{"ok":{Type:"bool"},"score":{Type:"number"}},Required:[]string{"ok","score"}},
   Effect:ReadOnlyEffect,Concurrent:true,
   Invoke:func(ctx context.Context,r Request)Response{
    item,_:=r.Positional[0].Float();mode,_:=r.Positional[1].Text()
    select{case <-time.After(time.Duration(4-item)*time.Millisecond):case <-ctx.Done():return Uncertain("check cancelled")}
    mu.Lock();completed++;mu.Unlock()
    if mode=="fault"{return Result(textValue("malformed result"))}
    return Result(object(map[string]Value{"ok":BoolValue(mode!="business"||item!=2),"score":number(item/10)}))
   }},
  {Operation:Operation{Language:"host",Path:"audit",Member:"save"},Version:"1",
   Params:[]Parameter{{Name:"item",Schema:Schema{Type:"number"}},{Name:"expected",Schema:Schema{Type:"number"}}},
   Result:Schema{Type:"null"},Effect:WriteEffect,
   Invoke:func(_ context.Context,r Request)Response{
    item,_:=r.Positional[0].Float();expected,_:=r.Positional[1].Float()
    mu.Lock();defer mu.Unlock()
    if completed!=int(expected){return Rejected("E-EARLY","publish preceded complete evidence")}
    fmt.Fprintf(os.Stderr,"saved %g\n",item)
    return Result(NullValue())
   }},
 }},nil
}`})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "checks.lip"), []byte(set.Sources["checks.lip"].Text), 0600); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		mu.Lock()
		defer mu.Unlock()
		completed, saved = 0, nil
	}
	for _, test := range []struct {
		name, items, mode, minimum, want, writes, code string
		publish                                        bool
		reads                                          int
	}{
		{"publish_duplicates", "[3,1,3]", "ok", "0.1", `{"allowed":true,"rejected":[],"values":[3,1,3]}`, "saved 3\nsaved 1\nsaved 3\n", "", true, 3},
		{"low_score", "[3,1,3]", "ok", "0.2", `{"allowed":false,"rejected":[1],"values":[3,1,3]}`, "", "", true, 3},
		{"business_failure", "[3,2,1]", "business", "0.1", `{"allowed":false,"rejected":[2],"values":[3,2,1]}`, "", "", true, 3},
		{"inspect_only", "[3,1,3]", "ok", "0.1", `{"allowed":true,"rejected":[],"values":[3,1,3]}`, "", "", false, 3},
		{"empty", "[]", "ok", "0.1", `{"allowed":false,"rejected":[],"values":[]}`, "", "", true, 0},
		{"host_schema_fault", "[2]", "fault", "0.1", "", "", "E-SCHEMA", true, 1},
		{"child_input_fault", `["bad"]`, "ok", "0.1", "", "", "E-SCHEMA", true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"items":%s,"minimum":%s,"mode":%q,"publish":%t}`, test.items, test.minimum, test.mode, test.publish)
			value, err := DecodeData([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := value.Fields()
			for _, parallel := range []int{1, 2, 7} {
				reset()
				journal := NewMemoryJournal()
				var wantError error
				for replay := 0; replay < 2; replay++ {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					report, runErr := p.Run(ctx, fields, Options{RunID: "decision-composition", Parallelism: parallel, Journal: journal})
					cancel()
					wantError = runErr
					if test.code == "" {
						if runErr != nil || display(t, report.Output) != test.want {
							t.Fatal("interpreter", parallel, replay, report, runErr)
						}
					} else if runErr == nil || report.Error == nil || report.Error.Code != test.code || report.EntryOutput != nil {
						t.Fatal("interpreter boundary", parallel, replay, report, runErr)
					}
					mu.Lock()
					reads, writes := completed, strings.Join(saved, "")
					mu.Unlock()
					if reads != test.reads || writes != test.writes || len(journal.Records()) != test.reads+strings.Count(test.writes, "\n") {
						t.Fatal("incomplete evidence, reordered writes, or repeated calls", parallel, replay, reads, writes, journal.Records())
					}
				}
				stdout, stderr, nativeErr := runNative(t, executable, "-input", input, "-parallel", fmt.Sprint(parallel))
				if test.code == "" {
					if nativeErr != nil || strings.TrimSpace(stdout) != test.want || stderr != test.writes {
						t.Fatal("generated", parallel, stdout, stderr, nativeErr)
					}
				} else if nativeErr == nil || stdout != "" || !strings.Contains(stderr, wantError.Error()) || strings.Contains(stderr, "saved ") {
					t.Fatal("generated boundary", parallel, stdout, stderr, nativeErr)
				}
			}
			reset()
			session, err := NewSession(host, SessionOptions{ProjectRoot: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			nativeCell(t, session, set.Sources["main.lip"].Text, "")
			call := fmt.Sprintf("Main(%s,%s,%q,%t)", test.items, test.minimum, test.mode, test.publish)
			result, runErr := session.Eval(context.Background(), call, Options{Parallelism: 7})
			if test.code == "" {
				if runErr != nil || !result.HasValue || display(t, result.Value) != test.want {
					t.Fatal("session", result, runErr)
				}
			} else if d, ok := runErr.(*Diagnostic); !ok || d.Code != test.code {
				t.Fatal("session boundary", result, runErr)
			}
			mu.Lock()
			reads, writes := completed, strings.Join(saved, "")
			mu.Unlock()
			if reads != test.reads || writes != test.writes {
				t.Fatal("session action counts", reads, writes)
			}
		})
	}
}

func TestIndexAndSliceConformance(t *testing.T) {
	source := `flow Main(op:string,x:any,i:any,start:any,stop:any,step:any)->any {
 return match op {"index"=>x[i],"slice"=>x[start:stop:step],
 "composition"=>{f=[fn(x:number)->number{x+1},fn(x:number)->number{x*2}];chosen=f[::-1][[0,1,0]];{values:map(chosen,fn(g:function)->number{g(3)}),nested:range(10)[2:8:2][::-1][[0,-1,0]],total:fold(slice(range(5),null,null,-1),0,fn(a:number,b:number)->number{a+b})}},
 _=>slice(x,start,stop,step)}
}`
	p, err := Compile(source, Host{})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, test := range []struct{ op, x, i, start, stop, step, want, code string }{
		{"composition", "null", "null", "null", "null", "null", `{"nested":[6,2,6],"total":10,"values":[6,4,6]}`, ""},
		{"index", "[0,1,2,3,4]", "-1", "null", "null", "null", "4", ""},
		{"index", "[0,1,2,3,4]", "[3,-1,3,0]", "null", "null", "null", "[3,4,3,0]", ""},
		{"index", "[]", "[]", "null", "null", "null", "[]", ""},
		{"index", `"a𐀀b"`, "-2", "null", "null", "null", `"𐀀"`, ""},
		{"index", `{"x":7}`, `"x"`, "null", "null", "null", "7", ""},
		{"slice", "[0,1,2,3,4]", "null", "null", "null", "-1", "[4,3,2,1,0]", ""},
		{"slice", "[0,1,2,3,4]", "null", "1", "5", "2", "[1,3]", ""},
		{"slice", "[0,1,2,3,4]", "null", "99", "-99", "-2", "[4,2,0]", ""},
		{"slice", "[0,1,2,3,4]", "null", "null", "-1", "-1", "[]", ""},
		{"slice", "[0,1,2,3,4]", "null", "-4", "-1", "null", "[1,2,3]", ""},
		{"slice", "[0,1,2,3,4]", "null", "4", "1", "1", "[]", ""},
		{"slice", "[0,1,2,3,4]", "null", "1", "4", "-1", "[]", ""},
		{"slice", "[0,1,2,3,4]", "null", "-9007199254740991", "9007199254740991", "9007199254740991", "[0]", ""},
		{"slice", "[0,1,2,3,4]", "null", "9007199254740991", "-9007199254740991", "-9007199254740991", "[4]", ""},
		{"slice", `"a𐀀b"`, "null", "null", "null", "-1", `"b𐀀a"`, ""},
		{"slice", "[]", "null", "null", "null", "-1", "[]", ""},
		{"slice", `""`, "null", "null", "null", "-1", `""`, ""},
		{"builtin", "[0,1,2,3,4]", "null", "-99", "99", "2", "[0,2,4]", ""},
		{"builtin", `"a𐀀b"`, "null", "null", "null", "-1", `"b𐀀a"`, ""},
		{"index", "[0]", "-2", "null", "null", "null", "", "E-INDEX"},
		{"index", "[0]", "1", "null", "null", "null", "", "E-INDEX"},
		{"index", "[0]", "[0,1]", "null", "null", "null", "", "E-INDEX"},
		{"index", "[0]", "0.5", "null", "null", "null", "", "E-INDEX"},
		{"index", "[0]", "9007199254740992", "null", "null", "null", "", "E-INDEX"},
		{"index", "[0]", "[false]", "null", "null", "null", "", "E-TYPE"},
		{"index", `"a"`, "[0]", "null", "null", "null", "", "E-TYPE"},
		{"index", `{}`, `"missing"`, "null", "null", "null", "", "E-INDEX"},
		{"index", `{}`, "0", "null", "null", "null", "", "E-TYPE"},
		{"slice", "[0]", "null", "null", "null", "0", "", "E-VALUE"},
		{"slice", "[]", "null", "null", "null", "0", "", "E-VALUE"},
		{"slice", "[0]", "null", "null", `"x"`, "1", "", "E-TYPE"},
		{"slice", "[0]", "null", "0.5", "null", "1", "", "E-INDEX"},
		{"slice", "[0]", "null", "null", "null", "-9007199254740992", "", "E-INDEX"},
		{"builtin", "[0]", "null", "null", "null", "0", "", "E-VALUE"},
		{"slice", "{}", "null", "null", "null", "1", "", "E-TYPE"},
	} {
		input := fmt.Sprintf(`{"op":%q,"x":%s,"i":%s,"start":%s,"stop":%s,"step":%s}`, test.op, test.x, test.i, test.start, test.stop, test.step)
		data, err := DecodeData([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := data.Fields()
		report, runErr := p.Run(context.Background(), fields, Options{MaxElements: 100})
		stdout, stderr, nativeErr := runNative(t, executable, "-input", input, "-elements", "100")
		if test.code == "" {
			if runErr != nil || display(t, report.Output) != test.want || nativeErr != nil || strings.TrimSpace(stdout) != test.want {
				t.Fatalf("%s: interpreter=%v %v compiled=%q %s %v", input, report, runErr, stdout, stderr, nativeErr)
			}
		} else if runErr == nil || runErr.(*Diagnostic).Code != test.code || nativeErr == nil || stdout != "" || !strings.Contains(stderr, runErr.Error()) {
			t.Fatalf("%s: interpreter=%v compiled=%q %s %v", input, runErr, stdout, stderr, nativeErr)
		}
	}
	// Inputs are already allocated: only the new gather/slice can exceed this budget.
	for _, op := range []string{"index", "slice", "builtin"} {
		input := fmt.Sprintf(`{"op":%q,"x":[0,1,2,3],"i":[0,0,0],"start":null,"stop":null,"step":1}`, op)
		data, _ := DecodeData([]byte(input))
		fields, _ := data.Fields()
		_, err := p.Run(context.Background(), fields, Options{MaxElements: 2})
		stdout, stderr, nativeErr := runNative(t, executable, "-input", input, "-elements", "2")
		if err == nil || err.(*Diagnostic).Code != "E-BUDGET" || nativeErr == nil || stdout != "" || !strings.Contains(stderr, "E-BUDGET") {
			t.Fatal(op, err, stdout, stderr, nativeErr)
		}
	}
}
