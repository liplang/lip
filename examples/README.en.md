# From Hello World to Flow composition, concurrency, and external calls

[English](README.en.md) | [简体中文](README.md)

These executable examples develop one LIP 1.0 model: pure functions compute values, Flows organize dependencies, Claims collect facts, and effect/perform executes external actions. `#` starts a line comment; each file explains its syntax in English. Run `make build` at the project root first. Examples are checked through interpretation and actual generated Go programs.

Start with `./lipc learn` for 18 interactive lessons or `./lipc repl` for interactive expressions, statements, and declarations. To see composition first, read [26_flows](advanced/26_flows.lip), then return to the basics. REPL Flow declarations persist and support calls/pipelines. Use `:begin` / `:end` to enter a whole script or forward declarations in one cell, and `-project DIR` for static imports. REPL `:run path` executes independent files; select multiple entries with `-entry` and provide `-input` or `-entry-input`.

## Five rules to remember

1. `flow Name(...) -> Type { ... }` declares a reusable graph. A sole root Flow is the default entry; choose among multiple roots with `-entry Name`. A valued Flow ends in return; void has no value.
2. `name = value` creates an immutable binding; no same-scope reassignment.
3. fn is pure. Its final expression, optionally prefixed by return, is its result. Flows are not function values; child calls/pipelines compose graphs with Data inputs/results.
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
| [04_containers](basics/04_containers.lip) | Lists/objects, indices, has/keys/put/concat | Original role remains reader |
| [05_strings_numbers](basics/05_strings_numbers.lip) | Unicode, slice/split, parsing, string order | score=0.8, middle="𐀀" |
| [22_multiline_strings](basics/22_multiline_strings.lip) | Triple quotes, indentation, escapes/newlines | HTML/prompts are ordinary strings |

`**` associates right: `2 ** 3 ** 2 = 512`. Power precedes left negation: `-3 ** 2 = -9`; use `(-3) ** 2 = 9` for a negative base. Negative exponents work: `2 ** -3 = 0.125`. `*/` uses its right operand as base: `8 */ 2 = 3`, at multiplication precedence, associating left.

number does not distinguish integer/fraction types: 3 equals 3.0 and displays as 3. Log arguments/base must be positive, base not 1; negative power bases require integral exponents. Fixed algorithms approximate power/log; domain/overflow errors are E-NUMBER.

Try `learn -lesson power` / `logarithm`. REPL/learn support arrow editing/history, Tab completion, and Ctrl-R search; see [keyboard help](../README.en.md#interactive-computation-and-learning).

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
| [12_carry](structures/12_carry.lip) | Value iteration, next/break, exhaustion | early=6, complete=6 |

Ordinary-loop bindings are independent each iteration. Use carry for previous results and Claims for independent contributions. Ordinary bare break and carry break value target their respective constructs.

## Advanced computation and concurrency

| File | Topics | Invocation/result |
|---|---|---|
| [13_tail_calls](advanced/13_tail_calls.lip) | 100,000 direct/mutual/indirect tail calls | sum=5000050000, even=true, indirect=0 |
| [14_parallel_claim](advanced/14_parallel_claim.lip) | parallel, logical positions, fact projections | -parallel 4; input-order results |
| [15_inputs](advanced/15_inputs.lip) | Typed parameters/JSON | -input below |
| [16_snapshots](advanced/16_snapshots.lip) | Live versus complete observations | Complete count 3, live count 0–3 |
| [21_business_failures](advanced/21_business_failures.lip) | Failures as Data, stop on third failure | failures=3, visited=4 |
| [27_linear_model](advanced/27_linear_model.lip) | Centered least squares, training/prediction Flows, MSE, singular input | -entry Main; slope=2, intercept=1, predictions=[9,11] |
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
./lipc run -entry Main -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip
# {"count":3,"values":[5,3,4]}
./lipc run -entry Adjust -entry-input '{"positional":[3],"named":{}}' -entry-output examples/advanced/26_flows.lip
# {"kind":"value","value":5}
```

`x |> Adjust` fixes x before fully completing the child; named pure fn/core functions also work as stages. Explicit Flow arguments must be Pure Data; use prior bindings/pipelines for child results. Native/function values use ordinary calls. OFFSET:number=2 is a static literal; export exposes it. Duplicate JSON keys, invalid Unicode/shape/arguments fail pre-run E-SCHEMA.

Save reuse.lip at the project root:

```lip
import lip "./examples/advanced/26_flows.lip" as adjust
flow Main(xs:list)->list {return [x |> adjust.Adjust for x in xs]}
```

`./lipc run -input '{"xs":[3,1,2]}' reuse.lip` yields [5,3,4]. Only exports are accessible; private add is not. Compilation freezes complete dependencies/versions; programs never reread modules. -project selects the project root. Every declaration must be valid, regardless of selected entry.

Practice with entries/flows/modules lessons; dependencies are bundled. In REPL use `:run -entry Main -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip`; file execution is independent of session bindings.

## Go / Python ecosystems

These examples explicitly enable teaching contracts with -host demo. Python examples need Python 3 without extra packages.

| File | Topics | Result |
|---|---|---|
| [17_go_python](ecosystem/17_go_python.lip) | Both ecosystems in one Flow | Python mean 2, Go SHA-256 string |
| [18_proposals](ecosystem/18_proposals.lip) | Bound/executed requests/chains | first="HELLO", second="HELLO, LIP" |
| [19_parallel_read_write](ecosystem/19_parallel_read_write.lip) | Parallel reads, complete collection, sequential writes | ["ADA","LIN","SAM"]; stderr logs |
| [20_recorded_iteration](ecosystem/20_recorded_iteration.lip) | Prior observation in next request | rounds=4, score=0.8 |

```sh
./lipc run -host demo examples/ecosystem/17_go_python.lip
./lipc run -host demo -parallel 4 examples/ecosystem/19_parallel_read_write.lip
./lipc run -host demo examples/ecosystem/20_recorded_iteration.lip
```

Teaching contracts/schemas/byte encoding live in DemoHost in [adapters.go](../script/adapters.go). The default CLI generic bridge also supports direct imported calls/properties/native objects, e.g. `import go "strings"; strings.ToUpper("hello")` or `import python "math"; math.sqrt(3)`. Calls/properties lower to perform effect. Arrays/native objects retain references; native_data(value) explicitly snapshots Data. See [configuration/conversions/prerequisites](../README.en.md#connecting-go--python).

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
