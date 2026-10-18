package tests

import (
	"context"
	"fmt"
	. "lip/script"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFunctionProposalSummaries(t *testing.T) {
	for _, test := range []struct{ name, declarations, setup, request string }{
		{"direct", `fn request(x:number)->effect {effect worker.read(x)}`, "", "request(x)"},
		{"any_annotation", `fn request(x:number)->any {effect worker.read(x)}`, "", "request(x)"},
		{"forward_chain", `fn request(x:number)->effect {middle(x)}
fn middle(x:number)->effect {leaf(x)}
fn leaf(x:number)->effect {effect worker.read(x)}`, "", "request(x)"},
		{"alias", `fn request(x:number)->effect {effect worker.read(x)}`, "f=request", "f(x)"},
		{"anonymous", "", "", `(fn(n:number)->effect {effect worker.read(n)})(x)`},
		{"capture", "", `p=effect worker.read(x); f=fn()->effect {p}`, "f()"},
		{"pipeline", `fn request(x:number)->effect {effect worker.read(x)}`, "", `(x |> request)`},
		{"branch_union", `fn request(x:number)->effect {if x==1 {effect worker.read(x)} else {effect worker.other(x)}}`, "", "request(x)"},
		{"match_union", `fn request(x:number)->effect {match x {1=>effect worker.read(x), _=>effect worker.other(x)}}`, "", "request(x)"},
		{"unused_write", `fn request(x:number)->effect {unused=effect worker.write(x); effect worker.read(x)}`, "", "request(x)"},
		{"effect_parameter_forwarding", `fn request(p:effect)->effect {p}`, "", "request(effect worker.read(x))"},
		{"projected_request", `fn request(x:number)->effect {[effect worker.read(x)][0]}`, "", "request(x)"},
		{"mutual_recursion", `fn request(x:number)->effect {left(5,x)}
fn left(n:number,x:number)->effect {if n==0 {effect worker.read(x)} else {right(n-1,x)}}
fn right(n:number,x:number)->effect {left(n,x)}`, "", "request(x)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
			other := host.Contracts[0]
			other.Operation.Member = "other"
			write := host.Contracts[0]
			write.Operation.Member = "write"
			write.Effect = WriteEffect
			host.Contracts = append(host.Contracts, other, write)
			source := `import python "example.worker" as worker
` + test.declarations + `
flow Main()->list {
 facts=claim()
 for x in [1,2,3] parallel {
 ` + test.setup + `
 value=perform ` + test.request + `
 facts <+ value
 }
 return [row.value for row in facts]
}`
			p, err := Compile(source, host)
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatal("checking invoked provider")
			}
			for _, parallel := range []int{1, 2, 7} {
				calls.Store(0)
				journal := NewMemoryJournal()
				for replay := 0; replay < 2; replay++ {
					r, err := p.Run(context.Background(), nil, Options{RunID: "function-summary", Parallelism: parallel, Journal: journal})
					if err != nil || display(t, r.Output) != "[1,2,3]" || calls.Load() != 3 || len(journal.Records()) != 3 {
						t.Fatal(parallel, replay, r, err, calls.Load())
					}
				}
			}
		})
	}
}

func TestFunctionProposalUnknownAndUnsafe(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	for _, kind := range []string{"write", "serial", "unknown"} {
		c := host.Contracts[0]
		c.Operation.Member = kind
		switch kind {
		case "write":
			c.Effect = WriteEffect
		case "serial":
			c.Concurrent = false
		case "unknown":
			c.Effect = UnknownEffect
		}
		host.Contracts = append(host.Contracts, c)
	}
	for _, test := range []struct{ name, declarations, setup, request string }{
		{"write", `fn bad(x:number)->effect {effect worker.write(x)}`, "", "bad(x)"},
		{"serial", `fn bad(x:number)->effect {effect worker.serial(x)}`, "", "bad(x)"},
		{"unknown_contract", `fn bad(x:number)->effect {effect worker.unknown(x)}`, "", "bad(x)"},
		{"unselected_write", `fn bad(x:number)->effect {if true {effect worker.read(x)} else {effect worker.write(x)}}`, "", "bad(x)"},
		{"function_parameter", `fn bad(f:function,x:number)->effect {f(x)}`, "", "bad(request,x)"},
		{"carry_request", `fn bad(x:number)->effect {for i in [0] with p=effect worker.read(x) {next p}}`, "", "bad(x)"},
		{"empty_recursive_component", `fn bad(x:number)->effect {bad(x)}`, "", "bad(x)"},
		{"recursive_unknown_parameter", `fn bad(n:number,p:effect)->effect {if n==0 {p} else {bad(n-1,p)}}`, "", "bad(2,effect worker.unknown(x))"},
		{"recursive_parameter_swap", `fn bad(n:number,p:effect,q:effect)->effect {if n==0 {p} else {bad(n-1,q,p)}}`, "", "bad(2,effect worker.read(x),effect worker.write(x))"},
		{"known_parameter_swap", `fn middle(p:effect,q:effect)->effect {p}
fn bad(p:effect,q:effect)->effect {middle(q,p)}`, "", "bad(effect worker.read(x),effect worker.write(x))"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `import python "example.worker" as worker
fn request(x:number)->effect {effect worker.read(x)}
` + test.declarations + `
flow Main()->void {for x in [1] parallel {` + test.setup + `;perform ` + test.request + `}}`
			_, err := Compile(source, host)
			if d, ok := err.(*Diagnostic); !ok || d.Code != "E-PARALLEL" || calls.Load() != 0 {
				t.Fatal(err, calls.Load())
			}
		})
	}
	// Argument evaluation effects must not be replaced by the returned Ops.
	_, err := Compile(`import python "example.worker" as worker
fn request(x:number)->effect {effect worker.read(x)}
flow Main()->void {for x in [1] parallel {p=request(perform effect worker.write(x))}}`, host)
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-PARALLEL" || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
	// The inferred operation's result schema participates in ordinary checking.
	_, err = Compile(`import python "example.worker" as worker
fn request()->effect {effect worker.read(1)}
flow Main()->number {return len(perform request())}`, host)
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-TYPE" || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
}

func TestFunctionProposalClosedValuesAndRecursiveParameters(t *testing.T) {
	tests := []struct {
		name, declarations, setup, request string
	}{
		{"list_function", ``, `f=[request][0]`, "f(x)"},
		{"conditional_function", ``, `f=if true {request} else {request}`, "f(x)"},
		{"returned_function", `fn maker()->function {request}`, `f=maker()`, "f(x)"},
		{"recursive_parameter", `fn recursive(n:number,p:effect)->effect {if n==0 {p} else {recursive(n-1,p)}}`, ``, "recursive(3,effect worker.read(x))"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			host := testHost(func(_ context.Context, r Request) Response {
				calls.Add(1)
				return Result(r.Positional[0])
			})
			source := `import python "example.worker" as worker
fn request(x:number)->effect {effect worker.read(x)}
` + test.declarations + `
flow Main()->list {
 facts=claim()
 for x in [1,2,3] parallel {
 ` + test.setup + `
 value=perform ` + test.request + `
 facts <+ value
 }
 return [row.value for row in facts]
}`
			p, err := Compile(source, host)
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatal("checking invoked provider", calls.Load())
			}
			r, err := p.Run(context.Background(), nil, Options{Parallelism: 3})
			if err != nil || display(t, r.Output) != "[1,2,3]" || calls.Load() != 3 {
				t.Fatal(r, err, calls.Load())
			}
		})
	}
}

// A provider rendezvous proves actual overlap both in explicit parallel loops
// and ordinary independent nodes, with a write barrier before and after reads.
func TestFunctionProposalSchedulingAndGo(t *testing.T) {
	const hostSource = `package main
import("context";"sync";"fmt")
func generatedHost()(Host,error){
 var mu sync.Mutex
 entered,finished,stage:=0,0,0
 release:=make(chan struct{})
 return Host{Contracts:[]Contract{
 {Operation:Operation{Language:"host",Path:"summary",Member:"read"},Version:"1",Params:[]Parameter{{Name:"value",Schema:Schema{Type:"number"}}},Result:Schema{Type:"number"},Effect:ReadOnlyEffect,Concurrent:true,
 Invoke:func(ctx context.Context,r Request)Response{
  mu.Lock();if stage!=1{mu.Unlock();return Rejected("E-ORDER","read crossed write")};entered++;if entered==2{close(release)};mu.Unlock()
  select{case <-release:case <-ctx.Done():return Uncertain("reads did not overlap")}
  mu.Lock();finished++;mu.Unlock();return Result(r.Positional[0])
 }},
 {Operation:Operation{Language:"host",Path:"summary",Member:"write"},Version:"1",Params:[]Parameter{{Name:"value",Schema:Schema{Type:"number"}}},Result:Schema{Type:"number"},Effect:WriteEffect,
 Invoke:func(_ context.Context,r Request)Response{
  mu.Lock();defer mu.Unlock();n,_:=r.Positional[0].Float()
  if n==0 {stage=1} else {if finished!=2{return Rejected("E-ORDER",fmt.Sprint(finished))};stage=2}
  return Result(r.Positional[0])
 }},
 }},nil
}`
	for _, flowBuilder := range []bool{false, true} {
		for _, body := range []string{
			`facts=claim();for x in [1,2] parallel {value=perform requests.request(x);facts <+ value};return [row.value for row in facts]`,
			`a=perform requests.request(1);b=perform requests.request(2);return [a,b]`,
		} {
			source := `import host "summary"
import lip "./requests.lip" as requests
flow Main()->list {perform effect summary.write(0);` + strings.Replace(body, "return ", "perform effect summary.write(1);return ", 1) + `}`
			set := SourceSet{Root: "main.lip", Sources: map[string]Source{
				"main.lip": {Version: "local", Text: source},
				"requests.lip": {Version: "local", Text: `import host "summary"
export fn request(x:number)->effect {wrap(x)}
fn wrap(x:number)->effect {effect summary.read(x)}`},
			}}
			if flowBuilder {
				module := set.Sources["requests.lip"]
				module.Text = strings.Replace(module.Text, "export fn request(x:number)->effect {wrap(x)}", "export flow request(x:number)->effect {return wrap(x)}", 1)
				set.Sources["requests.lip"] = module
			}
			// Runtime Host uses the same descriptor fingerprints as the generated fixture.
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			var finished atomic.Int64
			host := Host{Contracts: []Contract{
				{Operation: Operation{Language: "host", Path: "summary", Member: "read"}, Version: "1", Params: []Parameter{{Name: "value", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true,
					Invoke: func(ctx context.Context, r Request) Response {
						entered <- struct{}{}
						select {
						case <-release:
						case <-ctx.Done():
							return Uncertain("no overlap")
						}
						finished.Add(1)
						return Result(r.Positional[0])
					}},
				{Operation: Operation{Language: "host", Path: "summary", Member: "write"}, Version: "1", Params: []Parameter{{Name: "value", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: WriteEffect,
					Invoke: func(_ context.Context, r Request) Response {
						n, _ := r.Positional[0].Float()
						if n == 1 && finished.Load() != 2 {
							return Rejected("E-ORDER", "write preceded reads")
						}
						return Result(r.Positional[0])
					}},
			}}
			p, err := CompileSourceSet(set, host, CompileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			done := make(chan error, 1)
			go func() {
				r, err := p.Run(ctx, nil, Options{Parallelism: 2})
				if err == nil && display(t, r.Output) != "[1,2]" {
					err = fmt.Errorf("unexpected output")
				}
				done <- err
			}()
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-ctx.Done():
					cancel()
					t.Fatal("wrapped reads did not overlap")
				}
			}
			close(release)
			err = <-done
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			executable, _ := buildNative(t, p, GoOptions{HostSource: hostSource})
			out, stderr, err := runNative(t, executable, "-parallel", "2")
			if err != nil || strings.TrimSpace(out) != "[1,2]" {
				t.Fatal(out, stderr, err)
			}
		}
	}
}

func TestSessionFunctionProposalSummaries(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eval := func(source string) {
		t.Helper()
		if _, err := s.Eval(context.Background(), source, Options{Parallelism: 2}); err != nil {
			t.Fatal(source, err)
		}
	}
	eval(`import python "example.worker" as worker`)
	eval(`fn request(x:number)->effect {next_request(x)}
fn next_request(x:number)->effect {effect worker.read(x)}`)
	eval(`alias=request`)
	eval(`p=alias(1)`)
	eval(`captured=fn()->effect {p}`)
	eval(`for x in [1,2] parallel {perform alias(x)}`)
	eval(`for x in [1,2] parallel {perform captured()}`)
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
	for _, source := range []string{`dynamic=[request][0]`, `unknown=[p][0]`, `fn identity(p:effect)->effect {p}`} {
		eval(source)
	}
	for _, request := range []string{"dynamic(1)", "unknown", "identity(p)"} {
		if _, err := s.Eval(context.Background(), `for x in [1] parallel {perform `+request+`}`, Options{}); err != nil {
			t.Fatal(request, err)
		}
	}
	if calls.Load() != 7 {
		t.Fatal("known readonly projections were not executed", calls.Load())
	}
	// Deleting/reusing a public name must not retarget a saved closure or summary.
	if err := s.Delete("p", "request"); err != nil {
		t.Fatal(err)
	}
	eval(`p=[effect worker.read(9)][0]`)
	eval(`fn request(x:number)->effect {[effect worker.read(x)][0]}`)
	eval(`for x in [1] parallel {perform captured();perform alias(x)}`)
	if calls.Load() != 9 {
		t.Fatal(calls.Load())
	}
	// A failed declaration cell publishes no summary and does not replay history.
	_, err = s.Eval(context.Background(), `fn failed()->effect {effect worker.read(7)};fail("stop")`, Options{})
	if err == nil {
		t.Fatal("failed cell succeeded")
	}
	_, err = s.Eval(context.Background(), `failed()`, Options{})
	if err == nil || calls.Load() != 9 {
		t.Fatal("failed summary committed", err, calls.Load())
	}
}

func TestSessionClosedValuesAndRecursiveParameters(t *testing.T) {
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
	eval := func(source string, options Options) {
		t.Helper()
		if _, err := s.Eval(context.Background(), source, options); err != nil {
			t.Fatal(source, err)
		}
	}
	eval(`import python "example.worker" as worker`, Options{})
	eval(`fn request(x:number)->effect {effect worker.read(x)}
fn maker()->function {request}
fn retry(n:number,p:effect)->effect {if n==0 {p} else {retry(n-1,p)}}`, Options{})
	eval(`from_list=[request][0]
from_condition=if true {request} else {request}
from_return=maker()`, Options{})
	if calls.Load() != 0 {
		t.Fatal("summary construction invoked provider", calls.Load())
	}
	eval(`for x in [1,2] parallel {
perform from_list(x)
perform from_condition(x)
perform from_return(x)
perform retry(2,effect worker.read(x))
}`, Options{Parallelism: 4})
	if calls.Load() != 8 {
		t.Fatal("session did not preserve closed/recursive summaries", calls.Load())
	}
}

func TestFunctionProposalRuntimeBoundaries(t *testing.T) {
	const source = `import python "example.worker" as worker
fn request(x:number)->effect {effect worker.read(x)}
flow Main()->number {return perform request(1)}`
	t.Run("authorization", func(t *testing.T) {
		var calls atomic.Int64
		host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
		host.Contracts[0].Capabilities = []string{"read"}
		p, err := Compile(source, host)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Run(context.Background(), nil, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-CAPABILITY" || calls.Load() != 0 {
			t.Fatal(err, calls.Load())
		}
	})
	t.Run("completed_write_schema_fault", func(t *testing.T) {
		var calls atomic.Int64
		host := testHost(func(context.Context, Request) Response { calls.Add(1); return Result(textValue("invalid")) })
		host.Contracts[0].Effect = WriteEffect
		p, err := Compile(source, host)
		if err != nil {
			t.Fatal(err)
		}
		journal := NewMemoryJournal()
		for replay := 0; replay < 2; replay++ {
			r, err := p.Run(context.Background(), nil, Options{RunID: "summary-known-fault", Journal: journal})
			if d, ok := err.(*Diagnostic); !ok || d.Code != "E-SCHEMA" || calls.Load() != 1 || r.EntryOutput != nil {
				t.Fatal(replay, r, err, calls.Load())
			}
		}
	})
	t.Run("captured_snapshot_lifetime", func(t *testing.T) {
		s, err := NewSession(Host{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		_, err = s.Eval(context.Background(), `facts=claim();facts <+ 1;p=effect claim.snapshot("facts");reader=fn()->effect{p}`, Options{})
		if err != nil {
			t.Fatal(err)
		}
		// The known readonly summary permits checking; the old run-bound request
		// still fails resource validation rather than observing a different store.
		_, err = s.Eval(context.Background(), `for x in [1] parallel {perform reader()}`, Options{Parallelism: 2})
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-RESOURCE" {
			t.Fatal(err)
		}
	})
}
