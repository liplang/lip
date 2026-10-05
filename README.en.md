# LIP Alpha 0.4

[中文版 README](README.md)

Reference implementation version: `0.4.0` (see [VERSION](VERSION)).

LIP (Logical / Incremental / Parallel) is a dependency-oriented language. Its
central idea is one sentence:

> **Describe dependencies; let the Runtime decide execution.**

You write bindings, functions, and expressions that say what each result depends on.
The compiler turns them into a Dependency Graph. The Runtime then schedules work
according to data dependencies, gates, state changes, cancellation, and effect
constraints:

```lip
user = load_user(id)
orders = load_orders(id)
answer = combine(user, orders)
return answer
```

`user` and `orders` are independent, so they may run concurrently; `answer`
waits for both. Source order helps define and diagnose the program, but it is not
automatically an execution order. Actual concurrency remains subject to Runtime
resources, Host effects, and ordering constraints.

## The LIP computation model

LIP has two connected layers:

- **Source code** expresses bindings, calls, value selection, and execution gates in a
  familiar form.
- **The Runtime** maintains graph nodes, dependency snapshots, Logical Ticks,
  cancellation state, and effect metadata.

These layers establish a few stable semantic boundaries:

- **Dependencies are semantics.** Variable references form edges; fan-out, fan-in, and
  joins need no special pipeline syntax.
- **Source order is not a scheduling command.** Independent nodes may run in parallel.
  Data dependencies, `NodeSpec.After`, or Host effects express required ordering.
- **Static structure can expand dynamically.** `[expr for item in source]` is one Map
  node in the graph. The Runtime expands it for the actual collection, preserves input
  order, and applies a concurrency bound.
- **State changes propagate through Ticks.** A persistent `Instance` stores State and
  dependency snapshots. Pure nodes can be reused when their inputs are unchanged;
  external reads and writes follow their effect policy.
- **Execution policy belongs to the Runtime.** Concurrency, waiting, retry,
  cancellation, and resource limits are runtime decisions. A Flow does not require
  hand-written goroutines, thread pools, joins, or unbounded background tasks.
- **Host is the boundary.** Files, networks, databases, and scientific computing enter
  through Go Host Adapters. The language remains checkable while deployment controls
  permissions, timeouts, and isolation.

The design is advanced in properties that can be tested, rather than in a promise that
everything will run faster:

1. With the declared Effect/Ordering constraints satisfied, the same dependency
   structure can run sequentially or with bounded parallelism without changing result
   semantics.
2. State, Ticks, cache reuse, and cancellation reasons are observable in Trace.
3. Dynamic Map does not require one static node per runtime element.
4. Pure computation, read-only external access, and external writes have distinct
   caching and ordering rules.
5. Generated code is ordinary Go and can use Go's compiler, tests, deployment, and
   diagnostics.

## A convenient authoring experience

A typical Flow remains close to ordinary code:

```lip
flow Hello(request: string) {
    greeting = "Hello, " + request
    return greeting
}
```

You describe the computation relationship instead of designing an asynchronous API for
each step. Real capabilities are registered as Go Host operations:

```go
host.RegisterPure("load_profile", loadProfile)
host.RegisterReadOnly("load_model", loadModel)
host.Register("write_file", writeFile)
```

The compiler checks syntax, names, types, and graph structure. The Runtime executes the
graph. You can catch structural errors with `check`, then inspect node states, Ticks,
and reuse reasons in Trace.

## Current capabilities

| Capability | Alpha 0.4 status |
| --- | --- |
| `flow`, expression `fn`, single-assignment bindings | Supported, with name, scope, and type checks |
| `when` execution gates and `if` value selection | Supported with distinct semantics |
| Dynamic Map | Runtime expansion, stable order, bounded concurrency |
| Persistent Flow / State / Tick | `NewInstance`, `Tick`, `SetState`, and Trace |
| Incremental recomputation | Pure-node dependency cache; effects control external re-execution |
| Retry / Feedback | Runtime policies with explicit finite limits |
| Cancellation | Context, Await, Map, Retry, and Feedback propagation |
| Effects / Ordering | Host effect classes and `NodeSpec.After` |
| Ordinary `for` / `while`, event/stream DSL | Not in the Alpha language core |

Each milestone has an explicit boundary:
The [specification index](docs/SPECS.md) collects
[ALPHA-0.1-SPEC.md](docs/ALPHA-0.1-SPEC.md),
[ALPHA-0.2-SPEC.md](docs/ALPHA-0.2-SPEC.md),
[ALPHA-0.3-SPEC.md](docs/ALPHA-0.3-SPEC.md), and
[ALPHA-0.4-SPEC.md](docs/ALPHA-0.4-SPEC.md).

## lipc: check, build, and run like Go

Install the command:

```bash
go install ./cmd/lipc
```

Or invoke it from a checkout with `go run ./cmd/lipc`. Common commands:

```bash
# Check syntax, names, types, and the dependency graph
lipc check examples/hello.lip

# Build an executable; the default output is the Flow name
lipc build examples/hello.lip
lipc build -o /tmp/hello examples/hello.lip

# Compile temporarily and run; arguments after -- go to the generated program
lipc run examples/hello.lip -- Alice

# Emit Go source for review or a checked-in library
lipc build -emit-go -o hello_generated.go examples/hello.lip
lipc build -emit-go -no-main -package hostflow \
  -o flow/flow_gen.go flow.lip

# Show help and version
lipc help
lipc help build
lipc version
```

`lipc build` normally produces an executable. `-emit-go`, or an output path ending
in `.go`, emits Go source. The legacy shorthand `lipc file.lip` remains available
for source generation.

Generated library packages provide:

```go
Run(ctx, host, inputs)
RunSequential(ctx, host, inputs)
RunParallel(ctx, host, inputs, limit)
NewInstance(host, inputs)
```

## Persistent State and Logical Ticks

```lip
flow Counter(input: number) {
    count = state(0)
    doubled = count * 2
    return doubled + input
}
```

```go
instance, err := counter.NewInstance(host, map[string]runtime.Value{"input": 1})
value, trace, err := instance.Tick(ctx, nil)

err = instance.SetState("count", 3)
value, trace, err = instance.Tick(ctx, nil)
```

The initial State value is committed on the first Tick. Later Ticks reuse unaffected
pure nodes. Each Tick has a monotonically increasing logical count, and
`TraceEvent` records node status and the `reused` reason so the Runtime's choices
can be explained. Instance execution and observation methods are serialized; a Host
callback should not re-enter the same Instance.

## Go Host and the Python route

LIP does not directly import Go or Python in its language syntax. Python scientific
computing enters through a Host Adapter:

```text
LIP node → Go Adapter → Python Worker → NumPy / SciPy / Pandas / PyTorch
```

The planned conservative first step is a long-lived, independent Python Worker using
a JSON-lines request/response protocol. Alpha 0.4 does not yet provide a
`ProcessHost`; Go would own request IDs, deadlines, cancellation, restart,
backpressure, and effect classification, while Python owns scientific computing.
The concrete shape is one Python child process started by Go with `os/exec` and kept
resident, using stdin/stdout pipes for JSONL. If multiplexing or independent
connections become necessary, the transport can move to a Unix Domain Socket without
changing request, cancellation, error, or session semantics. Gob is not the preferred
cross-language wire format; large arrays should use memory-mapped files, Arrow, or
shared memory, with only handles and shape metadata in the control message. Evaluate
Unix socket/gRPC or embedded CPython only if benchmarks show that the process boundary
is unacceptable.

See [docs/PYTHON-INTEGRATION.md](docs/PYTHON-INTEGRATION.md) for the route comparison,
protocol constraints, data planes, and phase gates. Environments without Python can
still compile LIP, and the compiler core does not acquire the GIL, Python packaging, or
model-service lifecycle.

## Documentation

- [docs/QUICKSTART.md](docs/QUICKSTART.md): from checking to generation and execution;
- [docs/TUTORIAL.md](docs/TUTORIAL.md): language and Runtime tutorial;
- [docs/COMPILER.md](docs/COMPILER.md): AST, Graph, Scheduler, and generated code;
- [docs/ROADMAP.md](docs/ROADMAP.md): next milestones and acceptance criteria;
- [docs/PYTHON-INTEGRATION.md](docs/PYTHON-INTEGRATION.md): Python route comparison and protocol;
- [docs/CONSISTENCY-AUDIT.md](docs/CONSISTENCY-AUDIT.md): cross-check of implementation and design;
- [docs/SPECS.md](docs/SPECS.md): index of the Alpha milestone specifications;
- [examples/PROTOTYPES.md](examples/PROTOTYPES.md): coverage of 30 design prototypes;
- [CHANGELOG.md](CHANGELOG.md): release changes and checks.

## Verifying the implementation

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go test -race ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
```

The reference implementation targets Go 1.27. Alpha is experimental. Events/streams,
ordinary loops, a complete effect type system, and direct Python imports remain future
options whose priority depends on conformance results and measured workloads.
