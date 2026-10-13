package tests

import (
	"context"
	"errors"
	. "lipbeta/script"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
	for _, input := range []any{cyclic, int64(9007199254740992), []byte{1, 2}, mathNaN()} {
		if _, e := FromNative(input); e == nil {
			t.Fatalf("accepted %T", input)
		}
	}
}
func mathNaN() float64 { var zero float64; return zero / zero }
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
