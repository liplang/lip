# LIP 1.2

[English](README.en.md) | [简体中文](README.md)

**The assumption of sequential execution is suspect.**

Most programs list their steps one line after another, but much of that order is
just how the code happened to be written, not something the steps actually need.
Write three unrelated lookups one after another, and you wait twice for nothing.

LIP doesn't assume that order. **The LIP runtime arranges parallel execution automatically.**

Automatic parallelism needs two supporting mechanisms:

| Mechanism | What it solves |
|-----------|----------------|
| **`effect` + `perform`**: running requests | `effect` only builds a request and doesn't run it; `perform` is the only way to run one. Whether each action may run is decided later, at that point. |
| **`claim` + `fold`**: collecting information | Results produced in parallel go into a Claim with `<+` and are ordered by structural identity, not by which finished first. |

Pure computation goes in `fn`. Go and Python provide outside capabilities such as
databases, networking and models. LIP only describes how they relate: their
dependencies, concurrency, evidence and stopping conditions.

[Specification](spec/LIP-1.2-SPEC.md) · [Examples](examples/README.md) · [Implementation & Verification](IMPLEMENTATION.md)

## One example

Suppose the Host registers two operations, `agent.search` and `agent.chat`, and
both contracts are declared Concurrent ReadOnly:

```lip
import host "agent" as ai

fn ask(prompt:string)->effect { effect ai.chat(prompt) }
fn search(query:string)->effect { effect ai.search(query) }

flow Research(question:string)->object {
    evidence = claim()

    # Independent sources are searched at the same time; each result has its own
    # structural identity and source position.
    for source in ["docs", "papers", "news"] parallel {
        result = perform search(source + ": " + question)
        evidence <+ {source: source, result: result}
    }

    context = [row.value for row in evidence]
    draft = perform ask("Answer this question from the evidence: " + str(context))

    return {question: question, context: context, draft: draft}
}

flow Review(input:object)->object {
    # state is this round's immutable snapshot. next is followed by the complete
    # state for the next round; break is followed by the loop's return value.
    # They are control exits, not assignments to state.
    # A carried-value loop is an agent's plan → act → observe → decide.
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

There are no locks, no callbacks and no manual joins.

Replace `agent.search` with a database query or a Python retriever, and the
program structure stays the same.

### Flow: parallelism scheduled automatically
### effect + perform: review a request before it runs
### claim + fold: deterministic collection of parallel results

## Quick start

Requires Go 1.25 or later.

```sh
make build
./lipc run examples/basics/01_hello.lip
./lipc run -parallel 4 examples/evidence.lip
./lipc repl            # interactive; only successful cells are kept, failed cells don't overwrite existing bindings
./lipc learn -list     # step-by-step lessons: values → functions → Flow → Claim → Host
```

Build a standalone Go program:

```sh
./lipc build -o agent examples/advanced/26_flows.lip
./agent -input '{"xs":[3,1,2]}'
```

`-host demo` provides teaching contracts. Real applications register their own
Go/Python operations through `Host.Contracts`. Run `lipc` or `lipc <command> -h`
for command help. In the REPL, a Go function's return value is shown according to
its contract; use `print` when you want to show output.

## Contents

- [Basics](examples/basics/01_hello.lip): bindings, arithmetic, containers, strings
- [Structures](examples/structures/12_carry.lip): conditionals, functions, map/fold, carried-value loops
- [Parallel & Claim](examples/advanced/14_parallel_claim.lip): parallel collection, input, diagnostics
- [Requests & ecosystem](examples/ecosystem/18_proposals.lip): reviewing and approving requests, Go/Python calls, sequential writes
- [All examples](examples/README.md) · [LIP 1.2 Specification](spec/LIP-1.2-SPEC.md)
