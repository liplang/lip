package tests

import (
	"bytes"
	"context"
	"errors"
	. "lip/script"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRetainsValuesWithoutRepeatingCalls(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, request Request) Response {
		calls.Add(1)
		return Result(request.Positional[0])
	})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	eval := func(source, want string) {
		t.Helper()
		result, err := s.Eval(ctx, source, Options{Parallelism: 4, MaxSteps: 100000})
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if want != "" && (!result.HasValue || display(t, result.Value) != want) {
			t.Fatalf("%s: got %v %s want %s", source, result.HasValue, display(t, result.Value), want)
		}
	}
	eval(`import python "example.worker" as worker`, "")
	eval("x = perform effect worker.read(7)", "7")
	eval("fn plus(n:number)->number { n + x }", "")
	eval("plus(3)", "10")
	eval("alias = plus", "")
	eval("alias(n=5)", "12")
	eval("proposal = effect worker.read(x + 1)", "")
	if calls.Load() != 1 {
		t.Fatalf("creating or reading saved values repeated a call: %d", calls.Load())
	}
	eval("out = perform proposal", "8")
	eval("map([1,2], plus)", "[8,9]")
	eval("for n in [1,2,3] with total=0 { next total+n }", "6")
	if names := s.Complete("worker.r"); len(names) != 1 || names[0] != "worker.read" {
		t.Fatalf("registered operation completions: %v", names)
	}
	if names := s.Complete("pl"); len(names) != 1 || names[0] != "plus" {
		t.Fatalf("saved closure completions: %v", names)
	}
	if calls.Load() != 2 {
		t.Fatalf("saved request was performed %d times", calls.Load())
	}
	values := s.Values()
	values["x"] = number(999)
	eval("plus(0)", "7")
	if _, err := s.Eval(ctx, "x = 9", Options{}); err == nil {
		t.Fatal("allowed rebinding a saved value")
	}
	if _, err := s.Eval(ctx, `newvalue = perform effect worker.read(9); fail("stop")`, Options{}); err == nil {
		t.Fatal("failed cell succeeded")
	}
	if _, saved := s.Values()["newvalue"]; saved || calls.Load() != 3 {
		t.Fatal("failed cell committed bindings or rolled back an external action")
	}
	if _, err := s.Eval(ctx, `import go "strings" as x`, Options{}); err == nil {
		t.Fatal("allowed an import to hide a saved value")
	}
	eval("x", "7")
	s.Reset()
	if len(s.Values()) != 0 {
		t.Fatal("reset did not clear saved values")
	}
	if _, err := s.Eval(ctx, "plus(1)", Options{}); err == nil {
		t.Fatal("reset retained a function")
	}
	if _, err := s.Eval(ctx, "perform effect worker.read(0)", Options{}); err == nil {
		t.Fatal("reset retained an import")
	}
}

func TestSessionPreservesProposalOps(t *testing.T) {
	for _, rhs := range []string{
		`[effect worker.read(0)][0]`,
		`{request:effect worker.read(0)}.request`,
		`(fn()->effect {effect worker.read(0)})()`,
		`for i in [1] with p=effect worker.read(0) {next p}`,
	} {
		t.Run(rhs, func(t *testing.T) {
			var calls atomic.Int64
			host := testHost(func(_ context.Context, r Request) Response {
				calls.Add(1)
				return Result(r.Positional[0])
			})
			s, err := NewSession(host)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for _, source := range []string{`import python "example.worker" as worker`, `p=` + rhs, `alias=p`} {
				if _, err := s.Eval(context.Background(), source, Options{}); err != nil {
					t.Fatal(source, err)
				}
			}
			for _, name := range []string{"p", "alias"} {
				_, err := s.Eval(context.Background(), `for i in [1] parallel {perform `+name+`}`, Options{Parallelism: 7})
				if d, ok := err.(*Diagnostic); !ok || d.Code != "E-PARALLEL" {
					t.Fatalf("%s strengthened Unknown Ops: %v", name, err)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("rejected parallel call reached provider")
			}
			if _, err := s.Eval(context.Background(), `perform alias`, Options{}); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("ordinary Unknown proposal was not performed")
			}
			for _, source := range []string{`direct=effect worker.read(2)`, `for i in [1] parallel {perform direct}`} {
				if _, err := s.Eval(context.Background(), source, Options{Parallelism: 7}); err != nil {
					t.Fatal(source, err)
				}
			}
		})
	}
}

func TestSessionDeleteBindings(t *testing.T) {
	var calls, alternate atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	other := host.Contracts[0]
	other.Operation.Path = "other.worker"
	other.Invoke = func(_ context.Context, r Request) Response {
		alternate.Add(1)
		return Result(number(num(r.Positional[0]) + 100))
	}
	host.Contracts = append(host.Contracts, other)
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eval := func(source, want string) {
		t.Helper()
		result, err := s.Eval(context.Background(), source, Options{Parallelism: 7})
		if err != nil || want != "" && (!result.HasValue || display(t, result.Value) != want) {
			t.Fatalf("%s: %+v, %v; want %s", source, result, err, want)
		}
	}
	eval(`import python "example.worker" as worker`, "")
	eval(`saved=perform effect worker.read(7)`, "7")
	eval(`fn capture()->number {saved}`, "")
	eval(`fn proposal()->effect {effect worker.read(5)}`, "")
	eval(`request=proposal()`, "")
	eval(`flow Source(x:number)->number {return x+saved}`, "")
	eval(`flow Wrapper(x:number)->number {return x |> Source}`, "")
	if err := s.Delete("saved", "Source", "worker"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"saved", "Source", "worker"} {
		if _, ok := s.Values()[name]; ok || len(s.Complete(name)) != 0 {
			t.Fatalf("deleted name remains visible: %s", name)
		}
		if _, err := s.Eval(context.Background(), name, Options{}); err == nil {
			t.Fatalf("deleted name remains bound: %s", name)
		}
	}
	eval(`Wrapper(1)`, "8")
	eval(`saved=100`, "100")
	eval(`flow Source(x:string)->string {return x}`, "")
	eval(`import python "other.worker" as worker`, "")
	eval(`Wrapper(1)`, "8")
	eval(`capture()`, "7")
	eval(`Source("new")`, `"new"`)
	eval(`perform proposal()`, "5")
	eval(`perform request`, "5")
	eval(`worker.read(1)`, "101")
	if calls.Load() != 3 || alternate.Load() != 1 {
		t.Fatalf("deletion replayed or redirected actions: original=%d alternate=%d", calls.Load(), alternate.Load())
	}
	for _, names := range [][]string{nil, {"saved", "absent"}, {"saved", "bad.name"}, {"saved", ""}, {"len"}} {
		if err := s.Delete(names...); err == nil {
			t.Fatalf("invalid deletion succeeded: %v", names)
		}
		eval(`saved`, "100")
	}
	if err := s.Delete("capture", "Source", "worker", "request", "saved", "Wrapper", "proposal"); err != nil {
		t.Fatal(err)
	}
	// Name reuse is independent of both the old and the new binding's kind.
	eval(`capture=[1];Source={n:2};worker=fn()->number{3};request:bool=true;saved=fn(n:number)->number{n}`, "")
	if err := s.Delete("capture", "Source", "worker", "request"); err != nil {
		t.Fatal(err)
	}
	eval(`flow capture()->number {return 4};fn Source()->number {5};worker:number=6;request=claim();request <+ 7;[row.value for row in request]`, "[7]")
	if err := s.Delete("request"); err != nil {
		t.Fatal(err)
	}
	eval(`request=8`, "8")
	eval(`facts=claim();facts <+ 9;snapshot=perform effect claim.snapshot("facts");[row.value for row in facts]`, "[9]")
	if err := s.Delete("facts", "snapshot"); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`facts=claim();if true {facts=3}`,
		`facts=claim();for facts in [1] {facts <+ 3}`,
		`facts=claim();f=fn(facts:number)->number{facts}`,
	} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatalf("session symbol lowering allowed live Claim shadowing: %s", source)
		} else if d, ok := err.(*Diagnostic); !ok || d.Code != "E-SHADOW-CLAIM" {
			t.Fatalf("expected Claim shadowing diagnostic, got %v", err)
		}
	}
	s.Reset()
	if err := s.Delete("request"); err == nil {
		t.Fatal("reset retained the public namespace")
	}
}

func TestSessionDeleteModuleAlias(t *testing.T) {
	dir := t.TempDir()
	for name, offset := range map[string]string{"old.lip": "2", "new.lip": "20"} {
		text := `export OFFSET:number=` + offset + `;export fn add(x:number)->number{x+OFFSET};export flow Read(x:number)->number{return x |> add}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewSession(Host{}, SessionOptions{ProjectRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eval := func(source, want string) {
		t.Helper()
		result, err := s.Eval(context.Background(), source, Options{})
		if err != nil || want != "" && display(t, result.Value) != want {
			t.Fatalf("%s: %+v, %v", source, result, err)
		}
	}
	eval(`import lip "./old.lip" as lib;fn keep(x:number)->number{lib.add(x)};flow Via(x:number)->number{return x |> lib.Read}`, "")
	if err := s.Delete("lib"); err != nil {
		t.Fatal(err)
	}
	if len(s.Complete("lib")) != 0 {
		t.Fatal("deleted module remains in completion")
	}
	eval(`lib=5`, "5")
	eval(`keep(1)+Via(1)`, "6")
	if err := s.Delete("lib"); err != nil {
		t.Fatal(err)
	}
	eval(`import lip "./new.lip" as lib`, "")
	eval(`lib.Read(1)+keep(1)+Via(1)`, "27")
	if names := s.Complete("lib.R"); len(names) != 1 || names[0] != "lib.Read" {
		t.Fatal(names)
	}
}

func TestSessionPipelineClosures(t *testing.T) {
	s, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eval := func(source, want string) {
		t.Helper()
		result, err := s.Eval(context.Background(), source, Options{})
		if err != nil || want != "" && (!result.HasValue || display(t, result.Value) != want) {
			t.Fatalf("%s: %+v, %v; want %s", source, result, err, want)
		}
	}
	eval(`OFFSET:number=2;fn plus(x:number,delta:number)->number{x+delta}`, "")
	eval(`count=fn(xs:list)->number{xs |>len |>plus(delta=OFFSET)}`, "")
	eval(`nested=fn(x:number)->number{x |>plus(delta=x |>plus(delta=OFFSET))}`, "")
	eval(`[count([1,2]),nested(3)]`, "[4,8]")
	if err := s.Delete("OFFSET", "plus"); err != nil {
		t.Fatal(err)
	}
	eval(`OFFSET=50;plus=100`, "100")
	eval(`[count([1,2]),nested(3)]`, "[4,8]")
}

func FuzzSessionTransactions(f *testing.F) {
	for _, source := range []string{
		`flow Read(x:number)->number{return x+audit_guard};Read(2)`,
		`fn add(x:number,delta:number)->number{x+delta};f=fn(x:number)->number{x |>add(delta=audit_guard)};f(2)`,
		`facts=claim();for x in [1,2] parallel {facts <+ x};[row.value for row in facts]`,
		`fn even(n:number)->bool{if n==0 {true} else {odd(n-1)}};fn odd(n:number)->bool{if n==0 {false} else {even(n-1)}};even(20)`,
		`uncommitted=3;fail("stop")`, `if true {`, `return`, `1 |>len`, "\xff", "",
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 16384 {
			t.Skip()
		}
		s, err := NewSession(Host{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		options := Options{Parallelism: 7, MaxSteps: 1000, MaxNodes: 1000, MaxElements: 1024, MaxFrames: 32}
		if _, err := s.Eval(context.Background(), `audit_guard=17`, options); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err = s.Eval(ctx, source, options)
		cancel()
		if err != nil && len(s.Values()) != 1 {
			t.Fatal("failed cell changed the saved namespace")
		}
		if err := s.Delete("audit_guard"); err != nil {
			t.Fatal("cell lost an existing binding", err)
		}
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := s.Eval(ctx, `audit_guard=23`, options)
		if err != nil || !result.HasValue || num(result.Value) != 23 {
			t.Fatal("session failed to recover or reuse a deleted name", result, err)
		}
	})
}

func TestSessionSnapshotProposalLifetime(t *testing.T) {
	var authorizations atomic.Int64
	s, err := NewSession(Host{Authorize: func(context.Context, Request) error {
		authorizations.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eval := func(source string) CellResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, err := s.Eval(ctx, source, Options{Parallelism: 7})
		if err != nil {
			t.Fatal(source, err)
		}
		return result
	}
	// Construction neither observes the store nor invokes Host authorization.
	result := eval(`facts=claim();p=effect claim.snapshot("facts");read=fn()->effect{p};facts <+ 7;copy=perform p;[r.value for r in facts]`)
	if display(t, result.Value) != `[7]` || len(result.Report.Journal.Records()) != 1 || authorizations.Load() != 1 {
		t.Fatal(result)
	}
	for _, source := range []string{`uncommitted=perform p`, `perform read()`, `perform [p][0]`, `flow Read()->list{return perform p};Read()`} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := s.Eval(ctx, source, Options{Parallelism: 7})
		cancel()
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-RESOURCE" || d.Phase != "runtime" || d.Line == 0 || len(d.Position) == 0 || result.Report.EntryOutput != nil {
			t.Fatalf("expired snapshot: %+v, %v", result, err)
		}
		if _, committed := s.Values()["uncommitted"]; committed {
			t.Fatal("failed snapshot cell committed a binding")
		}
		if records := result.Report.Journal.Records(); len(records) != 1 || records[0].KnownFault || records[0].Response.Outcome != RejectedOutcome || records[0].Response.Error.Code != "E-RESOURCE" {
			t.Fatal("expired snapshot was not an admission rejection", records)
		}
		if authorizations.Load() != 1 {
			t.Fatal("expired snapshot reached Host authorization")
		}
		eval(`1+2`)
	}
	if err := s.Delete("facts"); err != nil {
		t.Fatal(err)
	}
	// Reusing the public name cannot retarget the old request to a new store.
	eval(`facts=claim();facts <+ 9;fresh=perform effect claim.snapshot("facts");[r.value for r in facts]`)
	if _, err := s.Eval(context.Background(), `perform p`, Options{}); err == nil {
		t.Fatal("old request observed the replacement store")
	}
	if got := display(t, eval(`[r.value for r in facts]`).Value); got != `[9]` {
		t.Fatal("new Claim snapshot lost its fixed Data", got)
	}
}

func TestSessionDeleteReleasesUncapturedValues(t *testing.T) {
	s, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var memory runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memory)
	baseline := memory.HeapAlloc
	for _, source := range []string{
		`payload=range(300000)`,
		`fn independent(n:number)->number{n+1}`,
		`fn even(n:number)->bool{if n==0 {true} else {odd(n-1)}};fn odd(n:number)->bool{if n==0 {false} else {even(n-1)}}`,
	} {
		if _, err := s.Eval(context.Background(), source, Options{}); err != nil {
			t.Fatal(source, err)
		}
	}
	if err := s.Delete("payload"); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&memory)
	if retained := int64(memory.HeapAlloc) - int64(baseline); retained > 12<<20 {
		t.Fatalf("unrelated named functions retained deleted payload: %d bytes", retained)
	}
	result, err := s.Eval(context.Background(), `[independent(2),even(10000),odd(10001)]`, Options{MaxSteps: 1000000})
	if err != nil || display(t, result.Value) != `[3,true,true]` {
		t.Fatal(result, err)
	}
}

func TestSessionFunctionsAndRunBoundaries(t *testing.T) {
	s, _ := NewSession(Host{})
	ctx := context.Background()
	_, err := s.Eval(ctx, `fn even(n:number)->bool { if n==0 {true} else {odd(n-1)} }
fn odd(n:number)->bool { if n==0 {false} else {even(n-1)} }`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Eval(ctx, "even(100000)", Options{MaxFrames: 5, MaxSteps: 2000000})
	if err != nil || display(t, result.Value) != "true" {
		t.Fatal(err)
	}
	for _, source := range []string{"fn even()->number {1}", "break", "continue"} {
		if _, err := s.Eval(ctx, source, Options{}); err == nil {
			t.Fatal("accepted invalid session boundary", source)
		}
	}
	for _, source := range []string{"facts=claim()", "flow Main()->number{return 1}", "return 1", "OFFSET:number=2", "export fn inc(x:number)->number{x+1}"} {
		if _, err := s.Eval(ctx, source, Options{}); err != nil {
			t.Fatalf("valid interactive syntax %s: %v", source, err)
		}
	}
	if _, err := s.Eval(ctx, "1", Options{RunID: "reuse"}); err == nil {
		t.Fatal("accepted cross-cell run identity reuse")
	}
	result, err = s.Eval(ctx, "if true { 1 } else { 2 }", Options{})
	if err != nil || display(t, result.Value) != "1" {
		t.Fatal(err)
	}
}

func TestSessionRecoversFromFrameBudget(t *testing.T) {
	s, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.Eval(ctx, `fn count(n:number)->number {if n==0 {0} else {1+count(n-1)}}
saved=7`, Options{}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Eval(ctx, "uncommitted=count(10000)", Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-BUDGET" || result.HasValue || result.Report.EntryOutput != nil {
		t.Fatalf("exhausted cell: %+v, %v", result, err)
	}
	if _, present := s.Values()["uncommitted"]; present {
		t.Fatal("failed recursive call committed its binding")
	}
	for _, source := range []string{"count(2048)", "saved+count(1)"} {
		if result, err := s.Eval(ctx, source, Options{}); err != nil || !result.HasValue {
			t.Fatalf("session did not recover: %+v, %v", result, err)
		}
	}
}

func TestSessionDirectCallsKeepHostSemantics(t *testing.T) {
	var arguments []float64
	host := testHost(func(_ context.Context, request Request) Response {
		value := request.Named["value"]
		if len(request.Positional) != 0 {
			value = request.Positional[0]
		}
		arguments = append(arguments, num(value))
		return Result(value)
	})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	eval := func(source, want string) {
		t.Helper()
		result, err := s.Eval(ctx, source, Options{Parallelism: 1})
		if err != nil || want != "" && display(t, result.Value) != want {
			t.Fatalf("%s: %v, %s", source, err, display(t, result.Value))
		}
	}
	eval(`import python "example.worker"`, "")
	eval(`x = worker.read(worker.read(3) + 1)`, "4")
	eval(`worker.read(value=x)`, "4")
	eval(`x + 1`, "5")
	eval(`if false { worker.read(99) } else { 8 }`, "8")
	if len(arguments) != 3 || arguments[0] != 3 || arguments[1] != 4 || arguments[2] != 4 {
		t.Fatalf("call order or saved call replay: %v", arguments)
	}
	for source, code := range map[string]string{
		`worker.read(worker.read(9), 2)`:         "E-ARITY",
		`worker.read("bad")`:                     "E-SCHEMA",
		`worker.missing(worker.read(9))`:         "E-OPERATION",
		`fn impure()->number { worker.read(9) }`: "E-PURITY",
		`effect worker.read(worker.read(9))`:     "E-PURITY",
	} {
		_, err := s.Eval(ctx, source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != code {
			t.Fatalf("%s: expected %s, got %v", source, code, err)
		}
	}
	if len(arguments) != 3 {
		t.Fatalf("invalid or unselected call executed provider: %v", arguments)
	}
	if _, err := Compile(`import python "example.worker"
flow Main()->number { return worker.read(3) }`, host); err == nil {
		t.Fatal("file compiler accepted an interactive shortcut")
	}
	host.Authorize = func(context.Context, Request) error { return errors.New("denied") }
	denied, _ := NewSession(host)
	_, _ = denied.Eval(ctx, `import python "example.worker"`, Options{})
	_, err = denied.Eval(ctx, `worker.read(9)`, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-CAPABILITY" || len(arguments) != 3 {
		t.Fatalf("direct call bypassed authorization: %v, %v", err, arguments)
	}
}

func TestSessionGeneralHelp(t *testing.T) {
	var calls atomic.Int64
	s, _ := NewSession(testHost(func(context.Context, Request) Response {
		calls.Add(1)
		return Result(number(1))
	}))
	ctx := context.Background()
	for _, source := range []string{`import python "example.worker"`, `fn twice(n:number)->number { n * 2 }`, `p = effect worker.read(3)`, `f = twice`} {
		if _, err := s.Eval(ctx, source, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	for source, want := range map[string]string{
		`help()`:            "LIP 1.0 interactive help",
		`help(3)`:           "binary64",
		`help(true)`:        "bool",
		`help(null)`:        "null",
		`help("hello")`:     "UTF-8",
		`help([1,2])`:       "list",
		`help({x:1})`:       "object",
		`help(len)`:         "len",
		`help(twice)`:       "fn(n:number) -> number",
		`help(f)`:           "fn(n:number) -> number",
		`help([twice][0])`:  "fn(n:number) -> number",
		`help(p)`:           "not yet performed",
		`help(worker)`:      "worker.read(value:number)",
		`help(worker.read)`: "worker.read(value:number)",
	} {
		result, err := s.Eval(ctx, source, Options{})
		text, _ := result.Value.Text()
		if err != nil || !result.Help || !strings.Contains(text, want) {
			t.Fatalf("%s: %v, %q", source, err, text)
		}
	}
	for source, code := range map[string]string{
		`help(1,2)`:                  "E-ARITY",
		`help(value=worker.read(9))`: "E-ARITY",
		`help(missing)`:              "E-NAME",
		`help(worker.missing)`:       "E-OPERATION",
	} {
		_, err := s.Eval(ctx, source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != code {
			t.Fatalf("%s: expected %s, got %v", source, code, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("help executed an operation being described")
	}
	for _, source := range []string{`h = help`, `h(twice)`, `fn describe(x:any)->string {help(x)}`, `describe(2)`, `fn invoke(help:function)->number {help(3)}`, `invoke(twice)`, `[help+1 for help in [1,2]]`, `for help in [1,2] with total=0 {next total+help}`} {
		if _, err := s.Eval(ctx, source, Options{}); err != nil {
			t.Fatalf("help value or lexical scope %s: %v", source, err)
		}
	}
	if _, err := s.Eval(ctx, `help = twice`, Options{}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Eval(ctx, `help(3)`, Options{})
	if err != nil || result.Help || num(result.Value) != 6 {
		t.Fatalf("help shadowing: %v, %v", result, err)
	}
}

func TestInteractivePythonCallsAndDocumentation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var authorized atomic.Int64
	moduleDir := t.TempDir()
	marker := filepath.Join(moduleDir, "imported")
	module := `from pathlib import Path
Path(__file__).with_name("imported").write_text("yes")
def add(x, y=1):
    """Add values for interactive adapter tests."""
    return x + y
def unsupported():
    return {1, 2}
def nonfinite():
    return float("inf")
`
	if err := os.WriteFile(filepath.Join(moduleDir, "lip_repl_fixture.py"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", moduleDir)
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	host := Host{Authorize: func(context.Context, Request) error { authorized.Add(1); return nil }}
	s, err := NewInteractiveSession(host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	eval := func(source, want string) CellResult {
		t.Helper()
		result, err := s.Eval(ctx, source, Options{})
		if err != nil || want != "" && display(t, result.Value) != want {
			t.Fatalf("%s: %v, %s", source, err, display(t, result.Value))
		}
		return result
	}
	eval(`import python "lip_repl_fixture" as py`, "")
	_ = s.Complete("py.")
	if _, err := os.Stat(marker); !os.IsNotExist(err) || authorized.Load() != 0 {
		t.Fatal("import or completion executed Python", err)
	}
	eval(`x=py.add(3, y=4)`, "7")
	eval(`py.add(py.add(x), y=2)`, "10")
	eval(`proposal=effect py.add(8, y=2)`, "")
	before := authorized.Load()
	eval(`perform proposal`, "10")
	eval(`x`, "7")
	if authorized.Load() != before+1 {
		t.Fatal("saved value or proposal repeated a call")
	}
	result := eval(`help(py.add)`, "")
	text, _ := result.Value.Text()
	if !result.Help || !strings.Contains(text, "Add values for interactive adapter tests.") {
		t.Fatal("missing Python member documentation", text)
	}
	result = eval(`help(py)`, "")
	text, _ = result.Value.Text()
	if !strings.Contains(text, "lip_repl_fixture") || strings.Contains(text, "\b") {
		t.Fatal("missing or terminal-formatted module documentation", text)
	}
	eval(`import python "math"`, "")
	eval(`math.sqrt(3)`, "1.7320508075688772")
	eval(`flow root(x:number)->number { return perform effect math.sqrt(x) }`, "")
	eval(`12 |> root`, "3.4641016151377544")
	eval(`root(12)`, "3.4641016151377544")
	eval(`flow direct(x:number)->number { return math.sqrt(x) }`, "")
	eval(`direct(9)`, "3")
	eval(`help("math.sqrt")`, "")
	for source, code := range map[string]string{
		`failed=py.nonfinite()`:        "E-VALUE",
		`py.nonfinite()`:               "E-VALUE",
		`py.missing()`:                 "E-EFFECT-REJECTED",
		`py.add(fn()->number {1})`:     "E-SCHEMA",
		`fn bad()->number {py.add(1)}`: "E-PURITY",
	} {
		_, err := s.Eval(ctx, source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != code {
			t.Fatalf("%s: expected %s, got %v", source, code, err)
		}
	}
	if _, saved := s.Values()["failed"]; saved {
		t.Fatal("failed call committed a binding")
	}
	if names := s.Complete("py.nonfinite"); len(names) != 0 {
		t.Fatalf("failed cell published its operation: %v", names)
	}
	eval(`py.add(1)`, "2")
	// An application authorization callback must run before module import.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	denied, _ := NewInteractiveSession(Host{Authorize: func(context.Context, Request) error { return errors.New("denied") }})
	t.Cleanup(func() { denied.Close() })
	_, _ = denied.Eval(ctx, `import python "lip_repl_fixture" as py`, Options{})
	for _, source := range []string{`py.add(1)`, `help(py)`} {
		_, err := denied.Eval(ctx, source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-CAPABILITY" {
			t.Fatalf("%s bypassed authorization: %v", source, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("denied operation imported the Python module", err)
	}
	if err := exec.CommandContext(ctx, python, "-c", "import numpy").Run(); err != nil {
		t.Log("NumPy unavailable; standard-library and fixture coverage passed")
	} else {
		eval(`import python "numpy" as np`, "")
		eval(`np.sqrt(3)`, "1.7320508075688772")
		eval(`flow sqr(x:number)->number { return perform effect np.sqrt(x) }`, "")
		eval(`12 |> sqr`, "3.4641016151377544")
		eval(`sqr(12)`, "3.4641016151377544")
		eval(`native_data(np.sqrt([1,4,9]))`, "[1,2,3]")
		eval(`np.sum([1,2,3])`, "6")
		eval(`np.all([true,false]).item()`, "false")
		eval(`import python "numpy"`, "")
		eval(`numpy.sqrt(3)`, "1.7320508075688772")
	}
	s.Reset()
	if _, err := s.Eval(ctx, `py.add(1)`, Options{}); err == nil {
		t.Fatal("reset retained an import")
	}
	eval(`import python "lip_repl_fixture" as py`, "")
	if names := s.Complete("py.add"); len(names) != 0 {
		t.Fatalf("reset retained a discovered operation: %v", names)
	}
	eval(`py.add(1)`, "2")
}

func TestCellContinuationUsesCoreGrammar(t *testing.T) {
	for _, source := range []string{"[1,\n", "range(5)[::", "range(5)[1:\n", "fn square(x:number)->number {\n", "1 + \\\n", "(1 +\n", "if true {1} else", "flow sqrt(x:number)->number", "export fn add(x:number)->number {", "LIMIT:object={", "import lip", "x=\"\"\"\n:help\n", `"""quote ""`} {
		if !CellIncomplete(source) {
			t.Fatalf("unfinished cell considered complete: %q", source)
		}
	}
	for _, source := range []string{`"["`, `range(5)[::-1]`, `range(5)[:::]`, "[1,2]\n", "[1)\n", `"bad`, "1 + )", "if true {1} else {2}", "# comment\n", "\"\"\"done\n\"\"\"", `"""bad\x`, `"""\u{d800}`} {
		if CellIncomplete(source) {
			t.Fatalf("complete/malformed cell requested more input: %q", source)
		}
	}
	if !CellInMultilineString("x=\"\"\"\n:quit\n") || CellInMultilineString(`"""done"""`) {
		t.Fatal("multiline string command context was lost")
	}
}

// Public CLI tests stay under tests/. A single built lipc serves all sessions.
func TestInteractiveCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lipc")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/lipc")
	build.Dir = ".."
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lipc: %v\n%s", err, data)
	}
	run := func(input string, args ...string) (string, string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = ".."
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("lipc %v: %v\n%s", args, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	t.Run("repl-index-and-slice", func(t *testing.T) {
		stdout, stderr := run("a=range(6)\nb=range(3)\na[b]\na[-1]\na[::-1]\na[\n1:5:2\n]\nbad=a[::0]\na[-1]\nhelp(slice)\n:quit\n", "repl")
		if !strings.HasPrefix(stdout, "[0,1,2,3,4,5]\n[0,1,2]\n[0,1,2]\n5\n[5,4,3,2,1,0]\n[1,3]\n5\n") || !strings.Contains(stdout, "null selects defaults") || !strings.Contains(stderr, "E-VALUE") {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("repl", func(t *testing.T) {
		stdout, stderr := run("x=2\nfn twice(n:number)->number {\n n*2\n}\ntwice(x)\nif true {\n x+1\n} else {0}\nx=9\nx\n:quit\n", "repl")
		if stdout != "2\n4\n3\n2\n" || !strings.Contains(stderr, "E-DUPLICATE") {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
	})
	t.Run("delete-bindings", func(t *testing.T) {
		stdout, stderr := run("x=2\nfn keep()->number {x}\nflow Old()->number {return x}\nflow Saved()->number {return Old()}\n:del x, Old\nx=9\nOld=3\nkeep()+Saved()\n:del x, absent\nx\n:del\n:del x,\nx\n:del x\nx=4\n:vars\n:quit\n", "repl")
		if !strings.HasPrefix(stdout, "2\n9\n3\n4\n9\n9\n4\n") || !strings.Contains(stdout, "x = 4\n") || strings.Contains(stdout, "$cell") || strings.Count(stderr, "E-NAME") != 1 || strings.Count(stderr, "E-SESSION") != 2 {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
		stdout, stderr = run("import python \"math\" as lib\n:del lib\nlib=2\n:del lib\nimport python \"cmath\" as lib\nhelp(lib)\n:del lib\nlib=3\n:quit\n", "repl")
		if !strings.HasPrefix(stdout, "2\n") || !strings.HasSuffix(stdout, "3\n") || stderr != "" || strings.Contains(stdout, "$cell") {
			t.Fatalf("alias reuse: stdout %q stderr %q", stdout, stderr)
		}
	})
	t.Run("recursion-budget", func(t *testing.T) {
		declaration := "fn count(n:number)->number {if n==0 {0} else {1+count(n-1)}}\n"
		file := filepath.Join(dir, "recursion.lip")
		if err := os.WriteFile(file, []byte(declaration+"flow Main(n:number)->number {return count(n)}"), 0600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := run("", "run", "-frames", "9", "-input", `{"n":8}`, file)
		if stdout != "8\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
		// A failed :run inherits the REPL budget, and later cells still work.
		input := declaration + "saved=7\nfailed=count(8)\nsaved+count(1)\n" +
			":run -input '{\"n\":8}' '" + file + "'\n" +
			":run -frames 9 -input '{\"n\":8}' '" + file + "'\nsaved\n:quit\n"
		stdout, stderr = run(input, "repl", "-frames", "8")
		if stdout != "7\n8\n8\n7\n" || strings.Count(stderr, "E-BUDGET") != 2 || strings.Count(stderr, "limit: 8") != 2 {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
		for _, args := range [][]string{
			{"run", "-frames", "-1", file},
			{"repl", "-frames", "-1"},
		} {
			cmd := exec.CommandContext(ctx, binary, args...)
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "-frames must be nonnegative") {
				t.Fatalf("negative frame budget: %s, %v", out, err)
			}
		}
	})
	t.Run("parallelism-configuration", func(t *testing.T) {
		file := filepath.Join(dir, "parallelism.lip")
		if err := os.WriteFile(file, []byte("flow Main()->number {return 7}"), 0600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := run("flow Value(x:number)->number {return x}\n3 |> Value\n:run '"+file+"'\n:run -parallel 17 '"+file+"'\n:quit\n", "repl", "-parallel", "73295")
		if stdout != "3\n7\n7\n" || stderr != "" {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
		for _, args := range [][]string{{"run", "-parallel", "-1", file}, {"repl", "-parallel", "-1"}} {
			cmd := exec.CommandContext(ctx, binary, args...)
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "-parallel must be nonnegative") {
				t.Fatalf("negative parallelism: %s, %v", out, err)
			}
		}
	})
	t.Run("script-syntax-and-multiline", func(t *testing.T) {
		stdout, stderr := run(`flow Empty()->void {}
if true {Empty()}
# Keep a following else in the same statement.
else {fail("wrong branch")}
:begin
export OFFSET:number=2
flow First(x:number)->number {return x |> Second}
flow Second(x:number)->number {return x+OFFSET}
facts=claim()
for x in [1,2] parallel {value=x |> First; facts <+ value}
[row.value for row in facts]
:end
First(3)
3 |> First
:begin
flow Rejected()->number {return missing}
:end
First(4)
:begin
flow Canceled()->number {return 0}
:cancel
:quit
`, "repl")
		if stdout != "[3,4]\n5\n5\n6\n" || !strings.Contains(stderr, "E-NAME") || strings.Count(stderr, "E-") != 1 {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
	})
	t.Run("repl-project-import", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "repl-module.lip"), []byte(`export flow Twice(x:number)->number {return x*2}`), 0600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := run("import lip \"./repl-module.lip\" as lib\n3 |> lib.Twice\nlib.Twice(4)\n:quit\n", "repl", "-project", dir)
		if stdout != "6\n8\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("entry-selection-and-input", func(t *testing.T) {
		file := filepath.Join(dir, "entries.lip")
		source := "flow First(x:number)->number {return x+OFFSET}\nflow Second()->null {return null}\nOFFSET:number=2"
		if err := os.WriteFile(file, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := run("", "run", "-entry", "First", "-entry-input", `{"positional":[3],"named":{}}`, "-entry-output", file)
		if stdout != "{\"kind\":\"value\",\"value\":5}\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
		stdout, stderr = run("", "run", "-entry", "Second", "-entry-output", file)
		if stdout != "{\"kind\":\"value\",\"value\":null}\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
		for _, input := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{"x":`, `[]`, `{},"positional":[]`} {
			cmd := exec.CommandContext(ctx, binary, "run", "-entry", "First", "-input", input, file)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "E-SCHEMA") {
				t.Fatalf("named input %s: %s %v", input, out, err)
			}
		}
	})
	t.Run("static-modules-run-and-build", func(t *testing.T) {
		project := filepath.Join(dir, "project")
		if err := os.MkdirAll(filepath.Join(project, "lib"), 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(project, "main.lip")
		dep := filepath.Join(project, "lib", "math.lip")
		for path, source := range map[string]string{
			file: "import lip \"./lib/math.lip\" as math\nflow Main(x:number)->number{return x |>math.Double}\nflow Empty()->void{}",
			dep:  "BASE:number=2\nexport flow Double(x:number)->number{return x*BASE}",
		} {
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
		}
		stdout, stderr := run("", "run", "-project", project, "-input", `{"x":3}`, file)
		if stdout != "6\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
		executable := filepath.Join(dir, "module-program")
		run("", "build", "-project", project, "-o", executable, file)
		stdout, stderr = run(":run -project '"+project+"' -input '{\"x\":3}' '"+file+"'\n:quit\n", "repl")
		if stdout != "6\n" || stderr != "" {
			t.Fatal("REPL file default entry", stdout, stderr)
		}
		// Executables use the frozen closure, even after a dependency changes.
		if err := os.WriteFile(dep, []byte("invalid source"), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, executable, "-entry-input", `{"positional":[3],"named":{}}`, "-entry-output")
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "{\"kind\":\"value\",\"value\":6}\n" {
			t.Fatal(string(out), err)
		}
	})
	t.Run("repl-v1-file-boundary", func(t *testing.T) {
		file := filepath.Join(dir, "entry with spaces.lip")
		if err := os.WriteFile(file, []byte("flow Add(x:number)->number{return x+2}\nflow Empty()->void{}"), 0600); err != nil {
			t.Fatal(err)
		}
		input := "x=9\nfn twice(n:number)->number{n*2}\n3 |>twice\n" +
			":run -entry Add -entry-input '{\"positional\":[3],\"named\":{}}' -entry-output \"" + file + "\"\n" +
			":run -entry Empty -entry-output '" + file + "'\n" +
			":run 'unfinished\nx\n:quit\n"
		stdout, stderr := run(input, "repl")
		if stdout != "9\n6\n{\"kind\":\"value\",\"value\":5}\n{\"kind\":\"void\"}\n9\n" || !strings.Contains(stderr, "unfinished quote") {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("repl-multiline-string", func(t *testing.T) {
		stdout, stderr := run("text=\"\"\"\n:quit\n\n# literal comment\n\"\"\"\ntext\n:quit\n", "repl")
		want := "\"\\u000a:quit\\u000a\\u000a# literal comment\\u000a\"\n"
		if stdout != want+want || stderr != "" {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("learn-multiline-string", func(t *testing.T) {
		stdout, stderr := run("\"\"\"Hello, LIP!\"\"\"\n:edit\nflow Hello()->string {\n s=\"\"\"\n:quit\n\"\"\"\n return \"Hello, LIP!\"\n}\n:submit\n:quit\n", "learn", "-lesson", "hello")
		if stderr != "" || strings.Count(stdout, "Passed") != 2 {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("repl-flow", func(t *testing.T) {
		stdout, stderr := run(":run examples/advanced/14_parallel_claim.lip\n:quit\n", "repl")
		if stderr != "" || !strings.Contains(stdout, `"count":3`) {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("web-server", func(t *testing.T) {
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("HTTP CLI process probe needs Python 3")
		}
		for _, name := range []string{"23_home", "24_echo", "25_greeting"} {
			data, err := os.ReadFile(filepath.Join("..", "examples", "web", name+".lip"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name+".lip"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		manifest := filepath.Join(dir, "routes.json")
		data := `[{"pattern":"GET /{$}","source":"23_home.lip"},{"pattern":"POST /api/echo","source":"24_echo.lip"},{"pattern":"GET /api/hello/{name}","source":"25_greeting.lip"}]`
		if err := os.WriteFile(manifest, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, python, "-c", webServingProbe, binary, manifest)
		if out, err := cmd.CombinedOutput(); err != nil {
			if failure, ok := err.(*exec.ExitError); ok && failure.ExitCode() == 77 && strings.Contains(string(out), "TCP_LISTEN_NOT_PERMITTED") {
				t.Skip("sandbox blocks TCP listeners; in-memory HTTP wire and handler tests cover the adapter")
			}
			t.Fatalf("web server: %v\n%s", err, out)
		}
	})
	t.Run("repl-host", func(t *testing.T) {
		stdout, stderr := run("import go \"strings\" as text\nx=perform effect text.ToUpper(\"Ada\")\nx+\"!\"\n:quit\n", "repl", "-host", "demo")
		if stdout != "\"ADA\"\n\"ADA!\"\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("repl-python-and-help", func(t *testing.T) {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("Python 3 unavailable")
		}
		stdout, stderr := run("3 ** -2\nimport python \"math\" as m\nm.sqrt(3)\nhelp(m.sqrt)\nimport python \"math\"\nmath.sqrt(3)\nhelp()\nhelp(len)\nhelp([1,2])\nm.missing()\nm.sqrt(4)\n:quit\n", "repl")
		if !strings.HasPrefix(stdout, "0.1111111111111111\n1.7320508075688772\nPython Library Documentation:") || strings.Count(stdout, "1.7320508075688772\n") != 2 || !strings.Contains(stdout, "Return the square root") || !strings.Contains(stdout, "LIP 1.0 interactive help\n") || !strings.Contains(stdout, "len:") || !strings.Contains(stdout, "list\n") || !strings.HasSuffix(stdout, "2\n") || !strings.Contains(stderr, "E-EFFECT-REJECTED") {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
	})
	t.Run("all-lessons", func(t *testing.T) {
		stdout, stderr := run("", "learn", "-list")
		if stderr != "" {
			t.Fatal(stderr)
		}
		var input strings.Builder
		lessons := 0
		for _, line := range strings.Split(stdout, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			lessons++
			fmtLine := ":lesson " + fields[1] + "\n:demo\n"
			input.WriteString(fmtLine)
		}
		input.WriteString(":quit\n")
		stdout, stderr = run(input.String(), "learn")
		if lessons < 10 || stderr != "" || strings.Count(stdout, "Case 1 ✓") != lessons {
			t.Fatal("lesson demos", lessons, stdout, stderr)
		}
	})
	t.Run("answers-and-progress", func(t *testing.T) {
		path := filepath.Join(dir, "progress.json")
		stdout, stderr := run("36\nprice * count\n:next\n:quit\n", "learn", "-lesson", "bindings", "-progress", path)
		if !strings.Contains(stdout, "Passed") || !strings.Contains(stderr, "expected 35") {
			t.Fatal(stdout, stderr)
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(data, []byte(`"bindings": true`)) || !bytes.Contains(data, []byte(`"current": "arithmetic"`)) {
			t.Fatal(string(data), err)
		}
		stdout, stderr = run(":quit\n", "learn", "-progress", path)
		if stderr != "" || !strings.Contains(stdout, "arithmetic") {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("v1-lessons", func(t *testing.T) {
		for _, test := range []struct{ id, answer string }{
			{"entries", "n+OFFSET"},
			{"slicing", "{picked:xs[indices],reverse:xs[::-1],stride:xs[1::2],text:label[::-1]}"},
			{"flows", "n |>Double |>adjust(delta=3)"},
			{"modules", "{total:n |>pricing.Adjust,base:pricing.BASE}"},
		} {
			stdout, stderr := run(test.answer+"\n:quit\n", "learn", "-lesson", test.id)
			if stderr != "" || !strings.Contains(stdout, "LIP 1.0") || strings.Count(stdout, "Case ") != 2 || !strings.Contains(stdout, "Passed") {
				t.Fatal(test.id, stdout, stderr)
			}
		}
		stdout, stderr := run("{total:n |>pricing.add,base:pricing.BASE}\n{total:n |>pricing.Adjust,base:pricing.BASE}\n:quit\n", "learn", "-lesson", "modules")
		if !strings.Contains(stderr, "E-EXPORT") || strings.Count(stdout, "Passed") != 1 {
			t.Fatal("module rejection must allow retry", stdout, stderr)
		}
	})
	t.Run("full-program", func(t *testing.T) {
		stdout, stderr := run(":edit\nflow Squares(xs:list)->list {\n facts=claim()\n for x in xs parallel {facts <+ x*x}\n return [row.value for row in facts]\n}\n:submit\n:quit\n", "learn", "-lesson", "claim")
		if stderr != "" || !strings.Contains(stdout, "Passed") {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("terminal-editing", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("PTY integration currently verified on Linux")
		}
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("PTY integration needs Python 3")
		}
		cmd := exec.CommandContext(ctx, python, "-c", terminalEditingProbe, binary)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("terminal editing: %v\n%s", err, out)
		}
	})
}

// Run against an actual PTY: escape bytes in a pipe would not exercise raw mode,
// redraw, terminal restoration, or Unicode editing in the interactive tool.
const terminalEditingProbe = `
import os, pty, select, signal, struct, subprocess, sys, termios, time, fcntl

class Terminal:
    def __init__(self, *args):
        self.master, self.slave = pty.openpty()
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
        self.original = termios.tcgetattr(self.slave)
        self.proc = subprocess.Popen([sys.argv[1], *args], stdin=self.slave, stdout=self.slave, stderr=self.slave)
        self.buffer = b''
        self.ready()
    def wait(self, token):
        deadline = time.monotonic() + 8
        while token not in self.buffer:
            if time.monotonic() > deadline:
                raise AssertionError('timeout waiting for %r; received %r' % (token, self.buffer[-3000:]))
            if select.select([self.master], [], [], .05)[0]:
                self.buffer += os.read(self.master, 65536)
        i = self.buffer.index(token) + len(token)
        self.buffer = self.buffer[i:]
    def ready(self):
        self.wait(b'\x1b[?2004h')
        assert not (termios.tcgetattr(self.slave)[3] & termios.ICANON)
    def send(self, text):
        os.write(self.master, text.encode('utf-8'))
    def value(self, keys, expected):
        self.send(keys)
        self.wait(b'\x1b[?2004l' + expected.encode() + b'\r\n')
        self.ready()
    def close(self, interrupt=False):
        try:
            if interrupt: self.proc.send_signal(signal.SIGINT)
            else: self.send(':quit\r')
            code = self.proc.wait(timeout=8)
            assert code == (1 if interrupt else 0), code
            assert termios.tcgetattr(self.slave) == self.original, 'terminal settings not restored'
        finally:
            if self.proc.poll() is None: self.proc.kill(); self.proc.wait()
            os.close(self.master); os.close(self.slave)

t = Terminal('repl')
try:
    t.value('2 ** 4\x1b[D\x1b[3~3\r', '8')       # left + Delete
    t.value('-3 ** 2\r', '-9')
    t.value('(-3) ** 2\r', '9')
    t.value('8 */ 3\x7f2\r', '3')
    t.value('3 + 4\x01\x0b\x19\r', '7')        # Home, kill, yank
    t.value('2+3\x7f\x1f\r', '5')               # undo deletion
    t.value('12\x14\r', '21')                    # transpose
    t.value('7 + 9\x17' + '2\r', '9')            # kill previous word
    t.value('3 + 4\x1bb\x1bd' + '5\r', '8')     # Alt word move/delete
    t.value('2 + 3\x01\x1b[C\x15' + '4\x05\r', '7') # cut to start + End
    t.value('42\r', '42')
    t.value('\x1b[A\r', '42')                    # history
    t.value('6\x1b[A\x1b[B\r', '6')             # recover current draft
    t.value('\x12' + '42\r', '42')               # reverse history search
    t.value('9\x12missing\x07\r', '9')           # cancel history search
    t.value('parse_n\t("3")\r', '3')            # core completion
    t.value('answer=5\r', '5')
    t.value('answ\t ** 2\r', '25')               # saved binding completion
    t.send('flow Adjust(x:number)->number {return x+2}\r'); t.ready()
    t.value('3 |> Adju\t\r', '5')                # retained Flow completion and pipeline
    t.value('Adjust(4)\r', '6')
    t.send('\x1b[200~flow Earlier(x:number)->number {return Later(x)}\nflow Later(x:number)->number {return x*2}\x1b[201~\r'); t.ready()
    t.value('Earlier(3)\r', '6')                   # bracketed script paste with forward calls
    t.value('"你a"\x1b[D\x7f好\x1b[F\r', '"你好"')
    t.value('"Ae\u0301"\x1b[D\x7fB\x1b[F\r', '"AB"')
    t.value('"A👩‍💻"\x1b[D\x7fB\x1b[F\r', '"AB"')
    t.value('\x1b[200~if true {\r\n2 ** 3\r\n} else {0}\x1b[201~\r', '8')
    t.send('msg="""\r'); t.ready()
    t.send(':quit\r'); t.ready()
    t.value('"""\r', '"\\u000a:quit\\u000a"')           # commands inside a multiline string are text
    t.send('fn broken(x:number)->number {\r'); t.ready()
    t.send('unfinished\x03'); t.wait(b'^C'); t.ready()
    t.value('1+2\r', '3')                         # Ctrl-C clears pending cell
finally:
    t.close()

t = Terminal('learn', '-lesson', 'arithmetic')
try:
    t.send('(x + 2) * 4\x1b[D\x1b[C\x7f3\r')  # both arrows edit lesson answer
    t.wait('Passed'.encode()); t.ready()
    t.send(':edit\r'); t.ready()
    t.send('broken\x03'); t.wait(b'^C'); t.ready()
    t.send('(x + 2) * 3\r')
    t.wait('Passed'.encode()); t.ready()
finally:
    t.close()

t = Terminal('repl')
t.close(interrupt=True)                           # asynchronous cancel restores raw mode

t = Terminal('repl', '-host', 'demo')
try:
    t.send('import go "strings" as text\r'); t.ready()
    t.value('perform effect text.ToU\t("ada")\r', '"ADA"')
finally:
    t.close()
`

const webServingProbe = `
import json, signal, subprocess, sys, urllib.request, urllib.error
p = subprocess.Popen([sys.argv[1], 'serve', '-addr', '127.0.0.1:0', '-routes', sys.argv[2]], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
try:
    line = p.stderr.readline().decode()
    if 'socket: operation not permitted' in line or 'socket: permission denied' in line:
        print('TCP_LISTEN_NOT_PERMITTED')
        raise SystemExit(77)
    assert ' at http://' in line, line
    base = line.split(' at ', 1)[1].strip()
    with urllib.request.urlopen(base+'/', timeout=5) as r:
        assert r.status == 200 and 'LIP website' in r.read().decode()
    with urllib.request.urlopen(base+'/api/hello/Ada', timeout=5) as r:
        assert json.load(r) == {'message':'Hello, Ada'}
    request = urllib.request.Request(base+'/api/echo?x=1&x=2', data=b'{"value":3}', headers={'Content-Type':'application/json'})
    with urllib.request.urlopen(request, timeout=5) as r:
        assert json.load(r) == {'received':{'value':3}, 'query':{'x':['1','2']}}
    try: urllib.request.urlopen(urllib.request.Request(base+'/', method='HEAD'), timeout=5)
    except urllib.error.HTTPError as e:
        assert e.code == 405 and e.headers['Allow'] == 'GET'
    else: raise AssertionError('HEAD fell back to GET')
    request = urllib.request.Request(base+'/api/echo', data=b'{"x":1,"x":2}', headers={'Content-Type':'application/json'})
    try: urllib.request.urlopen(request, timeout=5)
    except urllib.error.HTTPError as e: assert e.code == 400
    else: raise AssertionError('duplicate JSON fields accepted')
    p.send_signal(signal.SIGINT)
    assert p.wait(timeout=8) == 0
finally:
    if p.poll() is None: p.kill(); p.wait()
`

func TestLocalSourceSnapshot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.lip")
	dep := filepath.Join(dir, "dep.lip")
	if err := os.WriteFile(root, []byte("import lip \"./dep.lip\" as d\nflow Main()->number{return d.answer()}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dep, []byte("export fn answer()->number{42}"), 0600); err != nil {
		t.Fatal(err)
	}
	set, err := LoadSourceSet(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dep, []byte("export fn answer()->number{99}"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{})
	if err != nil || display(t, report.Output) != "42" {
		t.Fatal("compile reread frozen source", report, err)
	}
	logical := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(dir, logical); err == nil {
		for _, filename := range []string{root, filepath.Join(logical, "main.lip")} {
			if _, err := LoadSourceSet(filename, logical); err != nil {
				t.Fatal("physical project root", err)
			}
		}
	}
	if err := os.Remove(dep); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, dep); err != nil {
		t.Skip(err)
	}
	if _, err := LoadSourceSet(root, dir); err == nil || err.(*Diagnostic).Code != "E-MODULE" {
		t.Fatal("source symlink accepted", err)
	}
	if err := os.Remove(dep); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(root, dep); err != nil {
		t.Skip(err)
	}
	if _, err := LoadSourceSet(root, dir); err == nil || err.(*Diagnostic).Code != "E-MODULE" {
		t.Fatal("duplicate physical source accepted", err)
	}
}

func TestSessionPipelineDeclarationSurvivesCells(t *testing.T) {
	s, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, source := range []string{`fn twice(n:number)->number{n*2}`, `alias=twice`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Eval(context.Background(), `3 |> twice`, Options{})
	if err != nil || display(t, result.Value) != "6" {
		t.Fatal(result, err)
	}
	if _, err := s.Eval(context.Background(), `3 |> alias`, Options{}); err == nil {
		t.Fatal("dynamic stage accepted")
	}
	s.Reset()
	if _, err := s.Eval(context.Background(), `3 |> twice`, Options{}); err == nil {
		t.Fatal("reset retained a declaration")
	}
}

// Keep every bundled language example usable as interactive declarations plus
// an ordinary named invocation. This covers the same syntax and expected
// behavior used by interpreter/generated-program conformance tests.
func TestSessionScriptExamples(t *testing.T) {
	cases := append([]exampleCase(nil), examples...)
	for _, name := range []string{"23_home", "24_echo", "25_greeting"} {
		cases = append(cases, exampleCase{path: "web/" + name + ".lip", input: `{"request":{"params":{"name":"Ada"},"query":{"tag":["one","two"]},"json":{"value":3}}}`})
	}
	for _, example := range cases {
		t.Run(example.path, func(t *testing.T) {
			host := Host{}
			if example.demo {
				if _, err := exec.LookPath("python3"); err != nil {
					t.Skip("demo Host needs Python 3")
				}
				var err error
				host, err = DemoHost()
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
			build, _ := program.BuildInfo().Fields()
			entry, _ := build["entry"].Text()
			input := example.input
			if input == "" {
				input = "{}"
			}
			call := entry + "("
			value, err := DecodeData([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := value.Fields()
			for name, value := range fields {
				call += name + "=" + display(t, value) + ","
			}
			call += ")"
			s, err := NewSession(host)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if strings.HasPrefix(example.path, "web/") {
				report, err := program.Run(ctx, fields, Options{})
				if err != nil {
					t.Fatal(err)
				}
				example.want = display(t, report.Output)
			}
			if result, err := s.Eval(ctx, string(source), Options{}); err != nil || result.HasValue {
				t.Fatalf("declarations: %+v, %v", result, err)
			}
			// Repeated calls check that cached syntax and Claim instances remain
			// independent even when the previous call checked/lowered pipelines.
			for _, parallel := range []int{1, 4} {
				result, err := s.Eval(ctx, call, Options{Parallelism: parallel, MaxSteps: 10000000})
				if err != nil || !result.HasValue {
					t.Fatalf("%s: %+v, %v", call, result, err)
				}
				checkExample(t, example, display(t, result.Value))
			}
		})
	}
}

func TestSessionFlowTransactionsAndClaims(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, request Request) Response {
		calls.Add(1)
		return Result(request.Positional[0])
	})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	eval := func(source, want string) CellResult {
		t.Helper()
		result, err := s.Eval(ctx, source, Options{Parallelism: 4})
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if want != "" && (!result.HasValue || display(t, result.Value) != want) {
			t.Fatalf("%s: %+v", source, result)
		}
		return result
	}
	eval(`import python "example.worker" as worker`, "")
	eval(`saved = perform effect worker.read(7)`, "7")
	eval(`export OFFSET:number=2`, "")
	eval(`export fn plus(x:number)->number {x+OFFSET+saved}`, "")
	eval(`flow Read(x:number)->number {return perform effect worker.read(x)}`, "")
	eval(`flow Add(x:number)->number {return x |> plus}`, "")
	eval(`flow Main(x:number)->object {
    facts=claim()
    for n in [x,x+1] parallel { value=n |> Read |> Add; facts <+ value }
    return {values:[row.value for row in facts],run:facts[0].run_id}
}`, "")
	if calls.Load() != 1 {
		t.Fatal("declarations performed an action")
	}
	first := eval(`Main(1)`, "")
	second := eval(`1 |> Main`, "")
	a, _ := first.Value.Fields()
	b, _ := second.Value.Fields()
	if display(t, a["values"]) != "[10,11]" || display(t, b["values"]) != "[10,11]" || display(t, a["run"]) == display(t, b["run"]) || calls.Load() != 5 {
		t.Fatal("Flow calls reused stores or replayed historical calls", display(t, first.Value), display(t, second.Value), calls.Load())
	}
	for source, code := range map[string]string{
		`flow Bad()->number {return unknown}`:                                 "E-NAME",
		`flow Recur()->number {return Recur()}`:                               "E-FLOW-CYCLE",
		`flow Impure()->number {return Read(perform effect worker.read(99))}`: "E-PURITY",
		`fn impure()->number {Read(1)}`:                                       "E-PURITY",
		`Main()`:                                                              "E-ARITY",
		`Main("wrong")`:                                                       "E-TYPE",
		`flow Read()->null {return null}`:                                     "E-DUPLICATE",
		`Read=1`:                                                              "E-DUPLICATE",
	} {
		_, err := s.Eval(ctx, source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != code {
			t.Fatalf("%s: expected %s, got %v", source, code, err)
		}
	}
	if calls.Load() != 5 {
		t.Fatal("rejected syntax performed an action")
	}
	if _, err := s.Eval(ctx, `flow Uncommitted()->number {return 8}; temporary=Read(9); fail("stop")`, Options{}); err == nil {
		t.Fatal("failed cell succeeded")
	}
	if _, err := s.Eval(ctx, `Uncommitted()`, Options{}); err == nil {
		t.Fatal("failed cell committed a Flow")
	}
	if _, ok := s.Values()["temporary"]; ok || calls.Load() != 6 {
		t.Fatal("failed transaction or action replay")
	}
	eval(`Read(3)`, "3")
	eval(`facts=claim(); for n in [1,2] parallel {facts <+ n}; [row.value for row in facts]`, "[1,2]")
	eval(`[row.value for row in facts]`, "[1,2]")
	if _, err := s.Eval(ctx, `facts <+ 3`, Options{}); err == nil {
		t.Fatal("active Claim escaped its run")
	}
	if names := s.Complete("Mai"); len(names) != 1 || names[0] != "Main" {
		t.Fatal(names)
	}
	s.Reset()
	if _, err := s.Eval(ctx, `Main(1)`, Options{}); err == nil {
		t.Fatal("reset retained a Flow")
	}
}

func TestSessionStatementGrammar(t *testing.T) {
	var calls atomic.Int64
	s, _ := NewSession(testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) }))
	defer s.Close()
	ctx := context.Background()
	for _, source := range []string{
		`import python "example.worker" as worker`,
		`if true { worker.read(1) }`,
		"if false { worker.read(99) }\nelse {worker.read(2)}",
		`if false {a=1} else if true {a=2;worker.read(a)} else {worker.read(99)}`,
		`match 2 {1 => worker.read(99), 2 if true => {a=3;worker.read(a)}, _ => worker.read(99)}`,
		`for n in [1,2] {if n==1 {continue};worker.read(n);break}`,
		`return`,
		`flow Empty()->void {}`,
		`if true {Empty()} else {Empty()}`,
		`match true {true => Empty(), _ => Empty()}`,
	} {
		if _, err := s.Eval(ctx, source, Options{}); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if calls.Load() != 5 {
		t.Fatal("statement control flow", calls.Load())
	}
	for source, want := range map[string]string{
		"\"\"\"first\r\nsecond\"\"\"":                                    `"first\u000asecond"`,
		`if true {1} else {2}`:                                           "1",
		`match 2 {1 => 3, _ => 4}`:                                       "4",
		`{x=2; if true {x*3} else {0}}`:                                  "6",
		`for with n=0 {if n<3 {next n+1} else {break n}}`:                "3",
		`for n in {items:[1,2]}.items with total=0 {next total+n}`:       "3",
		`for n in if true {[1,2]} else {[]} with total=0 {next total+n}`: "3",
	} {
		r, err := s.Eval(ctx, source, Options{})
		if err != nil || !r.HasValue || display(t, r.Value) != want {
			t.Fatal(source, r, err)
		}
	}
}

func TestSessionFrozenModules(t *testing.T) {
	dir := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("pricing.lip", `import host "app" as app
RATE:number=2
export fn total(x:number)->number {x*RATE}
export fn request(x:number)->effect {effect app.read(x*RATE)}
export flow Quote(x:number)->number {return perform effect app.read(total(x))}`)
	write("extra.lip", `import host "other" as app
export fn increment(x:number)->number {x+1}`)
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	host.Contracts[0].Operation = Operation{Language: "host", Path: "app", Member: "read"}
	s, err := NewSession(host, SessionOptions{ProjectRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	eval := func(source, want string) {
		t.Helper()
		r, err := s.Eval(ctx, source, Options{})
		if err != nil || want != "" && display(t, r.Value) != want {
			t.Fatalf("%s: %+v, %v", source, r, err)
		}
	}
	eval(`import lip "./pricing.lip" as pricing`, "")
	if calls.Load() != 0 {
		t.Fatal("import executed a Flow")
	}
	eval(`total=pricing.total`, "")
	eval(`request=pricing.request`, "")
	eval(`perform request(3)`, "6")
	if names := s.Complete("pricing.Q"); len(names) != 1 || names[0] != "pricing.Quote" {
		t.Fatal(names)
	}
	eval(`flow Quote(x:number)->number {return x |> pricing.Quote}`, "")
	eval(`Quote(3)`, "6")
	write("pricing.lip", `export fn total(x:number)->number {x*100}`)
	eval(`import lip "./extra.lip" as extra`, "")
	eval(`total(3) |> extra.increment`, "7")
	eval(`3 |> pricing.total`, "6")
	eval(`perform request(3)`, "6")
	eval(`Quote(3)`, "6")
	if calls.Load() != 4 {
		t.Fatal("module closure replayed an action", calls.Load())
	}
	for source, code := range map[string]string{
		`pricing.RATE`:                           "E-EXPORT",
		`pricing`:                                "E-TYPE",
		`import lip "../outside.lip" as outside`: "E-MODULE",
		`import lip "./absent.lip" as absent`:    "E-MODULE",
	} {
		_, err := s.Eval(ctx, source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != code {
			t.Fatal(source, err)
		}
	}
	write("cycle.lip", `import lip "./cycle.lip" as self`)
	if _, err := s.Eval(ctx, `import lip "./cycle.lip" as cycle`, Options{}); err == nil {
		t.Fatal("module cycle accepted")
	}
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	// Frozen imports no longer depend on the source directory at evaluation.
	func() {
		defer func() {
			if err := os.Rename(moved, dir); err != nil {
				t.Fatal(err)
			}
		}()
		eval(`total(4)`, "8")
		eval(`Quote(4)`, "8")
	}()
	for name := range s.Values() {
		if strings.HasPrefix(name, "$") {
			t.Fatal("private linker name exposed", name)
		}
	}
	s.Reset()
	eval(`import lip "./pricing.lip" as pricing`, "")
	eval(`pricing.total(3)`, "300")
}

func TestSessionFlowNamesRespectPrelude(t *testing.T) {
	var calls atomic.Int64
	s, err := NewInteractiveSession(Host{Authorize: func(context.Context, Request) error { calls.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, source := range []string{
		`flow Local()->number {native_data=1;native_release=2;help=3;return native_data+native_release+help}`,
		`flow Document()->string {return help(1)}`,
	} {
		if _, err := s.Eval(ctx, source, Options{}); err != nil {
			t.Fatal(source, err)
		}
	}
	// Names local to a saved Flow must not shadow a later cell's prelude.
	result, err := s.Eval(ctx, `native_data({x:1})`, Options{})
	if err != nil || display(t, result.Value) != `{"x":1}` {
		t.Fatal(result, err)
	}
	before := calls.Load()
	for _, source := range []string{
		`flow help(x:number)->number {return x+1}`,
		`flow native_data(x:number)->number {return x+2}`,
		`flow native_release(x:number)->number {return x+3}`,
	} {
		if _, err := s.Eval(ctx, source, Options{}); err != nil {
			t.Fatal(source, err)
		}
	}
	result, err = s.Eval(ctx, `[help(1),native_data(2),native_release(3),Local()]`, Options{})
	if err != nil || result.Help || display(t, result.Value) != `[2,4,6,6]` || calls.Load() != before {
		t.Fatal(result, err, calls.Load())
	}
	result, err = s.Eval(ctx, `Document()`, Options{})
	text, _ := result.Value.Text()
	if err != nil || !strings.Contains(text, "binary64") {
		t.Fatal("saved tooling environment changed", result, err)
	}
}

func TestSessionIndexAndSlice(t *testing.T) {
	s, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, test := range []struct{ source, want string }{
		{`a=range(100)`, ""},
		{`b=range(12)`, `[0,1,2,3,4,5,6,7,8,9,10,11]`},
		{`a[b]`, `[0,1,2,3,4,5,6,7,8,9,10,11]`},
		{`a[range(22)]`, `[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]`},
		{`a[-1]`, `99`},
		{`a[2:12:2]`, `[2,4,6,8,10]`},
		{`rev=a[::-1]`, ""},
		{`rev[[0,-1,0]]`, `[99,0,99]`},
		{`fn select(xs:list,idx:list)->list {xs[idx][::-1]}`, ""},
		{`select(a,[0,-1,0])`, `[0,99,0]`},
		{`fns=[fn(x:number)->number{x+1},fn(x:number)->number{x*2}]`, ""},
		{`selected=fns[::-1][[0,1,0]]`, ""},
		{`map(selected,fn(f:function)->number{f(3)})`, `[6,4,6]`},
		{"a[\n :\n : -25\n]", `[99,74,49,24]`},
	} {
		nativeCell(t, s, test.source, test.want)
	}
	for _, source := range []string{`bad=a[[0,100]]`, `bad=a[::0]`, `bad=a[0.5:]`, `bad=a[[true]]`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatal("invalid index accepted", source)
		}
		if _, ok := s.Values()["bad"]; ok {
			t.Fatal("failed index committed binding")
		}
		nativeCell(t, s, `a[-1]`, `99`)
	}
	if got := s.Complete("sli"); len(got) != 1 || got[0] != "slice" {
		t.Fatal(got)
	}
	for _, source := range []string{`help()`, `help(slice)`, `help(a)`} {
		result, err := s.Eval(context.Background(), source, Options{})
		if err != nil {
			t.Fatal(err)
		}
		help, _ := result.Value.Text()
		if !strings.Contains(help, "step") {
			t.Fatal("outdated slice help", source, help)
		}
	}
}
