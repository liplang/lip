# LIP Alpha 0.4 Specification

This document is the normative boundary for LIP `0.4.0`. Alpha 0.3 persistent
instances and incremental recomputation remain valid.

## Retry

```lip
result = retry(fetch(input), 3)
```

The attempt count includes the first call and must be a positive integer
literal. Retry is bounded. A normal error is retried until success or the
limit; context cancellation and deadlines stop immediately and are never
retried. The final error is returned as the node error.

## Bounded Feedback

```lip
result = feedback(start(input), revise, verify, 3)
```

The first call creates a candidate. `verify(candidate)` must return a Boolean.
If it returns false, `revise(candidate)` creates the next candidate. The
attempt count limits verification rounds, so every Feedback execution has a
finite upper bound. Failure to converge is a node error. Feedback is a bounded
runtime loop and does not create a cyclic graph or an implicit State update.

## Cancellation

Runtime operations receive the execution context. Context cancellation and
deadline errors produce the `Cancelled` node status. Dependent nodes become
`Skipped` and do not execute. Retry, Feedback, Await and dynamic Map check the
context between work units and while waiting for asynchronous results.
Alpha's current invocation policy is fail-fast: a non-cancellation node error
ends that `Run`/`Tick`, marks pending nodes `Skipped` and lets no new
independent work start. A future supervision policy may retain independent
work, but it is not part of this release.

## Effect metadata

The runtime has three lightweight effect classes:

| Class | Meaning | Scheduler rule |
| --- | --- | --- |
| `Pure` | deterministic computation without external mutation | may run concurrently and may be cached by an Instance |
| `ReadOnly` | external read with no mutation | may run concurrently, but is re-evaluated on each Instance tick |
| `ExternalWrite` | operation that can mutate external state | sequential ordering barrier |

Go adapters select a class with `RegisterPure`, `RegisterReadOnly` or
`Register`. Unknown operations are treated conservatively. `NodeSpec.Effect`
can provide explicit runtime metadata; the language intentionally does not
introduce a complete effect type system in Alpha.

## Ordering

`NodeSpec.After` adds an execution-order constraint without passing a value.
The named predecessor must complete or be skipped before the node can run. A
failed or cancelled predecessor blocks the dependent node. External writes
also act as source-order barriers: independent later work does not overtake a
pending write, and writes never run concurrently with one another.

Data dependencies remain the primary scheduling rule. Effect and ordering
metadata constrain the legal schedules without changing the Flow value model.
