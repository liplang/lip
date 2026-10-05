# LIP Alpha 0.1 Specification

Status: normative executable specification for the first compiler/runtime
implementation.

Language version: `0.1` (Alpha). Reference implementation release:
`0.1.0`.

## 1. Purpose

LIP Alpha 0.1 validates one hypothesis:

> A small, readable language can describe computation dependencies while a Go
> runtime decides when each computation is allowed to execute.

Alpha 0.1 is an orchestration language, not a complete replacement for Go.
Algorithms, data structures, HTTP, databases and other domain code remain Go
operations exposed through a small host interface.

## 2. Compilation model

```text
.lip → lexer → parser → AST → resolver → dependency graph IR → Go source
      → gofmt → (optional go build)
```

The generated Go program uses the `runtime` package in this repository. Alpha
0.1 has no LIP-specific VM, garbage collector or LLVM backend; generated
programs use the ordinary Go toolchain and Go runtime. The front end checks
names, scopes and the types it can know statically; generated code and the
runtime check annotated inputs and every operator again. `lipc build` emits
gofmt-formatted Go source; compiling that source is a separate `go build`
step (or `go run`).
The reference implementation and module target Go 1.27 (`go 1.27`).

## 3. Source model

An Alpha 0.1 file contains zero or more expression functions followed by
exactly one `flow`:

```lip
flow Hello(request: string) {
    greeting = "Hello, " + request
    return greeting
}
```

```lip
fn twice(x: number) {
    return x * 2
}
```

The source order is used only as a deterministic tie-breaker. It is not a
general execution-order declaration.

### 3.1 Statements

The supported statements are:

```text
name = expression
call(...)
when identifier { statements }
return expression
```

`return` is the normal way to produce a Flow result. A Flow may contain more
than one return node in different gated paths; statements after a return in the
same block are rejected. At runtime its result is the value of the highest
source-order return node that completes; if every return node is skipped, the
result is `nil`. A standalone expression
statement is allowed only when it is a Host call, for example `print(value)`;
arbitrary unused expressions are rejected. `print(value)` is an ordinary Host
operation for console output. There is no separate `emit` keyword in Alpha 0.1;
event streams, if needed later, will get their own explicit design.

Bindings are single-assignment. Forward references, reassignment, loops and
cycles are compile-time errors.

`when` is an execution gate. If its condition is false, every node in the
block is `Skipped`. Bindings created inside a `when` block cannot be referenced
outside that block in Alpha 0.1.

An Alpha function has a single `return` expression. It is compiled as a Go
operation and can be called from a flow. Function bodies do not create graph
nodes. A function that only uses values and operators is pure; a function that
calls a Host operation is treated as effectful by the scheduler.

### 3.2 Expressions

Alpha 0.1 supports:

```text
identifiers
strings, integers, floating point numbers, booleans
function calls with simple expression arguments
list literals, field access (`value.name`) and indexing (`value[index]`)
binary operators: + - * / == != > >= < <= && ||
conditional value: if identifier then expression else expression
```

Operators have strict, unsurprising domains. `+`, `-`, `*` and `/` operate
on numbers; `+` also concatenates two strings, and `*` repeats a string by a
non-negative integer. A string is never silently converted to a number and a
number is never silently converted to a string. Use the explicit Host
operation `str(value)` when conversion is intended. Ordering comparisons are
numeric; `&&` and `||` require booleans. `==` and `!=` compare values without
string/number coercion (Go numeric representations are normalized as the one
Alpha `number` type). Equality between statically known incompatible types is
rejected; values whose type is `any` are checked by the runtime and compare
unequal when their concrete types differ.

Calls are opaque host operations unless the name refers to a local `fn`.
Nested calls remain outside the first expression subset; complex work belongs
in Go host code. Sequence, array and string indexes must be non-negative
integers; map indexes follow the Go reflection key rules for the Host value.

### 3.3 Grammar

The reference parser implements this compact grammar (whitespace and `#` or
`//` comments are ignored):

```ebnf
file        = { function } , flow , EOF ;
flow        = "flow" , identifier , [ "(" , [ parameters ] , ")" ] , block ;
declaration = function | flow ;
function    = "fn" , identifier , "(" , [ parameters ] , ")" , "{" , "return" , expression , "}" ;
parameters  = parameter , { "," , parameter } ;
parameter   = identifier , [ ":" , type ] ;
type        = "any" | "string" | "number" | "bool" ;
block       = "{" , { statement } , "}" ;
statement   = identifier , "=" , expression
            | call
            | "when" , expression , block
            | "return" , expression ;
call        = identifier , "(" , [ arguments ] , ")" ;
expression  = postfix , { binary_operator , postfix } ;
postfix     = atom , { "(" , [ arguments ] , ")"
                      | "." , identifier
                      | "[" , expression , "]" } ;
atom        = identifier
            | number
            | string
            | "true"
            | "false"
            | "if" , expression , "then" , expression , "else" , expression
            | "[" , [ arguments ] , "]"
            | "(" , expression , ")" ;
arguments   = expression , { "," , expression } ;
```

The semantic restrictions are intentionally stricter than the grammar: a
`when` condition must be an identifier (or literal `true`), call arguments
must be simple expressions, only a bare identifier can be called, and nested
calls are not supported. An omitted
parameter annotation means `any`; annotated flow inputs and local-function
arguments are checked at the generated Go boundary.

## 4. Dependency semantics

Each binding and `return` is a graph node. Every identifier used by an
expression creates a data-dependency edge.

```lip
a = foo()
b = bar()
c = combine(a, b)
```

means:

```text
foo ──┐
      ├──→ combine
bar ──┘
```

Fan-out and fan-in are derived from ordinary references. `pipe`, `fork` and
`merge` are not Alpha 0.1 keywords.

## 5. Runtime states

The runtime tracks each node as one of:

```text
Pending, Running, Completed, Error, Skipped
```

`Pending` is an execution state, not a user-visible union type. A host
operation may return a future channel; the scheduler waits for it and then
continues propagation. Alpha 0.1 has one execution tick per flow invocation;
persistent `state`, `prev`, feedback and external event ticks are deferred.

## 6. Scheduling

The reference semantic scheduler is deterministic and sequential:

1. A node is runnable when all data and gate dependencies are complete.
2. A false gate marks the node `Skipped`.
3. A host error marks the node `Error` and prevents dependent nodes from run-
   ning.
4. Runnable nodes are selected in source order.
5. A trace event is recorded for every observable execution transition;
   `Pending` is the initial state and is not emitted.

`Graph.RunParallel` is the bounded implementation extension. It may run
independent pure nodes concurrently up to a caller-provided limit. A host
operation is eligible only when registered with `Host.RegisterPure`; ordinary
`Host.Register` operations remain sequential and form side-effect barriers.
Successful sequential and parallel runs have the same dependency and gate
semantics; only the timing of independent pure work changes. Trace ordering,
and which independent failure is reported first, may differ under parallel
execution. Use `RunSequential` when reproducing an error in source order.

Generated programs expose `Run` as the automatic scheduler using
`DefaultParallelism()`, `RunSequential` for the reference order, and
`RunParallel` when the caller wants an explicit limit.

## 7. Host operations

The compiler does not inspect arbitrary Go signatures in Alpha 0.1. Go code
registers adapters with the runtime:

```go
type Op func(context.Context, []Value) Result
```

The default host contains only the explicit `str(value)` conversion and the
effectful `print(...)` operation. Arithmetic, comparison and boolean logic are
language operators, not Host calls. Applications can register adapters that
call any Go library. Direct arbitrary Go imports from LIP are intentionally
deferred until a `go/types`-based interop layer is designed.

`str` and `print` are ordinary Host operation names, not keywords or special
language syntax. They can be replaced or overridden by an application Host.
The old `identity`, `concat`, `add`, `mul`, `gt` and related names are not
part of Alpha 0.1: direct values and operators are clearer and leave the Host
namespace for domain operations.

For the same reason, the canonical forms are now:

| Old draft spelling | Alpha 0.1 spelling |
| --- | --- |
| `name = identity(request)` | `name = request` |
| `concat("Hello, ", name)` | `"Hello, " + name` |
| `gt(input, 0)` | `input > 0` |
| `mul(input, 2)` | `input * 2` |

Read-only or otherwise concurrency-safe adapters may use:

```go
host.RegisterPure("load_profile", adapter)
```

An adapter can return `Ready(value)`, `Failed(err)` or `Await(channel)`.

## 8. Errors and scope

The compiler reports undefined names, duplicate names, forward references,
illegal gated-value escape, unsupported syntax and graph cycles. Runtime errors
are returned from `Run` and recorded in the trace. Generated entry points check
required and annotated Flow inputs before creating a graph; such boundary
validation errors return without a node trace.

Alpha 0.1 deliberately excludes:

```text
for/while/comprehensions
dynamic map expansion
persistent state and feedback
event DSL and async/await syntax
retry/cancellation policy language
effect type systems
```

These are follow-up experiments, not prerequisites for validating the core
dependency model.

## 9. Naming and symbol policy

The Alpha surface intentionally follows familiar programming-language names:

| Name | Meaning |
| --- | --- |
| `flow` | A graph/scheduling boundary. It is not a library function. |
| `fn` | A local expression function compiled to ordinary Go computation. |
| `when` | An execution gate; it is deliberately distinct from value-selecting `if`. |
| `return` | The result of a Flow or local function. |
| `print` | A normal effectful Host operation for console output. |

Names such as `request`, `input` and `name` are ordinary user-defined
identifiers. Names such as `str` and `print` belong to the default Host only;
they are not hidden language features and can be replaced by an application
Host. Identifiers beginning with `__return_` or `__expr_` are reserved for
compiler-generated graph nodes and cannot be used for Flow or function
parameters or bindings.
