package runtime

import (
	"context"
	"errors"
	stdruntime "runtime"
	"testing"
	"time"
	"weak"
)

// A large allocation avoids the tiny-object batching allowed by weak.Pointer.
type lifetimePayload [1 << 20]byte

func payloadProducer(probe *weak.Pointer[lifetimePayload]) func(context.Context, map[string]Value) Result {
	return func(context.Context, map[string]Value) Result {
		value := new(lifetimePayload)
		value[0] = 7
		*probe = weak.Make(value)
		return Ready(value)
	}
}

func payloadAlive(probe weak.Pointer[lifetimePayload]) bool {
	stdruntime.GC()
	return probe.Value() != nil
}

func TestLargeIntermediateLifetime(t *testing.T) {
	for _, release := range []bool{false, true} {
		for _, scheduler := range []string{"sequential", "parallel", "tick"} {
			t.Run(scheduler+map[bool]string{false: "/legacy", true: "/release"}[release], func(t *testing.T) {
				var probe weak.Pointer[lifetimePayload]
				g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: release})
				g.Add(NodeSpec{Name: "large", Effect: EffectReadOnly, Eval: payloadProducer(&probe)})
				g.Add(NodeSpec{Name: "consume", Deps: []string{"large"}, Effect: EffectExternalWrite, Eval: func(_ context.Context, v map[string]Value) Result {
					return Ready(v["large"].(*lifetimePayload)[0])
				}})
				// After is an ordering edge and must not keep large alive.
				g.Add(NodeSpec{Name: "observe", After: []string{"consume"}, Effect: EffectExternalWrite, Output: true, Eval: func(context.Context, map[string]Value) Result {
					return Ready(payloadAlive(probe))
				}})
				var value Value
				var err error
				switch scheduler {
				case "sequential":
					value, _, err = g.Run(context.Background(), nil)
				case "parallel":
					value, _, err = g.RunParallel(context.Background(), NewHost(), nil, 4)
				case "tick":
					i := g.NewInstance(NewHost(), nil)
					value, _, err = i.Tick(context.Background(), nil)
					for _, cached := range i.cache {
						if cached.valid {
							t.Fatal("effectful node retained a cache")
						}
					}
				}
				if err != nil || value != !release {
					t.Fatalf("alive=%v, release=%v, err=%v", value, release, err)
				}
			})
		}
	}
}

func TestLastConsumerWaitsForFuture(t *testing.T) {
	var probe weak.Pointer[lifetimePayload]
	future := make(chan Result, 1)
	g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
	g.Add(NodeSpec{Name: "large", Effect: EffectPure, Eval: payloadProducer(&probe)})
	g.Add(NodeSpec{Name: "slow", Deps: []string{"large"}, Effect: EffectPure, Eval: func(_ context.Context, v map[string]Value) Result {
		if v["large"].(*lifetimePayload)[0] != 7 {
			return Failed(errors.New("missing async argument"))
		}
		return Await(future)
	}})
	g.Add(NodeSpec{Name: "fast", Deps: []string{"large"}, Effect: EffectPure, Eval: func(_ context.Context, v map[string]Value) Result {
		return Ready(v["large"].(*lifetimePayload)[0])
	}})
	g.Add(NodeSpec{Name: "during", After: []string{"fast"}, Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result {
		if !payloadAlive(probe) {
			return Failed(errors.New("released before final Future completed"))
		}
		// Nested Await must also finish before the consumer is completed.
		nested := make(chan Result, 1)
		nested <- Ready(9)
		future <- Await(nested)
		return Ready(nil)
	}})
	g.Add(NodeSpec{Name: "after", Deps: []string{"slow"}, After: []string{"during"}, Effect: EffectPure, Output: true, Eval: func(_ context.Context, v map[string]Value) Result {
		if payloadAlive(probe) {
			return Failed(errors.New("retained after last Future completed"))
		}
		return Ready(v["slow"])
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value, _, err := g.RunParallel(ctx, NewHost(), nil, 4)
	if err != nil || value != 9 {
		t.Fatalf("value=%v err=%v", value, err)
	}
}

func TestUnrelatedWorkerDoesNotRetainIntermediate(t *testing.T) {
	var probe weak.Pointer[lifetimePayload]
	started, stop := make(chan struct{}), make(chan struct{})
	g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
	g.Add(NodeSpec{Name: "large", Effect: EffectPure, Eval: payloadProducer(&probe)})
	g.Add(NodeSpec{Name: "seed", Deps: []string{"large"}, Effect: EffectPure, Eval: func(_ context.Context, v map[string]Value) Result {
		return Ready(v["large"].(*lifetimePayload)[0])
	}})
	g.Add(NodeSpec{Name: "consume", Deps: []string{"large", "seed"}, Effect: EffectPure, Eval: func(ctx context.Context, v map[string]Value) Result {
		select {
		case <-started:
			return Ready(v["large"].(*lifetimePayload)[0])
		case <-ctx.Done():
			return Failed(ctx.Err())
		}
	}})
	g.Add(NodeSpec{Name: "unrelated", Deps: []string{"seed"}, Effect: EffectPure, Eval: func(ctx context.Context, v map[string]Value) Result {
		close(started)
		select {
		case <-stop:
			return Ready(v["seed"])
		case <-ctx.Done():
			return Failed(ctx.Err())
		}
	}})
	g.Add(NodeSpec{Name: "observe", After: []string{"consume"}, Effect: EffectPure, Output: true, Eval: func(context.Context, map[string]Value) Result {
		alive := payloadAlive(probe)
		close(stop)
		return Ready(alive)
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value, _, err := g.RunParallel(ctx, NewHost(), nil, 4)
	if err != nil || value != false {
		t.Fatalf("unrelated worker retained object: alive=%v err=%v", value, err)
	}
}

func TestSkippedConsumerAndOutOfOrderProducer(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		var probe weak.Pointer[lifetimePayload]
		g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
		g.Add(NodeSpec{Name: "blocked", Gates: []string{"enabled"}, Gate: func(v map[string]Value) (bool, error) { return Bool(v["enabled"]) }, Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result {
			return Failed(errors.New("blocked evaluator ran"))
		}})
		g.Add(NodeSpec{Name: "skip", Deps: []string{"blocked", "large", "large"}, Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result {
			return Failed(errors.New("skipped consumer ran"))
		}})
		g.Add(NodeSpec{Name: "consume", Deps: []string{"large"}, Effect: EffectPure, Eval: func(_ context.Context, v map[string]Value) Result {
			return Ready(v["large"].(*lifetimePayload)[0])
		}})
		g.Add(NodeSpec{Name: "large", Effect: EffectPure, Eval: payloadProducer(&probe)})
		g.Add(NodeSpec{Name: "observe", After: []string{"skip", "consume"}, Effect: EffectExternalWrite, Output: true, Eval: func(context.Context, map[string]Value) Result {
			return Ready(payloadAlive(probe))
		}})
		var value Value
		var err error
		if parallel {
			value, _, err = g.RunParallel(context.Background(), NewHost(), map[string]Value{"enabled": false}, 4)
		} else {
			value, _, err = g.Run(context.Background(), map[string]Value{"enabled": false})
		}
		if err != nil || value != false {
			t.Fatalf("parallel=%v: alive=%v err=%v", parallel, value, err)
		}
	}
}

func TestOutputAndHostAliasesRemainValid(t *testing.T) {
	var probe weak.Pointer[lifetimePayload]
	var hostOwned Value
	g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
	g.Add(NodeSpec{Name: "large", Effect: EffectPure, Eval: payloadProducer(&probe)})
	g.Add(NodeSpec{Name: "return", Deps: []string{"large"}, Output: true, Eval: func(_ context.Context, v map[string]Value) Result {
		hostOwned = v["large"]
		return Ready(v["large"])
	}})
	g.Add(NodeSpec{Name: "observe", After: []string{"return"}, Eval: func(context.Context, map[string]Value) Result {
		if !payloadAlive(probe) {
			return Failed(errors.New("output alias was collected"))
		}
		return Ready(nil)
	}})
	checkReturnedAlias(t, g, &hostOwned)
	if g.output != nil {
		t.Fatal("one-shot Graph retained the returned value")
	}
	if !payloadAlive(probe) || hostOwned.(*lifetimePayload)[0] != 7 {
		t.Fatal("Host-owned alias was cleared")
	}
	hostOwned = nil
	if payloadAlive(probe) {
		t.Fatal("completed Graph retained a discarded output")
	}
	stdruntime.KeepAlive(g)
}

// Keep the strong returned reference outside the GC probe's stack frame.
//
//go:noinline
func checkReturnedAlias(t *testing.T, g *Graph, hostOwned *Value) {
	t.Helper()
	value, _, err := g.RunParallel(context.Background(), NewHost(), nil, 4)
	if err != nil || value != *hostOwned || value.(*lifetimePayload)[0] != 7 {
		t.Fatalf("output alias: %v", err)
	}
}

func TestTickRetainsOnlyReusableCacheAndState(t *testing.T) {
	g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
	var sourceRuns, pureRuns, stateRuns int
	g.Add(NodeSpec{Name: "source", Deps: []string{"input"}, Effect: EffectReadOnly, Eval: func(_ context.Context, v map[string]Value) Result {
		sourceRuns++
		return Ready([]Value{v["input"]})
	}})
	g.Add(NodeSpec{Name: "state", Deps: []string{"source"}, State: true, Eval: func(_ context.Context, v map[string]Value) Result {
		stateRuns++
		return Ready(v["source"])
	}})
	g.Add(NodeSpec{Name: "pure", Deps: []string{"source"}, Effect: EffectPure, Eval: func(_ context.Context, v map[string]Value) Result {
		pureRuns++
		return Ready(v["source"])
	}})
	g.Add(NodeSpec{Name: "output", Deps: []string{"pure", "state"}, Effect: EffectExternalWrite, Output: true, Eval: func(_ context.Context, v map[string]Value) Result {
		return Ready([]Value{v["pure"], v["state"]})
	}})
	i := g.NewInstance(NewHost(), map[string]Value{"input": 1})
	for _, input := range []int{1, 1, 2} {
		if _, _, err := i.Tick(context.Background(), map[string]Value{"input": input}); err != nil {
			t.Fatal(err)
		}
	}
	if sourceRuns != 3 || pureRuns != 2 || stateRuns != 1 {
		t.Fatalf("runs: read=%d pure=%d state=%d", sourceRuns, pureRuns, stateRuns)
	}
	state, _ := i.State("state")
	if state.([]Value)[0] != 1 || i.inputs["input"] != 2 {
		t.Fatal("persistent roots lost")
	}
	for index, cache := range i.cache {
		if cache.valid != (index == 2) {
			t.Fatalf("unexpected cache at %s: %v", g.nodes[index].Name, cache.valid)
		}
	}
	if err := i.SetState("state", []Value{4}); err != nil {
		t.Fatal(err)
	}
	value, _, err := i.Tick(context.Background(), nil)
	if err != nil || value.([]Value)[1].([]Value)[0] != 4 {
		t.Fatalf("state override: %v %v", value, err)
	}
}

func TestLegacyGraphAllowsUndeclaredReads(t *testing.T) {
	g := NewGraph()
	g.Add(NodeSpec{Name: "first", Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result { return Ready(7) }})
	g.Add(NodeSpec{Name: "second", After: []string{"first"}, Effect: EffectPure, Output: true, Eval: func(_ context.Context, v map[string]Value) Result {
		return Ready(v["first"])
	}})
	value, _, err := g.RunParallel(context.Background(), NewHost(), nil, 2)
	if err != nil || value != 7 {
		t.Fatalf("legacy values-map contract: %v %v", value, err)
	}
}

func TestInvalidatedTickCacheIsReleasedBeforeRecomputation(t *testing.T) {
	var old weak.Pointer[lifetimePayload]
	runs := 0
	g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
	g.Add(NodeSpec{Name: "pure", Deps: []string{"input"}, Effect: EffectPure, Output: true, Eval: func(context.Context, map[string]Value) Result {
		runs++
		if runs > 1 && payloadAlive(old) {
			return Failed(errors.New("old cache retained while recomputing"))
		}
		return payloadProducer(&old)(context.Background(), nil)
	}})
	i := g.NewInstance(NewHost(), map[string]Value{"input": 1})
	for _, input := range []int{1, 2, 3} {
		if _, _, err := i.Tick(context.Background(), map[string]Value{"input": input}); err != nil {
			t.Fatal(err)
		}
	}
	stdruntime.KeepAlive(i)
}

func TestAbortedParallelRunDropsLateResults(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		var probe weak.Pointer[lifetimePayload]
		lateStarted, stuckStarted := make(chan struct{}), make(chan struct{})
		allowLate, stopStuck := make(chan struct{}), make(chan struct{})
		lateReturned, stuckReturned := make(chan struct{}), make(chan struct{})
		g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
		g.Add(NodeSpec{Name: "late", Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result {
			close(lateStarted)
			<-allowLate
			defer close(lateReturned)
			return payloadProducer(&probe)(context.Background(), nil)
		}})
		g.Add(NodeSpec{Name: "stuck", Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result {
			close(stuckStarted)
			<-stopStuck
			defer close(stuckReturned)
			return Ready(nil)
		}})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		g.Add(NodeSpec{Name: "fail", Effect: EffectPure, Eval: func(context.Context, map[string]Value) Result {
			<-lateStarted
			<-stuckStarted
			if cancellation {
				cancel()
				return Failed(context.Canceled)
			}
			return Failed(errors.New("abort"))
		}})
		_, _, err := g.RunParallel(ctx, NewHost(), nil, 4)
		cancel()
		close(allowLate)
		<-lateReturned
		// Let the late worker's cancelled send finish while another worker still
		// holds the result channel. Buffered abandoned results would stay rooted.
		alive := true
		for attempt := 0; attempt < 20 && alive; attempt++ {
			stdruntime.Gosched()
			alive = payloadAlive(probe)
		}
		close(stopStuck)
		<-stuckReturned
		if err == nil || cancellation && !errors.Is(err, context.Canceled) || alive {
			t.Fatalf("cancellation=%v: late result alive=%v err=%v", cancellation, alive, err)
		}
		stdruntime.KeepAlive(g)
	}
}

func TestLifetimePlanRebuildsAfterAdd(t *testing.T) {
	g := NewGraphWithOptions(GraphOptions{ReleaseIntermediates: true})
	g.Add(NodeSpec{Name: "null", Effect: EffectPure, Output: true, Eval: func(context.Context, map[string]Value) Result { return Ready(nil) }})
	if _, _, err := g.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	g.Add(NodeSpec{Name: "isNull", Deps: []string{"null"}, Effect: EffectPure, Output: true, Eval: func(_ context.Context, v map[string]Value) Result {
		value, present := v["null"]
		return Ready(present && value == nil)
	}})
	for run := 0; run < 2; run++ {
		value, _, err := g.RunParallel(context.Background(), NewHost(), nil, 2)
		if err != nil || value != true {
			t.Fatalf("plan was stale or null disappeared: %v %v", value, err)
		}
	}
}
