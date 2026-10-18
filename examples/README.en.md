# From Hello World to Flow composition, concurrency, and external calls

[English](README.en.md) | [简体中文](README.md)

Every example uses `Main` as its default entry; helper Flows have names describing their roles.

These executable examples develop one LIP model (the working tree follows the [1.1 draft](../spec/LIP-1.1-SPEC.en.md)): pure functions compute values, Flows organize dependencies, Claims collect facts, and effect/perform executes external actions. `#` starts a line comment; each file explains its syntax in English. Run `make build` at the project root first. Examples are checked through interpretation and actual generated Go programs.

Start with `./lipc learn` for 38 interactive lessons and 76 small examples or `./lipc repl` for interactive expressions, statements, and declarations. To see composition first, read [26_flows](advanced/26_flows.lip), then return to the basics. REPL Flow declarations persist and support calls/pipelines. Use `:begin` / `:end` to enter a whole script or forward declarations in one cell, and `-project DIR` for static imports. REPL `:run path` executes independent files; select another entry with `-entry` and provide `-input` or `-entry-input`.

[30_native_purity](ecosystem/30_native_purity.lip) shows a marked inner native function inside an ordinary outer fn, plus NaN/infinity handling. Run `./lipc run examples/ecosystem/30_native_purity.lip`; Python 3 and its standard math module are sufficient. `@{...}` is the author’s promise of computational purity. The example displays `[3,nan,4,inf]` through `str`; returning nonfinite numbers directly requires `-data-encoding c-hex`. Default JSON remains strict.

## Five rules to remember

1. `flow Name(...) -> Type { ... }` declares a reusable graph. Root `Main` is the default entry; otherwise use the sole root Flow or choose with `-entry Name`. Explicit selection overrides the default, and names are case-sensitive. A valued Flow ends in return; void has no value.
2. `name = value` creates an immutable binding; no same-scope reassignment.
3. fn is pure. Its final expression, optionally prefixed by return, is its result. Flows are not function values; child calls/pipelines compose graphs with ordinary inputs/results, including functions and effects, within one run.
4. `effect op(args)` only constructs a request. perform executes the registered operation.
5. claim() creates a fact collection; `<+` appends contributions, and complete reads return identified records.

The CLI JSON-encodes string/number/list/object results. Braces define control blocks independently of indentation. any permits dynamic values but does not exempt operation-specific type checks.

## Basic syntax

Run `./lipc run examples/basics/FILE.lip`.

| File | Topics | Main result |
|---|---|---|
| [01_hello](basics/01_hello.lip) | Entry, return annotation, final result | `"Hello, LIP!"` |
| [02_bindings](basics/02_bindings.lip) | Immutable names for new results | subtotal=36, total=31 |
| [03_arithmetic](basics/03_arithmetic.lip) | Precedence, power/log, division/floor/remainder, logic | 2**3=8, 8*/2=3 |
| [04_containers](basics/04_containers.lip) | Lists/objects, negative/gather indices, slices, has/keys/put/concat | Original role remains reader |
| [05_strings_numbers](basics/05_strings_numbers.lip) | Unicode, slice/split, parsing, string order | score=0.8, middle="𐀀" |
| [22_multiline_strings](basics/22_multiline_strings.lip) | Triple quotes, indentation, escapes/newlines | HTML/prompts are ordinary strings |

`**` associates right: `2 ** 3 ** 2 = 512`. Power precedes left negation: `-3 ** 2 = -9`; use `(-3) ** 2 = 9` for a negative base. Negative exponents work: `2 ** -3 = 0.125`. `*/` uses its right operand as base: `8 */ 2 = 3`, at multiplication precedence, associating left.

number does not distinguish integer/fraction types: 3 equals 3.0 and displays as 3. `3 / 0` is `inf`, `0 / 0` is `nan`, `222 ** 8888` overflows to `inf`, `nan * inf` is `nan`, and `0 ** -1` is `inf`. Log accepts a nonnegative argument, a positive base other than 1; invalid domains and nonintegral negative powers yield `nan`, while zero arguments and overflow can yield signed `inf`. Fixed algorithms approximate power/log.

Try `learn -lesson power` / `logarithm`. REPL/learn support arrows for editing/history (vertical movement in multiline input), Ctrl-P/N for history, Alt-Enter for a new line, Tab completion, and Ctrl-R search; see [keyboard help](../README.en.md#interactive-computation-and-learning).

The REPL also displays library return values. Go fmt.Print/Printf/Println return `(bytesWritten,error)`; the adapter handles error and returns the count. Thus fmt.Print("hello") appears as `hello5`: text followed by the REPL's 5. UTF-8 byte counts differ from Unicode character counts; this does not add a second library print. See the [full explanation](../README.en.md#why-does-fmtprint-show-a-number).

## Program structure

Run `./lipc run examples/structures/FILE.lip`.

| File | Topics | Main result |
|---|---|---|
| [06_conditions](structures/06_conditions.lip) | if expressions, locals, short-circuiting | grade="ready", safe=false |
| [07_match](structures/07_match.lip) | Literal/type/wildcard patterns, guards | ["empty","nonnegative","other","note","other"] |
| [08_functions_closures](structures/08_functions_closures.lip) | Function values, closures, named args | mapped=[11,12,13], product=20 |
| [09_map_fold](structures/09_map_fold.lip) | Pure callbacks, finite transforms, ordered reduction | total=30 |
| [10_comprehensions](structures/10_comprehensions.lip) | Filtering, guard before body | reciprocals=[0.5,0.25] |
| [11_loops](structures/11_loops.lip) | for, break/continue, complete Claim reads | [1,3] |
| [12_carry](structures/12_carry.lip) | Value iteration, next/break, list append and object updates | early=6, complete=6; two tool batches and a name index; original list still has 1 item |

Ordinary-loop bindings are independent each iteration. Use carry for previous results and Claims for independent contributions. Ordinary bare break and carry break value target their respective constructs.

`with` supports incremental container construction: each round appends with `concat(s.tools, [tool])`, adds or replaces a field with `put(s.by_name, name, tool)`, and passes the whole new state through `next`. The result can seed another batch; empty input preserves the initial value, and old containers remain unchanged. Inside a Flow, first bind `value = perform request`, then add value to the state; `next` itself remains a pure computation.

## Advanced computation and concurrency

| File | Topics | Invocation/result |
|---|---|---|
| [13_tail_calls](advanced/13_tail_calls.lip) | 100,000 direct/mutual/indirect tail calls | sum=5000050000, even=true, indirect=0 |
| [14_parallel_claim](advanced/14_parallel_claim.lip) | parallel, logical positions, fact projections | -parallel 4; input-order results |
| [15_inputs](advanced/15_inputs.lip) | Typed parameters/JSON | -input below |
| [16_snapshots](advanced/16_snapshots.lip) | Live versus complete observations | Complete count 3, live count 0–3 |
| [21_business_failures](advanced/21_business_failures.lip) | Failures as Data, stop on third failure | failures=3, visited=4 |
| [27_linear_model](advanced/27_linear_model.lip) | Centered least squares, training/prediction Flows, MSE, singular input | default Main; slope=2, intercept=1, predictions=[9,11] |
| [29_diagnostics](advanced/29_diagnostics.lip) | print/log/telemetry inside fn; one-way diagnostic output | Result `6`; diagnostics on stderr, `-trace=false` disables delivery |
| [refine](refine.lip) | Source-free carry until threshold | rounds=4, score=0.8 |
| [evidence](evidence.lip) | Parallel evidence/complete reports | missing=["b","c"] |

```sh
./lipc run examples/advanced/13_tail_calls.lip
./lipc run -parallel 4 examples/advanced/14_parallel_claim.lip
./lipc run -input '{"name":"Ada","scores":[0.6,0.9]}' examples/advanced/15_inputs.lip
./lipc run -parallel 4 examples/advanced/16_snapshots.lip
```

Live snapshot variation is intentional. Read a Claim completely for final conclusions rather than guessing all work finished from a live observation. fail(...) is an uncatchable program error; recoverable business failures require explicit Data such as `{ok:false,...}`.

## Declarations, entries, and reuse

[26_flows](advanced/26_flows.lip) combines static Data, private fn, exported children, pipelines, and parallel Claims:

```sh
./lipc run -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip
# {"count":3,"values":[5,3,4]}
./lipc run -entry Adjust -entry-input '{"positional":[3],"named":{}}' -entry-output examples/advanced/26_flows.lip
# {"kind":"value","value":5}
```

`x |> Adjust` fixes x before fully completing the child; named pure fn/core functions also work as stages. Explicit Flow arguments evaluate in source order, allowing nested calls and function/effect values. Native/function values use ordinary calls. OFFSET:number=2 is a static literal; export exposes it. Duplicate JSON keys, invalid Unicode/shape/arguments fail pre-run E-SCHEMA.

Save reuse.lip at the project root:

```lip
import lip "./examples/advanced/26_flows.lip" as adjust
flow Main(xs:list)->list {return [x |> adjust.Adjust for x in xs]}
```

`./lipc run -input '{"xs":[3,1,2]}' reuse.lip` yields [5,3,4]. Only exports are accessible; private add is not. Compilation freezes complete dependencies/versions; programs never reread modules. -project selects the project root. Every declaration must be valid, regardless of selected entry.

Practice with entries/flows/modules lessons; dependencies are bundled. In REPL use `:run -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip`; file execution is independent of session bindings.

## Go / Python ecosystems

These examples explicitly enable teaching contracts with -host demo. Python examples need Python 3 without extra packages.

| File | Topics | Result |
|---|---|---|
| [17_go_python](ecosystem/17_go_python.lip) | Both ecosystems in one Flow | Python mean 2, Go SHA-256 string |
| [18_proposals](ecosystem/18_proposals.lip) | Pure request construction/passing, chains, different result types | `first="HELLO", second="HELLO, LIP", average=2` |
| [19_parallel_read_write](ecosystem/19_parallel_read_write.lip) | Function-summary parallel reads, complete collection, sequential writes | ["ADA","LIN","SAM"]; stderr logs |
| [20_recorded_iteration](ecosystem/20_recorded_iteration.lip) | Prior observation in next request | rounds=4, score=0.8 |

```sh
./lipc run -host demo examples/ecosystem/17_go_python.lip
./lipc run -host demo examples/ecosystem/18_proposals.lip
./lipc run -host demo -parallel 4 examples/ecosystem/19_parallel_read_write.lip
./lipc run -host demo examples/ecosystem/20_recorded_iteration.lip
```

Teaching contracts/schemas/byte encoding live in DemoHost in [adapters.go](../script/adapters.go). The default CLI generic bridge also supports direct imported calls/properties/native objects, e.g. `import go "strings"; strings.ToUpper("hello")` or `import python "math"; math.sqrt(3)`. Calls/properties lower to perform effect. Arrays/native objects retain references; native_data(value) explicitly snapshots Data. See [configuration/conversions/prerequisites](../README.en.md#connecting-go--python).

Keep three points in mind when reading the effect examples:

1. `upper_request("hello")` creates a request without calling the library. `perform request` executes it and returns the Data specified by its contract.
2. `forward(request:effect)->effect` in `18_proposals` shows that fn parameters and results can carry requests. Prepare/Execute also show Flows returning/accepting requests. Host parameters/results, entry protocols, and Claims still require Data.
3. `19_parallel_read_write` directly performs known request builders' results, preserving parallel eligibility. A request returned through `forward`'s unknown parameter or a container projection can execute sequentially, but these paths cannot establish parallel safety.

Project operations can accept ordinary `policy` parameters carrying token, money, and time options, with enforcement implemented by the Host. Contracts declare permission requirements and the Host grants access. See the short example and current boundaries in the [README effect explanation](../README.en.md#every-external-action-follows-one-protocol).

[28_flow_values](advanced/28_flow_values.lip) is a small pure-core example: Offset returns a function capturing delta, and Apply accepts and calls it. It also shows nested arguments and pipeline named arguments. A Flow itself remains a non-value.

## Local Ollama chat, saved calculations, and reminders

[tmp_ollama_chat.lip](tmp_ollama_chat.lip) implements multi-turn streaming chat, tool argument validation, saved calculations, and reminders in one LIP file. The model chooses tools, LIP dispatches by explicit name, and results return to the model for its next decision. It uses the official Python `ollama` client, `prompt_toolkit` for terminal editing, and standard libraries for storage and timers.

The 1.1 rewrite captures the model and configuration in a pure request builder passed to `RunAgent`. Each round, `StreamReply(request(s.messages, tools), terminal)` receives and performs the effect. The model is selected at startup; a replacement builder can use another implementation of the Ollama chunk interface. `Query(request:effect)` returns rows; `Write(request:effect)` returns the affected-row count. Both require a request producing a SQLite cursor, which they close and release. SQL keeps separate bound arguments. Use `py.getattr(cursor, "rowcount")` for native attributes on dynamic request results and explicit `effect cursor.method(...)` for methods, without assuming known static provenance.

Pure `Tool(name, description, properties, required)` standardizes tool definitions. This application chooses to call `BuildTools()` once at startup; append with `concat(base, [Tool(...)])` and carry the new list into the next round as in [12_carry](structures/12_carry.lip). A comprehension can also build entries conditionally. To update tools during a conversation, put the list alongside messages in the carry state. `Tool` only builds the model-facing schema; new tools still need business validation and `DispatchTool` dispatch. Top-level static declarations accept literals, so call builders inside fn/Flow.

Business logic remains in one file: currently 848 lines, compared with the original 831. This round reduces `Main` from 80 lines to 15 and handles startup, the conversation, and shutdown. `OpenModel` returns an ordinary object containing a request closure; `Conversation` owns input, `RunCommand` handles commands, `ChatTurn` prepares context, and `RunTools` feeds back observations. Streaming separates native chunk decoding, pure `MergeChunk` accumulation, and terminal output; `StreamReply` falls from 42 lines to 17. Tool construction uses existing pure functions; passing request functions, effects, and nested containers across Flows uses 1.1. The refactor reduces each structure’s complexity while adding a few focused declarations. Validation, history trimming, streaming, reminders, and terminal editing are preserved. Dynamic request Ops remain Unknown and gain no concurrency permission.

Install the client in the Python 3.9+ environment used by `lipc`, start local Ollama, and download a model that supports tool calls. Run these commands from the repository root; the default model is `llama3.1:8b`:

```sh
python3 -m pip install 'ollama>=0.6.3' 'prompt_toolkit>=3.0'
ollama pull llama3.1:8b
make build
LIP_CHAT_TZ=Asia/Taipei ./lipc run examples/tmp_ollama_chat.lip
```

With `lipc` installed, you can also run `~/go/bin/lipc run ./lipv1/examples/tmp_ollama_chat.lip` from the parent directory. The program uses this single `.lip` file without other application source files or `PYTHONPATH` configuration. Set `LIP_CHAT_MODEL` to override the default model; configure the server address and timeout near the top of the file. Time follows the system timezone by default. Set `LIP_CHAT_TZ` to an IANA timezone such as `Asia/Taipei` or `America/New_York`; startup shows the local date and UTC offset.

Try asking to calculate `123 * 456` with a budget label, find previous calculations, set a reminder in ten seconds, or find the time in 31 hours and 52 minutes. Calculations save their operands, result, label, and timestamp. A dedicated time-offset tool handles date arithmetic and local time. Reminders accept a delay or an RFC 3339 time with a timezone.

- `/time` and `/after 31h52m` (also `24m`, `-1h`, `30s`) return time immediately without a model request.
- `/calculations` and `/reminders` query recent records; `/new` clears chat context while keeping saved records and reminders; `/help` shows help.
- Terminal editing supports Unicode deletion, arrows, Delete, history, Ctrl-R search, Ctrl-A/E, Ctrl-U, and bracketed paste. Ctrl-C discards the current input; Ctrl-D or `/exit` exits. Missing `prompt_toolkit` falls back to line input. Pipes remain plain text.
- Tool output is concise; set `LIP_CHAT_DEBUG=1` for full arguments and JSON results. Explicit identity questions and editing/speed feedback receive no business tools.

The SQLite database is created automatically at `.ollama_chat/state.sqlite3` relative to the **current working directory**; set `LIP_CHAT_DB` to choose a path. Use the same absolute path to share records when running from different directories. Reminders appear while waiting for input or model output, preserving the input draft. No notifications appear while stopped; restarting with the same database delivers overdue reminders. Context retains at most twelve complete user turns within an approximate 24000-character budget, always preserving the newest turn. Chat is not restored after restart; tools retrieve saved records. Each input allows up to eight model requests. Requests use low temperature, a 1024-token output limit, and a 30-minute model keep-alive; the terminal shows a waiting indicator and streams text. Actual speed depends on the model and hardware. Tool argument errors return to the model; connection and database failures still stop the program.

## Website entries

HTTP Hosts pass request objects and receive response objects:

| File | Route | Topics |
|---|---|---|
| [23_home](web/23_home.lip) | GET /{$} | Triple-quoted HTML |
| [24_echo](web/24_echo.lip) | POST /api/echo | Strict JSON/repeated query values |
| [25_greeting](web/25_greeting.lip) | GET /api/hello/{name} | Parameters/JSON responses |

```sh
./lipc serve examples/web/23_home.lip
./lipc serve -route 'GET /api/hello/{name}' examples/web/25_greeting.lip
```

See [HTTP integration](../README.en.md#websites-and-http-integration) for protocols, route manifests, budgets, and current scope. HTTP uses explicit contracts rather than the session-level generic bridge.

## Compile the same examples

```sh
make compile SOURCE=examples/advanced/13_tail_calls.lip OUTPUT=.build/tail
./.build/tail
make compile SOURCE=examples/ecosystem/17_go_python.lip HOST=demo OUTPUT=.build/interop
./.build/interop
make compile SOURCE=examples/advanced/26_flows.lip ENTRY=Main OUTPUT=.build/flows
./.build/flows -input '{"xs":[3,1,2]}'
```

Generated programs fix entry/module snapshots, accept runtime -input/-entry-input/-entry-output/-parallel options, and do not accept .lip files. make compile supports ENTRY/PROJECT/HOST. See [input/output conventions](../README.en.md#language-core-and-program-structure).

make examples prints acceptance results; make clean removes outputs/caches.

`a[-1]` selects the last item, `a[[2,0,-1]]` gathers in index order, `a[1::2]` strides, and `a[::-1]` reverses. Slice bounds clip; strings slice by Unicode scalar. `learn -lesson slicing` exercises populated and empty inputs.
