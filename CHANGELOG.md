# Changelog

## 0.6.4 — Consistent collection APIs and bounded infinite loops

Local implementation snapshot; tags and release artifacts are separate publication steps.

- added `isEmpty`/`isNotEmpty`, uniform random scalar/list generation, Python-style list and
  string slicing, named `reverse` options, stable `sort_by`/`sort_with` comparator APIs and
  short aliases that preserve the canonical `list.*` names;
- aligned `fold` with `list.fold` while keeping `scan` distinct: fold returns the final
  accumulator, scan returns the seed and every intermediate accumulator;
- added explicit `for { ... }` loops with `break`, cooperative Context cancellation and no
  materialized sentinel range; finite `range` remains eager and bounded;
- documented why `range(...)` remains the sequence constructor while `start:end:step` is
  reserved for bounded slicing, and clarified State, Retry and Feedback with beginner flows;
- retained dependency-based intermediate release for Go GC, documenting why recursive size
  estimates and a 1 MiB threshold would cost more than they save;
- lowered tail-recursive and mutually tail-recursive local calls through a bounded-context
  trampoline, lowered safe linear accumulative recurrences such as factorial and sum to
  cancellation-aware loops, and lowered safe two-branch integer recurrences to bounded
  state loops without relying on function names. Raised the fallback local call-depth
  bound to 1024 while keeping data-walker nesting limits independent;
- match arms accept comma-separated literal alternatives such as `0, 1 => value`; the
  alternatives share one guard and result, with unified duplicate and exhaustiveness checks;
- match also accepts runtime type alternatives (`number`, `string`, `bool`, `list`, `object`)
  for `any`/nullable dispatch and narrows an identifier inside the selected arm; `_` remains
  the fallback for null and other Host values;
- synchronized the 0.6.4 specification, tutorials, examples, editor catalogs and generated
  sources; versioned the reference implementation as 0.6.4.

## 0.6.3 — Dependency lifetimes and release consistency

Validated local implementation and source/tool packages; Git tags and remote publication remain separate release steps.

- released generated execution-table references after the last declared consumer
  completes or is skipped, including asynchronous results, gates and loop captures;
  Go GC still owns reclamation, and outputs, State and valid pure caches retain
  their semantics;
- restricted parallel worker snapshots to declared dependencies and discarded
  late results after errors/cancellation without retaining abandoned channel buffers;
- removed redundant effectful/State caches, cleared invalidated pure caches before
  recomputation, and stopped one-shot Graphs retaining returned outputs;
- kept NewGraph's full values-map contract for existing Go callers; generated
  graphs opt in through GraphOptions.ReleaseIntermediates;
- added indexed reusable lifetime plans, weak-pointer reachability tests, generated
  integration/race checks and scalar/large-buffer benchmarks; synchronized the
  0.6 specification, tutorials, examples and all tracked generated sources;
- unified Go-defined scalar types across boundaries, operations and display;
  prevented unhashable Host map indices from panicking and fixed Unicode fields;
- accepted empty semicolon statements consistently in files, blocks and REPL;
  allowed namespace aliases to share builtin/local function names, resolving bare
  and member calls independently; explicit single-operation aliases retain priority;
- required adapters only for used external operations, keeping unused imports as
  metadata without blocking standalone/REPL execution or starting Python;
- embedded the Runtime/CLI version in generated sources and added reproducible
  local source/tool packages with SHA-256 verification and offline smoke checks.
- added native Neovim Lua, Vim9script and Emacs Elisp editor packages, generated
  language catalogs, Unicode-aware compiler diagnostics and integration tests;
  included the packages and installation guide in source/tool archives.

## 0.6.2 — Composable language, interactive tools and consistent contracts

Local implementation snapshot; tags and release artifacts are separate publication steps.

- normalized disposable standalone build paths to reuse bundled-runtime cache
  entries and produce identical binaries from identical sources in different
  temporary modules; execution diagnostics retain the original LIP locations;
- audited all documentation LIP blocks and checked diagnostic examples, version references,
  lesson indexes, standard-library catalogs and tracked generated sources automatically;
  synchronized the 0.6 specification, capability tables and release checks;
- unified Python/Host/Go import completion, respected shadowed names, and corrected
  interactive reference return types and policy placement;
- audited import scopes and backend resolution: parameters, bindings and iteration
  variables can shadow imports lexically; conflicting backend namespaces require
  explicit aliases; Host imports preserve their identity with or without `as`;
  local function registration is isolated from same-named Host operations;
- allowed external composition in all Flow expressions, with whole-expression
  effects for scheduling and Tick caching, including Map sources, state initialization
  and retry/feedback;
  pure functions and collection callbacks still reject external operations;
- extended nullable `T?` to parameters, named/inline functions and CLI inputs,
  with match-arm narrowing after null handling; loop errors locate the innermost
  failing statement, and feedback accepts dotted core operations without imports;
- aligned heterogeneous if and match results: branch types infer any when needed,
  while declared function and Flow boundaries still validate every returned result;
- extended `as` to Host operations, Host wildcard namespaces and Go packages;
  aliases preserve canonical operation names, dependency identity and metadata,
  including nested loop calls, retry/feedback and Host registration checks;
- added sequential statement `for` over finite lists in Flows, top-level programs
  and REPL cells, with immutable iteration scopes, nested loops, bindings, match,
  awaited external calls and nearest-loop `break` / `continue`; sources evaluate
  once, errors/cancellation stop iteration, and loops rerun on each Tick;
- added floor division `//`, divisor-signed modulo `%`, right-associative power
  `**` and base logarithm `*/`, with numeric/domain/finite-result checks;
  moved LIP line comments to `#` so `//` has one unambiguous operator meaning;
- replaced gated syntax with Rust-style `match`: literal patterns, `_` defaults,
  optional `if` guards, lazy value expressions and mutually exclusive Flow arms;
  arms may return different types, checked at the enclosing fn or Flow boundary;
  migrated examples, generated code, diagnostics and interactive lessons;
- unified collection callbacks across map/filter/fold/scan/grouping/sorting with
  inline pure `fn(x) { expression }` callbacks, optional type annotations and
  immutable captures tracked as dependencies;
- added `range(end)` and optional `return` for single-expression named/inline fn
  bodies; improved callback signatures, accumulator types, sorting keys and
  transpose errors while preserving their actual causes;
- allowed comprehensions to use pure source expressions directly and compose in
  call arguments, pure functions, conditionals and nested comprehensions;
  standalone Maps retain bounded parallelism, composed Maps preserve ordered,
  lazy evaluation without multiplying workers;
- unified external declarations under `import`; Python modules now support explicit
  `as` aliases and dotted module paths, preserving original names by default;
- added REPL cursor editing, history with draft restoration, Unicode-aware deletion,
  Tab completion, cancellation and bracketed multiline paste; piped input is quiet;
- separated diagnostic repairs by their actual cause, with expected argument counts,
  operation spelling suggestions and focused callback, type, dependency and return advice;
- unified print/str/REPL value formatting and language type names in runtime errors;
- added a compiled `lipc repl` with persistent values/functions, multiline input,
  session commands and recovery after failed cells without replaying effects;
- allowed effect-only Flows to omit their output declaration and return, with
  optional explicit `-> void`; these programs no longer print an automatic null;
- allowed files to contain top-level statements without a Flow wrapper, implicitly
  executing as main() with no inputs or result through the same CLI/compiler path;
- required newlines or semicolons between executable statements; same-line
  statements now need `;`, with a repair hint in files and the REPL;
- improved source errors with caret markers and specific repair examples; runtime
  failures identify the source statement and suggest fixes instead of dumping graph internals;
- consolidated line comments and conditionals to block-style `if`;
  removed legacy syntax parsing, migration, source-generation shorthand and short options;
- consolidated Python adapter names to PythonWorker/PythonWorkerConfig and Python/Script;
  replaced legacy NodeSpec/MapSpec purity flags with NodeSpec.Effect;
- consolidated specifications into the current language and library references,
  keeping release history here; simplified tutorials around the current rules;
- unified help on stdout, concise usage errors on stderr and exit code 2 across
  commands; bare `lipc` shows help and `inspect --help` works;
- validated run inputs before invoking Go, naming missing inputs and showing the
  source command; added source excerpts and a repair hint to terminal diagnostics;
- unified command options before the entry file, including inline values; allowed
  explicit source paths without a .lip suffix, and made --no-main select library source;
- supported build output directories, Windows default .exe names and trace parent
  directories; prevented build/trace output from overwriting source aliases;
- simplified beginner examples to hello/run/build, using local output paths and
  keeping Go source integration in the compiler guide;
- documented installation via `go install ./cmd/lipc` following existing GOBIN/GOPATH
  settings, with PATH lookup or direct invocation according to user preference;
- made the run entry file the argument boundary: `lipc run [--trace path.json]
  file.lip args...`; tool options precede it, program inputs follow it, and
  `--` after the file is an ordinary input; tool options use double hyphens;
- merged standalone build sources, installation guidance and regression coverage
  into existing runtime/catalog, quickstart, release script and test files;
- bundled version-matched runtime sources into lipc so run/build work outside the
  checkout, using temporary modules isolated from the caller's go.mod/go.work;
- preserved the caller's working directory for execution and local Python imports;
- verified offline builds, toolchain-free core executables and temporary-file cleanup.

## 0.6.1 — Text processing, verified tutorials and consistent boundaries

- added 19 fixed pure string operations with shared compiler/runtime signatures,
  Unicode character positions, explicit decimal parsing and bounded allocation;
- added pure `fail(message)` with internal bottom-type inference so failure
  branches preserve the successful result type;
- added `lipc check --json` (`lip.diagnostics.v1`) with source location, context,
  stable diagnostic categories and repair guidance;
- expanded the tutorial to 21 progressive sections, checked source snippets,
  actual output/failure cases, and complete State/Host/Python demonstrations;
- verified one Flow composing Go IO, pure parsing, Python calculation and pure
  aggregation in sequential, automatic and bounded parallel execution;
- bounded string operators/typed boundaries at 16 MiB and Map/fold sources at
  1,000,000 elements before expansion; fixed the file-reading example's effect;
- rejected Python nonfinite-to-null conversion and colliding dictionary keys;
  kept unordered sets as handles and rejected implicit ordered-list conversion;
- reclaimed handles allocated during failed Python result conversion; Python
  startup failures no longer masquerade as missing-interpreter test skips;
- added compiler fuzzing, resource-boundary tests and repeatable acceptance.

Alpha behavior changes: invalid UTF-8/oversized strings now fail consistently;
oversized Map/fold inputs fail; Python NaN/Inf no longer become null, and sets
must be explicitly ordered (for example with `builtins.sorted`) before conversion.
No new language keywords, hot reload, automatic AI calls or performance promises.

## 0.6.0 — Small, complete core

Specification: [ALPHA-0.6-SPEC.md](docs/ALPHA-0.6-SPEC.md)

- froze a self-contained core and deferred Worker pools, embedded Python, streams,
  new backends and unrelated DSL extensions; retained existing adapters;
- added source-ordered object literals, null, explicit list/object boundary types,
  optional null results, boolean negation and consistent Unicode string indexing;
- added Rust-style `if condition { value } else { value }`, retaining 0.5 syntax;
- added fixed pure `len`, bounded half-open `range` and ordered seeded `fold`;
- added a 34-operation pure list library inspired by Mathematica, with a shared
  signature catalog, stable grouping/sorting, shape transforms, filtering and scans;
- enabled direct/mutual pure recursion with declared cycle result types, cancellation
  checks and a maximum local call depth of 256;
- added stable `lipc inspect` graph JSON and `lipc run --trace path.json` lifecycle JSON;
- preserved program exit codes by running a temporary binary, forwarded interrupts,
  and made standalone cleanup execute on failure as well as success;
- added dependency-free report, range/Map/fold and recursive tree and list-processing examples, negative
  and end-to-end conformance tests, and `scripts/verify-release.sh`.

Go string indexing now returns one Unicode character as a string rather than a byte
number; use a Host adapter when raw byte operations are required. Collection types
check outer shape; ordinary loops, closures and a generic iterator protocol remain
outside this release. Range is capped at 1,000,000 elements; recursion has no tail-call
optimization. This is Alpha 0.6, not a 1.0 compatibility commitment.

## 0.5.0 — Alpha 0.5 complete program contract

- required explicit Flow/function parameter types and Flow output types, with one
  return and explicit optional gated outputs (`Type?`);
- added pure local function composition, expression conditions, unary signs, finite
  decimal/exponent literals, lazy `if` and short-circuit boolean expressions;
- checked dynamic output/future values, rejected extra library inputs and invalid
  Tick updates, and preserved inferred State types through `SetState`;
- added `lipc migrate` with source-preserving diagnostics and checked output;
- unified CLI parsing and JSON output across run/build/generated-source paths,
  with end-to-end conformance tests;
- removed the special Pandas operation, used real object methods, isolated local
  Host registrations, and invalidated stale handles across Worker restarts;
- revalidated cached blob descriptors against file checksums.

Python control/data plane included in this release:

- added a resident `runtime.ProcessHost`/`PythonWorker` JSONL adapter with handshake,
  request IDs, bounded queueing, deadlines, cancellation, restart, and Python errors;
- added generic dotted Python calls through `runtime.NewPythonHost`, explicit
  Pure/ReadOnly/session Host registration, configurable module policy, and generic
  Python object handles (`python.call`, `python.to_json`, `python.release`);
- added a runnable scientific LIP Flow under `examples/python`, protocol tests, and
  cold-start/warm-call benchmarks. The Worker does not maintain a fixed library list:
  any installed package reachable by a dotted callable path can be used.
- started the P2 local data plane with atomic, checksummed, size-limited blob files,
  read-only raw/`.npy` mappings, explicit release, tamper detection, and JSON-versus-
  mapped-array benchmarks.
- made generated entry points strict: Flow parameters are required positional inputs,
  typed values are parsed without defaults or `LIP_INPUT`, and `any` uses JSON;
- added top-level Python/Go/Host dependency metadata, `lipc check`
  reporting, generated `RequiredDependencies()`, and automatic Python Worker startup
  for standalone programs that declare a Python requirement.
- rejected Flows without any `return` output and declared Host requirements in the
  prototype/conformance sources so examples no longer hide their external boundary.
- made undeclared external calls a `lipc check` error: dotted calls need a Python (or
  explicit Host) requirement, and bare calls need a Host requirement.

## 0.4.0 — Alpha 0.4

Alpha 0.4 completes the next runtime milestone on top of persistent Alpha 0.3
instances.

Included:

- bounded `retry(call, attempts)` execution;
- bounded `feedback(initial, revise, verify, attempts)` execution;
- cancellation and deadline propagation with `Cancelled` trace states;
- `EffectPure`, `EffectReadOnly` and `EffectExternalWrite` metadata;
- concurrent read-only Host operations and sequential external-write barriers;
- explicit `NodeSpec.After` ordering constraints;
- Go-like `lipc help`, executable `build`, temporary `run` and `--emit-go` source mode;
- serialized Graph/Instance observation and execution for safe object reuse;
- State/Tick and incremental cache tests, examples and specifications.

The language still keeps effect metadata at the Go Runtime boundary rather than
introducing a full effect type system.

## 0.3.0 — Alpha 0.3

Included:

- persistent `runtime.Instance` and generated `NewInstance` APIs;
- `state(initial)`, `SetState`, `State` and monotonic logical ticks;
- dependency snapshots and incremental reuse of unchanged pure nodes;
- per-tick Trace events and stateful examples.

## 0.2.0 — Alpha 0.2

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

This is the first publishable Alpha release of LIP.

Included:

- handwritten lexer/parser and dependency-graph compiler;
- `flow`, expression `fn`, single-assignment bindings, gated execution, `return`;
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
