# LIP Alpha 0.4 Specification

This document is the normative boundary for LIP `0.4.0`. Alpha 0.3 persistent
instances and incremental recomputation remain valid.

## Inputs and dependency declarations

Flow parameters are the complete input declaration. Generated standalone entry
points bind command-line arguments to those parameters in declaration order and
reject missing or extra arguments. `string` is passed through, `number` accepts a
finite decimal, `bool` accepts only `true` or `false`, and `any` uses JSON at the
command-line boundary. No generated entry point may invent an input value or read
`LIP_INPUT`.

Every Flow must contain at least one `return` output node. A `when` gate may still
skip all output nodes at runtime, which produces the existing `nil` result semantics;
the compiler never invents a fallback value.

Ordinary local variables use single-assignment binding (`name = expression`),
which both declares the name and creates its graph node. Undefined names, forward
references, duplicate bindings and bindings escaping a `when` block are compile
errors.

A source file may begin with declarative requirements:

```lip
require python "numpy>=1.26"
require go "github.com/acme/adapter"
require host "load_profile"
```

The `require` directive records environment metadata and is exposed by `lipc check` and the
generated library's `RequiredDependencies()` function. It does not import code,
install packages or alter the selected Python environment. Python calls remain
generic dotted Host operations; Go packages are imported and Host operations are
registered by the Go program embedding the generated library. A standalone
generated `main` starts the default Python Worker when the file declares a
`python` requirement; library mode always leaves Worker ownership to its host.
External calls must have a matching `require python` or `require host` declaration;
the built-in value/control operations are the only exceptions.

## Retry

```lip
require host "fetch"

result = retry(fetch(input), 3)
```

The attempt count includes the first call and must be a positive integer
literal. Retry is bounded. A normal error is retried until success or the
limit; context cancellation and deadlines stop immediately and are never
retried. The final error is returned as the node error.

## Bounded Feedback

```lip
require host "start"
require host "revise"
require host "verify"

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
