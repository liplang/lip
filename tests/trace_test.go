package tests

import (
	"context"
	"errors"
	"fmt"
	. "lip/script"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func traceBuffer(t *testing.T, capacity int) *TraceBuffer {
	t.Helper()
	b, err := NewTraceBuffer(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func traceEvents(b *TraceBuffer) []TraceEvent {
	b.Close()
	var out []TraceEvent
	for event := range b.Events() {
		out = append(out, event)
	}
	return out
}

func TestDiagnosticComposition(t *testing.T) {
	const source = `fn mark(x:number)->number {log("debug",x);x*2}
fn walk(n:number,total:number)->number {if n==0 {total} else {log("tail",n);walk(n-1,total+n)}}
fn make(x:number)->function {fn(y:number)->number{telemetry("closure",{x:x,y:y});x+y}}
flow Pass(f:function,x:number)->number{return f(x)}
flow Main()->object {
 skipped=if false {mark(999)} else {0}
 short=false && {print("unselected");true}
 values=map([1,2],mark)
 folded=fold([1,2],0,fn(acc:number,x:number)->number{log("fold",x);acc+x})
 carried=for x in [1,2] with s=0 {next {print(x);s+x}}
 guarded=[x for x in [1,2] if {log("guard",x);x>1}]
 captured=Pass(make(10),2)
 piped=3 |>mark
 tail=walk(3,0)
 described=print({callback:make(1)})
 constants=[print("one"),log("info","two"),telemetry("event",{})]
 return {skipped:skipped,short:short,values:values,folded:folded,carried:carried,
 guarded:guarded,captured:captured,piped:piped,tail:tail,described:described,constants:constants}
}`
	const want = `{"captured":12,"carried":3,"constants":[null,null,null],"described":null,"folded":3,"guarded":[2],"piped":6,"short":false,"skipped":0,"tail":6,"values":[2,4]}`
	p, err := Compile(source, Host{})
	if err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 7} {
		var steps int64
		for _, mode := range []string{"off", "on", "full", "closed"} {
			t.Run(fmt.Sprintf("%d/%s", parallel, mode), func(t *testing.T) {
				var traces *TraceBuffer
				if mode != "off" {
					capacity := 64
					if mode == "full" {
						capacity = 1
					}
					traces = traceBuffer(t, capacity)
					if mode == "closed" {
						traces.Close()
					}
				}
				r, err := p.Run(context.Background(), nil, Options{Parallelism: parallel, Trace: traces})
				if err != nil || display(t, r.Output) != want || len(r.Journal.Records()) != 0 {
					t.Fatal(r, err)
				}
				if mode == "off" {
					steps = r.Steps
				} else if r.Steps != steps {
					t.Fatal("diagnostics changed the instruction budget", steps, r.Steps)
				}
				if traces != nil {
					events := traceEvents(traces)
					if mode == "on" {
						if len(events) != 17 || traces.Dropped() != 0 {
							t.Fatal("missing/extra emission attempts", len(events), traces.Dropped(), events)
						}
						var tail []string
						for _, event := range events {
							if event.Text == "999" || event.Text == "unselected" || event.Line < 1 || len(event.Position) == 0 {
								t.Fatal("unselected output or missing attribution", event)
							}
							if event.Level == "tail" {
								tail = append(tail, event.Text)
							}
						}
						if strings.Join(tail, ",") != "3,2,1" {
							t.Fatal("local diagnostic order", tail)
						}
					} else if traces.Dropped() == 0 {
						t.Fatal("full/closed receiver did not drop diagnostics")
					}
				}
			})
		}
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, parallel := range []string{"1", "7"} {
		for _, enabled := range []string{"true", "false"} {
			stdout, stderr, err := runNative(t, executable, "-parallel", parallel, "-trace="+enabled)
			if err != nil || strings.TrimSpace(stdout) != want || strings.Contains(stderr, "unselected") || strings.Contains(stderr, "999") {
				t.Fatal(stdout, stderr, err)
			}
			if enabled == "true" && (!strings.Contains(stderr, "[telemetry closure]") || !strings.Contains(stderr, "<function>")) || enabled == "false" && stderr != "" {
				t.Fatal("generated diagnostic delivery", stderr)
			}
		}
	}
	session, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	traces := traceBuffer(t, 64)
	if _, err := session.Eval(context.Background(), source, Options{Trace: traces}); err != nil {
		t.Fatal(err)
	}
	if len(traces.Events()) != 0 {
		t.Fatal("declaration executed diagnostics")
	}
	r, err := session.Eval(context.Background(), "Main()", Options{Trace: traces, Parallelism: 7})
	if err != nil || display(t, r.Value) != want || len(traceEvents(traces)) != 17 {
		t.Fatal(r, err)
	}
}

func TestDiagnosticArgumentsAndEffects(t *testing.T) {
	for _, source := range []string{
		`flow Main()->number {print(parse_number("01"));return 7}`,
		`flow Main()->number {f=[log][0];f(3,{print("argument");4});return 7}`,
	} {
		p, err := Compile(source, Host{})
		if err != nil {
			t.Fatal(err)
		}
		var first string
		for _, enabled := range []bool{false, true} {
			var traces *TraceBuffer
			if enabled {
				traces = traceBuffer(t, 8)
			}
			r, err := p.Run(context.Background(), nil, Options{Trace: traces})
			if err == nil || r.Error == nil {
				t.Fatal("argument failure disappeared", r, err)
			}
			if !enabled {
				first = err.Error()
			} else if first != err.Error() {
				t.Fatal("receiver changed the error", first, err)
			}
			if traces != nil {
				events := traceEvents(traces)
				if strings.Contains(source, "argument") && (len(events) != 1 || events[0].Text != "argument") {
					t.Fatal("argument evaluation order", events)
				}
			}
		}
		executable, _ := buildNative(t, p, GoOptions{})
		for _, enabled := range []string{"false", "true"} {
			stdout, stderr, err := runNative(t, executable, "-trace="+enabled)
			if err == nil || stdout != "" || !strings.Contains(stderr, first) {
				t.Fatal(stdout, stderr, err)
			}
		}
	}
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	write := host.Contracts[0]
	write.Operation.Member, write.Effect = "write", WriteEffect
	host.Contracts = append(host.Contracts, write)
	for _, source := range []string{
		`fn bad()->null {print(perform effect worker.read(1))} flow Main()->null{return bad()}`,
		`flow Main()->void {for x in [1] parallel {print(perform effect worker.write(x))}}`,
		`fn bad()->number {telemetry("event",[]);1} flow Main()->number{return bad()}`,
	} {
		if _, err := Compile("import python \"example.worker\" as worker\n"+source, host); err == nil || calls.Load() != 0 {
			t.Fatal("diagnostic bypassed ordinary checking", source, err, calls.Load())
		}
	}
	// Dynamic arity failure precedes both provider execution and diagnostic args.
	p, err := Compile(`import python "example.worker" as worker
flow Main()->null {f=[print][0];return f(perform effect worker.read(1),print("wrong"))}`, host)
	if err != nil {
		t.Fatal(err)
	}
	traces := traceBuffer(t, 8)
	r, err := p.Run(context.Background(), nil, Options{Trace: traces})
	if err == nil || r.Error.Code != "E-ARITY" || calls.Load() != 0 || len(traceEvents(traces)) != 0 {
		t.Fatal(r, err, calls.Load())
	}
}

func TestDiagnosticRequestSummariesAndReplay(t *testing.T) {
	const source = `import python "example.worker" as worker
fn request(x:number)->effect {log("request",x);effect worker.read(x)}
flow Main()->list {facts=claim();for x in [1,2] parallel {v=perform request(x);facts <+ v};return [row.value for row in facts]}`
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	journal := NewMemoryJournal()
	for repeat := 0; repeat < 2; repeat++ {
		traces := traceBuffer(t, 8)
		r, err := p.Run(context.Background(), nil, Options{RunID: "trace-replay", Trace: traces, Parallelism: 7, Journal: journal})
		if err != nil || display(t, r.Output) != "[1,2]" || len(journal.Records()) != 2 || calls.Load() != 2 || len(traceEvents(traces)) != 2 {
			t.Fatal("diagnostics changed operation identity/replay", repeat, r, err, calls.Load())
		}
	}
	// Known read wrappers containing diagnostics still overlap in reality.
	entered, release := make(chan struct{}, 2), make(chan struct{})
	host.Contracts[0].Invoke = func(ctx context.Context, r Request) Response {
		entered <- struct{}{}
		select {
		case <-release:
			return Result(r.Positional[0])
		case <-ctx.Done():
			return Rejected("E-CANCELLED", "cancelled")
		}
	}
	p, err = Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.Run(ctx, nil, Options{Parallelism: 2, Trace: traceBuffer(t, 8)}); done <- err }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("diagnostics serialized read wrappers")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	const hostSource = `package main
import("context";"sync/atomic")
func generatedHost()(Host,error) {
 var entered atomic.Int32;ready:=make(chan struct{})
 return Host{Contracts:[]Contract{{Operation:Operation{"python","example.worker","read"},Version:"1",Params:[]Parameter{{Name:"value",Schema:Schema{Type:"number"}}},Result:Schema{Type:"number"},Effect:ReadOnlyEffect,Concurrent:true,Invoke:func(ctx context.Context,r Request)Response{if entered.Add(1)==2{close(ready)};select{case <-ready:return Result(r.Positional[0]);case <-ctx.Done():return Rejected("E-CANCELLED","cancelled")}}}}},nil
}`
	executable, _ := buildNative(t, p, GoOptions{HostSource: hostSource})
	stdout, stderr, err := runNative(t, executable, "-parallel", "2")
	if err != nil || strings.TrimSpace(stdout) != "[1,2]" || !strings.Contains(stderr, "[request]") {
		t.Fatal(stdout, stderr, err)
	}
}

type traceFaultWriter struct{ panic bool }

func (w traceFaultWriter) Write([]byte) (int, error) {
	if w.panic {
		panic("diagnostic receiver failed")
	}
	return 0, fmt.Errorf("diagnostic receiver failed")
}

func TestDiagnosticReceiverIsolation(t *testing.T) {
	p, err := Compile(`fn sum(n:number,s:number)->number {if n==0 {s} else {print(n);sum(n-1,s+n)}} flow Main()->number{return sum(100,0)}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	for _, panics := range []bool{false, true} {
		traces, stop := StartTraceOutput(traceFaultWriter{panic: panics})
		r, err := p.Run(context.Background(), nil, Options{Trace: traces})
		stop()
		stop()
		if err != nil || display(t, r.Output) != "5050" {
			t.Fatal(r, err)
		}
	}
	traces := traceBuffer(t, 1)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := p.Run(context.Background(), nil, Options{Trace: traces, Parallelism: 7})
			if err != nil || display(t, r.Output) != "5050" {
				t.Error(r, err)
			}
		}()
	}
	traces.Close()
	wg.Wait()
	if traces.Dropped() == 0 {
		t.Fatal("closed/full buffer did not remain bounded")
	}
	// Large Unicode values, deep containers and closures are detached and bounded.
	p, err = Compile(`flow Main(s:string)->number {f=fn()->string{s};print({text:s,callback:f});print(for x in range(100) with s=[] {next [s]});return 7}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	traces = traceBuffer(t, 8)
	large, err := StringValue(strings.Repeat("中文🙂", 10000))
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), map[string]Value{"s": large}, Options{Trace: traces})
	if err != nil || display(t, r.Output) != "7" {
		t.Fatal(r, err)
	}
	for _, event := range traceEvents(traces) {
		if len(event.Text) > 4096 || !utf8.ValidString(event.Text) {
			t.Fatal("unbounded/invalid diagnostic", len(event.Text))
		}
	}
}

// Modules may return diagnostic functions and captured functions as ordinary
// values. Rendering a native object, even after release, never calls its methods.
func TestDiagnosticModulesAndNativeValues(t *testing.T) {
	const module = `export fn saved(x:number)->function {fn(y:number)->number{log("capture",x);x+y}}
export flow Bundle(x:number)->object{return {callback:saved(x),emit:telemetry}}
export fn emit(f:function,x:any)->null {f(x)}`
	const source = `import lip "./lib.lip" as lib
import go "bytes"
flow Main()->number {
    ref=bytes.NewBufferString("abc")
    print(ref)
    print(effect ref.String())
    native_release(ref)
    print(ref)
    bundle=lib.Bundle(2)
    lib.emit([print][0],9)
    bundle.emit("bundle",{ready:true})
    return bundle.callback(4)
}`
	host := NewNativeHost(Host{})
	defer host.Close()
	p, err := CompileSourceSet(SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "local", Text: source}, "lib.lip": {Version: "local", Text: module},
	}}, host, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 7} {
		traces := traceBuffer(t, 16)
		r, err := p.Run(context.Background(), nil, Options{Parallelism: parallel, Trace: traces})
		if err != nil || display(t, r.Output) != "6" || len(r.Journal.Records()) != 2 {
			t.Fatal(r, err)
		}
		events := traceEvents(traces)
		if len(events) != 6 {
			t.Fatal(events)
		}
		var native, effect, captured int
		for _, event := range events {
			switch event.Text {
			case "<native reference>":
				native++
			}
			if strings.HasPrefix(event.Text, "operation: ") &&
				!strings.HasPrefix(event.Text, "operation: {") &&
				strings.Contains(event.Text, "\ncontract.version: \"") &&
				strings.Contains(event.Text, "\narguments.") &&
				strings.Contains(event.Text, "contract.parameters[0].schema.") {
				effect++
			}
			if event.Level == "capture" {
				captured++
				if event.SourceID != "lib.lip" || event.Text != "2" {
					t.Fatal("lost definition source/capture", event)
				}
			}
		}
		if native != 2 || effect != 1 || captured != 1 {
			t.Fatal(events)
		}
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, parallel := range []string{"1", "7"} {
		stdout, stderr, err := runNative(t, executable, "-parallel", parallel)
		if err != nil || stdout != "6\n" || strings.Count(stderr, "<native reference>") != 2 || strings.Count(stderr, "operation: ") != 1 || strings.Contains(stderr, "operation: {") || !strings.Contains(stderr, "[capture] 2") {
			t.Fatal(stdout, stderr, err)
		}
	}
}

func TestEffectReviewDiagnosticsUseSearchablePaths(t *testing.T) {
	host := Host{Contracts: []Contract{{
		Operation: Operation{Language: "host", Path: "review", Member: "inspect"},
		Version:   "v2",
		Params: []Parameter{{Name: "payload", Schema: Schema{Type: "object", Fields: map[string]Schema{
			"nested": {Type: "object", Fields: map[string]Schema{"text": {Type: "string"}}},
		}}}},
		Result: Schema{Type: "string"}, Effect: ReadOnlyEffect, Concurrent: true,
		Invoke: func(context.Context, Request) Response {
			value, _ := StringValue("ok")
			return Result(value)
		},
	}}}
	p, err := Compile(`import host "review"
flow Main()->string {
    request = effect review.inspect({nested:{text:"hello"}})
    print(request)
    return "done"
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	traces := traceBuffer(t, 8)
	r, err := p.Run(context.Background(), nil, Options{Trace: traces})
	if err != nil || display(t, r.Output) != `"done"` {
		t.Fatal(r, err)
	}
	var text string
	for _, event := range traceEvents(traces) {
		if strings.HasPrefix(event.Text, "operation: ") {
			text = event.Text
			break
		}
	}
	for _, want := range []string{
		"operation: host.review.inspect",
		"operation.language: \"host\"",
		"contract.parameters[0].schema.fields.nested.fields.text.type: \"string\"",
		"arguments.positional[0].nested.text: \"hello\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("review output %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, `operation: {`) || strings.Contains(text, `arguments: {`) {
		t.Fatalf("review output is not path-oriented: %q", text)
	}
}

type blockedTraceWriter struct {
	entered, release, done chan struct{}
	once                   sync.Once
}

func (w *blockedTraceWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	return len(p), nil
}

func TestDiagnosticBlockedReceiverAndBudgets(t *testing.T) {
	p, err := Compile(`fn count(n:number)->number {if n==0 {7} else {print(n);count(n-1)}} flow Main()->number{return count(10000)}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	writer := &blockedTraceWriter{entered: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	defer close(writer.release)
	traces, stop := StartTraceOutput(writer)
	done := make(chan error, 1)
	go func() {
		r, err := p.Run(context.Background(), nil, Options{Trace: traces})
		if err == nil && display(t, r.Output) != "7" {
			err = fmt.Errorf("wrong output")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked diagnostic receiver blocked calculation")
	}
	select {
	case <-writer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver never started")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("blocked diagnostic receiver blocked shutdown")
	}
	// Closing/using a zero buffer is also a disabled receiver, without panic.
	var zero TraceBuffer
	zero.Close()
	var budgetError string
	var steps int64
	for _, sink := range []*TraceBuffer{nil, &zero, traceBuffer(t, 1)} {
		r, err := p.Run(context.Background(), nil, Options{MaxSteps: 200, Trace: sink})
		if err == nil || r.Error == nil || r.Error.Code != "E-BUDGET" {
			t.Fatal(r, err)
		}
		if budgetError == "" {
			budgetError = err.Error()
			steps = r.Steps
		} else if budgetError != err.Error() || r.Steps != steps {
			t.Fatal("receiver changed budget failure", r, err)
		}
		if sink != nil {
			sink.Close()
		}
	}
	if zero.Dropped() == 0 {
		t.Fatal("zero buffer did not discard events")
	}
	for _, capacity := range []int{-1, 65537} {
		if _, err := NewTraceBuffer(capacity); err == nil {
			t.Fatal("unbounded buffer accepted", capacity)
		}
	}
}

func TestDiagnosticSessionFailureAndCaptures(t *testing.T) {
	s, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	traces := traceBuffer(t, 32)
	eval := func(source string) CellResult {
		t.Helper()
		r, err := s.Eval(context.Background(), source, Options{Trace: traces})
		if err != nil {
			t.Fatal(source, err)
		}
		return r
	}
	eval(`x=2`)
	eval(`fn keep()->number {log("saved",x);x}`)
	s.Complete("pri")
	if len(traces.Events()) != 0 {
		t.Fatal("declaration or completion emitted diagnostics")
	}
	if err := s.Delete("x"); err != nil {
		t.Fatal(err)
	}
	eval(`x=9`)
	if r := eval(`keep()`); display(t, r.Value) != "2" {
		t.Fatal(r)
	}
	if _, err := s.Eval(context.Background(), `uncommitted={print("before failure");parse_number("01")}`, Options{Trace: traces}); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := s.Eval(context.Background(), `uncommitted`, Options{Trace: traces}); err == nil {
		t.Fatal("failed cell committed a binding")
	}
	if r := eval(`keep()`); display(t, r.Value) != "2" {
		t.Fatal(r)
	}
	events := traceEvents(traces)
	if len(events) != 3 || events[0].Text != "2" || events[1].Text != "before failure" || events[2].Text != "2" {
		t.Fatal("diagnostics rolled back or changed captures", events)
	}
}

func TestDiagnosticCancellation(t *testing.T) {
	p, err := Compile(`fn forever(n:number)->null {print(n);forever(n+1)} flow Main()->null{return forever(0)}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	traces := traceBuffer(t, 1)
	done := make(chan error, 1)
	go func() { _, err := p.Run(ctx, nil, Options{Trace: traces, MaxSteps: 1000000000}); done <- err }()
	select {
	case <-traces.Events():
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("no diagnostic progress")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic recursion ignored cancellation")
	}
	traces.Close()
}
