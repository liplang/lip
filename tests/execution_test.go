package tests

import (
	"context"
	"errors"
	"fmt"
	. "lip/script"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const parallelWorkSource = `import python "example.worker" as worker
flow Main(n:number)->number {
 facts=claim()
 for x in range(n) parallel {
  value=perform effect worker.read(x)
  facts <+ value
 }
 return fold([row.value for row in facts],0,fn(acc:number,x:number)->number{acc+x})
}`

func TestHostRunLifecycle(t *testing.T) {
	type leaseKey struct{}
	for _, mode := range []string{"success", "failure", "budget", "cancel", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var active atomic.Int64
			began, ended := 0, 0
			host := testHost(func(ctx context.Context, r Request) Response {
				active.Add(1)
				defer active.Add(-1)
				if ctx.Value(leaseKey{}) != "leased" {
					t.Error("missing lease context")
				}
				if mode == "cancel" {
					cancel()
					return Uncertain("cancelled write")
				}
				return Result(r.Positional[0])
			})
			host.BeginRun = func(ctx context.Context, info RunInfo) (RunLease, error) {
				began++
				return RunLease{Context: context.WithValue(ctx, leaseKey{}, "leased"), End: func(ctx context.Context, end RunEnd) error {
					ended++
					if active.Load() != 0 || ctx.Err() != nil || end.RunID == "" || end.Journal == nil {
						t.Error("cleanup preceded settlement or inherited cancellation", end)
					}
					if (mode == "failure" || mode == "budget" || mode == "cancel") && end.Error == nil {
						t.Error("cleanup lost run failure")
					}
					if mode == "cleanup" {
						return errors.New("lease cleanup failed")
					}
					return nil
				}}, nil
			}
			body := `return perform effect worker.read(1)`
			if mode == "failure" {
				body = `x=perform effect worker.read(1);return fail("stop")`
			}
			p, err := Compile(`import python "example.worker" as worker;flow Main()->number{`+body+`}`, host)
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{}
			if mode == "budget" {
				opts.MaxSteps = 1
			}
			report, err := p.Run(ctx, nil, opts)
			if mode == "success" && err != nil || mode != "success" && err == nil {
				t.Fatal(mode, report, err)
			}
			if began != 1 || ended != 1 {
				t.Fatal("lifecycle count", began, ended)
			}
			if mode == "cleanup" && (len(report.CleanupErrors) != 1 || report.Error != nil || report.EntryOutput != nil) {
				t.Fatal("cleanup fault became a node error or successful output", report)
			}
		})
	}
}

func TestDiagnosticPhases(t *testing.T) {
	_, err := Compile(`flow Main()->number{return missing}`, Host{})
	if d, ok := err.(*Diagnostic); !ok || d.Phase != "static" {
		t.Fatal(err)
	}
	p, err := Compile(`flow Main(n:number)->number{return 1/n}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Run(context.Background(), nil, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Phase != "host" {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), map[string]Value{"n": number(0)}, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Phase != "runtime" || report.Error.Phase != "runtime" {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	_, stderr, err := runNative(t, executable, "-input", `{"n":0}`)
	if err == nil || !strings.Contains(stderr, "[runtime]") {
		t.Fatal(stderr, err)
	}
}

func recordPeak(peak *atomic.Int64, current int64) {
	for old := peak.Load(); current > old; old = peak.Load() {
		if peak.CompareAndSwap(old, current) {
			return
		}
	}
}

func TestArbitraryParallelismOverlap(t *testing.T) {
	limits := []int{3, 7, 17, 1238, 73295}
	stress := 0
	if setting := os.Getenv("LIP_STRESS_PARALLELISM"); setting != "" {
		var err error
		stress, err = strconv.Atoi(setting)
		if err != nil || stress <= 0 {
			t.Fatal("LIP_STRESS_PARALLELISM must be a positive integer")
		}
		found := false
		for _, limit := range limits {
			found = found || limit == stress
		}
		if !found {
			limits = append(limits, stress)
		}
	}
	for _, limit := range limits {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			began := time.Now()
			items := limit
			if limit == stress {
				// This opt-in case requires enough memory for the requested real
				// provider overlap, rather than just a large admission ceiling.
			} else if limit > 2048 {
				items = 2048
			} else {
				items += 5
			}
			firstWave := min(limit, items)
			started := make(chan struct{}, items)
			release := make(chan struct{})
			var active, peak atomic.Int64
			host := testHost(func(ctx context.Context, request Request) Response {
				recordPeak(&peak, active.Add(1))
				defer active.Add(-1)
				started <- struct{}{}
				select {
				case <-release:
					return Result(request.Positional[0])
				case <-ctx.Done():
					return Uncertain(ctx.Err().Error())
				}
			})
			p, err := Compile(parallelWorkSource, host)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			done := make(chan Report, 1)
			go func() {
				report, _ := p.Run(ctx, map[string]Value{"n": number(float64(items))}, Options{Parallelism: limit, MaxNodes: items*3 + 100, MaxSteps: int64(items)*64 + 1000})
				done <- report
			}()
			for i := 0; i < firstWave; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatalf("only %d/%d providers overlapped: %v", i, firstWave, ctx.Err())
				}
			}
			close(release)
			report := <-done
			if report.Error != nil || report.EntryOutput == nil || num(report.Output) != float64(items*(items-1)/2) || len(report.Journal.Records()) != items || peak.Load() < int64(firstWave) || peak.Load() > int64(limit) || active.Load() != 0 {
				t.Fatalf("limit=%d peak=%d active=%d calls=%d error=%v", limit, peak.Load(), active.Load(), len(report.Journal.Records()), report.Error)
			}
			t.Logf("limit=%d items=%d peak=%d nodes=%d elapsed=%s", limit, items, peak.Load(), len(report.Nodes), time.Since(began))
		})
	}
}

func TestParallelismConfiguration(t *testing.T) {
	p, err := Compile(`flow Main()->number{return 1}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 1, 7, 1238, 73295, int(^uint(0) >> 1)} {
		report, err := p.Run(context.Background(), nil, Options{Parallelism: limit})
		if err != nil || num(report.Output) != 1 {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	if _, err := p.Run(context.Background(), nil, Options{Parallelism: -1}); err == nil {
		t.Fatal("negative parallelism silently selected sequential execution")
	}
}

func TestGraphConstructionStopsAtNodeBudget(t *testing.T) {
	var source strings.Builder
	source.WriteString("flow Main()->number{if true {\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&source, "value%d=%d\n", i, i)
	}
	source.WriteString("};return 7}")
	p, err := Compile(source.String(), Host{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 73295, MaxNodes: 10})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-BUDGET" || len(report.Nodes) > 10 || report.EntryOutput != nil {
		t.Fatalf("graph construction exceeded its budget: nodes=%d, error=%v", len(report.Nodes), err)
	}
}

func TestLargeParallelNestedFlowsAndPermits(t *testing.T) {
	for _, limit := range []int{7, 1238, 73295} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var active, peak, calls atomic.Int64
			host := testHost(func(ctx context.Context, request Request) Response {
				recordPeak(&peak, active.Add(1))
				defer active.Add(-1)
				calls.Add(1)
				return Result(request.Positional[0])
			})
			host.Resources = NewResourcePool(map[string]uint64{"connections": 11})
			host.Contracts[0].Resources = map[string]uint64{"connections": 1}
			p, err := CompileWithOptions(`import python "example.worker" as worker
flow Read(x:number)->number {return perform effect worker.read(x)}
flow Twice(x:number)->number {first=x |> Read;return first |> Read}
flow Main(n:number)->number {
 facts=claim()
 for x in range(n) parallel {value=x |> Twice;facts <+ value}
 return fold([row.value for row in facts],0,fn(acc:number,x:number)->number{acc+x})
}`, host, CompileOptions{Entry: "Main"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			report, err := p.Run(ctx, map[string]Value{"n": number(257)}, Options{Parallelism: limit, MaxNodes: 10000, MaxSteps: 100000})
			if err != nil || num(report.Output) != 32896 || calls.Load() != 514 || len(report.Journal.Records()) != 514 || peak.Load() > int64(min(limit, 11)) || active.Load() != 0 {
				t.Fatalf("limit=%d peak=%d active=%d calls=%d: %v", limit, peak.Load(), active.Load(), calls.Load(), err)
			}
		})
	}
}

func TestLargeParallelCancellationReleasesPermits(t *testing.T) {
	var active atomic.Int64
	var cancelRun atomic.Bool
	cancelRun.Store(true)
	started := make(chan struct{}, 11)
	host := testHost(func(ctx context.Context, request Request) Response {
		active.Add(1)
		defer active.Add(-1)
		if cancelRun.Load() {
			started <- struct{}{}
			<-ctx.Done()
			return Uncertain("cancelled")
		}
		return Result(request.Positional[0])
	})
	host.Resources = NewResourcePool(map[string]uint64{"connections": 11})
	host.Contracts[0].Resources = map[string]uint64{"connections": 1}
	p, err := Compile(parallelWorkSource, host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.Run(ctx, map[string]Value{"n": number(257)}, Options{Parallelism: 73295})
		done <- err
	}()
	for i := 0; i < 11; i++ {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("providers did not acquire available permits")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || active.Load() != 0 {
			t.Fatalf("cancellation did not drain active calls: %v, %d", err, active.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled run stranded workers or permit waiters")
	}
	cancelRun.Store(false)
	next, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if report, err := p.Run(next, map[string]Value{"n": number(23)}, Options{Parallelism: 1238}); err != nil || num(report.Output) != 253 {
		t.Fatalf("later run could not reuse permits: %+v, %v", report, err)
	}
}

func BenchmarkParallelScheduler(b *testing.B) {
	host := testHost(func(_ context.Context, request Request) Response { return Result(request.Positional[0]) })
	p, err := Compile(parallelWorkSource, host)
	if err != nil {
		b.Fatal(err)
	}
	for _, items := range []int{1000, 10000, 73295} {
		for _, limit := range []int{7, 1238, 73295} {
			b.Run(fmt.Sprintf("items=%d/parallel=%d", items, limit), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					report, err := p.Run(context.Background(), map[string]Value{"n": number(float64(items))}, Options{Parallelism: limit, MaxNodes: 300000, MaxSteps: 10000000})
					if err != nil || num(report.Output) != float64(items*(items-1)/2) {
						b.Fatal(report.Error, err)
					}
				}
			})
		}
	}
}

func TestCompletedScopesReleaseLocalValues(t *testing.T) {
	const source = `import python "example.worker" as worker
flow Scratch(n:number)->number {payload=range(n);return len(payload)}
flow Build(n:number)->list {return range(n)}
flow Count(xs:list)->number {return len(xs)}
flow Main(mode:string,n:number)->number {
 before=perform effect worker.read(0)
 facts=claim()
 if mode=="block" {payload=range(n+before);facts <+ len(payload)}
 for i in (if mode=="loop" {[0,1]} else {[]}) {
  payload=range(n+before)
  facts <+ len(payload)
 }
 size=match mode {
  "flow" => n+before |> Scratch,
  "pipeline" => n+before |> Build |> Count,
  _ => fold([row.value for row in facts],0,fn(a:number,b:number)->number{a+b})
 }
 return perform effect worker.read(size)
}`
	for _, mode := range []string{"flow", "pipeline", "block", "loop"} {
		t.Run(mode, func(t *testing.T) {
			var baseline uint64
			var retained int64
			host := testHost(func(_ context.Context, request Request) Response {
				runtime.GC()
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				if num(request.Positional[0]) == 0 {
					baseline = memory.HeapAlloc
				} else {
					retained = int64(memory.HeapAlloc) - int64(baseline)
				}
				return Result(request.Positional[0])
			})
			p, err := CompileWithOptions(source, host, CompileOptions{Entry: "Main"})
			if err != nil {
				t.Fatal(err)
			}
			const elements = 300000
			report, err := p.Run(context.Background(), map[string]Value{"mode": textValue(mode), "n": number(elements)}, Options{Parallelism: 7})
			want := float64(elements)
			if mode == "loop" {
				want *= 2
			}
			if err != nil || num(report.Output) != want {
				t.Fatal(report, err)
			}
			// A single payload contains tens of MiB of Values. Inspect the live
			// heap before the run ends, allowing ample unrelated GC variation.
			if retained > 12<<20 {
				t.Fatalf("completed %s scope retained %d bytes", mode, retained)
			}
			t.Logf("live heap change before run completion: %d bytes", retained)
		})
	}
}

func TestParallelActuallyOverlapsAndSelectsEarliestError(t *testing.T) {
	started := make(chan float64, 2)
	release := make(chan struct{})
	host := testHost(func(ctx context.Context, r Request) Response {
		x := num(r.Positional[0])
		started <- x
		if x == 0 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return Rejected("provider-failure", "failure "+formatNumber(x))
	})
	p, err := Compile(`import python "example.worker" as worker
flow Main()->void { for x in [0,1] parallel { perform effect worker.read(x) } }`, host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Report, 1)
	go func() { r, _ := p.Run(ctx, nil, Options{Parallelism: 2}); done <- r }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("calls did not overlap")
		}
	}
	close(release)
	r := <-done
	if r.Error == nil || r.Error.Message != "failure 0" {
		t.Fatal(r.Error)
	}
}
func TestPrerequisiteBeyondErrorPrefixRemains(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response { calls.Add(1); return Result(number(9)) })
	p, err := Compile(`import python "example.worker" as worker
flow Main()->void {
  fail("earlier")
  fail("middle")
  perform effect worker.read(9)
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	sites := p.Sites()
	ordered := []int{}
	for site := range sites {
		ordered = append(ordered, site)
	}
	sort.Ints(ordered)
	r, err := p.Run(context.Background(), nil, Options{Parallelism: 1, After: []After{{Site: ordered[0], DependsOn: ordered[2]}}})
	if err == nil || r.Error.Message != "earlier" || calls.Load() != 1 {
		t.Fatalf("%v calls=%d", err, calls.Load())
	}
}
func TestFutureLoopPrerequisiteBeyondErrorPrefix(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response { calls.Add(1); return Result(number(1)) })
	p, err := Compile(`import python "example.worker" as worker
flow Main()->void {
 fail("earlier")
 fail("middle")
 for x in [1,2] { perform effect worker.read(x) }
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	sites := []int{}
	for site := range p.Sites() {
		sites = append(sites, site)
	}
	sort.Ints(sites)
	r, err := p.Run(context.Background(), nil, Options{After: []After{{Site: sites[0], DependsOn: sites[2]}}})
	if err == nil || r.Error.Message != "earlier" || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
	var body Position
	for _, n := range r.Nodes {
		if len(n.Position) == 2 {
			body = n.Position.Copy()
		}
	}
	if len(body) == 0 {
		t.Fatal(r.Nodes)
	}
	body[0].Index = "1"
	node := func(pos Position) Value { return ListValue(textValue("node"), pos.Wire()) }
	for _, requires := range []Value{
		ListValue(textValue("region"), textValue("loop"), Position{{Site: sites[2], Index: "0"}}.Wire()),
		ListValue(textValue("site"), textValue("node"), textValue(fmt.Sprint(body[1].Site))),
		node(body),
	} {
		calls.Store(0)
		r, err := p.Run(context.Background(), nil, Options{MaxSteps: 10000, After: []After{{Target: node(Position{{Site: sites[0], Index: "0"}}), Requires: requires}}})
		if err == nil || r.Error.Message != "earlier" || calls.Load() != 2 {
			t.Fatal("wire lost future prerequisite", display(t, requires), err, calls.Load(), r.Nodes)
		}
	}
}
func TestBlockOpsAndParallelCarry(t *testing.T) {
	host := testHost(func(_ context.Context, request Request) Response { return Result(request.Positional[0]) })
	r := runSource(t, `import python "example.worker" as worker
flow Main()->list {
 facts=claim()
 for x in [1,2] parallel {
   p={ effect worker.read(x) }
   answer=for round in [0] with s=0 { out=perform p; break out }
   facts <+ answer
 }
 return [row.value for row in facts]
}`, host, Options{Parallelism: 2})
	if display(t, r.Output) != "[1,2]" {
		t.Fatal(display(t, r.Output))
	}
}
func TestNodeLocalJoinsAndParallelRejected(t *testing.T) {
	for _, source := range []string{
		`flow Main()->void { facts=claim(); x={ facts <+ 1; 0 } }`,
		`flow Main()->void { x={ for a in [0] parallel { len([]) }; 0 } }`,
	} {
		if _, err := Compile(source, Host{}); err == nil {
			t.Fatal("accepted forbidden local graph operation")
		}
	}
}
func TestCoreTypesAndOperationArityAreStatic(t *testing.T) {
	host := testHost(func(context.Context, Request) Response { return Result(number(0)) })
	for _, source := range []string{
		`flow Main()->any { return len(1) }`,
		`import python "example.worker" as worker
flow Main()->any { return perform effect worker.read() }`,
		`import python "example.worker" as worker
flow Main()->any { return perform effect worker.read("x") }`,
	} {
		if _, err := Compile(source, host); err == nil {
			t.Fatal("accepted statically invalid arguments")
		}
	}
}

func TestEmptyInfiniteGraphLoopBudgetAndCancellation(t *testing.T) {
	p, err := Compile(`flow Main()->void { for {} }`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{MaxSteps: 10})
	if err == nil || r.Error == nil || r.Error.Code != "E-BUDGET" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = p.Run(ctx, nil, Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestEmptyPureLoopBudgetAndCancellation(t *testing.T) {
	p, err := Compile(`fn spin()->null { for {}; null }
flow Main()->null { return spin() }`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{MaxSteps: 10})
	if err == nil || r.Error == nil || r.Error.Code != "E-BUDGET" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = p.Run(ctx, nil, Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestThreeBusinessFailuresStopSequentialCarry(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response {
		calls.Add(1)
		return Result(object(map[string]Value{"ok": BoolValue(false)}))
	})
	host.Contracts[0].Result = Schema{Type: "object", Fields: map[string]Schema{"ok": {Type: "bool"}}, Required: []string{"ok"}}
	r := runSource(t, `import python "example.worker" as worker
flow Search()->number {
 failures=for item in [0,1,2,3,4] with s=0 {
   result=perform effect worker.read(item)
   count=if result.ok { s } else { s+1 }
   if count >= 3 { break count } else { next count }
 }
 return failures
}`, host, Options{})
	if num(r.Output) != 3 || calls.Load() != 3 {
		t.Fatal(r.Output, calls.Load())
	}
}
func TestCapabilitiesAndZeroResourceNeeds(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response { calls.Add(1); return Result(number(1)) })
	host.Contracts[0].Capabilities = []string{"compute"}
	host.Contracts[0].Resources = map[string]uint64{"unused": 0}
	source := `import python "example.worker" as worker
flow Main()->number { return perform effect worker.read(0) }`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{})
	if err == nil || r.Error.Code != "E-CAPABILITY" || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
	host.Capabilities = map[string]bool{"compute": true}
	r = runSource(t, source, host, Options{})
	if num(r.Output) != 1 || calls.Load() != 1 {
		t.Fatal(r.Output, calls.Load())
	}
}
func TestAtomicResourcesAreReleased(t *testing.T) {
	pool := NewResourcePool(map[string]uint64{"cpu": 1, "io": 1})
	cpuStarted, releaseCPU, ioStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	contract := func(member string, resources map[string]uint64, handler func(context.Context, Request) Response) Contract {
		return Contract{Operation: Operation{Language: "host", Path: "resources", Member: member}, Version: "1", Result: Schema{Type: "null"}, Effect: ReadOnlyEffect, Concurrent: true, Resources: resources, Invoke: handler}
	}
	host := Host{Resources: pool, Contracts: []Contract{
		contract("cpu", map[string]uint64{"cpu": 1}, func(ctx context.Context, _ Request) Response {
			close(cpuStarted)
			select {
			case <-releaseCPU:
			case <-ctx.Done():
			}
			return Result(NullValue())
		}),
		contract("both", map[string]uint64{"cpu": 1, "io": 1}, func(context.Context, Request) Response { return Result(NullValue()) }),
		contract("io", map[string]uint64{"io": 1}, func(context.Context, Request) Response { close(ioStarted); return Result(NullValue()) }),
	}}
	program := func(member string) *Program {
		p, e := Compile("import host \"resources\" as r\nflow Main()->void{ perform effect r."+member+"() }", host)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	cpu, both, io := program("cpu"), program("both"), program("io")
	cpuDone := make(chan error, 1)
	go func() { _, e := cpu.Run(context.Background(), nil, Options{}); cpuDone <- e }()
	<-cpuStarted
	ctx, cancel := context.WithCancel(context.Background())
	bothDone := make(chan error, 1)
	go func() { _, e := both.Run(ctx, nil, Options{}); bothDone <- e }()
	// FIFO admission may keep io behind the waiter. Cancellation must release
	// that waiter without retaining either of its requested resources.
	cancel()
	if err := <-bothDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := io.Run(context.Background(), nil, Options{}); err != nil {
		t.Fatal(err)
	}
	<-ioStarted
	close(releaseCPU)
	if err := <-cpuDone; err != nil {
		t.Fatal(err)
	}
	if _, err := both.Run(context.Background(), nil, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTimeoutIsUncertainAndNeverRetried(t *testing.T) {
	var calls atomic.Int64
	contract := Contract{Operation: Operation{Language: "go", Path: "example.store", Member: "write"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect}
	handler, err := GoHandler(contract, func() error { calls.Add(1); return context.DeadlineExceeded })
	if err != nil {
		t.Fatal(err)
	}
	contract.Invoke = handler
	p, err := Compile(`import go "example.store" as store
flow Main()->void { perform effect store.write() }`, Host{Contracts: []Contract{contract}})
	if err != nil {
		t.Fatal(err)
	}
	journal := NewMemoryJournal()
	for i := 0; i < 2; i++ {
		r, err := p.Run(context.Background(), nil, Options{RunID: "timeout", Journal: journal})
		if err == nil || r.Error.Code != "E-UNCERTAIN" {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("retried uncertain write")
	}
}
func TestGoArgumentConversionFailsBeforeNativeAction(t *testing.T) {
	var calls atomic.Int64
	c := Contract{Operation: Operation{Language: "go", Path: "example.math", Member: "integer"}, Version: "1", Params: []Parameter{{Name: "x", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true}
	handler, err := GoHandler(c, func(x int) int { calls.Add(1); return x })
	if err != nil {
		t.Fatal(err)
	}
	c.Invoke = handler
	p, err := Compile(`import go "example.math" as math
flow Main()->number { return perform effect math.integer(1.5) }`, Host{Contracts: []Contract{c}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{})
	if err == nil || r.Error.Code != "E-SCHEMA" || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
}

func TestNilGoHandlerRejectedAtRegistration(t *testing.T) {
	contract := Contract{Operation: Operation{Language: "go", Path: "example", Member: "read"}, Version: "1", Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true}
	var callback func() float64
	if _, err := GoHandler(contract, callback); err == nil {
		t.Fatal("registered a nil native function")
	}
}

func TestDynamicArityPrecedesEffectfulArguments(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, request Request) Response {
		calls.Add(1)
		return Result(request.Positional[0])
	})
	p, err := Compile(`import python "example.worker" as worker
flow Main()->any {
 f=[fn(x:any)->any{x}][0]
 return f(perform effect worker.read(1), perform effect worker.read(2))
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 4})
	if err == nil || report.Error.Code != "E-ARITY" || calls.Load() != 0 {
		t.Fatal("arity check evaluated arguments", err, calls.Load())
	}
}

// All argument actions belong to the caller cursor. Compare concrete results,
// errors and action traces across nested Flow calls, dynamic functions, control
// expressions, replay and actual generated programs.
func TestFlowValueControlAndFailureComposition(t *testing.T) {
	const source = `import host "composition"
fn erase(value:any)->any{value}
fn again(n:number,f:function)->any{if n==0 {f}else{again(n-1,f)}}
fn request_a(n:number,value:number)->effect{if n==0 {effect composition.touch(value)}else{request_b(n-1,value)}}
fn request_b(n:number,value:number)->effect{request_a(n,value)}
flow Tap(value:number)->number{return perform effect composition.touch(value)}
flow Echo(value:any)->any{return value}
flow Add(x:number,y:number)->number{return x+y}
flow Need(x:number,y:any)->number{Tap(99);return x}
flow Execute(p:effect)->any{return perform p}
flow Main(mode:string)->any{
 return match mode {
 "callee_bad"=>({Tap(1);erase(17)})(Tap(9)),
 "arity"=>[fn(x:number)->number{x}][0](Tap(1),Tap(2)),
 "named_arity"=>[fn(x:number)->number{x}][0](other=Tap(9)),
 "argument_failure"=>Add(fail("first"),y=Tap(9)),
 "function_type"=>[fn(x:number,y:number)->number{fail("body")}][0](erase("bad"),Tap(2)),
 "flow_type"=>Need(erase("bad"),Tap(2)),
 "function_named"=>[fn(x:number,y:number)->number{x*10+y}][0](y=Tap(2),x=Tap(1)),
 "flow_named"=>Add(y=Tap(2),x=Tap(1)),
 "pipeline"=>Tap(1) |>Add(y=Tap(2)),
 "conditional"=>if true {Echo(fn(x:number)->number{x+1})(Tap(3))}else{Tap(99)},
 "short_circuit"=>[false && (Tap(9)==9),true || (Tap(99)==99)],
 "empty"=>[Echo(effect composition.touch(i)) for i in []],
 "carry_effect"=>{p=for i in [1,2] with state=effect composition.touch(0) {value=Execute(Echo(state));next effect composition.touch(value+i)};Execute(p)},
 "carry_function"=>{f=for i in [1,2,3] with state=fn(x:number)->number{x} {saved=Echo(state);next fn(x:number)->number{saved(x)+i}};Echo(f)(0)},
 "closure_list"=>map([Echo(fn(x:number)->number{x+i}) for i in [1,2,3]],fn(f:function)->number{f(10)}),
 "tail_callback"=>Echo(again(5000,fn(x:number)->number{x+1}))(3),
 "recursive_request"=>perform request_b(5,4),
 _=>fail("unknown test mode")
 }
}`
	var mu sync.Mutex
	var trace []int
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "composition", Member: "touch"}, Version: "1", Params: []Parameter{{Name: "value", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: WriteEffect,
		Invoke: func(_ context.Context, r Request) Response {
			mu.Lock()
			trace = append(trace, int(num(r.Positional[0])))
			mu.Unlock()
			return Result(r.Positional[0])
		},
	}}}
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := buildNative(t, p, GoOptions{HostSource: `package main
import("context";"fmt";"os")
func generatedHost()(Host,error){return Host{Contracts:[]Contract{{Operation:Operation{Language:"host",Path:"composition",Member:"touch"},Version:"1",Params:[]Parameter{{Name:"value",Schema:Schema{Type:"number"}}},Result:Schema{Type:"number"},Effect:WriteEffect,Invoke:func(_ context.Context,r Request)Response{value,_:=r.Positional[0].Float();fmt.Fprintf(os.Stderr,"touch=%d\n",int(value));return Result(r.Positional[0])}}}},nil}`})
	for _, test := range []struct{ mode, want, code, trace string }{
		{"callee_bad", "", "E-TYPE", "[1]"},
		{"arity", "", "E-ARITY", "[]"},
		{"named_arity", "", "E-ARITY", "[]"},
		{"argument_failure", "", "E-FAIL", "[]"},
		{"function_type", "", "E-TYPE", "[2]"},
		{"flow_type", "", "E-SCHEMA", "[2]"},
		{"function_named", "12", "", "[2 1]"},
		{"flow_named", "3", "", "[2 1]"},
		{"pipeline", "3", "", "[1 2]"},
		{"conditional", "4", "", "[3]"},
		{"short_circuit", "[false,true]", "", "[]"},
		{"empty", "[]", "", "[]"},
		{"carry_effect", "3", "", "[0 1 3]"},
		{"carry_function", "6", "", "[]"},
		{"closure_list", "[11,12,13]", "", "[]"},
		{"tail_callback", "4", "", "[]"},
		{"recursive_request", "4", "", "[4]"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			for _, parallel := range []int{1, 7} {
				mu.Lock()
				trace = nil
				mu.Unlock()
				journal := NewMemoryJournal()
				input := map[string]Value{"mode": textValue(test.mode)}
				var diagnosticText string
				for replay := 0; replay < 2; replay++ {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					r, runErr := p.Run(ctx, input, Options{Parallelism: parallel, RunID: "control-composition", Journal: journal})
					cancel()
					mu.Lock()
					gotTrace := fmt.Sprint(trace)
					mu.Unlock()
					if gotTrace != test.trace {
						t.Fatal(parallel, replay, "action trace", gotTrace, test.trace)
					}
					if test.code == "" {
						if runErr != nil || display(t, r.Output) != test.want {
							t.Fatal(parallel, replay, r, runErr)
						}
					} else {
						if runErr == nil || r.Error == nil || r.Error.Code != test.code || len(r.Error.Position) == 0 {
							t.Fatal(parallel, replay, r, runErr)
						}
						if replay != 0 && diagnosticText != runErr.Error() {
							t.Fatal("replay changed diagnostic attribution", diagnosticText, runErr)
						}
						diagnosticText = runErr.Error()
					}
				}
				stdout, stderr, nativeErr := runNative(t, exe, "-parallel", strconv.Itoa(parallel), "-input", `{"mode":"`+test.mode+`"}`)
				var nativeTrace []int
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, "touch=") {
						n, err := strconv.Atoi(strings.TrimPrefix(line, "touch="))
						if err != nil {
							t.Fatal(line, err)
						}
						nativeTrace = append(nativeTrace, n)
					}
				}
				if fmt.Sprint(nativeTrace) != test.trace {
					t.Fatal("generated action trace", nativeTrace, stderr)
				}
				if test.code == "" {
					if nativeErr != nil || strings.TrimSpace(stdout) != test.want {
						t.Fatal(stdout, stderr, nativeErr)
					}
				} else if nativeErr == nil || stdout != "" || !strings.Contains(stderr, diagnosticText) {
					t.Fatal("generated error attribution", diagnosticText, stdout, stderr, nativeErr)
				}
			}
		})
	}
}

func TestNullableAnyOperationSchemaUsesCoreTypeRules(t *testing.T) {
	host := testHost(func(_ context.Context, request Request) Response { return Result(request.Positional[0]) })
	host.Contracts[0].Params[0].Schema.Type = "any?"
	host.Contracts[0].Result.Type = "any?"
	r := runSource(t, `import python "example.worker" as worker
flow Main()->string {x=perform effect worker.read("hello");return x+"!"}`, host, Options{})
	if display(t, r.Output) != `"hello!"` {
		t.Fatal(r.Output)
	}
}
func TestProviderCannotImpersonateAdmissionFailure(t *testing.T) {
	host := testHost(func(context.Context, Request) Response { return Rejected("E-CAPABILITY", "provider error") })
	p, err := Compile(`import python "example.worker" as worker
flow Main()->void { perform effect worker.read(0) }`, host)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{})
	if err == nil || r.Error.Code != "E-EFFECT-REJECTED" {
		t.Fatal(err)
	}
}
func TestAfterCycleRejected(t *testing.T) {
	p, e := Compile(`flow Main()->void { len([]); len([]) }`, Host{})
	if e != nil {
		t.Fatal(e)
	}
	sites := []int{}
	for s := range p.Sites() {
		sites = append(sites, s)
	}
	_, e = p.Run(context.Background(), nil, Options{After: []After{{Site: sites[0], DependsOn: sites[1]}, {Site: sites[1], DependsOn: sites[0]}}})
	if e == nil || !strings.Contains(e.Error(), "E-GRAPH-CYCLE") {
		t.Fatal(e)
	}
}

func TestAfterFailureKeepsUnderlyingRoot(t *testing.T) {
	p, err := Compile(`flow Main()->void { len([]); fail("source failure") }`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	sites := []int{}
	for site := range p.Sites() {
		sites = append(sites, site)
	}
	sort.Ints(sites)
	r, err := p.Run(context.Background(), nil, Options{After: []After{{Site: sites[0], DependsOn: sites[1]}}})
	if err == nil || r.Error.Code != "E-FAIL" || r.Error.Message != "source failure" {
		t.Fatalf("root error: %v", err)
	}
	for _, node := range r.Nodes {
		if node.Position[len(node.Position)-1].Site != sites[0] {
			continue
		}
		if node.Status != Skipped || node.Cause != "after_failure" || node.Error != nil || node.CauseError == nil || node.CauseError.Message != "source failure" {
			t.Fatalf("dependent node: %+v", node)
		}
		node.CauseError.Message = "mutated by report consumer"
		if r.Error.Message != "source failure" {
			t.Fatal("node diagnostic aliases the root error")
		}
		return
	}
	t.Fatal("dependent node missing")
}

func TestJournalDiagnosticsAreImmutableObservations(t *testing.T) {
	var calls atomic.Int64
	failure := Rejected("provider-failure", "original failure")
	host := testHost(func(context.Context, Request) Response {
		calls.Add(1)
		return failure
	})
	p, err := Compile(`import python "example.worker" as worker
flow Main()->void { perform effect worker.read(1) }`, host)
	if err != nil {
		t.Fatal(err)
	}
	journal := NewMemoryJournal()
	options := Options{RunID: "immutable-journal", Journal: journal}
	r, err := p.Run(context.Background(), nil, options)
	if err == nil || r.Error.Message != "original failure" {
		t.Fatal(err)
	}
	failure.Error.Message = "mutated by provider"
	records := journal.Records()
	if len(records) != 1 {
		t.Fatal("record missing")
	}
	records[0].Response.Error.Message = "mutated by journal consumer"
	records[0].Request.CallID.Position[0].Index = "999"
	r.Error.Message = "mutated by report consumer"
	r, err = p.Run(context.Background(), nil, options)
	if err == nil || r.Error.Message != "original failure" || calls.Load() != 1 {
		t.Fatalf("replay: %v, calls=%d", err, calls.Load())
	}
}

func TestBarrierAfterUnexpandedLoop(t *testing.T) {
	var mu sync.Mutex
	reads := 0
	host := testHost(func(context.Context, Request) Response {
		time.Sleep(time.Millisecond)
		mu.Lock()
		reads++
		mu.Unlock()
		return Result(number(1))
	})
	write := Contract{Operation: Operation{Language: "go", Path: "example.store", Member: "save"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect, Invoke: func(context.Context, Request) Response {
		mu.Lock()
		defer mu.Unlock()
		if reads != 4 {
			return Rejected("bad-order", "write crossed loop")
		}
		return Result(Value{})
	}}
	host.Contracts = append(host.Contracts, write)
	r := runSource(t, `import python "example.worker" as worker
import go "example.store" as store
flow Main()->void {
 for x in [1,2,3,4] parallel { perform effect worker.read(x) }
 perform effect store.save()
}`, host, Options{Parallelism: 3})
	if len(r.Journal.Records()) != 5 {
		t.Fatal("calls missing")
	}
}
func TestCarryPerformFeedsNextProposal(t *testing.T) {
	var arguments []float64
	host := testHost(func(ctx context.Context, r Request) Response {
		arguments = append(arguments, num(r.Positional[0]))
		return Result(number(num(r.Positional[0]) + 1))
	})
	r := runSource(t, `import python "example.worker" as worker
flow Main()->number {
 answer=for x in [0,1,2] with s=0 {
   next_value=perform effect worker.read(s)
   next next_value
 }
 return answer
}`, host, Options{RunID: "carry"})
	if num(r.Output) != 3 {
		t.Fatal(r.Output)
	}
	if len(arguments) != 3 || arguments[2] != 2 {
		t.Fatal(arguments)
	}
	indices := []string{}
	for _, entry := range r.Journal.Records() {
		indices = append(indices, entry.Request.CallID.EvalPath[0].Index)
	}
	sort.Strings(indices)
	if strings.Join(indices, ",") != "0,1,2" {
		t.Fatal(indices)
	}
}
func TestSkippedGateProducesNeitherCallNorError(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response { calls.Add(1); return Result(number(0)) })
	r := runSource(t, `import python "example.worker" as worker
flow Main()->number {
 if false { x=1/0; perform effect worker.read(0) }
 return 7
}`, host, Options{Parallelism: 4})
	if num(r.Output) != 7 || calls.Load() != 0 {
		t.Fatal(r.Error, calls.Load())
	}
}
func TestKnownSchemaFaultNeverRetries(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response {
		calls.Add(1)
		r := Result(textValue("invalid"))
		r.Receipt = object(map[string]Value{"done": BoolValue(true)})
		return r
	})
	source := `import python "example.worker" as worker
flow Main()->void { perform effect worker.read(0) }`
	p, e := Compile(source, host)
	if e != nil {
		t.Fatal(e)
	}
	journal := NewMemoryJournal()
	for i := 0; i < 2; i++ {
		r, e := p.Run(context.Background(), nil, Options{RunID: "fault", Journal: journal})
		if e == nil || r.Error.Code != "E-SCHEMA" {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 || !journal.Records()[0].KnownFault {
		t.Fatal(calls.Load())
	}
}
func TestAuthorizationBeforeProvider(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(context.Context, Request) Response { calls.Add(1); return Result(number(1)) })
	host.Authorize = func(context.Context, Request) error { return errors.New("denied") }
	p, e := Compile(`import python "example.worker" as worker
flow Main()->void { perform effect worker.read(0) }`, host)
	if e != nil {
		t.Fatal(e)
	}
	r, e := p.Run(context.Background(), nil, Options{})
	if e == nil || r.Error.Code != "E-CAPABILITY" || calls.Load() != 0 {
		t.Fatal(e, calls.Load())
	}
}
func TestGoAndPythonUseSameDataBoundary(t *testing.T) {
	contract := Contract{Operation: Operation{Language: "go", Path: "example.math", Member: "sum"}, Version: "1", Params: []Parameter{{Name: "xs", Schema: Schema{Type: "list", Items: &Schema{Type: "number"}}}}, Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true}
	handler, e := GoHandler(contract, func(xs []float64) float64 {
		sum := 0.0
		for _, x := range xs {
			sum += x
		}
		return sum
	})
	if e != nil {
		t.Fatal(e)
	}
	contract.Invoke = handler
	source := `import go "example.math" as math
flow Main()->number { return perform effect math.sum([1,2,3]) }`
	r := runSource(t, source, Host{Contracts: []Contract{contract}}, Options{})
	if num(r.Output) != 6 {
		t.Fatal(r.Output)
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("python3 unavailable")
	}
	contract.Operation = Operation{Language: "python", Path: "builtins", Member: "sum"}
	contract.Invoke = PythonHandler(python, contract)
	source = strings.ReplaceAll(source, `go "example.math"`, `python "builtins"`)
	r = runSource(t, source, Host{Contracts: []Contract{contract}}, Options{})
	if num(r.Output) != 6 {
		t.Fatal(r.Output)
	}
}
func TestNativeCyclesAndLossyIntegersRejected(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	for _, input := range []any{cyclic, int64(9007199254740992), []byte{1, 2}, complex(1, 2)} {
		if _, e := FromNative(input); e == nil {
			t.Fatalf("accepted %T", input)
		}
	}
}

func TestGoDataSharedSlicesAndInvalidFieldNames(t *testing.T) {
	for _, test := range []struct{ mode, want string }{
		{"prefix", `[7,[7]]`}, {"empty", `[[]]`}, {"shared", `[[7],[7]]`},
	} {
		value, err := FromNative(sharedNativeValue(test.mode))
		if err != nil || display(t, value) != test.want {
			t.Fatalf("%s: %v, %v; want %s", test.mode, value, err, test.want)
		}
	}
	for _, mode := range []string{"slice_cycle", "map_cycle", "mixed_cycle", "pointer_cycle"} {
		if _, err := FromNative(sharedNativeValue(mode)); err == nil {
			t.Fatal("accepted cyclic Data", mode)
		}
	}
	invalid := struct {
		Value int `json:"\xff"`
	}{7}
	if _, err := FromNative(invalid); err == nil {
		t.Fatal("invalid UTF-8 struct key became a successful null")
	}
	var calls atomic.Int64
	c := Contract{Operation: Operation{Language: "go", Path: "shared", Member: "read"}, Version: "1",
		Params: []Parameter{{Name: "mode", Schema: Schema{Type: "string"}}}, Result: Schema{Type: "any"}, Effect: WriteEffect}
	var err error
	c.Invoke, err = GoHandler(c, func(mode string) any {
		calls.Add(1)
		if mode == "invalid" {
			return invalid
		}
		return sharedNativeValue(mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(`import go "shared";flow Main(mode:string)->any{return perform effect shared.read(mode)}`, Host{Contracts: []Contract{c}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ mode, want string }{
		{"prefix", `[7,[7]]`}, {"empty", `[[]]`}, {"invalid", ""}, {"slice_cycle", ""},
	} {
		journal := NewMemoryJournal()
		before := calls.Load()
		for i := 0; i < 2; i++ {
			report, err := p.Run(context.Background(), map[string]Value{"mode": textValue(test.mode)}, Options{RunID: test.mode, Journal: journal})
			if test.want != "" {
				if err != nil || display(t, report.Output) != test.want {
					t.Fatal(test.mode, report, err)
				}
			} else if d, ok := err.(*Diagnostic); !ok || d.Code != "E-VALUE" || report.EntryOutput != nil {
				t.Fatal("conversion failure published success or uncertainty", test.mode, report, err)
			}
			if calls.Load() != before+1 {
				t.Fatal("terminal conversion result repeated the provider", test.mode, calls.Load(), before)
			}
		}
		if records := journal.Records(); len(records) != 1 || records[0].KnownFault != (test.want == "") || !records[0].Response.KnownCompleted {
			t.Fatal("conversion lost the completed provider fact", test.mode, records)
		}
	}
}

func TestFunctionCapturesSnapshotNotMutableInput(t *testing.T) {
	fields := map[string]Value{"score": number(1)}
	original, _ := ObjectValue(fields)
	fields["score"] = number(2)
	p, e := Compile(`flow Main(data:object)->number { f=fn()->number{data.score}; return f() }`, Host{})
	if e != nil {
		t.Fatal(e)
	}
	r, e := p.Run(context.Background(), map[string]Value{"data": original}, Options{})
	if e != nil || num(r.Output) != 1 {
		t.Fatal(e)
	}
}

func TestAfterWireAndChildCompletion(t *testing.T) {
	p, err := CompileWithOptions(`flow Child()->number{value=2;return value} flow Main()->list{first=Child();later=3;return [first,later]}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := p.Run(context.Background(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := []Position{}
	child := []Position{}
	for _, n := range baseline.Nodes {
		if len(n.Position) == 1 {
			root = append(root, n.Position)
		} else {
			child = append(child, n.Position)
		}
	}
	sort.Slice(root, func(i, j int) bool { return root[i][0].Site < root[j][0].Site })
	sort.Slice(child, func(i, j int) bool { return child[i][2].Site < child[j][2].Site })
	if len(root) != 3 || len(child) != 2 {
		t.Fatal(baseline.Nodes)
	}
	node := func(pos Position) Value { tag, _ := StringValue("node"); return ListValue(tag, pos.Wire()) }
	region := func(kind string, pos Position) Value {
		tag, _ := StringValue("region")
		k, _ := StringValue(kind)
		return ListValue(tag, k, pos.Wire())
	}
	for _, parallel := range []int{1, 4} {
		report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel, After: []After{{Target: node(child[0]), Requires: node(root[1])}}})
		if err != nil || display(t, report.Output) != "[2,3]" {
			t.Fatal(report, err)
		}
	}
	for _, requires := range []Value{node(root[0]), region("flow", nil)} {
		report, err := p.Run(context.Background(), nil, Options{After: []After{{Target: node(child[0]), Requires: requires}}})
		if err == nil || !strings.Contains(err.Error(), "E-AFTER-CYCLE") || len(report.Nodes) != 0 {
			t.Fatal("ancestor wait accepted", report, err)
		}
	}
	invalid := append(child[0].Copy(), Pair{Site: 999, Index: "0"})
	if _, err := p.Run(context.Background(), nil, Options{After: []After{{Target: node(root[0]), Requires: node(invalid)}}}); err == nil {
		t.Fatal("invalid dynamic prefix accepted")
	}
	callSite := child[0][1].Site
	site, _ := DecodeData([]byte(fmt.Sprintf(`["site","call","%d"]`, callSite)))
	if _, err := p.Run(context.Background(), nil, Options{After: []After{{Target: node(root[0]), Requires: site}}}); err == nil || !strings.Contains(err.Error(), "CYCLE") {
		t.Fatal("call completion cycle accepted", err)
	}
}

func TestAfterRejectsImpossibleLoopInstances(t *testing.T) {
	for _, source := range []string{
		`flow Child()->number{return 2} flow Main(xs:list)->void{for x in xs parallel{value=Child()};done=1}`,
		`flow Child()->number{return 2} flow Main(xs:list)->void{values=[Child() for x in xs];done=1}`,
	} {
		p, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		baseline, err := p.Run(context.Background(), map[string]Value{"xs": ListValue(number(1))}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		var child, done Position
		for _, n := range baseline.Nodes {
			if len(n.Position) > len(child) {
				child = n.Position.Copy()
			}
			if len(n.Position) == 1 && (len(done) == 0 || n.Position[0].Site > done[0].Site) {
				done = n.Position.Copy()
			}
		}
		if len(child) < 3 {
			t.Fatal(baseline.Nodes)
		}
		node := func(pos Position) Value { return ListValue(textValue("node"), pos.Wire()) }
		for _, count := range []int{0, 1} {
			pos := child.Copy()
			if count == 1 {
				// The first graph/local-loop pair is an iteration index.
				index := 0
				if !strings.Contains(source, "parallel") {
					index = 1
				}
				pos[index].Index = "1"
			}
			xs := ListValue()
			if count == 1 {
				xs = ListValue(number(1))
			}
			_, err := p.Run(context.Background(), map[string]Value{"xs": xs}, Options{Parallelism: 4, After: []After{{Target: node(done), Requires: node(pos)}}})
			if err == nil || !strings.Contains(err.Error(), "E-AFTER-CYCLE") {
				t.Fatal("impossible instance accepted", count, pos, err)
			}
		}
	}
}

func TestAfterCallSiteWaitsForEveryLocalInstance(t *testing.T) {
	var reads atomic.Int64
	host := testHost(func(_ context.Context, request Request) Response {
		n := num(request.Positional[0])
		if n == 9 && reads.Load() != 2 {
			return Rejected("order", "call site released before its final instance")
		}
		if n != 9 {
			reads.Add(1)
		}
		return Result(number(n))
	})
	p, err := CompileWithOptions(`import python "example.worker" as worker
flow Child(x:number)->number{return perform effect worker.read(x)}
flow Main()->number{values=[Child(x) for x in [1,2]];done=perform effect worker.read(9);return done}`, host, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := p.Run(context.Background(), nil, Options{Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	var invocation int
	sites := []int{}
	for site := range p.Sites() {
		sites = append(sites, site)
	}
	sort.Ints(sites)
	for _, n := range baseline.Nodes {
		if len(n.Position) == 4 {
			invocation = n.Position[2].Site
		}
	}
	for _, parallel := range []int{1, 4} {
		reads.Store(0)
		target := ListValue(textValue("node"), Position{{Site: sites[1], Index: "0"}}.Wire())
		requires := ListValue(textValue("site"), textValue("call"), textValue(fmt.Sprint(invocation)))
		report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel, After: []After{{Target: target, Requires: requires}}})
		if err != nil || num(report.Output) != 9 || reads.Load() != 2 {
			t.Fatal(report, err, reads.Load())
		}
	}
}

func TestAfterNestedCursorCompletion(t *testing.T) {
	asObject := func(fields map[string]Value) Value {
		t.Helper()
		value, err := ObjectValue(fields)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	p, err := Compile(`flow Value(n:number)->number{return n}
flow Keep(value:any)->any{return value}
flow Main(mode:string,flag:bool,xs:list)->any{
 return match mode{
  "branch"=>Keep(if flag {Value(9)}else{Value(1)}),
  "short"=>Keep(flag && (Value(9)==9)),
  "loop"=>Keep([Value(x) for x in xs]),
  "local_loop"=>{for x in xs {Value(x)};Keep(1)},
  "local_if"=>{if flag {Value(9)};Keep(1)},
  "local_match"=>{match flag {true=>{Value(9)}};Keep(1)},
  _=>Keep(Value(1))
 }
}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	// The trusted embedding harness supplies Host After through the same public
	// Run API. Generated code remains compiled LIP, with no source interpreter.
	exe, _ := buildNative(t, p, GoOptions{HostSource: `package main
import("context";"fmt";"os";"strconv")
func generatedHost()(Host,error){return Host{},nil}
func init(){
 wire:=os.Getenv("LIP_TEST_AFTER");if wire==""{return}
 constraint,err:=DecodeData([]byte(wire));if err!=nil{panic(err)};fields,_:=constraint.Fields()
 input,err:=DecodeData([]byte(os.Getenv("LIP_TEST_AFTER_INPUT")));if err!=nil{panic(err)};args,_:=input.Fields()
 parallel,_:=strconv.Atoi(os.Getenv("LIP_TEST_AFTER_PARALLEL"))
 p,err:=generatedProgram(Host{});if err!=nil{panic(err)}
 r,err:=p.Run(context.Background(),args,Options{Parallelism:parallel,After:[]After{{Target:fields["target"],Requires:fields["requires"]}}})
 if err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)};out,err:=Display(r.Output);if err!=nil{panic(err)};fmt.Println(out);os.Exit(0)
}`})
	for _, test := range []struct {
		mode, want string
		flag       bool
		empty      bool
		reverse    bool
	}{
		{"branch", "1", false, false, false}, {"branch", "9", true, false, false},
		{"short", "false", false, false, false}, {"short", "true", true, false, false},
		{"loop", "[1,2]", false, false, false}, {"loop", "[]", false, true, false},
		{"local_loop", "1", false, false, false}, {"local_loop", "1", false, true, false},
		{"local_if", "1", true, false, false}, {"local_if", "1", false, false, false},
		{"local_match", "1", true, false, false}, {"local_match", "1", false, false, false},
		{"direct", "1", false, false, false}, {"direct", "", false, false, true},
	} {
		t.Run(fmt.Sprintf("%s/%t/%t/%t", test.mode, test.flag, test.empty, test.reverse), func(t *testing.T) {
			input := map[string]Value{"mode": textValue(test.mode), "flag": BoolValue(test.flag), "xs": ListValue(number(1), number(2))}
			prefixes := map[int]Position{}
			for _, flag := range []bool{true, false} {
				input["flag"] = BoolValue(flag)
				r, err := p.Run(context.Background(), input, Options{Parallelism: 1})
				if err != nil {
					t.Fatal(err)
				}
				for _, n := range r.Nodes {
					if len(n.Position) > 1 {
						prefix := n.Position[:len(n.Position)-1].Copy()
						prefixes[prefix[len(prefix)-1].Site] = prefix
					}
				}
			}
			var sites []int
			for site := range prefixes {
				sites = append(sites, site)
			}
			sort.Ints(sites)
			if len(sites) < 2 {
				t.Fatal("missing invocation prefixes", prefixes)
			}
			input["flag"] = BoolValue(test.flag)
			if test.empty {
				input["xs"] = ListValue()
			}
			for _, selectorMode := range []int{0, 1, 2} {
				dynamic := selectorMode == 1
				if selectorMode == 2 && (test.mode != "direct" || test.reverse) {
					continue
				}
				if dynamic && (test.mode == "loop" || test.mode == "local_loop") {
					continue // Empty dynamic indices are covered by impossible-index tests.
				}
				selector := func(site int) Value {
					if dynamic {
						return ListValue(textValue("region"), textValue("flow"), prefixes[site].Wire())
					}
					return ListValue(textValue("site"), textValue("call"), textValue(strconv.Itoa(site)))
				}
				target, requires := sites[0], sites[1]
				if strings.HasPrefix(test.mode, "local_") {
					// Source order places the statement call before Keep here.
					target, requires = requires, target
				}
				if selectorMode == 2 {
					other := map[string]Value{"mode": textValue("branch"), "flag": BoolValue(true), "xs": ListValue()}
					r, err := p.Run(context.Background(), other, Options{})
					if err != nil {
						t.Fatal(err)
					}
					// Value(9) lies in a different, unselected outer match arm.
					requires = 0
					for _, n := range r.Nodes {
						if len(n.Position) > 1 && n.Position[len(n.Position)-2].Site > requires {
							requires = n.Position[len(n.Position)-2].Site
						}
					}
				}
				constraint := After{Target: selector(target), Requires: selector(requires)}
				if test.reverse {
					constraint.Target, constraint.Requires = constraint.Requires, constraint.Target
				}
				for _, parallel := range []int{1, 7} {
					for repeat := 0; repeat < 10; repeat++ {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						r, err := p.Run(ctx, input, Options{Parallelism: parallel, After: []After{constraint}})
						cancel()
						if test.reverse {
							if d, ok := err.(*Diagnostic); !ok || d.Code != "E-GRAPH-CYCLE" {
								t.Fatal(dynamic, parallel, repeat, "accepted reversed cursor order", r, err)
							}
						} else if err != nil || display(t, r.Output) != test.want {
							t.Fatal(dynamic, parallel, repeat, r, err)
						}
					}
					t.Setenv("LIP_TEST_AFTER", display(t, asObject(map[string]Value{"target": constraint.Target, "requires": constraint.Requires})))
					t.Setenv("LIP_TEST_AFTER_INPUT", display(t, asObject(input)))
					t.Setenv("LIP_TEST_AFTER_PARALLEL", strconv.Itoa(parallel))
					stdout, stderr, err := runNative(t, exe)
					if test.reverse {
						if err == nil || !strings.Contains(stderr, "E-GRAPH-CYCLE") {
							t.Fatal(dynamic, parallel, stdout, stderr, err)
						}
					} else if err != nil || strings.TrimSpace(stdout) != test.want {
						t.Fatal(dynamic, parallel, stdout, stderr, err)
					}
				}
			}
		})
	}
}

func TestAfterEmptyFlowCompletion(t *testing.T) {
	p, err := CompileWithOptions(`flow Empty()->void{} flow Main()->number{Empty();marker=fail("later");return marker}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	sites := []int{}
	for site := range p.Sites() {
		sites = append(sites, site)
	}
	sort.Ints(sites)
	call := ListValue(textValue("site"), textValue("call"), textValue(fmt.Sprint(sites[0])))
	flow := ListValue(textValue("region"), textValue("flow"), Position{{Site: sites[0], Index: "0"}, {Site: sites[0], Index: "0"}}.Wire())
	marker := ListValue(textValue("node"), Position{{Site: sites[1], Index: "0"}}.Wire())
	for _, target := range []Value{call, flow} {
		for _, parallel := range []int{1, 4} {
			report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel, After: []After{{Target: target, Requires: marker}}})
			if err == nil || report.Error.Message != "later" {
				t.Fatal(report, err)
			}
			for _, n := range report.Nodes {
				if n.Position[0].Site == sites[0] && n.Status != Skipped {
					t.Fatal("empty child ignored its completion dependency", n)
				}
			}
		}
		if _, err := p.Run(context.Background(), nil, Options{After: []After{{Target: target, Requires: target}}}); err == nil || !strings.Contains(err.Error(), "E-AFTER-CYCLE") {
			t.Fatal("empty child accepted self dependency", err)
		}
	}
	if _, err := p.Run(context.Background(), nil, Options{After: []After{{Target: call, Requires: flow}}}); err == nil || !strings.Contains(err.Error(), "CYCLE") {
		t.Fatal("empty child accepted equivalent completion cycle", err)
	}
}

func TestPermitWaitYieldsWorker(t *testing.T) {
	release, entered := make(chan struct{}), make(chan struct{}, 1)
	host := Host{Resources: NewResourcePool(map[string]uint64{"one": 1}), Contracts: []Contract{
		{Operation: Operation{Language: "host", Path: "app", Member: "read"}, Version: "1", Params: []Parameter{{Name: "n", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "null"}, Effect: ReadOnlyEffect, Concurrent: true, Resources: map[string]uint64{"one": 1}, Invoke: func(ctx context.Context, r Request) Response {
			if num(r.Positional[0]) == 1 {
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return Uncertain("cancelled")
				}
			}
			return Result(NullValue())
		}},
		{Operation: Operation{Language: "host", Path: "app", Member: "release"}, Version: "1", Result: Schema{Type: "null"}, Effect: ReadOnlyEffect, Concurrent: true, Invoke: func(ctx context.Context, r Request) Response {
			select {
			case <-entered:
				close(release)
				return Result(NullValue())
			case <-ctx.Done():
				return Uncertain("worker did not yield")
			}
		}},
	}}
	p, err := Compile("import host \"app\"\nflow Main()->void{perform effect app.read(1);perform effect app.read(2);perform effect app.release()}", host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	report, err := p.Run(ctx, nil, Options{Parallelism: 2})
	if err != nil || len(report.Journal.Records()) != 3 {
		t.Fatal("permit waiter consumed a worker", report, err)
	}
}

func TestSubFlowBarrierCompletion(t *testing.T) {
	var sequence []float64
	var mu sync.Mutex
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "write"}, Version: "1", Params: []Parameter{{Name: "n", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "null"}, Effect: WriteEffect, Invoke: func(_ context.Context, r Request) Response {
		mu.Lock()
		sequence = append(sequence, num(r.Positional[0]))
		mu.Unlock()
		return Result(NullValue())
	}}}}
	p, err := CompileWithOptions(`import host "app"
flow Write(n:number)->number{perform effect app.write(n);return n}
flow Main()->number{perform effect app.write(0);value={perform effect app.write(1);Write(2)};perform effect app.write(3);return value}`, host, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 4} {
		sequence = nil
		report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel})
		if err != nil || fmt.Sprint(sequence) != "[0 1 2 3]" || num(report.Output) != 2 {
			t.Fatal(sequence, report, err)
		}
	}
}
