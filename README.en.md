# LIP beta 0.9

[简体中文](README.md) | [English](README.en.md)

LIP is a small computational language for organizing calculations, parallel tasks, and tool calls. You describe inputs, dependencies, and results; the runtime builds a dependency graph and runs eligible work concurrently.

We build only the language core. Files, networking, databases, models, and other library capabilities primarily come from the **Go / Python ecosystems**, connected through a single external operation interface.

## Three mechanisms to know

**Graph-based parallelism.** A computation that uses another computation's result waits for it to finish. Independent work that permits concurrency can run at the same time. This lets you organize multiple analysis tasks without managing threads, task queues, or completion counters yourself.

**Claim collects facts.** Parallel tasks submit results with `<+`, without overwriting existing facts. A complete Claim read waits for the relevant producers to finish, then returns records ordered by their logical program positions. Each record carries its value and source position, making it easier to combine results, check for missing evidence, and trace conclusions.

**effect / perform calls external operations.** `effect` creates a request; `perform` executes it. All Go, Python, and Host operations use this same interface, with consistent checks for arguments, return values, permissions, and call records. You can save a request and use it as input to a later step; `import` itself does not execute native code.

```lip
import go "strings" as text

flow Analyze() -> list {
    facts = claim()                 # Create a store for collecting facts.
    for name in ["ada", "lin", "sam"] parallel {
        # The Host has registered this Go operation and permits concurrent reads.
        result = perform effect text.ToUpper(name)
        facts <+ {input: name, result: result}  # Append a fact.
    }
    # Reading facts waits for contributions; row.value contains the submitted data.
    return [row.value for row in facts]
}
```

Immutable values, pure functions, conditions, loops that carry values between iterations, and these three mechanisms are enough to build **ReAct** programs: “think → call a tool → observe the result → decide again.” Reactive workflows can follow “receive an event → run a computation → collect results → perform an action.” The graph manages dependencies within one run; the Host handles ongoing event reception and starts subsequent runs. A live Claim snapshot is a single observation, with no automatic subscription or threshold trigger.

## Getting started

Go **1.25 or newer** is required. No specific patch release or `toolchain` version is pinned. The current validation environment uses Go 1.27. Basic Python lessons use the Python 3 standard library; scientific examples also require the corresponding third-party packages.

Install the command from the beta project root, which contains `go.mod`:

```sh
go install ./cmd/lipc
```

Go installs `lipc` in the directory specified by `go env GOBIN`. If GOBIN is empty, it uses the `bin` directory under GOPATH, usually `~/go/bin`. Add that directory to PATH to start the interactive tools from any directory:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"  # When using the default installation directory.
lipc help
lipc repl
lipc learn
lipc learn -lesson power
lipc run /path/to/program.lip
lipc build -o /path/to/program /path/to/program.lip
```

You can also choose an installation directory, for example on Linux/macOS:

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/lipc
export PATH="$HOME/.local/bin:$PATH"
```

For continued use, add the appropriate PATH setting to your shell configuration. On Windows, add the installation directory to your user PATH; the command is named `lipc.exe`. The REPL and learning tool include their runtime and lessons, so these commands do not require the source directory after installation.

The module name in `go.mod` is currently `lipbeta`, so the commands above install from local source. A remote `go install repository/path/cmd/lipc@version` command will be provided once the published module path is established.

If linking fails with `mapping output file failed: no space left on device`, the temporary filesystem used by Go usually lacks free space or inodes. Clear caches you have confirmed are no longer needed there and retry, or select a temporary directory with enough space. At this point lipc has not started, so it cannot clean up automatically or append a project-specific hint to the Go command's error.

Check the actual build locations. The commands below assume the default temporary directory, `/tmp`; if you set `GOTMPDIR` or `TMPDIR`, check that directory instead:

```sh
df -h . /tmp
df -i . /tmp
go env GOTMPDIR GOCACHE
```

`/tmp` may be a separate, small tmpfs. Go links in its temporary directory before copying to the installation directory, so free space at the destination does not ensure the build can succeed. `go clean -cache` only clears the build cache selected by `go env GOCACHE`; if that cache is on another filesystem, clearing it cannot free space in the full temporary filesystem. For other Go build caches, confirm the directory and its purpose before using `GOCACHE=/confirmed/cache/directory go clean -cache`. Keep active build directories and other files.

Alternatively, choose a writable temporary directory with enough space in the current terminal, then install:

```sh
mkdir -p .build/tmp
export GOTMPDIR="$PWD/.build/tmp"
export TMPDIR="$GOTMPDIR"
go install ./cmd/lipc
```

These variables affect only the current terminal and its child processes. A direct `go install` does not read Makefile settings; `make build` automatically uses a project-local temporary directory. Automatic Go adapters and `lipc build` also need temporary space, so retain these variables if the default directory remains full. `make clean` removes `.build`; recreate the directory before further use, or choose another writable directory on a disk with enough space.

To try examples from the source directory, build locally:

```sh
make build
./lipc run examples/basics/01_hello.lip
./lipc run -parallel 4 examples/evidence.lip
./lipc run -host demo examples/ecosystem/17_go_python.lip
```

The respective outputs are:

```text
"Hello, LIP!"
{"missing":["b","c"]}
{"digest":"a615eeaee21de5179de080de8c3052c8da901138406ba71c38c032845f7d54f4","mean":2}
```

The [example tutorial](examples/README.md) starts with Hello World, bindings, and arithmetic, then introduces functions, branches, loops, parallelism, Claim, and external calls. The tutorial and comments in the LIP programs under `examples/` are currently in Chinese.

Arithmetic supports `+ - * / // % ** */`. `**` is exponentiation; `a */ b` computes the logarithm of a with base b, the operand on the right:

```lip
flow Numbers() -> object {
    return {
        power: 2 ** 3,             # 8
        chain: 2 ** 3 ** 2,        # 512, equivalent to 2 ** (3 ** 2)
        negative: -3 ** 2,         # -9, equivalent to -(3 ** 2)
        square: (-3) ** 2,         # 9; parentheses make the negative number the base.
        reciprocal: 2 ** -3,       # 0.125
        logarithm: 8 */ 2          # 3, the same number as 3.0
    }
}
```

`*/` has the same precedence as multiplication and division and associates from left to right. Logarithms require a positive left operand and a positive base other than 1. Powers with a negative base require an integer exponent. `0 ** 0` is defined as 1; `0 ** -1` is an error. Exponentiation and logarithms use the fixed binary64 algorithms in the specification. Results are approximate and are not guaranteed to be correctly rounded; domain errors and non-finite results raise `E-NUMBER`. Use Go/Python libraries through the Host for other numerical capabilities.

Pass Flow arguments as a JSON object:

```sh
./lipc run -input '{"name":"Ada","scores":[0.6,0.9]}' examples/advanced/15_inputs.lip
```

The CLI writes the final result as JSON. Use registered external operations to print messages or write files. The demo Host's `console.write` writes to stderr, leaving stdout for the result.

## Interactive computation and learning

```sh
./lipc repl
./lipc repl -host demo
./lipc learn
./lipc learn -list
./lipc learn -lesson claim
```

In the REPL, you can enter expressions, immutable bindings, functions, and requests directly:

```text
In [1]: x = 6
6
In [2]: fn twice(n:number) -> number { n * 2 }
In [3]: twice(x)
12
```

The REPL enables a general Go adapter by default. Import a standard library or a third-party package resolved by the current Go module to call exported functions, construct types, read attributes, and invoke methods without a per-package registry:

```text
In [4]: import go "strings"
In [5]: strings.ToUpper("1waht")
"1WAHT"
In [6]: strings.ToTitle("waht 12 ase? as a")
"WAHT 12 ASE? AS A"
In [7]: import go "bytes"
In [8]: buffer = bytes.NewBufferString("hello")
<go *bytes.Buffer>
In [9]: buffer.WriteString("!")
1
In [10]: buffer.String()
"hello!"
```

Third-party Go libraries use **full, case-sensitive package paths**. A project name or website label does not replace the package path; `as` provides a convenient name for calls. For example:

| Library | Go package path | Import example |
|---|---|---|
| Gonum matrices | `gonum.org/v1/gonum/mat` | `import go "gonum.org/v1/gonum/mat" as mat` |
| Gorgonia | `gorgonia.org/gorgonia` | `import go "gorgonia.org/gorgonia" as gorgonia` |
| Gin | `github.com/gin-gonic/gin` | `import go "github.com/gin-gonic/gin" as gin` |
| Viper | `github.com/spf13/viper` | `import go "github.com/spf13/viper" as viper` |

Configure a Go module in your own working directory, then start the installed lipc there. Skip initialization if you already have a `go.mod`. Run these commands in your terminal:

```sh
mkdir analysis
cd analysis
go mod init example.org/analysis
go get gonum.org/v1/gonum/mat
lipc repl
```

Then call the library in the REPL:

```text
In [1]: import go "gonum.org/v1/gonum/mat" as mat
In [2]: m = mat.NewDense(2,2,[1,2,3,4])
<go *mat.Dense>
In [3]: m.At(1,0)
3
```

For other libraries, configure the corresponding package path with `go get` in the terminal before importing it in LIP. Installing lipc does not configure your working directory's dependencies; `import` does not add dependencies or map short names to preset projects. Gonum contains multiple packages, so choose the path for the functionality you need. These libraries all use the generic adapter.

`fmt.Print`, `Printf`, and `Println` use the same adapter; their returned byte counts appear as expression results. `import go "string"` reports a missing package at import time; the correct package name is `strings`.

Python uses a persistent worker with a separate protocol channel to load libraries from the current Python environment. Module attributes, submodules, classes, methods, keyword arguments, and native function references share this bridge:

```text
In [11]: import python "scipy" as sp
In [12]: sp.__version__
...the installed SciPy version...
In [13]: import python "numpy" as np
In [14]: a = np.array([1,2,3], dtype=np.int64)
<python numpy.ndarray>
In [15]: a.reshape(3,1).tolist()
[[1],[2],[3]]
In [16]: sp.linalg.norm(a)
3.7416573867739413
```

Without `as`, the last path component becomes the alias, so `import python "scipy"` allows `scipy.__version__`. `help()` shows interactive help; `help(module)` and `help(module.member)` show Python documentation or Go export signatures. `help(value)` describes language values, and `help("math.sqrt")` looks up Python documentation. Completion never executes library code.

Direct calls and attribute reads lower to ordinary `perform effect` operations before checking. File execution, the REPL, and generated programs use the same path. Explicit application contracts take precedence; generic operations declare `Unknown` without inferring purity or concurrency. Imports declare namespaces: Go uses its toolchain to discover exports, while Python loads a module on its first actual operation. Neither installs dependencies. Package initialization occurs during an actual Host-authorized operation.

Open parentheses or braces allow multiline input; other continuations use a trailing `\`. `:vars` shows saved values, `:history` shows input history, `:reset` clears the session, `:cancel` discards unfinished input, and `:quit` exits. Piped input automatically hides prompts:

```sh
printf 'x=6\nx*2\n:quit\n' | ./lipc repl
```

Each input uses the same compilation and execution rules. Successful inputs preserve values and closures, without rerunning earlier `perform` calls. Failed inputs do not save new bindings, and external actions that have already occurred are not rolled back. Each input has its own run identity. Use `:run examples/advanced/14_parallel_claim.lip` to execute a complete Flow with Claim and parallel collection. File runs do not read or change REPL bindings.

The REPL and learning tool share a terminal editor that lets you edit input before submitting it. Common shortcuts are:

| Key | Action |
|---|---|
| ← / →; Ctrl-B / Ctrl-F | Move left / right |
| Home / End; Ctrl-A / Ctrl-E | Move to the start / end of the line |
| Alt-B / Alt-F; Ctrl-← / Ctrl-→ | Move by word |
| Backspace / Delete; Ctrl-D | Delete the previous / next character; Ctrl-D exits on empty input |
| Ctrl-U / Ctrl-K / Ctrl-W; Alt-D | Cut to the start / end of the line / previous word / next word |
| Ctrl-Y | Paste the most recently cut text |
| Ctrl-_ (often Ctrl-/ in terminals) | Undo the last edit |
| Ctrl-T | Swap the two characters near the cursor |
| ↑ / ↓; Ctrl-R | Browse / search history; Ctrl-G cancels a search, Enter submits |
| Tab | Complete core names, saved REPL names, registered import members, and commands |
| Ctrl-L; Ctrl-C | Clear the screen / discard current input and unfinished program drafts |

Bracketed paste is supported: paste a multiline program and press Enter to submit it. Chinese characters, common combining characters, and emoji can be moved over and deleted as units. Terminal raw mode is active only while editing; original settings are restored before executing programs or calling providers. Piped input stays in plain text mode. Terminal editing supports Linux, macOS/BSD, and Windows consoles with VT enabled; Linux is the platform tested so far.

`learn` includes 15 lessons, from strings, exponentiation, and logarithms to parallel Claim collection and tool calls. The built-in lesson text is currently in Chinese. Enter an answer for the template to validate it; `:hint` shows a hint, `:demo` runs an explanatory example, and `:next` advances to the next lesson. Use `:edit` to begin a complete program and `:submit` to submit it. Lessons check results with multiple inputs; running a demo does not automatically mark a lesson complete.

Learning progress is not saved by default. To save it, specify a file:

```sh
./lipc learn -progress /path/to/progress.json
```

## Generating Go programs

```sh
./lipc emit-go -o .build/hello.go examples/basics/01_hello.lip
go build -o .build/hello .build/hello.go
./.build/hello

# You can also generate an executable in one step.
./lipc build -o .build/refine examples/refine.lip
./.build/refine
```

In restricted environments, use the Makefile to keep caches inside the project:

```sh
make compile SOURCE=examples/ecosystem/17_go_python.lip HOST=demo OUTPUT=.build/interop
./.build/interop -parallel 4
```

The generator compiles LIP expressions into dedicated Go functions and includes the graph scheduler and external operation runtime. Generated programs do not parse LIP source or depend on the source directory. The pure core and demo Host require only the Go standard library. Generic Python calls need Python and the relevant packages on the runtime machine. Automatically discovered Go packages compile and load their adapters at runtime, so they require a matching Go toolchain, CGO, and a platform supporting Go plugins.

Interpreted execution and generated Go programs share rules for numbers, function calls, Claim, errors, and the Host boundary. Generated programs check Host contracts at startup to prevent differences between operation definitions at compile time and runtime. Executables support `-input`, `-parallel`, `-steps`, and `-elements`.

## Connecting Go / Python

The CLI enables the generic bridge by default. Embedded applications enable it with `script.NewNativeHost(host)` or create a bridged session with `script.NewInteractiveSession(host)`. `script.NewSession(host)` retains the supplied Host configuration. Call `Close()` when finished; `:reset` clears bindings and invalidates resource references. Complete programs support direct library calls as well as explicit `effect` creation and execution.

The Go adapter uses export type information and reflection for variadic arguments, multiple results, structs, interfaces, and objects passed between packages. A trailing error in a multi-result signature is the failure channel; a sole error result remains a native object, allowing `errors.New`, `fmt.Errorf`, and error comparisons to compose. Register explicit contracts for other error mappings. Generic functions infer concrete type arguments from actual inputs. Automatic loading requires the Go toolchain, CGO, Go plugin support (Linux, macOS, or FreeBSD), and dependencies resolved by the current module. Embedded applications can also register Go exports directly, avoiding dynamic compilation:

```go
host := script.NewNativeHost(script.Host{})
defer host.Close()
host.Native.Go.RegisterPackage("application/text", map[string]any{
    "Upper": strings.ToUpper,
})
// LIP: import go "application/text" as text; text.Upper("hello")
```

For generic functions whose type parameters cannot be inferred, generic types, and application-specific types, register concrete instances or constructors. When emitting these programs, supply the same registration through `GoOptions.HostSource`. LIP pure functions cannot serve directly as native callbacks; pass native function references within the same runtime or register a dedicated application adapter.

The persistent Python worker retains modules and objects within a Host. Responses use a dedicated pipe, separate from library stdout. Imports, attributes, and calls use Python's own protocols, without a package whitelist or premature conversion of arrays to lists. Cancellation or connection loss never triggers an automatic restart or retry; close or reset the session to continue.

| Value | Generic bridge behavior |
|---|---|
| null, bool, finite number, string | Ordinary Data; native integers outside the safe range are rejected |
| LIP list / object | Convert according to the Go target type, or to Python list / dict |
| Ordinary Python list / tuple / string-keyed dict | Convert to Data, possibly containing resource references |
| Go containers, structs, pointers, functions; Python arrays, DataFrames, bytes, complex values, etc. | Preserve native objects behind Host-scoped resource references |
| `native_data(value)` | Explicit Data snapshot: Go recursively reads containers and exported fields; Python supports containers and `tolist()`; other objects need library conversion methods |
| `native_release(value)` | Invalidate the reference; subsequent access fails. This does not replace an object's own file/resource close method |

Go checks numeric conversions against the target signature; string to `[]byte` explicitly uses UTF-8. Python converts safe integral numeric inputs to `int` and other numbers to `float`. Use library types and constructors to preserve a specific dtype. Transfer values between Go and Python through explicit Data snapshots or byte encodings; native references cannot cross runtimes. Non-finite numbers and cyclic objects cannot become Data.

Applications can still register stricter contracts through `script.Host`, declaring versions, schemas, effects, concurrency, capabilities, and resources. `GoHandler` adapts fixed signatures and supports context injection. `PythonHandler` provides one process per call for explicitly registered operations. Authorization and parameter validation precede invocation. Conversion failures after a completed action retain that fact, and replay does not repeat the action.

The [demo Host](script/adapters.go) registers Go string and digest operations, Python mean and sum operations, and console output. Enable it explicitly with `-host demo`. `script.DemoHost()` creates the same configuration, and `script.DemoHostGoSource()` provides the corresponding registration code for generated programs. Applications can also embed the core with `Compile(source, host)` and `Program.Run(...)`, or generate Go programs containing their own Host configuration through `Program.EmitGo(...)`.

## Project layout

```text
cmd/lipc/       File execution, Go generation, REPL, and lessons
script/         Compilation, static checks, immutable values, function evaluation,
                graph scheduling, and the Host interface
examples/       LIP programs with comments
tests/          Public API, interactive CLI, and interpreter/compiler consistency tests
spec/           The beta 0.9 specification (currently in Chinese)
.agents/skills/ Reusable beta development guidance
```

Code is organized by semantics into files with substantive responsibilities. REPL session management lives in `script/session.go` and reuses the compiler and execution engine. CLI interaction and lessons live in `cmd/lipc/interactive.go`, with the shared terminal editor in `cmd/lipc/editor.go`. Platform-specific terminal interfaces are separated with build tags.

## Guarantees, scope, and validation

With fixed external responses and the prerequisites in the [specification](spec/BETA-0.9-SPEC.md), legal schedules produce the same result or the same primary error. A live Claim snapshot records the state at the moment of observation and can depend on scheduling. Once an error occurs, external actions that have already started are not rolled back; different schedules may also start different calls.

The current implementation includes an interpreter, Go code generation, and Go/Python operation interfaces. `MemoryJournal` provides recording and replay within a run. **The optional durable recovery profile is not implemented**: there is no resume, reconcile, or fencing after a process crash. Libraries, ongoing event listeners, storage, and deployment are supplied by applications and the Go/Python ecosystems.

`Options.After` currently supports dependency constraints for top-level nodes and regions. `Options` also supports budgets for steps, individual list size, graph size, and non-tail call frames. Tail calls do not accumulate function stack frames. See the API and specification for the precise limits.

```sh
make test       # Examples, interaction, and semantic regressions; runs generated Go programs.
make race       # Checks parallel data races in the interpreter and generated programs.
make vet
make examples   # Shows validation results for each example.
make clean     # Removes lipc, .build, and Python caches.
```

Examples run through both the interpreter and generated Go programs, with concurrency limits of 1 and 4. Regression coverage also includes exponentiation and logarithm precedence and numerical boundaries, tail calls, type and argument checks, unselected branches, request chains, error ordering, call replay, and permission checks. Interactive tests use real pseudoterminals on Linux to check shortcuts and terminal setting restoration; plain piped input is checked separately. Development conventions and validation guidance are available in the [beta development skill](.agents/skills/lipbeta-development/SKILL.md), currently in Chinese.
