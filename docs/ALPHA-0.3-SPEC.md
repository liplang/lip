# LIP Alpha 0.3 Specification

This document is the normative boundary for LIP `0.3.0`. Alpha 0.1 and 0.2
behavior remains valid unless this document adds persistent execution rules.

## Persistent instances

Generated Flow packages expose `NewInstance`, `Tick`, `SetState`, `State` and
`TickCount`. The existing one-shot `Run`, `RunSequential` and `RunParallel`
functions remain available and keep their independent invocation semantics.

An instance owns input values, committed State values, node cache entries and a
monotonic logical tick counter. `Tick(ctx, changedInputs)` merges the supplied
inputs and evaluates the graph. A nil input map means that no input changes.
An Instance serializes concurrent `Tick`, `SetState`, `State`, `Trace` and
`TickCount` calls; callers do not need a second lock around one instance.

## State

```lip
flow Counter(input: number) {
    count = state(0)
    doubled = count * 2
    return doubled + input
}
```

`state(initial)` is a Flow binding and must have one simple initial expression.
The initial expression runs on the first tick. Its value is committed as the
State value; later ticks reuse that value until the host calls
`SetState("count", value)`. A state update is an external input to the graph,
not an implicit graph feedback edge. The update is consumed by the next Tick.

State values are readable with `State`. An unknown state name is an error.

## Logical ticks and trace

Each successful or failed `Tick` increments `TickCount`. Trace events include
the logical tick, node name, status and an optional reason. Unchanged pure nodes
may be recorded as `Completed` with reason `reused`; the trace does not expose
internal cache structures.

## Incremental recomputation

The runtime snapshots each node's dependency values. A pure node is reusable
when its data dependencies, gate values and explicit ordering predecessors have
not changed. A changed input or State value invalidates that node and all
dependent nodes through normal dependency comparison. External operations are
evaluated again on each tick because their result may depend on the outside
world. State nodes retain their committed value and do not re-run their
initializer merely because an unrelated input changed.

Incremental caching is an optimization with the same observable value and
error semantics as a fresh execution. Caches are scoped to one instance and
are never shared between instances.

One-shot Graph execution is also serialized when the same `Graph` value is
used concurrently. Host operations should still protect their own mutable
state, and a Host must be fully registered before execution begins.

## Boundaries

Alpha 0.3 does not add events, streams, ordinary language loops, automatic
State updates or graph cycles. Feedback and retry policy are specified by
Alpha 0.4.
