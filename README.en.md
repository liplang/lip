# LIP 1.2

[English](README.en.md) | [简体中文](README.md)

LIP separates two jobs that are often tangled in workflow code: `fn` computes an immutable value, while `flow` organizes work as a dependency graph. Three boundaries complete the model: `effect` builds a request, `perform` executes it, and `claim` gathers facts with their sources.

```text
input → pure preparation → Flow graph → parallel tool calls → Claim evidence → pure decision → output
```

The core stays small. Go and Python provide databases, networks, models, and scientific libraries; LIP describes how their work depends, which reads may overlap, when a result is complete, and where each fact came from.

[Language specification](spec/LIP-1.2-SPEC.en.md) · [Examples](examples/README.en.md) · [Implementation notes](IMPLEMENTATION.en.md)

## A complete LLM agent prototype in a few lines

An agent usually needs four moves: ask the model for the next step, call a tool, put the observation back into context, and stop when the task is done. LIP values, Flows, Claims, requests, and carried state form that system directly.

The Host below is assumed to register `agent.chat` and `agent.search`; the LIP source does not depend on an SDK or a provider implementation.

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

The agent is split into two Flows: `Research` runs the parallel searches and creates a draft, while `Review` receives its complete result and runs the carried review loop. `Main` connects them with `question |> Research |> Review`; the pipeline waits for each stage to complete before starting the next one.

`for with state = initial` is a carried-value expression. The first round sees `initial`,
`next e` makes the value of `e` the next round's `state`, and `break e` returns the value of `e`.
Each `state` binding is immutable, so this is an explicit `stateₙ → stateₙ₊₁` machine:
`next` means “continue with this state” and `break` means “finish with this value”; both take a
value expression directly, rather than assigning to `state`. Without a source, it keeps
transitioning until a branch breaks.

The three searches can overlap. Reading `evidence` waits for all selected producers and returns a complete, position-ordered snapshot. Each `Review` round reads the previous immutable state. The model, search service, and database remain Host capabilities; LIP describes their order, concurrency, evidence, and stopping condition.

## Why the features form one system

**Values and functions** make data transformation composable. Immutable bindings and pure `fn` support snapshots, recursion, map/fold, closures, functions returned as values, and requests passed through ordinary code.

**Requests are values with one execution boundary.** `effect op(args)` never calls a provider. It creates a request with a contract identity. Requests can live in lists and objects, closures, function arguments, and Flow results. `perform` is where schema, authorization, resources, and the provider call happen, so an agent can plan first and execute at an explicit point.

**Finite descriptions keep wrappers analyzable.** LIP 1.2 preserves request and callable sources through fixed fields, literal indexes and slices, `put`, function parameters and returns, closures, and static Flow results. `identity(p:effect)->effect {p}`, a request in a container, or several pure helper functions do not turn a known read into a black box. A Write, non-concurrent read, or unknown candidate makes `parallel` fail at compile time.

**Flows are dependency graphs.** Sequential statements create prerequisites, independent nodes can overlap, and a child Flow publishes only after its selected work completes. `|>` passes one completed value into the next stage; a later stage never starts from an early partial result.

**Claims are evidence boundaries.** `<+` appends a fact with a JoinID. Replaying the same identity and value is idempotent; equal values from different identities remain separate contributions. A complete read waits for producers, while a live snapshot observes one linearization point. Model answers, tool results, and checks can all become facts before pure functions score them and decide what to publish.

**Host contracts are the external boundary.** A contract declares parameter and result schemas, version, effect, concurrency, capabilities, and resources. Go/Python adapters, the CLI, the REPL, and generated programs share that boundary. Building a request is side-effect free; an action that already happened is never hidden by a retry.

**Diagnostics and identity make results reviewable.** Node positions, call paths, Claim sources, and provider records use structural identity instead of completion order. `print`, `log`, and `telemetry` are one-way diagnostics; business actions remain `effect/perform`.

## A minimal parallel program

```lip
import python "example.worker" as worker

fn read_request(x:number)->effect { effect worker.read(x) }
fn identity(p:effect)->effect { p }

flow Main()->list {
    facts = claim()
    for x in [1, 2, 3] parallel {
        boxed = {request: identity(read_request(x))}
        value = perform boxed.request
        facts <+ value
    }
    return [row.value for row in facts]
}
```

The request keeps its `worker.read` source through the function and object field, so the three read-only actions can overlap. The complete Claim read still returns `[1,2,3]` in input order.

The same rule applies to known closed containers, conditional/returned functions, and recursive parameter forwarding. No new syntax is needed: the static summary substitutes a known request source at the call site. Open containers, dynamic callables, Writes, and nonconcurrent reads remain ineligible when their source cannot be proven. See [`21_summary_recovery`](examples/ecosystem/21_summary_recovery.lip) for a complete example.

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

Go 1.25 or newer is required. `-host demo` supplies teaching contracts; applications register their own Go/Python operations through `Host.Contracts`. The REPL keeps successful immutable values and closures across cells, and `lipc learn` uses the same editor to teach values, functions, Flows, Claims, and Hosts.

## Explore

- [Basics](examples/basics/01_hello.lip): bindings, arithmetic, containers, and strings.
- [Structures](examples/structures/12_carry.lip): conditions, functions, map/fold, and carried state.
- [Advanced](examples/advanced/14_parallel_claim.lip): parallel Claims, inputs, tail calls, and diagnostics.
- [Ecosystem](examples/ecosystem/18_proposals.lip): Go/Python requests, ordered writes, and recorded iteration.
- [Example guide](examples/README.en.md): run the complete set of `.lip` programs by concept.

```sh
go install ./cmd/lipc
lipc help
```

The complete syntax, value encoding, call boundary, scheduler, Claims, Host rules, and determinism guarantees are in the [LIP 1.2 specification](spec/LIP-1.2-SPEC.en.md).

## Language core and program structure

The complete syntax, values, Flows, Claims, and entry protocol are in the [LIP 1.2 specification](spec/LIP-1.2-SPEC.en.md). Most programs follow the same shape: prepare values, build requests, schedule a Flow, gather Claims, and make a pure decision.

## Interactive computation and learning

`lipc repl` and `lipc learn` share the same editor and runtime. Use the REPL to try values and requests, then submit multiline Flows with `:begin`/`:end`; lessons break the same composition into runnable steps.

## Why does fmt.Print show a number

Go return values cross the Host contract and are displayed by the REPL; use `print`, `log`, or `telemetry` for one-way output.

## Connecting Go / Python

Applications register external capabilities through `Host.Contracts` and adapters; LIP source depends on the operation contract. Contracts describe schemas, permissions, resources, effects, and concurrency, so a model or tool implementation can change without changing the Flow.

## Every external action follows one protocol

`effect` constructs a request and `perform` executes it. Go, Python, HTTP, and custom Hosts use the same boundary; construction composes freely, while execution applies schema, authorization, resource, and provider checks.

## Websites and HTTP integration

An HTTP handler maps request data to an entry Flow and external services to Host operations; business steps remain Flows, and the page or JSON response is simply the Flow result.
