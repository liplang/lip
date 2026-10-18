# LIP development guide

[English](DEVELOPMENT.en.md) | [简体中文](DEVELOPMENT.md)

Project guidance accompanying the [development skill](.agents/skills/lip-development/SKILL.md).

Use this guide when developing/reviewing LIP 1.2 draft specifications, compiler, Go backend, Go/Python Host boundary, examples, or interactive tools. Locate the user-selected root containing `spec/LIP-1.2-SPEC.md`, `script/`, and `go.mod`. If it cannot be written in a restricted environment, use an already authorized local copy. Paths below are relative to that root.

## Design basis

The [LIP 1.2 draft specification](spec/LIP-1.2-SPEC.en.md), also available in [Chinese](spec/LIP-1.2-SPEC.md), is the complete independent language design authority. Current code retains the [1.1 specification](spec/LIP-1.1-SPEC.en.md) implementation baseline; new requirements are not yet accepted. Distinguish implementation gaps from target rules. Read relevant sections before tracing implementation. Keep the specification self-contained: define symbols, states, identities, and errors before use. Integrate accepted findings without review priority labels, issue numbers, or conversational names. Explain concrete reasons for rejected design suggestions in delivery notes.

Prefer implementation fixes unless the task concerns the specification or reveals a contradiction. Distinguish specification review from implementation review and report only checks actually run.

The [implementation guide](IMPLEMENTATION.en.md#v12-implementation-sequence) owns the v1.2 phase order and acceptance conditions: descriptions/contracts first, then container/call transfer, recursion/sessions, integrated contracts/diagnostics/adaptation, and cross-path acceptance. Do not upgrade conformance claims before phase acceptance. Boundary/order regressions follow §16.8–16.9, including partial candidate evidence, cache context, and stable selection among multiple failures.

Keep the core small: immutable values, pure functions, control flow, graph dependencies, append-only Claims, effect/perform. Compose ordinary data operations from existing pure operations, obtaining most libraries through Go/Python. Avoid business-reducer syntax, arbitrary mutable lattices, automatic subscriptions, parallel writes, threshold waits, and unnecessary files.

## Implementation boundaries

- `script/compiler.go` and `checker.go` own syntax/static guarantees. Hidden function/any signatures require dynamic checking, not omitted checks.
- `values.go` defines shared Data, numbers, encoding, identities, and diagnostics. any? equals any; paths consistently treat nullable types, nonfinite values, duplicate fields, and unsupported native values.
- In 1.1, `@{...}` promises purity only for native calls directly in that body; nested definitions need their own markers and ordinary wrappers may call marked functions. Every language fn activation needs a distinct call identity. Keep NaN language equality separate from canonical identity; signed infinities are valid numbers; division by zero, overflow, and power/log boundaries produce canonical NaN or signed infinity. c-hex encodes each entry argument individually; protocol metadata stays JSON.
- `**` associates right, precedes left negation, and accepts signed exponents. `*/` uses its right operand as base, shares multiplication precedence, and associates left. Follow §16.5's fixed algorithm rather than platform pow/log, synchronizing reference code, errors, and interpreted/generated vectors.
- `evaluator.go`/`scheduler.go` own pure functions, closures, proper tail calls, carry, gates, barriers, primary errors, and Claims. Unselected branches produce no dynamic errors/calls; preserve node positions/evaluation paths.
- `host.go`/`adapters.go` are the only external boundary. Proposal construction calls no provider; validate arguments/authorization/resources before actions. Distinguish known completion with failed validation from Uncertain; retries cannot hide writes.
- The 1.2 target uses finite descriptions under §§5.1–5.3 for containers, abstract parameter-to-return transfer, and known callable origins, with BaseOps fixed points and Active reentry fallback. Keep RetInfo separate from execution Summary, without specializing child execution effects or hiding argument effects. Unjustified sources remain Unknown; private optimizations cannot admit parallel work. Existing projection/forwarding restrictions remain documented implementation gaps, not claims of target support.
- Native result provenance stays separate from Ops. Symbolic parameters and projections track actual returned positions, without merging every input into the result or retaining AST/business values. Process Flows by dependency and preserve summaries across session cells. Process request builders by dependency, use existing return summaries to complete recursive request results, then refresh enclosing closure/container return shapes without lowering or packing proposals twice. Solve ordinary recursive parameter provenance simultaneously within call-graph components, using non-recursive return structures as a finite domain and propagating parameter identities to stability rather than a fixed refresh count. Growing containers/projections and recursive higher-order calls may remain unknown and use explicit receiver requests. Never flatten deep provenance into the root and taint adjacent pure fields. Cross modules, recursion, closures, and pipelines in regressions; reverse declarations and rename functions. Ordinary Host Data field reads must remain pure.
- An effect is an ordinary value outside Data: fn parameters/results, containers, and immutable captures may carry it. Same-run Flow inputs/results may carry any ordinary values; entry protocols, Claim contributions, and Host arguments/results still require recursive Data. Flow definitions remain non-values. Actual operation schemas determine perform result shapes. Projects define request builders and register contracts, without introducing effect<T> or custom source types.
- `backend.go` emits actual Go evaluation, never invokes an interpreter over bundled source. When adding helpers, check embedded-declaration filtering for missing generated symbols.
- Generated static metadata initializes once and remains read-only. Concurrent Program construction/runs cannot rewrite active ASTs; native fixtures also use race detection.
- `session.go` retains successful immutable values, closures, and checker-produced Ops/RetOps with the same checker/graph engine. Never strengthen Unknown from saved runtime function/proposal identities or rerun history. Failed cells commit no bindings/summaries, but performed actions remain. Deleting/reusing names does not retarget old captures; complete Claim programs have explicit run lifecycles.
- REPL/learn share `cmd/lipc/editor.go`. Raw mode covers input only and restores before evaluation/provider calls, including cancel/EOF/errors. No background terminal prefetch. Verify shortcuts, Unicode, and bracketed paste with real PTYs; pipes remain plain text. Completion executes no source/native calls.

Keep §3.2 diagnostics separate from business effects: fn permits core print/log/telemetry while results, errors, and business control choices remain independent of the receiver. This grants no arbitrary native calls. The new core names cannot be redefined; older custom bindings need renaming. `Options.Trace=nil` disables delivery by default. To receive, create a bounded `NewTraceBuffer(capacity)`, consume `Events()`, and `Close()` after all shared runs finish; `Dropped()` reports losses to the embedding application. Receivers provide no LIP-readable acknowledgement or feedback. Events retain only bounded text/positions, with fn/effect/native values opaque. Arguments still evaluate and merge effects normally. Keep stderr and `-trace=false` behavior consistent across run, repl, learn, serve, and generated programs. Receiver configuration cannot alter Steps, journal, RetOps, or concurrency permission.

## Call consistency

Ordinary dynamic calls evaluate callee → validate callable/actual arity/names → evaluate arguments in source order → check parameter types → execute body → validate result. Arity errors precede argument performs; parameter errors precede bodies. map/fold callbacks follow the same rules.

Flow arguments and pipeline extra arguments use the caller cursor and allow nested calls; include all argument effects in node summaries. perform first fully evaluates its request expression, then executes the request; combine both effects. Flow return descriptions and execution effects are separate. The 1.2 return positions transfer abstract arguments, while execution Summary remains conservative in the declaration environment.

Cover direct functions, aliases, anonymous/container-extracted functions, closures, higher-order callbacks, and named arguments. Reject statically known errors early, including callback arity on empty lists. Interpreter/generated dynamic errors must agree.

Claims append identified facts; complete reads wait for every producer, while live snapshots are individual external observations. Positions use decimal-string wire pairs with numeric sorting. Compare full canonical identities; digests are indexes only. Conflicts never use first-arrival-wins.

## Documentation and layout

Use English as the primary language for normal prompts/help/diagnostics/lessons/example comments, optionally supplemented by Chinese. Prefer English when space is limited. Give every Chinese document a linked English version; synchronize contracts, examples, section numbers, and navigation. Keep public documentation in linked Chinese and English editions, and code, comments, and normal interactive text in English.

When static acceptance changes, synchronize both specifications' main rules, module/pipeline rules, appendix vectors, and relevant guides, help, lessons, and examples. Keep bilingual example code blocks in corresponding sections and the same order: documentation consistency checks compare them sequentially.

Explain what graph concurrency, Claims, and effects solve before usage. Explain ReAct as think → call a tool → observe → decide again. Separate within-run graph dependencies from Host-managed long-lived events. State the small-core/Go-Python scope and unimplemented optional durable recovery accurately.

`examples/` contains commented executable `.lip` programs progressing from Hello World/bindings/arithmetic/branches/functions to carry/Claims/external work. Teaching registrations belong in `script/adapters.go`, explicitly enabled by DemoHost. `backend.go` derives DemoHostGoSource from the same registration function. Tests live in `tests/`. Group code by semantics; new files should own complete responsibilities rather than tiny helpers.

During example refactoring, use pure fn for single-step request construction and child Flows for multiple external steps; Flow targets stay static. Compare both entry-file size and total implementation size. Avoid mechanically wrapping one-off calls or shortening code by deleting validation, resource cleanup, or terminal/streaming behavior.

Keep each learn blank focused on one idea. Show concrete examples and input/output before increasing composition difficulty; execute examples and reference answers. Reveal hints gradually, exclude demos from completion, and retain failed answers for revision. Store progress by stable lesson IDs so inserted lessons preserve earlier completion marks. Check editor changes through the REPL, learning mode, real PTYs, and pipes; completion must not execute examples or external code.

Retain 1.0 and 1.1 as historical/implementation-baseline specifications. Design follows the independent complete 1.2 editions without treating target rules as implemented capabilities or changing the engineering release version. Explain semantic reasons for constraints rather than presenting accidental analyzer limits as architectural benefits. Regressions cover wrapping, modules, names, declaration order, and interpreter/generated/REPL agreement. If the skill directory is read-only, record experience here without bypassing permissions.

Minimum Go is 1.25; allow higher versions without pinning patch/toolchain. Adjust versions according to user requirements and real dependencies.

Explicit native receiver requests fix only ordinary wrapper fields along the actual member path, keeping unrelated fn/effect fields outside the Host Data boundary. Native lookup remains deferred to perform and actual arguments retain Data validation. `NativeExplicitReceiverMixedContainers` covers request builders, mixed containers across children, dynamic aliases, unselected missing paths, original-object preservation, and invalid arguments through interpretation, generated programs, and sessions. The chat Agent’s OpenModel return object supplies the application regression.

## Validation and delivery

For a requested deep composition review, use the repository's [composition review skill](skills/lip-composition-review/SKILL.md). Cross-check calls, conditions, recursion, concurrency, lifetimes and execution paths against concrete outputs, error attribution, call counts and ordering. It records the standing request to finish the semantic checks without treating elapsed time as evidence of completion; it remains readable when the environment prevents updates under `.agents/skills`.

Add regressions for real semantic risks rather than restating implementation. Useful checks include numeric/encoding vectors; terminal if/match/value blocks and malformed arguments/source; actual generated binaries/diagnostic locations; legal schedule variations, closed-gate zero calls, prerequisite closure/barriers/Claims; identical Go/Python Data boundaries, pre-authorization zero calls, known-fault replay without reruns; REPL multiline/error recovery/closures/non-repeated historical actions; every lesson with multiple inputs.

Request-summary regressions also prove actual provider-read overlap and surrounding Write barriers in ordinary graphs/parallel loops, plus imported child-Flow composition, rather than checking compilation alone.

Run `make test`, `make race`, `make vet`. race also builds/runs generated binaries with race detection. Makefile caches/temp files stay in `.build` for restricted workspaces. After validation run `make clean`; deliver no lipc, `.build`, Python caches, or other generated products. Restrict cleanup to project products. Report changes, actual checks, and limitations without claiming optional profiles are implemented.
