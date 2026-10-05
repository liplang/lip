# Changelog

## 0.4.0 — Alpha 0.4

Specification: [ALPHA-0.4-SPEC.md](docs/ALPHA-0.4-SPEC.md)

Alpha 0.4 completes the next runtime milestone on top of persistent Alpha 0.3
instances.

Included:

- bounded `retry(call, attempts)` execution;
- bounded `feedback(initial, revise, verify, attempts)` execution;
- cancellation and deadline propagation with `Cancelled` trace states;
- `EffectPure`, `EffectReadOnly` and `EffectExternalWrite` metadata;
- concurrent read-only Host operations and sequential external-write barriers;
- explicit `NodeSpec.After` ordering constraints;
- Go-like `lipc help`, executable `build`, temporary `run` and `-emit-go` source mode;
- serialized Graph/Instance observation and execution for safe object reuse;
- State/Tick and incremental cache tests, examples and specifications.

The language still keeps effect metadata at the Go Runtime boundary rather than
introducing a full effect type system.

## 0.3.0 — Alpha 0.3

Specification: [ALPHA-0.3-SPEC.md](docs/ALPHA-0.3-SPEC.md)

Included:

- persistent `runtime.Instance` and generated `NewInstance` APIs;
- `state(initial)`, `SetState`, `State` and monotonic logical ticks;
- dependency snapshots and incremental reuse of unchanged pure nodes;
- per-tick Trace events and stateful examples.

## 0.2.0 — Alpha 0.2

Specification: [ALPHA-0.2-SPEC.md](docs/ALPHA-0.2-SPEC.md)

Alpha 0.2 adds one-shot dynamic Map execution while keeping Flow invocations
independent and the language core small.

Included:

- `[expr for item in source]` comprehension syntax;
- static Map graph nodes with runtime dynamic expansion;
- list/slice sources, stable result ordering and empty-map support;
- bounded Map concurrency through `RunParallel` and automatic scheduling;
- Map element error and context-cancellation propagation;
- Map examples and conformance tests.

Still intentionally not included:

- persistent state, logical ticks or incremental recomputation;
- feedback cycles, event/stream syntax or retry policy syntax;
- general `for`/`while` loops and nested comprehensions;
- effect ordering metadata or direct Go imports.

Release verification:

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go test -race ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
```

## 0.1.0 — Alpha 0.1

Specification: [ALPHA-0.1-SPEC.md](docs/ALPHA-0.1-SPEC.md)

This is the first publishable Alpha release of LIP.

Included:

- handwritten lexer/parser and dependency-graph compiler;
- `flow`, expression `fn`, single-assignment bindings, `when`, `return`;
- strict built-in operators with optional explicit parameter annotations;
- Go 1.27 code generation and the `runtime.Host` adapter boundary;
- deterministic sequential scheduling and bounded pure-node parallelism;
- `Ready`, `Failed` and `Await` Host results;
- Quickstart, tutorial, compiler notes and the 30-prototype coverage map.

Intentionally not included:

- loops, comprehensions or dynamic map expansion;
- persistent state, events, streams or feedback;
- retry/cancellation policy syntax;
- direct Go imports or automatic Go signature reflection;
- incremental recomputation across Flow invocations.

Release verification:

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
for f in examples/*.lip; do
  GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc check "$f" || exit 1
done
```
