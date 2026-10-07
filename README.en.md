# LIP Alpha 0.6

[中文版 README](README.md) · Reference implementation `0.6.1` ([VERSION](VERSION))

LIP (Logical / Incremental / Parallel) is a small dependency-oriented language:
**Describe dependencies; let the Runtime decide execution.** Compose data,
selection, Map, aggregation and pure recursion with immutable bindings, then
inspect the graph and execution trace.

Syntax follows Rust, and ranges follow Python conventions. Here is a complete
data program:

```lip
fn add(total: number, value: number) -> number {
    return total + value
}

flow Report(values: list) -> object {
    doubled = [x * 2 for x in values]
    total = fold(doubled, 0, add)
    return {
        count: len(values),
        values: doubled,
        total: total,
        average: if len(values) == 0 { null } else { total / len(values) }
    }
}
```

Go 1.27 or newer is required. Install from the repository root:

```bash
go install ./cmd/lipc
```

The destination follows your Go configuration: GOBIN, or `bin` under the first
GOPATH entry when GOBIN is empty. Use `lipc` if that directory is already on PATH,
or invoke the installed file directly. Examples below use `lipc` for the tool:

```bash
lipc check examples/core.lip
lipc run examples/core.lip '[1,2,3]'
# {"average":4,"count":3,"total":12,"values":[2,4,6]}
lipc run examples/core.lip '[]'
# {"average":null,"count":0,"total":0,"values":[]}
```

With uncustomized Go settings, the usual path is `~/go/bin/lipc`, so
`~/go/bin/lipc help` also works. On Windows it is usually `go\bin\lipc.exe` under
your user directory. See [quickstart](docs/QUICKSTART.md#安装) for direct invocation.

`lipc` bundles its runtime sources, so installed `run/build` commands work in any
project directory without a LIP checkout or project `go.mod`. Both still require
the Go toolchain; compiled core executables run independently.
Place tool options before the file and program inputs directly after it.

## Language and runtime

| Capability | 0.6 contract |
| --- | --- |
| Complete program | Requirements, pure fn, one flow, explicit inputs/output, one return |
| Data | null, bool, number, string, list, object; nested construction |
| Composition | Immutable bindings, operators, Rust-style if, when gates, Map |
| Pure operations | str, len, half-open range, ordered fold with an explicit seed, explicit fail |
| List library | 34 pure functions for grouping, merging, transpose, windows, filtering, sorting, uniqueness, scans and Cartesian products |
| String library | 19 pure operations for cleanup, splitting, joining, searching, slicing, replacement and decimal parsing |
| Recursion | Direct/mutual pure recursion; cycle result types required, call depth at most 256 |
| Execution | Automatic/sequential/bounded parallel scheduling, effects, cancellation, State/Tick, pure dependency reuse |
| Observation | inspect graph JSON, run --trace lifecycle JSON |
| Existing extensions | Host Adapters, Await, bounded Retry/Feedback, Python Worker |

`range(start, end[, step])` materializes at most 1,000,000 elements. Map comprehensions
require a bound source; fold/list.* also accept nested pure expressions. The [list library](docs/LIST-LIBRARY.md) adds collection
operations without new syntax. A fold reducer is a local two-parameter pure fn; an empty
list returns its seed. Prefer Map/fold for collection traversal and recursion for
trees or divide-and-conquer.

list/object annotations validate outer shape; dynamic element types are checked
when used. CLI strings are literal, numbers finite decimal, booleans true/false,
and any/list/object inputs JSON. A Flow output `T?` accepts null or no value from a
closed gate.

## Dependencies determine execution

Flow bindings become graph nodes; variable references become dependencies. Pure
expressions inside fn do not become graph nodes. Independent nodes may run in
parallel; consumers wait for their dependencies. if selects a value, when gates
execution. Short-circuit expressions do not cancel separately bound external work.

Host adapters provide real capabilities:

```go
host := runtime.DefaultHost()
host.RegisterPure("compute", compute)
host.RegisterReadOnly("load_profile", loadProfile)
host.Register("write_file", writeFile)
```

Pure nodes may be reused on unchanged Ticks. ReadOnly nodes can run concurrently
but execute every Tick; writes form ordered barriers. Adapters must declare
accurate effects, cooperate with cancellation and protect shared state. Treat
values handed to a Flow as immutable. Completed external writes are not rolled back.

Persistent instances expose `NewInstance`, `SetState` and `Tick`; the host advances
each computation. One-shot `Run`, `RunSequential` and `RunParallel` share the same input
and result contracts.

## Check, run and inspect

0.6.1 adds 19 pure `string.*` operations, explicit `fail(message)`, and structured
`lipc check --json` diagnostics (`lip.diagnostics.v1`). The expanded [tutorial](docs/TUTORIAL.md)
has progressive, runnable examples with tested outputs and failures. String
boundaries/operators share a 16 MiB UTF-8 limit; Map/fold sources are capped at
1,000,000 elements before expansion. See the [string reference](docs/STRING-LIBRARY.md)
and [generation/verification workflow](docs/VIBE-CODING.md).

Go Host, Python and pure libraries use the same calls and value dependencies.
Ordinary Python values compose directly; [mixed.lip](examples/tutorial/mixed.lip)
combines Go file reading, text parsing, Python math and pure aggregation. Run
`go run ./examples/tutorial/mixed_demo` with standard Python, without scientific packages.
Python NaN/Inf and colliding object keys fail; sets require explicit ordering.

```bash
lipc check --json examples/strings.lip
lipc run examples/strings.lip ' Rust, LIP, rust, ,你好 '
lipc inspect examples/core.lip
lipc run --trace report-trace.json examples/core.lip '[1,2,3]'
lipc run examples/range.lip 5
lipc run examples/lists.lip '[{"department":"A","amount":3},{"department":"B","amount":2}]'
lipc run examples/tree.lip '{"value":1,"left":{"value":2,"left":null,"right":null},"right":null}'

lipc build examples/core.lip
./Report '[1,2,3]'
```

On Windows, run `.\Report.exe '[1,2,3]'`. See the [compiler guide](docs/COMPILER.md#lipc-命令)
for output paths and Go source generation.

inspect does not execute Host work and emits `lip.graph.v1`. Trace writes
`lip.trace.v1` states, Ticks and reasons to a separate file on success or execution
failure, preserving ordinary result output.

Declare external work with `require`. Generate libraries with `--no-main
--package name` for Host/Go adapters. Python requirements enable a resident
Worker using installed packages. See the [Host example](examples/host_adapter)
and [Python integration](docs/PYTHON-INTEGRATION.md).

## Specification and verification

The [language specification](docs/ALPHA-0.6-SPEC.md) defines the current rules.
Use `//` for line comments, block-style `if` to select values, and `when` to gate
execution. See the [roadmap](docs/ROADMAP.md) for future directions.

```bash
bash scripts/verify-release.sh
```

This runs tests, race checks, vet, builds, all LIP example checks/generation and
core execution acceptance. Python tests run when their environment is available;
independent core examples need no Python.

- [Quickstart](docs/QUICKSTART.md) · [Tutorial](docs/TUTORIAL.md)
- [Compiler and Host API](docs/COMPILER.md) · [Specifications](docs/SPECS.md)
- [Roadmap](docs/ROADMAP.md) · [Consistency audit](docs/CONSISTENCY-AUDIT.md)
- [Prototype coverage](examples/PROTOTYPES.md) · [Changelog](CHANGELOG.md) · [Release checklist](RELEASE.md)
