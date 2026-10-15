package tests

import (
	"context"
	"errors"
	"fmt"
	. "lip/script"
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
