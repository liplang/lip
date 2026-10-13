package tests

import (
	"bytes"
	"context"
	"fmt"
	. "lipbeta/script"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type exampleCase struct {
	path, input, want string
	demo, snapshot    bool
}

var examples = []exampleCase{
	{path: "basics/01_hello.lip", want: `"Hello, LIP!"`},
	{path: "basics/02_bindings.lip", want: `{"subtotal":36,"total":31}`},
	{path: "basics/03_arithmetic.lip", want: `{"division":3.5,"grouped":20,"inverse_power":0.125,"logarithm":3,"logic":true,"negative_base":9,"negative_power":-9,"power":8,"power_chain":512,"quotient":-2,"remainder":1,"sum":14}`},
	{path: "basics/04_containers.lip", want: `{"fields":["name","role"],"first":"Ada","has_age":false,"names":["Ada","Lin","Sam"],"original_role":"reader","updated":{"name":"Ada","role":"writer"}}`},
	{path: "basics/05_strings_numbers.lip", want: `{"display":"score=0.8","length":3,"lexical_order":true,"middle":"𐀀","score":0.8,"words":["red","","blue"]}`},
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
	{path: "ecosystem/18_proposals.lip", demo: true, want: `{"first":"HELLO","second":"HELLO, LIP"}`},
	{path: "ecosystem/19_parallel_read_write.lip", demo: true, want: `["ADA","LIN","SAM"]`},
	{path: "ecosystem/20_recorded_iteration.lip", demo: true, want: `{"rounds":4,"score":0.8}`},
	{path: "advanced/21_business_failures.lip", want: `{"failures":3,"visited":4}`},
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
			program, err := Compile(string(source), host)
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
	p, err := Compile(`flow Main()->void{}`, Host{})
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
