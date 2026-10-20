# LIP 1.2

[English](README.en.md) | [简体中文](README.md)

LIP separates two jobs that are often tangled in workflow code: `fn` computes an immutable
value, while `flow` organizes work as a dependency graph. Three boundaries complete the model:
`effect` builds a request, `perform` executes it, and `claim` gathers facts with their sources.

```text
input → pure preparation → Flow graph → parallel tool calls → Claim evidence → pure decision → output
```

The core stays small. Go and Python provide databases, networks, models, and scientific
libraries; LIP describes how their work depends, which reads may overlap, when a result is
complete, and where each fact came from.

[Language specification](spec/LIP-1.2-SPEC.en.md) · [Examples](examples/README.en.md) ·
[Implementation notes](IMPLEMENTATION.en.md)

## A complete LLM agent prototype in a few lines

An agent usually needs four moves: ask the model for the next step, call a tool, put the
observation back into context, and stop when the task is done. LIP values, Flows, Claims,
requests, and carried state form that system directly.

The Host below is assumed to register `agent.chat` and `agent.search`; the LIP source does not
depend on an SDK or a provider implementation.

```lip
import host "agent" as ai

fn ask(prompt:string)->effect { effect ai.chat(prompt) }
fn search(query:string)->effect { effect ai.search(query) }

flow Research(question:string)->object {
    evidence = claim()
    for source in ["docs", "papers", "news"] parallel {
        result = perform search(source + ": " + question)
        evidence <+ {source: source, result: result}
    }

    context = [row.value for row in evidence]
    draft = perform ask("Answer this question from the evidence: " + str(context))
    return {question: question, context: context, draft: draft}
}

flow Review(input:object)->object {
    # state is this round's immutable snapshot. next takes the complete
    # snapshot for the following round; break returns the loop value.
    answer = for with state = {text: input.draft, round: 0} {
        review = perform ask("Review this answer and say DONE or REVISE: " + state.text)
        next_state = if review == "DONE" {
            {text: state.text, round: state.round + 1}
        } else {
            revised = perform ask("Rewrite this answer using the review: " + review + ": " + state.text)
            {text: revised, round: state.round + 1}
        }
        if review == "DONE" || state.round >= 2 { break next_state }
        else { next next_state }
    }
    return {question: input.question, answer: answer.text, evidence: input.context}
}

flow Main(question:string)->object { return question |> Research |> Review }
```

The agent is split into two Flows: `Research` runs the parallel searches and creates a draft,
while `Review` receives its complete result and runs the carried review loop. `Main` connects
them with `question |> Research |> Review`; the pipeline waits for each stage to complete before
starting the next one.

`for with state = initial` is a carried-value expression. The first round sees `initial`,
`next e` makes the value of `e` the next round's `state`, and `break e` returns the value of `e`.
Each `state` binding is immutable, so this is an explicit `stateₙ → stateₙ₊₁` machine: `next`
means “continue with this state” and `break` means “finish with this value”; both take a value
expression directly, rather than assigning to `state`. Without a source, it keeps transitioning
until a branch breaks.

The three searches can overlap. Reading `evidence` waits for all selected producers and returns
a complete, position-ordered snapshot. Each `Review` round reads the previous immutable state.
The model, search service, and database remain Host capabilities; LIP describes their order,
concurrency, evidence, and stopping condition.

## Why the features form one system

**Values and functions** make data transformation composable. Immutable bindings and pure `fn`
support snapshots, recursion, map/fold, closures, functions returned as values, and requests
passed through ordinary code.

**Requests are values with one execution boundary.** `effect op(args)` never calls a provider.
It creates a request with a contract identity. Requests can live in lists and objects, closures,
function arguments, and Flow results. `perform` is where schema, authorization, resources, and
the provider call happen, so an agent can plan first and execute at an explicit point. When a
request needs an in-program review, `str(request)` returns a stable string summary containing its
operation, contract, and Data arguments. It is pure: it does not call the provider or expose a
CallID or runtime resource.

After obtaining an effect, a coder should review it in this order before keeping its `perform`:

1. Bind `request = effect ...`, then use `print(request)` / `help(request)` to inspect easy-to-scan
   labelled lines: the first is `operation: go.strings.ToUpper`, followed by `field: value` lines for
   the version, arguments, schemas, effect class, concurrency, capabilities, and resources.
2. Compare the summary with the Host contract. Go/Python package path and member are included in
   the operation; package documentation remains available through Host/REPL help tools.
3. Let a pure review rule read the string, and put `perform request` only in the approved branch;
   the rejected branch makes no provider call.
4. Check the `perform` node and its prerequisites. Only a known request declared Concurrent
   ReadOnly by its contract belongs in `parallel`.

This is a coder/program review inside the Flow: the request can be passed to fn or Flow values,
and the summary string can be passed as ordinary Data. It is not a cross-Flow human approval
message channel. If a product needs runtime human approval, its Host must provide that protocol
outside this language boundary.

The smallest review-to-execution shape is:

```lip
import go "strings" as text

fn upper_request(value:string)->effect { effect text.ToUpper(value) }

flow Main()->object {
    request = upper_request("hello")
    # The coder inspects the labelled lines, then chooses whether to set this gate true.
    print(request)
    coder_approved = true
    decision = if coder_approved {
        result = perform request
        {approved: true, result: result}
    } else {
        {approved: false, result: null}
    }
    return decision
}
```

The example keeps the review decision in `coder_approved`: print the labelled summary, check the operation,
arguments, version, schemas, effect, and permissions, then set it to `true`; set it to `false` to
reject the request. A stricter application can save the printed summary as an audit record and
compare the string to bind approval to this request; the full canonical literal is better suited to
tests or generated configuration than hand-written business code. `str(effect)` is not field
projection and does not make effects comparable; it is a reviewable, passable string. The complete Go/Python example is
[18_proposals](examples/ecosystem/18_proposals.lip); it also covers passing requests through
functions/Flows, parallel read-only calls, and separate sequential writes.

The labelled display also splits nested leaves into searchable paths, so a coder can locate any key
and read the value after its colon directly:

```text
operation: go.strings.ToUpper
operation.language: "go"
operation.path: "strings"
operation.member: "ToUpper"
kind: "effect"
contract.version: "1"
contract.effect: "ReadOnly"
contract.concurrent: true
contract.parameters[0].name: "text"
contract.parameters[0].schema.type: "string"
contract.result.type: "string"
arguments.positional[0]: "hello"
arguments.named: {}
```

These paths are human-facing diagnostics. Use `str(request)` when the complete, lossless summary must
be saved or compared as an exact string.

For strict string matching, keep the review record in generated configuration or an audit file; the
LIP code compares the complete `str(request)` string:

```lip
summary = str(request)
print(request)
approved = summary == REVIEWED_SUMMARY  # generated by the review tool
result = if approved { perform request } else { null }
```

The complete stable output of `str(request)` can look like this. It is useful for logs, audit records,
and test snapshots; ordinary code usually keeps the shorter labelled print-and-gate form above:

```text
{"arguments":{"named":{},"positional":["hello"]},"contract":{"capabilities":[],"concurrent":true,"effect":"ReadOnly","parameters":[{"name":"text","optional":false,"schema":{"type":"string"}}],"resources":{},"result":{"type":"string"},"version":"1"},"kind":"effect","operation":{"language":"go","member":"ToUpper","path":"strings"}}
```

The review decision can also come from entry input. Entry input is ordinary Data; convert it to
characters before choosing whether to release the request:

```lip
import go "strings" as text
fn upper_request(value:string)->effect { effect text.ToUpper(value) }

flow ReviewFromInput(input:object)->object {
    request = upper_request(input.text)
    print(request)
    decision = to_chars(input.approve)
    approved = if len(decision) == 0 { false } else { decision[0] == 'y' }
    result = if approved { perform request } else { null }
    return {approved: approved, result: result}
}
```

With `-entry ReviewFromInput -entry-input '{"positional":[{"text":"hello","approve":"y"}],"named":{}}'`, the labelled summary is printed
before the request runs; changing `approve` to `"n"` returns the rejection result. Real terminal input,
human approval, and messages between Flows require a Host operation and protocol.

**Finite descriptions keep wrappers analyzable.** LIP 1.2 preserves request and callable sources
through fixed fields, literal indexes and slices, `put`, function parameters and returns,
closures, and static Flow results. `identity(p:effect)->effect {p}`, a request in a container,
or several pure helper functions do not turn a known read into a black box. A Write,
non-concurrent read, or unknown candidate makes `parallel` fail at compile time.

**Flows are dependency graphs.** Sequential statements create prerequisites, independent nodes
can overlap, and a child Flow publishes only after its selected work completes. `|>` passes one
completed value into the next stage; a later stage never starts from an early partial result.

**Claims are evidence boundaries.** `<+` appends a fact with a JoinID. Replaying the same
identity and value is idempotent; equal values from different identities remain separate
contributions. A complete read waits for producers, while a live snapshot observes one
linearization point. Model answers, tool results, and checks can all become facts before pure
functions score them and decide what to publish.

**Host contracts are the external boundary.** A contract declares parameter and result schemas,
version, effect, concurrency, capabilities, and resources. Go/Python adapters, the CLI, the
REPL, and generated programs share that boundary. Building a request is side-effect free; an
action that already happened is never hidden by a retry.

**Diagnostics and identity make results reviewable.** Node positions, call paths, Claim sources,
and provider records use structural identity instead of completion order. `print`, `log`, and
`telemetry` are one-way diagnostics; business actions remain `effect/perform`.


## Run and build

```sh
make build
./lipc run examples/basics/01_hello.lip
./lipc run -parallel 4 examples/evidence.lip
./lipc repl
./lipc learn -list
```

Generate a standalone Go program:

```sh
./lipc emit-go -o agent.go examples/advanced/26_flows.lip
./lipc build -o agent examples/advanced/26_flows.lip
./agent -input '{"xs":[3,1,2]}'
```

Go 1.25 or newer is required. `-host demo` supplies teaching contracts; applications register
their own Go/Python operations through `Host.Contracts`. The REPL keeps successful immutable
values and closures across cells, and `lipc learn` uses the same editor to teach values,
functions, Flows, Claims, and Hosts.

## Explore

- [Basics](examples/basics/01_hello.lip): bindings, arithmetic, containers, and strings.
- [Structures](examples/structures/12_carry.lip): conditions, functions, map/fold, and carried
  state.
- [Advanced](examples/advanced/14_parallel_claim.lip): parallel Claims, inputs, tail calls, and
  diagnostics.
- [Ecosystem](examples/ecosystem/18_proposals.lip): displaying searchable `print(request)` lines, gating effect execution in an `if`, Go/Python requests, ordered writes, and
  recorded iteration.
- [Example guide](examples/README.en.md): run the complete set of `.lip` programs by concept.

```sh
go install ./cmd/lipc
lipc help
lipc help serve
lipc serve help
```

When a command is unclear, run `lipc` for the command overview. Every subcommand accepts
`help`, `-h`, and `--help`; for example, `lipc run -h` describes input encoding, entry Flows,
and execution budgets, while `lipc serve -h` explains single-file routes and the `-routes`
manifest format.

The complete syntax, value encoding, call boundary, scheduler, Claims, Host rules, and
determinism guarantees are in the [LIP 1.2 specification](spec/LIP-1.2-SPEC.en.md).

## Language core and program structure

The complete syntax, values, Flows, Claims, and entry protocol are in the
[LIP 1.2 specification](spec/LIP-1.2-SPEC.en.md). Most programs follow the same shape: prepare
values, build requests, schedule a Flow, gather Claims, and make a pure decision.

## Interactive computation and learning

`lipc repl` and `lipc learn` share the same editor and runtime. Use the REPL to try values and
requests, then submit multiline Flows with `:begin`/`:end`; lessons break the same composition
into runnable steps.

## Why does fmt.Print show a number

Go return values cross the Host contract and are displayed by the REPL; use `print`, `log`, or
`telemetry` for one-way output.

## Connecting Go / Python

Applications register external capabilities through `Host.Contracts` and adapters; LIP source
depends on the operation contract. Contracts describe schemas, permissions, resources, effects,
and concurrency, so a model or tool implementation can change without changing the Flow.

## Every external action follows one protocol

`effect` constructs a request and `perform` executes it. Go, Python, HTTP, and custom Hosts use
the same boundary; construction composes freely, while execution applies schema, authorization,
resource, and provider checks. For review, usually print the request first; its labelled lines make
the operation, contract, and Data arguments easy to locate without calling the provider. For an exact
machine-bound review, bind `summary = str(request)` and put the release point in a Flow with
`if summary == reviewed_summary {perform request} else {null}`; effects still have no field projection or equality.

## Websites and HTTP integration

An HTTP handler maps request data to an entry Flow and external services to Host operations;
business steps remain Flows, and the page or JSON response is simply the Flow result.
