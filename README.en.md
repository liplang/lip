# LIP Alpha 0.6.4

[中文版 README](README.md) · Reference implementation `0.6.4` ([VERSION](VERSION))

LIP (Logical / Incremental / Parallel) is a small dependency-oriented language:
**Describe dependencies; let the Runtime decide execution.** Compose data,
selection, Map, aggregation and pure recursion with immutable bindings, then
inspect the graph and execution trace.

Here is a complete data program:

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

Native Neovim (Lua), Vim 9 (Vim9script) and Emacs (Elisp) packages provide `.lip`
highlighting, indentation, completion and compiler diagnostics. See the
[editor installation guide](docs/EDITORS.md).

Use `lipc learn` (or `go run ./cmd/lipc learn` in this checkout) for 26 guided
lessons in Chinese, with runnable examples, code exercises, incremental hints
and verification against multiple inputs. The course covers syntax, program
organization, list/string libraries, graphs, State/Tick, Host and Python.
Progress is saved automatically; `:next` advances after a lesson and
`lipc learn --list` lists the course. See [interactive learning](docs/INTERACTIVE-LEARNING.md).

Use `lipc repl` to try expressions, bindings and `fn` declarations interactively:

```text
In [1]: value = 79 / 134
In [2]: value
Out[2]: 0.5895522388059702
In [3]: print(value)
0.5895522388059702
```

Values and functions persist between cells; open delimiters allow multiline input.
Use `:help` for commands and `:quit` to exit. Each cell is compiled; previous effects
are not replayed. Arrow keys edit input and browse history, Tab completes names,
and Ctrl-C cancels a pending cell. Files can also contain just a group of statements:

```lip
a = 2
b = 3
print(a / (a + b))
```

`lipc run examples/statements.lip` prints `0.4`. Top-level statements implicitly run
inside `flow main() { ... }` with no inputs or result. Explicit Flows can also omit
their output declaration and return; optional `-> void` is accepted.
`print` accepts every value type. Declare `-> number`, etc. when returning a value.
Newlines separate statements; statements on the same line need semicolons:
`a = 2; b = 3; print(a / b)`. This also applies to the REPL.

`lipc` bundles its runtime sources, so installed `run/build` commands work in any
project directory without a LIP checkout or project `go.mod`. Both still need
the Go toolchain; compiled core executables run independently.
Place tool options before the file and program inputs directly after it.

## Language and runtime

| Capability | 0.6.4 contract |
| --- | --- |
| Complete program | Requirements with Python/Host/Go aliases, pure fn, explicit flow or top-level statements; typed output via return or mutually exclusive match arms |
| Data | null, bool, number, string, list, object; nested construction |
| Composition | Immutable bindings, operators, block if, literal/type match, wildcards, guards, Map, sequential for, break/continue |
| Arithmetic | + - * /, floor division //, modulo %, power **, logarithm */; [example](examples/math.lip) |
| Core operations | str, len, isEmpty/isNotEmpty, half-open range, ordered fold with an explicit seed, explicit fail, random/random_list/random_int/random_choice/random_shuffle |
| List library | 36 pure functions for grouping, merging, transpose, windows, filtering, sorting, uniqueness, folds, scans and Cartesian products |
| String library | 19 pure operations for cleanup, splitting, joining, searching, slicing, replacement and decimal parsing |
| Recursion | Direct/mutual pure recursion; cycle result types required; tail and common accumulative recurrences lower to loops, other calls have a 1024-frame safety bound |
| Execution | Automatic/sequential/bounded parallel scheduling, effects, cancellation, State/Tick, pure dependency reuse |
| Observation | inspect graph JSON, run --trace lifecycle JSON |
| Existing extensions | Host Adapters, Await, bounded Retry/Feedback, Python Worker |

`range(end)` or `range(start, end[, step])` materializes at most 1,000,000 elements. Comprehensions
accept pure list expressions as sources and compose in arguments, e.g.
`np.mean([x for x in range(1, 19)])` after `import python "numpy" as np`.
Standalone Maps retain bounded parallelism; composed comprehensions run in source order.
Use `for i in range(100) { print(i) }` for sequential operations without collecting
results. Bodies support bindings, match, nested loops and nearest-loop break/continue;
each iteration has its own scope, awaits external calls and stops on cancellation or
failure. Sources evaluate once; loops rerun on every Tick. Transform with comprehensions
and aggregate with fold. See the [loop example](examples/for.lip).
For a service-style loop with no finite source, write `for { ... }` and end it with
`break`, cancellation or an error; it does not create a sentinel range or an overflowing
index. `range(...)` remains the finite sequence constructor.
The [list library](docs/LIST-LIBRARY.md) provides composable collection operations.
Callbacks accept a local function name or an inline pure `fn(x) { x * x }`, e.g.
`list.map(range(5), fn(x) { x * x })`. Fold accepts a two-parameter callback;
an empty list returns its seed. Single-expression fn bodies may omit `return`.
Prefer Map/fold for collection traversal and recursion for
trees or divide-and-conquer.

list/object annotations validate outer shape; dynamic element types are checked
when used. CLI strings are literal, numbers finite decimal, booleans true/false,
and any/list/object inputs JSON. Parameters and fn/callback results also accept nullable `T?`. Nullable CLI inputs
use `null`; a `string?` can use a JSON-quoted string such as `'"null"'` for literal text.
Flow expressions can compose external calls such as `print(np.mean(values))`, with
ordered argument evaluation and effects aggregated across the whole expression.
Pure fn and collection callbacks remain pure. A Flow output `T?` accepts null or no value from a
closed gate.

## Dependencies determine execution

Flow bindings become graph nodes; variable references become dependencies. Pure
expressions inside fn do not become graph nodes. Independent nodes may run in
parallel; consumers wait for their dependencies. if selects a value; literal match can
select a value or execute a matching statement arm. Arms may produce different
types; the enclosing fn or Flow checks its declared return type. Short-circuit
expressions do not cancel separately bound external work.

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

Generated graphs drop execution references after the last declared consumer
completes or is skipped; parallel workers carry only their own dependencies.
LIP tracks computational lifetimes while Go GC handles memory reclamation.
Outputs, State and valid pure caches remain available. See [value lifetimes and measurements](docs/VALUE-LIFETIMES.md).

## Check, run and inspect

The core includes 19 pure `string.*` operations, explicit `fail(message)`, and structured
`lipc check --json` diagnostics (`lip.diagnostics.v1`). The expanded [tutorial](docs/TUTORIAL.md)
has progressive, runnable examples with tested outputs and failures. String
boundaries/operators share a 16 MiB UTF-8 limit; Map/fold sources are capped at
1,000,000 elements before expansion. See the [string reference](docs/STRING-LIBRARY.md)
and [generation/verification workflow](docs/VIBE-CODING.md).

Go Host, Python and pure libraries use the same calls and value dependencies.
Ordinary Python values compose directly; [mixed.lip](examples/tutorial/mixed.lip)
combines Go file reading, text parsing, Python math and pure aggregation. Run
`go run ./examples/tutorial/mixed_demo` with standard Python, without scientific packages.
Python NaN/Inf and colliding object keys fail; sets need explicit ordering.

```bash
lipc check --json examples/strings.lip
lipc run examples/strings.lip ' Go, LIP, go, ,你好 '
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

Declare external work with `import`. Python, Host and Go all support aliases such as
`import python "numpy" as np`, `import host "service.*" as s`, and `import go "fmt" as f`.
Without `as`, the actual Python module name is
preserved; the compiler does not map installation names to module names.
Generate libraries with `--no-main
--package name` when using Host/Go adapters. Actual Python operations enable a resident
Worker using installed packages; unused imports remain metadata. Namespace aliases
can share builtin/local function names: bare and member calls resolve separately.
Single-operation aliases resolve according to their explicit declaration. See the [Host example](examples/host_adapter)
and [Python integration](docs/PYTHON-INTEGRATION.md).

## Specification and verification

The [language specification](docs/ALPHA-0.6-SPEC.md) defines the current rules.
Use `#` for line comments, block-style `if` to select values, and `match` to gate
execution. See the [roadmap](docs/ROADMAP.md) for future directions.

```bash
bash scripts/verify-release.sh
```

This runs tests, race checks, vet, builds, all LIP example checks/generation and
core execution acceptance. Python tests run when their environment is available;
independent core examples need no Python.

- [Quickstart](docs/QUICKSTART.md) · [Tutorial](docs/TUTORIAL.md)
- [Editor support](docs/EDITORS.md) (Neovim / Vim 9 / Emacs)
- [Compiler and Host API](docs/COMPILER.md) · [Specifications](docs/SPECS.md)
- [Roadmap](docs/ROADMAP.md) · [Consistency audit](docs/CONSISTENCY-AUDIT.md)
- [Prototype coverage](examples/PROTOTYPES.md) · [Changelog](CHANGELOG.md) · [Release checklist](RELEASE.md)

Local source/tool archives, SHA-256 verification and offline installation checks are described in [RELEASE.md](RELEASE.md#本地发行包).
