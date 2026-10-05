# LIP Alpha 0.2 Specification

> Describe dependencies. Let the runtime decide execution.

This is the normative boundary for LIP `0.2.0`. Alpha 0.1 behavior remains
valid unless this document adds a Map rule.

## Core program shape

A source file contains optional expression functions followed by one `flow`.
Flow bindings are single-assignment graph nodes. A node depends on every
previous binding referenced by its expression. Forward references and bindings
inside a `when` block escaping that block are rejected.

`when condition { ... }` is an execution gate. A false gate skips its body and
all dependent nodes. `if condition then a else b` selects a value. Neither form
creates a feedback edge.

## Dynamic Map

Alpha 0.2 adds the restricted comprehension form:

```lip
values = [1, 2, 3]
doubled = [x * 2 for x in values]
```

The form is one graph node with these parts:

```text
source: values
element expression: x * 2
element binding: x
```

The source must be an identifier. At runtime it must contain a Go slice or
array, including `[]runtime.Value`; otherwise the Map node fails. The runtime
creates one execution instance for each source element. The result is a list
with the same length and source order as the input. An empty source produces an
empty list.

The element binding is local to the comprehension. Outer Flow bindings remain
read-only. Nested comprehensions and general `for`/`while` statements are not
part of Alpha 0.2. A comprehension is supported as a Flow binding or Flow
return expression; it is not accepted inside another expression or local `fn`.

## Scheduling and purity

`RunSequential` evaluates Map elements in source order. `Run` and
`RunParallel` may evaluate independent Map elements concurrently, subject to
the caller's bounded parallelism limit. A Map whose element expression has no
Host calls is pure. A Map containing Host calls is parallel-eligible only when
all referenced operations were registered with `Host.RegisterPure`; otherwise
its elements run sequentially as an effectful node.

The runtime preserves output order even when completion order differs. A Map
element error fails the Map node and prevents downstream nodes from running.
The execution context is passed to every element; cancellation returns the
context error and stops pending element work.

## Runtime boundary

`Pending`, `Running`, `Completed`, `Error` and `Skipped` remain runtime
execution states, not language-level value types. Map expansion is one-shot and
does not persist between Flow invocations. Alpha 0.2 does not define state,
logical ticks, incremental recomputation, feedback, events, streams, retry
syntax, or effect-ordering metadata.

## Host boundary

LIP continues to use Go Host adapters for IO, network, database and other
external capabilities. The compiler does not reflect Go signatures or import
Go packages directly.
