# LIP 1.0 Language Specification: A Small Core and a Unified Call Boundary

[English](LIP-1.0-SPEC.en.md) | [简体中文](LIP-1.0-SPEC.md)

Version: 1.0 final. Date: 2026-10-15.

## 0. Scope, terminology, and conventions

This document defines the complete LIP 1.0 language and execution contract. The English and Chinese editions describe the same specification, with matching section numbers. Sections 1–9, 12–15, and the appendices in §16 define core conformance; §10 defines its mandatory Host boundary. The HTTP Host profile in §12.1 and the durable crash-recovery profile in §11 are optional. Neither is required for core conformance. Language semantics, identity encodings, and core conformance cases are defined here without requiring implementation documentation.

The core consists of immutable values, expressions, pure `fn`, static modules and exports, `if/match/for`, `effect/perform`, Flow dependency graphs, structured child Flows, and join-only Claim stores. Within its owning node, `|>` fixes each intermediate result before invoking the next stage using §2.1 sequential bindings; it is not argument nesting. Go and Python share one call protocol. Libraries and Hosts may provide persistence, transactions, retries, business aggregation, and evidence presentation within these boundaries.

“MUST” and “MUST NOT” express conformance requirements; “MAY” expresses an implementation choice. Examples illustrate requirements; conformance cases specify mandatory input/result relationships. Each implementation declares what it supports. Identity encodings for existing single-source programs without child Flows remain unchanged. Deployment, authentication products, persistent connections, and streaming-library development plans are outside this specification. §17 records repository status and is non-normative.

| Term | Meaning |
|---|---|
| Runtime / scheduler | Execution engine / selects work among eligible graph nodes |
| Host / adapter / provider | Manages external operations and permissions / converts native arguments and results / actually performs an operation |
| operation / descriptor / registry | External operation / its registered contract / contracts fixed for a run |
| schema / capability | Argument or result constraints / permission for operations and resource scopes |
| run / binding | One selected entry execution, including all child Flows / a name associated with an immutable value |
| entry / child Flow / invocation | Host-selected root Flow / Flow instantiated inside a parent graph / structurally identified call |
| module / export / source set | Build-time LIP source module / explicitly public declaration / root source and complete locked dependency closure |
| node / graph | Independently schedulable computation instance / nodes and data, control, and execution-order dependencies |
| gate / selector | Condition selecting a node / node computing a branch or loop decision |
| region / producer / writer | Structural branch, loop, or invocation region / statement or region that may produce nodes / node contributing to a Claim store |
| proposal / perform | Request value containing an operation and fixed arguments / admission, invocation, and recording of that request |
| Claim / Claim store / join | Fact with contribution identity / run-local fact collection / contribution through `<+` |
| snapshot / projection | Immutable record list from a store read / pure computation of business values from that list |
| journal / replay / reconcile | Call records / reuse recorded observations / resolve pending calls with trusted external evidence |
| terminal outcome | Call record whose external fact and validation result are settled; distinct from a terminal node state |

Code tokens such as `fn`, `effect`, and `perform` are source syntax. “carry” is documentation shorthand for §6.1, not another keyword. `Recorded` means an observation is journaled, not a source type. Arity is argument count; canonical means the encoding defined here, and wire means its protocol representation.

EBNF uses `::=` for productions, `|` for alternatives, `[...]` for optional parts, `*` for repetition, and quoted source tokens. Formal rules and protocol records are descriptive notation, not executable LIP. `{field:type}` constrains a field; `?` after a field name permits omission. Structural `...` means repetition and is not source syntax. Mathematical notation is defined before use.

## 1. Core objects

| Object | Rule |
|---|---|
| Data | null, bool, finite binary64 number, UTF-8 string, list, string-keyed object |
| fn | Pure closure over immutable values; recursion, mutual recursion, and function values are supported |
| effect | Immutable proposal containing a resolved operation and fixed Data arguments |
| Flow | Named graph definition lowered from bindings, control structures, and child calls; not an ordinary value |
| Claim store | Information slot growing only through join during a run |

Ordinary values V recursively comprise null, bool, number, string, list<V>, object<string,V>, fn, and effect. Data excludes fn/effect recursively. Ordinary containers may hold them, but Host arguments/results, Claim contributions, Flow inputs, and successful outputs MUST be Data. Stores are not values or transferable handles. Names bind once; inner scopes may shadow ordinary values but not store names. Flows are neither V, Data, nor function values: they cannot be contained, captured as values, aliased, passed, returned, or supplied to map/fold. Their targets resolve statically under §2.1.

`effect` constructs requests; `perform` interprets them. Every Go/Python call, external property read, random draw, clock read, log, storage access, and network access uses perform. Pure core/local fn calls are direct; external operations are not. The generic adapter profile in §10 may lower convenient direct syntax to perform before core checking; it still uses the same boundary.

## 2. Source files and syntax

Files MUST be valid UTF-8 without BOM. LF/CRLF, spaces/tabs, `#` line comments, semicolons, and physical newline separators are supported. Other bare control characters are invalid, apart from multiline-string rules below. A trailing backslash explicitly continues a line and MUST be its final character. Parentheses, argument lists, lists, and objects permit natural newlines; block newlines separate statements. The lexer preserves newlines and the parser handles context; a following operator does not implicitly continue a line.

Identifiers match `[A-Za-z_][A-Za-z0-9_]*`. Reserved words are `import as flow fn return if else match for in parallel break continue with next effect perform export true false null void`. `go/python/host/lip` are contextual after import; type names have special meaning only in type/pattern positions. `claim`, import aliases, and §3 core function names cannot be redefined. Other duplicate same-scope declarations are static errors.

Strings use `"..."` or ASCII `"""..."""`. Bare newline/tab are invalid in ordinary strings; triple-quoted strings allow them, normalize LF/CRLF to LF, and reject bare CR. Both forms support only `\"`, `\\`, `\n`, `\r`, `\t`, and `\u{HEX}` with 1–6 hexadecimal digits denoting a Unicode scalar, excluding surrogates and values above U+10FFFF. No Unicode normalization occurs. Triple quotes take lexical precedence and end at the first unescaped three consecutive ASCII double quotes; one/two internal quotes are text, and escaped quotes can represent three literal quotes. `#`, brackets, and operators inside strings are text. No trimming, automatic dedentation, interpolation, raw/f-string prefixes, or backslash-newline continuation inside strings is provided. An unclosed triple quote is E-LEX; interactive tools may collect more lines and MUST treat embedded `:help` and similar text as string content. Both forms produce the same UTF-8 string type, encoding, comparison, and Host boundary.

Number tokens match `(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`, converted to finite binary64 with nearest/ties-to-even rounding. Overflow is invalid; underflow MUST round to a subnormal or zero, rather than optionally fail or flush to zero. Signs are unary operators; there is no implicit octal. Operators use longest match: `**`, `*/`, `//`, `|>` are single tokens without internal whitespace.

In the grammar, `sep` means one or more semicolons/newlines; `padding` means zero or more. `id`, `string`, and `number` denote identifiers/literals. EOF tests end-of-file without consuming a token; NEWLINE is a physical newline token. `decl_end` requires a separator or EOF, so a final import/static-data declaration needs no trailing newline, while subsequent content still requires a separator.

```ebnf
file       ::= padding import* (declarations | fn_decl* statements) EOF
declarations ::= declaration+
declaration ::= ["export"] (fn_decl | flow_decl | data_decl)
import     ::= "import" ("go" | "python" | "host") string ["as" id] decl_end
             | "import" "lip" string "as" id decl_end
fn_decl    ::= "fn" id params "->" type fn_body padding
flow_decl  ::= "flow" id params "->" (type | "void") block padding
data_decl  ::= id ":" type "=" static_data decl_end
decl_end   ::= sep | EOF
static_data ::= literal | ("+" | "-") number
              | "[" [static_data ("," static_data)* [","]] "]"
              | "{" [static_field ("," static_field)* [","]] "}"
static_field ::= (id | string) ":" static_data
params     ::= "(" [parameter ("," parameter)* [","]] ")"
parameter  ::= id ":" type
type       ::= "null" | type_name ["?"]
type_name  ::= "any" | "bool" | "number" | "string" | "list" | "object"
             | "function" | "effect"

statements ::= padding [stmt (sep stmt)* padding]
block      ::= "{" statements "}"
stmt       ::= id "=" expr | id "=" "claim" "(" ")" | id "<+" expr
             | "return" [expr] | "break" | "continue"
             | if_stmt | match_stmt | for_stmt | call | perform | pipeline_stmt
if_stmt    ::= "if" expr block ["else" (block | if_stmt)]
for_stmt   ::= "for" id "in" expr ["parallel"] block | "for" block
match_stmt ::= "match" expr "{" padding arm_stmt (arm_sep arm_stmt)*
               [arm_sep] "}"
arm_stmt   ::= patterns ["if" expr] "=>" (block | stmt)
arm_sep    ::= "," | sep
patterns   ::= pattern ("," pattern)*
pattern    ::= literal | ["+" | "-"] number | concrete_type | "_"
concrete_type ::= "bool" | "number" | "string" | "list" | "object"
                | "function" | "effect"

expr       ::= pipeline
pipeline   ::= or ("|>" stage)*
stage      ::= static_target ["(" [named_args [","]] ")"]
static_target ::= id | id "." id
pipeline_stmt ::= or "|>" stage ("|>" stage)*
or         ::= and ("||" and)*
and        ::= equality ("&&" equality)*
equality   ::= compare [("==" | "!=") compare]
compare    ::= add [("<" | "<=" | ">" | ">=") add]
add        ::= multiply (("+" | "-") multiply)*
multiply   ::= unary (("*" | "/" | "//" | "%" | "*/") unary)*
unary      ::= ("!" | "-" | "+") unary | power
power      ::= power_base ["**" unary]
power_base ::= perform | proposal | postfix
postfix    ::= primary suffix*
suffix     ::= "(" [arguments [","]] ")" | "." id
             | "[" (expr | slice_spec) "]"
slice_spec ::= [expr] ":" [expr] [":" [expr]]
arguments  ::= positional_args ["," named_args] | named_args
positional_args ::= expr ("," expr)*
named_args ::= named_arg ("," named_arg)*
named_arg  ::= id "=" expr
primary    ::= literal | id | "(" expr ")" | list | object | value_block
             | "fn" params "->" type fn_body
             | if_expr
             | "match" expr "{" padding arm_expr (arm_sep arm_expr)*
               [arm_sep] "}"
             | carry_expr
if_expr    ::= "if" expr value_block "else" (value_block | if_expr)
proposal   ::= "effect" operation "(" [arguments [","]] ")"
perform    ::= "perform" (proposal | postfix)
operation  ::= id ("." id)*
call       ::= postfix                 # last suffix must be an argument list
fn_body    ::= "{" padding (pure_stmt sep)* fn_tail padding "}"
fn_tail    ::= ["return"] expr
pure_stmt  ::= id "=" expr | call | pipeline_stmt | if_stmt | match_stmt | for_stmt
             | "break" | "continue"
local_stmt ::= id "=" expr | call | perform | pipeline_stmt | if_stmt | match_stmt | for_stmt
value_block ::= "{" padding (local_stmt sep)* expr padding "}"
carry_expr ::= "for" id "in" expr "with" id "=" expr carry_block
             | "for" "with" id "=" expr carry_block
carry_block ::= "{" padding (local_stmt sep)* carry_exit padding "}"
carry_exit ::= "next" expr | "break" expr | carry_if
             | "match" expr "{" padding carry_arm (arm_sep carry_arm)*
               [arm_sep] "}"
carry_if   ::= "if" expr carry_block "else" (carry_block | carry_if)
carry_arm  ::= patterns ["if" expr] "=>" carry_block
arm_expr   ::= patterns ["if" expr] "=>" expr
list       ::= "[" [expr ("," expr)* [","]] "]"
             | "[" expr "for" id "in" expr ["if" expr] "]"
object     ::= "{" [field ("," field)* [","]] "}"
field      ::= (id | string) ":" expr
literal    ::= number | string | "true" | "false" | "null"
```

The file mode is chosen uniquely from module-level declarations: any export, Flow, or data declaration selects `declarations`; otherwise use `fn_decl* statements`, whose statements may be empty. A root with only imports/ordinary fn therefore has an implicit empty main; a dependency merely supplies declarations. Tokens inside bodies do not choose the mode. Declaration mode cannot mix top-level statements, and legacy statement mode cannot declare static data. All imports precede every declaration/statement, including exported data.

Nested `pure_stmt` blocks are pure statements and cannot return. A function has exactly its final result expression, optionally preceded by return. It cannot declare/read/join stores, perform, or call child Flows, including Pure Flows; constructing an effect is allowed, including when returning function/effect values. Collect named fn/Flow signatures before body checking: named calls may refer forward. fn supports recursion/mutual recursion; §6.3 rejects Flow cycles. Ordinary values cannot refer forward; static data follows §2.2. Data/fn/Flow share a module declaration namespace; duplicates are E-DUPLICATE. Lexical shadowing never silently falls back to a Flow declaration. Parameters, local bindings, and loop variables are unique within their scopes. A shadowing RHS evaluates in the outer environment; the new binding is visible only afterward. Each loop iteration creates a fresh scope. fn loops are sequential, disallow parallel, and do not mutate or leak outer bindings.

A valued Flow MUST have exactly one final top-level return. A void Flow may omit it or end with bare return. Return does not skip other nodes; branches/loops cannot return. Top-level statements form a zero-parameter void `main`, optionally preceded by ordinary fn. Imported modules cannot have top-level statements or implicit main. Explicit roots may declare several Flows; one run selects exactly one. An explicit Host/tool root Flow declaration name takes precedence. Otherwise select the root Flow named exactly `Main`; if absent, select the sole root Flow, or reject with E-ENTRY when there is no unique default. Names are case-sensitive: `main` has no priority, and declaration order does not affect selection. An unknown/non-root-Flow explicit choice is E-ENTRY, without fallback to the default. Imported Flows do not count toward root entry selection. Declaration roots with data/export but no root Flow are E-ENTRY. All source-set declarations are checked, including unselected entries.

Flow parameter/result annotations `function`/`effect`, including optional forms, are E-TYPE. list/object/any inputs/results require recursive Data validation; fn interfaces do not. fn has no void. Discarded pure calls may validate/propagate errors; result-free external actions use perform. `null` is a Data type with one value; `void` means absence of a return and is allowed only as a Flow return annotation. `any?` equals any and retains Data checks. Nullable parameters still must be supplied. `return F(x)` is legal as the final return of a valued Flow, waits for full F success, and then checks the enclosing return annotation.

In expression position, `{}` is an empty object; a field-colon lookahead after newlines distinguishes nonempty objects from value blocks. if branches explicitly require value blocks, so an object result uses `{{a:1}}`. Statement blocks always contain statements; match expression arms may directly return objects. Newlines between `}` and `else` belong to the if. `<+` is only a complete join-statement token, never split into comparison/unary plus in expressions. A binding RHS `claim()` preferentially declares a store; claim cannot be aliased, passed, or ordinarily called. Join targets MUST resolve to declared stores.

Proposals are not postfix primaries: `effect op(args)` ends at its closing parenthesis and accepts no immediate field/index/call suffix. Use `(perform effect op(args)).field` to read a performed result. Empty/comment-only lines create no nodes. The parser ignores NEWLINE in parentheses/arguments/lists/objects and preserves block separators.

Value-block locals are sequential and local to that block; in Flow context they may perform but cannot declare/join stores, including in nested blocks. Full reads retain §9 position rules; fn cannot reference live stores. Local statements do not become separate graph nodes. Unselected blocks do not evaluate. Child calls create §6.3 subgraphs and retain the local cursor. Local ordinary for remains sequential; local parallel is forbidden.

For fn/value/carry blocks, remove trailing padding before classifying the final item as fn_tail/expr/carry_exit. This takes precedence over statement classification of if/match/for, so terminal branches use value/carry blocks as appropriate. Other items require separators. Trailing newline/semicolon does not discard the final expression: `{ f(); }` returns f's result; `{ f(); null }` discards it and returns null. Statement blocks remain statements. Parsers may defer shared-structure classification without unbounded backtracking. `for … with` is an expression for bindings/arguments/return/block tails. carry exits act only at their carry-block tail and never cross fn/carry boundaries; bare break/continue target the nearest ordinary local loop.

All positional arguments precede named arguments; `f(x=1,2)` is E-PARSE. Duplicate names are static E-ARITY even for dynamic callees. Ordinary/core/proposal calls share argument syntax; §3.1 additionally rejects named core arguments.

### 2.1. Child Flow calls and pipelines

`F(args)` is a structured child invocation when its callee lexically resolves to a named Flow. Targets MUST be statically known local declarations or exported `alias.Name` members (§2.2), never Flow values, dynamic callees, ordinary object members, or runtime name lookup.

Explicit Flow arguments MUST be Pure Data expressions. They may reference completed bindings/full Claim snapshots but contain neither perform nor another child call, even a Pure child. First fix such results with bindings or pipelines. Parameters follow positional-then-named binding, without defaults, spreading, or variadics.

`|>` has lower precedence than `||` and runs stages left to right. A stage MUST resolve lexically to a named fn/Flow, an exported module fn/Flow, or a core pure function. It does not search past a shadowing local. Undeclared targets are E-NAME, private members E-EXPORT, and dynamic function values/object members/other nonstatic targets E-TYPE. Both `x |> F` and `x |> F()` bind x to the first declared parameter; `x |> F(limit=20)` also binds named arguments. Stages accept no extra positional arguments, placeholders, or implicit field spreading. Missing first parameter, duplicate first binding, missing/unknown arguments are E-ARITY. Core functions still reject names, receive exactly one positional argument, and MUST accept that arity: len/str/range work; map/fold need a named fn wrapper.

Before execution, check all stage names, argument structure, and statically known types. Evaluate the initial left side exactly once. Each stage waits for complete success of the previous stage, then evaluates its named arguments in source order, validates arguments, and invokes. Later arguments cannot execute early. The initial expression obeys its caller context; a Flow stage accepts its fixed Data result and Pure Data extra arguments. fn stages retain ordinary parameter/result checks. Proposals returned by fn/core calls still have Unknown Ops.

For `e |> S1 |> … |> Sn`, use these invisible sequential bindings within the owner cursor:

```text
t0 = evaluate(e)
ti = invoke_stage(Si, first_argument=ti-1, explicit_named_arguments=Si.args)  # i=1…n
result = tn
```

For a final void Flow in a standalone pipeline statement, wait for completion without creating tn/result or substituting null. Every step must fully succeed; failure stops the cursor. A fixed value reference is passed rather than reevaluating its source. Thus `Read(perform effect worker.read(2))` and `Adjust(Read(2),delta=3)` violate explicit Flow argument purity; `perform effect worker.read(2) |> Read` and `2 |> Read |> Adjust(delta=3)` can satisfy it. Ordinary fn arguments in Flow context may evaluate calls sequentially, e.g. `f(Read(2))`, while fn bodies remain pure. This conservative restriction is intentional in 1.0 and may be relaxed compatibly later.

A whole pipeline belongs to one caller node, including inside value blocks/carry. Its temporaries/stages introduce no graph nodes or new source tokens/sites; child Flows instantiate their own regions. Lowered line numbers/temporary IDs MUST NOT replace §6.2 identities. Source rewrites change revision; semantic equivalence promises no cross-revision CallID equality. A Flow stage waits for the complete region, not an early return value. Pipelines neither inspect business Result values nor automatically unwrap `{ok:false}`, truncate, or retry.

Pipelines are expressions or standalone statements; context purity/type/Data restrictions apply recursively. Guards, proposal/join RHS, carry exits, and fn bodies allow pure fn/core stages but no perform/Flow calls hidden by containers/parentheses/blocks. `ok && x |> F` groups as `(ok && x) |> F`; `x |> F == y` is E-PARSE; use `(x |> F) == y`. §15 recommends W-PIPE-PRECEDENCE for unparenthesized mixed boolean/pipeline expressions. Void Flows are permitted only as standalone calls or final standalone pipeline stages; other value uses are E-TYPE. Pipelines are single evaluations, not streaming, subscriptions, background tasks, or message delivery; use bindings/if/match/for for branching, joins, and iteration.

### 2.2. Static modules, exports, and build inputs

Each module is one file identified by SourceID: a canonical relative path within the project root, using `/`, with no empty segment, `.`, `..`, or absolute prefix. Preserve UTF-8 bytes without normalization. Tools/Hosts fix the root SourceID; in-memory sources may use fixed virtual paths. Physical directories/cache locations are not language identity.

Local path components MUST byte-match directory-entry names, without case folding or NFC/NFD matching. Ambiguous exact resolution is E-MODULE. Reject all symlinks below the root, even internal ones; the Host first fixes the project root to its resolved physical directory. Different SourceIDs identifying one physical file, including hard links, are E-MODULE. Supplied source tables use exact keys without filesystem merging. Physical paths/inodes never enter identity.

`import lip "./lib/pricing.lip" as pricing` requires explicit as. Resolve relative to the importer into a `.lip` file within the project root and normalize to SourceID. Hosts may use local files or supplied tables, but MUST NOT download/load dependencies or initialize modules during run startup, requests, completion, or pure evaluation. Missing/escaping/ambiguous/lock-mismatched content is E-MODULE. 1.0 defines no package manager, remote import, or runtime module loading.

Resolve each SourceID once, without duplicating declarations/execution across aliases/importers. All static imports form the module graph; direct/indirect cycles are E-MODULE-CYCLE even in unused code. Dependencies may have imports/declarations without Flows, but no top-level statements/evaluation. perform/claim appear only in legal Flow bodies. Discarding a top-level call result does not make it a declaration. Roots retain §2 entry rules.

Declarations default to private. export modifies only top-level fn/Flow/static Data, not locals/imports/namespaces. Access is by explicit aliases, without wildcard import, implicit namespace merging, namespace reexport, or textual concatenation. Private access is E-EXPORT. Aliases are not module values passable to functions. Private same-named declarations in different modules do not conflict.

`export TAX_RATE:number = 0.1` is immutable static Data. Initializers are only §2 static_data literal trees, with no names/calls/proposals/Flows/evaluation. Validate types, finiteness, Unicode, duplicate fields, and Data normally. After signature collection, module definitions may forward-reference static data; it cannot be reassigned. Parsing/validation/memory count toward build budgets without starting runs or invoking Hosts.

An exported `pricing.total` fn evaluates in its defining module environment and may be used as a function value; it captures only that module's immutable data/closures. Exported `pricing.Quote(args)` is a static child call, also expressible as `request |> pricing.Quote`. Exported data is a fixed reference. Distinguish module resolution from object projection during checking. Flow export never creates a dynamic function value. Cross-module fn proposals retain Unknown Ops. Imported definitions use their own imports/data/private declarations. External aliases are not directly exported; use fn proposals or Flow wrappers. Canonical operation identity uses the defining module's original import, unaffected by caller aliases.

Before execution, freeze the root and entire dependency closure, including unused imports. Preserve each SourceID, nonempty version string (`local` is allowed), complete source bytes, and digest. Checking/interpreting/REPL/Go generation MUST use the same snapshot, never hash then reread for compilation. Changed sources require rebuilding; no live replacement. Missing versions/content or lock mismatch reject before external actions.

Build notation: Normalize replaces CRLF with LF only; SourceHash is lowercase SHA-256 of normalized UTF-8 bytes. Whitespace/comments/Unicode/escapes receive no other normalization. SourceTable, hashes, and token numbering use the same Normalize(source); diagnostics may map to original physical locations, but extra CRLF bytes do not change sites. DependencyLock has one row per distinct edge: `[importer_id, decoded_import_path, target_id, target_version, SourceHash(target)]`, sorted lexicographically by UTF-8 bytes of the first four fields. An importer/path cannot resolve to different targets; identical edges are deduplicated. SourceTable contains `[SourceID, version, Normalize(source)]` for root/dependencies, sorted by UTF-8 SourceID. Manifest formats are not core but MUST reconstruct this unique snapshot without affecting §6.2 identity.

Diagnostics retain defining module, original token, and position, never expanded line numbers. Export/purity/call-graph/transitive-effect checks cover every declaration. Generated Go isolates module symbols and executes actual Go with static metadata, never reparsing/interpreting LIP at runtime. Interactive module support cannot reconstruct environments by rerunning successful cells; existing bindings/closures retain frozen versions, and failed cells commit no module bindings.

Two independent files, with only Main as root entry:

```lip
# lib/pricing.lip
export TAX_RATE:number = 0.1
export fn total(price:number) -> number { price * (1 + TAX_RATE) }
export flow Quote(request:object) -> object {
    return {total:total(request.price)}
}
```

```lip
# main.lip
import lip "./lib/pricing.lip" as pricing
flow Main(request:object) -> object {
    quote = request |> pricing.Quote
    return {quote:quote, rate:pricing.TAX_RATE}
}
```

### 2.3. Entry input, output, and validation before execution

The Host supplies this unique logical input protocol:

```text
EntryInput = {positional:list<Data>, named:object<string,Data>}
```

Both fields are required and no others are accepted. JSON decoding MUST reject duplicate object keys. Bind positional values in declaration order, then named values by parameter name. Each parameter binds exactly once; reject duplicate/unknown/missing arguments, without defaults/spreading. Recursively validate Data, then annotations. Zero-parameter entries, including implicit main, accept `{positional:[],named:{}}`. `flow Main(x:number,y:string)->object` accepts either `{positional:[2],named:{y:"a"}}` or `{positional:[],named:{x:2,y:"a"}}`.

Select the entry first under §2. Input structure/binding/annotation failure is always Host pre-run E-SCHEMA: allocate no run_id, journal, nodes, stores, root error/ErrorKey, or provider admission. Durable begin occurs only after validation. This differs from static call-site E-ARITY/E-TYPE and node-local dynamic E-SCHEMA. Successful valued returns, including entries, validate annotation and Data at their return node; void has no return value to validate.

CLI/SDK/HTTP conveniences MUST explicitly convert to EntryInput before validation, without guessing mappings from runtime values. HTTP fixes `{positional:[request],named:{}}` (§12.1). Durable storage fixes the validated complete EntryInput and selected entry; recovery does not reinterpret convenience formats.

Only after the entire entry succeeds does the Host receive:

```text
EntryOutput = {kind:"value", value:Data} | {kind:"void"}
```

Valued entries use only value with their validated result; void entries, including implicit main, use only void without a value field. Extra fields are forbidden. `{kind:"value",value:null}` differs from `{kind:"void"}`. Failure/cancellation produces no EntryOutput. This protocol is neither a new LIP type nor error-catching mechanism; CLI/SDK may explicitly unwrap it, and HTTP uses value under §12.1.

## 3. Values and core operations

All values are immutable. Constructors/operators evaluate operands left to right; only selected branches of if/match and required operands of `&&`/`||` evaluate. Logical operands and conditions must be bool, without truthiness. Equality is structural on Data; comparing fn/effect or containers containing them is E-TYPE. Objects ignore field order for equality; duplicate decoded keys are invalid. Strings compare lexicographically by UTF-8 bytes, without normalization. Ordering accepts two numbers or two strings; other comparisons are E-TYPE. `+` accepts two numbers or two strings, never implicit conversion. Numeric unary `+/-`, arithmetic, and ordering use finite binary64; normalize negative zero to zero. Overflow/nonfinite results and division by zero are errors.

Data is a finite acyclic value tree: lists preserve order, objects have unique keys and canonical UTF-8 key order. Native mutable objects must be copied to Data or represented by declared Host references, never directly shared. Equality is typed and structural: null equals only null, and bool never equals number; fn/effect are not comparable even inside containers. Field/index access creates no implicit binding. Stop operand evaluation after an error.

Basic arithmetic MUST use binary64 round-to-nearest/ties-to-even, without reassociation or result-changing implicit FMA. Division by zero/nonfinite results are E-NUMBER. `a // b` is the binary64 value of the floor of the exact mathematical quotient. `a % b` is its corresponding mathematical remainder, rounded to binary64 and constrained to `[0,b)` for b>0 or `(b,0]` for b<0. If rounding reaches the excluded boundary b, use `nextafter(b,0)`; normalize zero. nextafter denotes one binary64 step toward zero and is not a LIP builtin. Implementations MUST NOT substitute truncating division. Safe integers have absolute value at most `2^53-1`. Host integers outside this range require rejection or explicit string/bytes schema encoding.

`a ** b` is real exponentiation; `a */ b` is real logarithm to base b. Both require numbers (E-TYPE otherwise). `0 ** 0 = 1`; zero to a negative power and negative bases to nonintegral powers are E-NUMBER. Integral exponent means the stored binary64 is exactly integral, without the safe-index limit. Log requires a>0, b>0, b!=1; `1 */ b = 0` and `a */ a = 1`. Overflow is E-NUMBER; subnormals/underflow to zero are allowed, with zero normalization. Both MUST use §16.5's fixed binary64 algorithm, not platform/version-dependent pow/log. They approximate real operations without correctly-rounded guarantees; mathematically equivalent expressions need not be bit-identical. Other numerical functions/complex values belong to libraries.

`**` associates right and binds tighter than its left unary sign, while allowing a signed exponent. `*/` shares `* / // %` precedence and associates left.

| Expression | Grouping | Result |
|---|---|---|
| `2 ** 3 ** 2` | `2 ** (3 ** 2)` | 512 |
| `-3 ** 2` | `-(3 ** 2)` | -9 |
| `(-3) ** 2` | Whole negative base | 9 |
| `2 ** -3` | `2 ** (-3)` | 0.125 |
| `8 */ 2` | Logarithm of 8 to base 2 | 3 |
| `8 */ 2 * 4` | `(8 */ 2) * 4` | 12 |
| `256 */ 2 ** 2` | `256 */ (2 ** 2)` | 4 |

There is one number type: 3 and 3.0 are identical and display as `3`.

List/string scalar indexing `v[i]` requires a safe integer. With length n, a negative i is adjusted once to n+i; the resulting index MUST be in [0,n), otherwise E-INDEX. Strings count Unicode scalars, without grapheme clustering or normalization. Object indices remain string keys; `.name` is equivalent lookup. Missing keys are E-INDEX; wrong index/receiver types are E-TYPE.

For a list a, `a[indices]` also accepts a list of safe integers. It returns a new list in index order, preserving duplicates, and applies scalar negative-index rules to every element. Any invalid index fails the whole expression; there is no boolean mask, recursive flattening, or multidimensional indexing. An empty index list returns an empty list even for an empty source. Strings do not accept index lists. For `a=range(5)`, `a[[3,-1,3,0]]` is `[3,4,3,0]`, and `a[range(3)]` is `[0,1,2]`.

List/string slicing uses `v[start:stop]` or `v[start:stop:step]`, returning the same type with stop excluded. Each component may be omitted; explicit null means the same omission. Non-null components MUST be safe integers (wrong type E-TYPE, fractional/unsafe integer E-INDEX). Step defaults to 1; zero is E-VALUE. Bounds are normalized as follows, using exact integer arithmetic:

- For positive step, omitted start/stop are 0/n. Add n once to explicit negative bounds, then clamp each to [0,n].
- For negative step, omitted start/stop are n−1/−1. Add n once to explicit negative bounds, then clamp each to [−1,n−1]. An omitted stop therefore differs from explicit −1, which denotes the last element before clamping.
- Starting at normalized start, select successive indices separated by step while index < stop for positive step or index > stop for negative step. A direction mismatch returns an empty list/string. Bounds outside the source are clipped, including empty sources; scalar/gather indices remain strict.

The receiver and supplied index/bound expressions evaluate exactly once, left to right (receiver, start, stop, step), stopping at the first error. Result allocation is checked against the list budget before allocation (E-BUDGET); no partial result is published. These operations preserve immutable values, including function/effect elements, without invoking them. Projections, gathers, and slices retain Unknown normative Ops (§5). Core Data follows these rules on all execution paths; opaque Host resources use their registered native indexing protocols.

| Expression | Result |
|---|---|
| `range(5)[-1]` | `4` |
| `range(5)[::-1]` | `[4,3,2,1,0]` |
| `range(5)[1:5:2]` | `[1,3]` |
| `range(5)[99:-99:-2]` | `[4,2,0]` |
| `range(5)[:-1:-1]` / `range(5)[:null:-1]` | `[]` / `[4,3,2,1,0]` |
| `"a𐀀b"[::-1]` | `"b𐀀a"` |
| `[][:]` / `""[::-1]` | `[]` / `""` |
| `[0][-2]` / `[0][[0,1]]` / `[0][0.5]` | E-INDEX |
| `[0][true]` / `"a"[[0]]` / `[0][:"x"]` | E-TYPE |
| `[0][::0]` | E-VALUE |

### 3.1. Core pure functions (1.0)

These are the complete non-overridable core functions. They accept V, return new values, and never mutate inputs.

| Signature | Arity | Contract |
|---|---|---|
| len(value) | 1 | string/list/object → number; strings count Unicode scalars |
| str(value) | 1 | Data → string; strings unchanged, other Data as compact canonical JSON |
| range(end) / range(start,end) / range(start,end,step) | 1/2/3 | Safe integers, nonzero step → half-open number list; defaults start=0, step=1 |
| map(xs,f) | 2 | list, pure unary function → results in input order |
| fold(xs,seed,f) | 3 | list, V, pure binary function → strict left fold; empty list returns seed |
| fail(message) | 1 | string → E-FAIL, no successful value |
| concat(a,b) | 2 | Two lists → concatenation; append is concat(a,[v]) |
| slice(v,start,stop) / slice(v,start,stop,step) | 3/4 | Same rules as bracket slicing; null denotes an omitted component |
| keys(o) | 1 | object → string list sorted by UTF-8 bytes |
| has(o,k) | 2 | object, string → bool; missing key is false |
| put(o,k,v) | 3 | object, string, V → object with the key added/replaced |
| split(s,separator) | 2 | string, nonempty string → nonoverlapping left-to-right split retaining empty segments |
| parse_number(s) | 1 | string → finite number from complete text |

Core functions reject named arguments and check declared arity statically when known and dynamically through function values. They may be passed as pure function values. map calls f(element); fold calls f(accumulator,element), using §5's callable/arity/type checks even on empty inputs when statically known. Unguarded comprehensions are ordered map; guards follow §6.2. Flow comprehensions containing perform run sequentially like list literals. range checks size budget before allocation and computes length/elements with exact integers before converting to number, without floating-point loops. len MUST return a safe integer or explicitly fail, never round an incorrect length.

slice uses exactly the bracket slicing rules above, including negative bounds, clipping, nullable components, and a nonzero optional step. `slice(v,null,null,-1)` equals `v[::-1]`. has/put require string keys. Empty split separator is E-VALUE; `split("",",")=[""]` and `split("a,,b,",",")=["a","","b",""]`. There are no hidden parameters.

parse_number accepts an optional ASCII sign followed by exactly §2's number token. Reject whitespace, leading zeroes, hex, NaN/Infinity, and trailing text. Invalid text/overflow is E-VALUE; wrong type E-TYPE. Rounding, subnormals, underflow, and zero normalization match literals, not permissive Host parsers. `parse_number(str(n)) == n` holds for every core number.

Filtering, defaults, object merging, concatenation, and membership can be composed in ordinary pure fn:

```text
fn filter(xs: list, predicate: function) -> list {
    [x for x in xs if predicate(x)]
}
fn get(o: object, key: string, fallback: any) -> any {
    if has(o, key) { o[key] } else { fallback }
}
fn merge_objects(a: object, b: object) -> object {
    fold(keys(b), a, fn(acc: object, key: string) -> object { put(acc, key, b[key]) })
}
```

Canonical JSON display is independent of Host printing. Zero is `0`. For the exact mathematical value v of a nonzero binary64, among round-tripping decimal values `m×10^e`, choose m with the fewest significant digits and no leading/trailing zeroes, then the smallest exact distance to v, then smaller absolute value on a tie. Let its scientific exponent be k: use plain decimal when `-6≤k<21`, otherwise one integer digit, optional fraction, lowercase e, and exponent without plus sign/leading zeroes. Plain fractions need an integer part and no meaningless trailing zeroes; prefix negatives with `-`. Thus `str(1e3)=1000`, `str(1e21)=1e21`. Escape only quote, backslash, and U+0000–001F; controls use lowercase `\u00xx`, all other UTF-8 is literal. Recurse into lists/objects and sort object fields by UTF-8 bytes. fn/effect and containers containing them cannot be str-encoded.

This display is not RFC 8785/JCS and need not match JSON.stringify bytes. Display is not injective: `str("1")==str(1)` is valid. Fingerprints/identities use typed C (§16.1), never str. JSON inputs MUST reject duplicate keys, including equal decoded keys.

Closures capture immutable snapshots, permitting structural sharing without physical copying per call. They MUST NOT hide live stores/native callables. Read a store into Data first, then capture it. `g=fn()->list{facts}` for a live store is static E-PURITY, not an implicit read at closure construction; use `captured=facts` and capture captured, retaining complete-read dependencies/position rules.

## 4. Function computation and execution machine

The language imposes no maximum iteration/recursion count. Termination is not a static type property. Divergence MUST NOT be presented as success/null or silent truncation. All execution paths MUST support proper tail calls, including direct, mutual, indirect function-value calls, and final selected if/match calls, without accumulating frames or recursion-depth failures. Trampolines, explicit continuations, or CPS may implement this.

```text
fn sum_to(n: number, acc: number) -> number {
    if n == 0 { acc } else { sum_to(n - 1, acc + n) }
}

flow Main(n: number) -> number {
    return sum_to(n, 0)
}
```

Non-tail recursion may grow memory; arbitrary recursion is not constant-space. Hosts may publish memory/instruction/time/output budgets: exhaustion is E-BUDGET and external cancellation is Cancelled. Unbounded semantics do not promise infinite physical resources. Check cancellation at loop/recursion safe points, and prevent long pure work from indefinitely monopolizing all parallel execution opportunities.

Budget exhaustion is an execution failure, not a function result. Implementations MUST NOT substitute a type-derived default such as null, 0, false, an empty string, list, or object, including when the return annotation accepts null or any. A Host may map the diagnostic to an explicitly defined application error or fallback response; this does not make the failed execution a successful EntryOutput.

## 5. Types and pure evaluation

`Γ ; S ⊢ e : τ ! ε` means expression e has type τ and evaluation effect ε in ordinary-name environment Γ and Claim-store environment S. Γ records value/function types and analysis information; S records declared store identities in the current Flow instance. `Γ(x)=τ` denotes an ordinary name type; C is reserved for §16.1 encoding. Φ is the module-qualified Flow signature/structure table, separate from ordinary values. Resolve lexical Flow declarations before using Φ; Flows never participate in dynamic callable checks.

| Effect ε | Meaning |
|---|---|
| Pure | Core computation without external execution |
| ReadOnly | External reads; registered contract guarantees no external writes |
| Write | May change external state |
| Unknown | Operation set/effect cannot be proven; admitted under write restrictions |

Recorded is journal metadata for perform observations, not a propagated value label/type dimension. Unknown has Write admission/concurrency restrictions. Idempotency is a Host contract, not another effect level. Concurrent is a descriptor bool: true permits overlapping calls; false uses §7 barriers. ReadOnly does not imply Concurrent. Each run fixes registry Σ, including names/versions/schemas/effects. Capability revocation may reject execution without changing existing proposals.

any is the top V type; τ? means τ or null, and any? equals any. list/object annotations constrain outer shape, not recursive Data. Only §2's bare/nullable source types exist; list<object> is descriptive notation. Closures carry actual names/types/arity/result annotations, while bare function parameters need not reveal them statically. All language fn are pure; hidden signatures require dynamic checking, not arbitrary higher-order inference.

User calls bind positional then named arguments by declared names/order, without defaults/spread/variadics. Reject duplicate/missing/unknown/arity mismatches. range alone has 1/2/3 arities. Reject statically known type errors before execution; dynamic any mismatches are E-TYPE, while Flow/adapter schema failures are E-SCHEMA. No implicit string/number/bool conversion occurs.

Known callable/arity/name/type errors MUST be rejected statically; only information hidden by dynamic function values/any remains dynamic. Ordinary dynamic calls execute in this order:

1. Evaluate callee; accept a language closure or §3.1 pure core function, rejecting native callables with E-TYPE.
2. Validate actual signature, argument structure/names/duplicates/missing values/arity before evaluating arguments. Fail E-ARITY; core functions reject names.
3. Evaluate arguments left to right in source order, then validate all parameter types before executing the body.
4. Execute the body; validate its result annotation before publishing success. Body actions have already occurred at a failed result check. Earlier argument perform actions are not rolled back by later type failures.

map/fold use the same callback rules. Proposals bind against operation schemas rather than ordinary callable rules; §10 separates construction/execution. Child calls first statically validate target/arity/names/Pure Data, evaluate arguments in order, check annotations/recursive Data, then instantiate the graph. Dynamic input failures are E-SCHEMA without body execution; failed output validation retains completed effects/joins. any cannot hide fn/proposal/Flow/store across Flow boundaries. Statically known Flow type/Data mismatch is E-TYPE; dynamically discovered mismatch E-SCHEMA. Host EntryInput has separate §2.3 pre-run validation.

Summary(F) conservatively combines all possible performs and child summaries across all paths/loops/nodes. The acyclic call graph permits callee-first computation; constant guards do not remove candidates. No performs means Pure; Unknown remains Unknown. Child execution contributes to its caller effect; reading an already completed Data result is Pure. Pure summaries never remove complete-read producer dependencies or permit child calls in Pure-only positions (fn, guards, proposal/join RHS, explicit child arguments).

parallel checks all transitive normative Ops, not just result types/Flow names. Only Pure/join/proven Concurrent ReadOnly graphs are accepted. Write/Unknown/nonconcurrent ReadOnly is static E-PARALLEL. Summaries do not strengthen Ops: fn results, projections, and carry results remain Unknown.

In the rules below, op is a resolved operation, args are argument expressions, and p a proposal. `args : Data ! Pure` means every argument is pure and Data. Ops(p) is a nonempty finite set or Unknown; EffectOf takes the greatest registered effect, and ResultType conservatively merges schemas, yielding Unknown/any respectively for unknown Ops.

```text
Γ(x) = τ
──────────────────────────────────
Γ ; S ⊢ x : τ ! Pure

Γ ; S ⊢ args : Data ! Pure
─────────────────────────────────────────────────
Γ ; S ⊢ effect op(args) : effect ! Pure

Γ ; S ⊢ p : effect ! Pure
─────────────────────────────────────────────────────
Γ ; S ⊢ perform p : ResultType(Ops(p)) ! EffectOf(Ops(p))
```

Composite effects take the supremum `Pure<ReadOnly<Write<Unknown`. Reading a completed binding is Pure and does not re-observe its origin, so a recorded result can feed a new proposal. Proposals cannot internally perform, query live state, or draw randomness; bind those observations first. Store names use §9 rather than ordinary-name reference rules: complete reads become Pure Data with producer dependencies; live snapshots are Recorded performs.

Ops is compiler information, not a writable effect[Ops] type. Implementations MUST use the following analysis for parallel acceptance; private optimizations/stronger higher-order reasoning cannot admit a normative Unknown perform.

| Proposal origin | Ops |
|---|---|
| Direct effect op(args) | Singleton resolved op |
| Analyzed binding reference / parentheses | Preserve existing Ops, including captured values |
| if/match result | Union all candidates; any Unknown yields Unknown; no constant-guard pruning |
| value block | Analyze tail in sequential local environment |
| list/object index, gather, slice, or field projection | Unknown, including literal containers |
| fn/core/higher-order result or carry result | Unknown; no function-return/cross-iteration analysis |
| Other parameter/input/any | Unknown; an any annotation alone does not erase existing binding information |

Storing proposals does not perform them. Given a number x and registered version-1 Concurrent ReadOnly `worker.read(number)->number`, `p=[effect worker.read(x)]; q=p[0]; r=perform q` is legal sequentially, but q has Unknown Ops and its node is a barrier; parallel placement is E-PARALLEL. Merge effects of argument evaluation and every perform site separately, rather than masking them with result Ops. Returned proposals still carry resolved operation/version/arguments and undergo runtime contract checks. Every operation is authorized at perform; any never exempts Data/purity. Pure function parameters never become native callables. perform requires a proposal. Construction only resolves contracts/fixes arguments; construction/schema errors do not imply provider invocation.

Equal branch types stay equal; τ plus null becomes τ?; other different bare types become any. Check return annotations statically where possible, dynamically otherwise; no writable union type is introduced. Merge operation result schemas conservatively; Unknown Ops yields static any but still validates the actual schema. Every parallel perform MUST prove all possible operations Concurrent ReadOnly; potential runtime safety is insufficient. Sequential/graph nodes may use Unknown with runtime registration/authorization/schema checks.

The closed pure small-step machine is `⟨e,ρ,κ⟩ → ⟨e',ρ',κ'⟩`: expression, immutable runtime name/value environment, and continuation including remaining work/return checks. Fixed operand/match/primitive order makes reduction deterministic. For closure f and values vs bound in declared parameter order, body(f), captures(f), params(f) denote its body, captures, and names:

```text
⟨call f(vs), ρ, κ⟩ → ⟨body(f), captures(f)[params(f)↦vs], κ⟩
```

A tail call adds no return continuation. Reusing it MUST preserve every still-active return annotation; finite bare-type constraints may be combined without frames accumulating per tail call. Only non-tail work retains corresponding frames.

## 6. Flows, loops, and positions

Ordinary bindings create nodes; references create data dependencies, never mutation. fn-local evaluation does not expand into the global graph. if/match selectors create control dependencies. Every node, including Pure nodes, MUST wait for its gate; speculative semantic evaluation is forbidden. Calls/performs/pipeline statements, joins, selectors, loop sources, and returns have nodes. Structural blocks have completion conditions rather than standalone computation nodes: every selected node must complete or normally skip. Child calls create §6.3 regions within the same run. Loops have entry, iteration-entry, and overall completion conditions. Branch values stay local. Node fusion cannot change identities/dependencies/records/external order/error boundaries.

break/continue target the nearest lexical sequential for. Later iteration work cannot begin semantic evaluation before earlier selectors that may transfer control are resolved. Unselected branches are normal gate Skipped, not root errors, and consume no unexecuted instruction/call budget; actual construction/checking/memory costs may still count. Static syntax/name/type checks cover unselected code, but dynamic division-by-zero/fail cannot be upgraded by constant folding into unconditional errors. Optimization preserves positions, gates, and published observable budget rules.

match selects the first source-order arm whose pattern matches and Pure bool guard is true. Guard false skips; guard Error fails the match. Type patterns use nonnullable concrete types. `_` is the sole pattern in its arm, optionally guarded. Expression no-match is E-MATCH-NO-ARM; statement no-match is a no-op. No partial-object patterns, binding, or destructuring exist; guard missing-key errors are not “no match.”

`for x in source` evaluates a list source once, then fresh iteration scopes sequentially. The next iteration waits for all current work to succeed/normally skip; Error/Cancelled/failure-skip stops the loop. continue closes remaining unstarted work; break also closes future entries. Started actions are not rolled back. `for { ... }` runs indefinitely until break/cancellation/budget/divergence.

`for x in source parallel` expands within Host limits. Bodies allow Pure, joins, and proven Concurrent ReadOnly operations, never Write/Unknown, cross-iteration ordinary bindings, or break/continue targeting the parallel graph loop. Node-local carry exits and break/continue targeting local ordinary loops are permitted; all performs still require proof and local parallel remains forbidden. Iterations use separate immutable snapshots, without shared mutable native objects.

### 6.1. Sequential iteration with a carried value

`for x in source with s = init { ... }` first evaluates/validates source as a list, then init, each once in the outer environment. Every iteration creates fresh x/s; s begins as init. Locals do not escape. Each normal path ends in next e, break e, or an if/match of such exits. next finishes current local statements and supplies the next s; break returns e. Exhaustion returns the last s; empty source returns init. No matching carry arm is Error. Source-free `for with s=init` continues until break/divergence/cancellation/budget.

Exit e MUST be Pure but may reference completed observations; unselected exits do not evaluate. Error/cancellation stops iteration. Ordinary-loop break has no value and continue is only for ordinary loops; carry continues unchanged through next s. The source type of s is any; values remain V and actual operations/results obey normal checks. There is no shared mutable state.

carry runs sequentially within its owner node, like comprehensions, creating no global nodes. It allows local bindings/if/match/sequential loops/perform, but no store declaration/join/local parallel. Inside fn, perform is forbidden; inside Flow, allowed subject to enclosing parallel restrictions. The node effect includes all possible performs; indices enter EvalPath (§6.2). fn/fold callbacks remain pure. Carry ordinary Data evidence across iterations and join through a separate statement when needed.

```text
import python "application.llm" as llm

flow Refine(initial: string) -> object {
    final = for round in range(10) with s = {draft: initial, score: 0} {
        out = perform effect llm.refine(s.draft)
        revised = {draft: out.text, score: out.score}
        if revised.score >= 0.8 { break revised } else { next revised }
    }
    return final
}
```

This example registers version-1 `llm.refine(draft:string)` returning Data `{text:string,score:number}`, Concurrent ReadOnly. Each draft comes from the previous fixed result; the descriptor, not the name llm, establishes effects/concurrency.

### 6.2. Positions and call identities

Independent nodes may overlap. Within a node, expressions/arguments/performs run left to right. fn/map/fold callbacks cannot perform or invoke Flows. Flow comprehensions may perform sequentially; concurrency comes only from graphs/explicit parallel loops. Evaluate comprehension source once first, then one scope per original item. Its optional guard evaluates exactly once per original item, MUST be Pure bool, and runs before the body. False skips the body, true appends its result, Error stops. Guard/body EvalPath appends the original zero-based source index, not a filtered-result index; source evaluation does not append its own index. Nested indices follow actual enclosing guard/body order. Result construction overhead MUST be O(n+k), excluding guard/body/value costs, with n source items and k retained. Private builders are allowed; final values remain immutable. concat has no amortized O(1) guarantee, so fold+concat can copy quadratically.

Each source has zero-based canonical token ordinals: remove comments/whitespace, preserve physical NEWLINE outside strings, and number from its first token. Source-set identity order is root first, then other SourceIDs sorted by UTF-8 bytes. StaticSite is local ordinal plus preceding sources' token_count. No AST-child/link/load numbering is permitted. Single-source offsets are zero. Longest matching applies; CRLF is one NEWLINE; multiline-string body newlines are inside one string token. Explicit continuation backslash/newline and EOF have no token. Parser-ignored NEWLINE still counts. node_site is a statement's first token; loop/comprehension anchors are for tokens. The Host fixes a nonempty UTF-8 run_id unique within its identity domain.

Positions identify graph nodes, EvalPath identifies node-local dynamic evaluation, JoinID contributions, and CallID observations. Sites/indices are nonnegative integers. enclosing_loop_site/index are graph-loop anchors/zero-based iterations; node_local_loop_site/iteration_or_element_index describe node-local carry/ordinary loops/comprehensions; perform_site is the perform token StaticSite.

InvocationPrefix is empty for entry. A child uses its caller owner node's Position, the complete EvalPath_at_call, and invocation_site: the first static target identifier, or alias in alias.Name, for direct calls and pipeline stages. `++` concatenates pair sequences. Repeated local calls use sites/original iteration paths, never start/completion counters. Child EvalPath starts empty; caller dynamics already enter its prefix.

```text
InvocationPrefix(entry) = []
InvocationPrefix(child) = Position(owner) ++ EvalPath_at_call ++ [(invocation_site, 0)]
Position = InvocationPrefix(current_flow) ++ [(enclosing_loop_site, index), ..., (node_site, 0)]
EvalPath = [(node_local_loop_site, iteration_or_element_index), ...]
JoinID   = (run_id, Position)
CallID   = (run_id, Position, EvalPath, perform_site)
```

Compare Position/EvalPath lexicographically as sequences of integer pairs, each component an arbitrary-precision nonnegative integer, never decimal strings/binary64; shorter equal prefixes come first. Position begins with invocation prefix, then current Flow graph loops outer-to-inner, then its statement. A loop selector excludes its own iteration and is shorter than descendants. With sibling anchors 2/5 and body sites 3/6, `[(2,3),(3,0)] < [(5,0),(6,0)]`: every iteration of the first precedes the second. EvalPath records enclosing local loops/comprehensions outer-to-inner, empty when absent. Performs/errors use the path at their actual occurrence.

Within a revision, Position uniquely identifies a node and InvocationPrefix a Flow instance. Global sites identify original tokens; fixed region/call metadata distinguishes loops, owner nodes, local loops, and targets; every repetition includes its original index. A loop anchor and call-target token cannot admit conflicting structural interpretations in one context, even at index zero. Decode callee contexts using metadata. Wire needs no tags but cannot be interpreted structurally without the same revision metadata.

Ancestor(a,b) means a is a strict execution-container ancestor of b through child/loop/branch regions, determined from structural metadata, not arbitrary numeric prefixes. For nodes/regions:

```text
Earlier(a,b) iff Position(a) < Position(b) and not Ancestor(a,b)
```

A child region orders at its InvocationPrefix; loop/branch regions at selector Position. Earlier defines implicit external order only, not data/After/error ordering. Descendants inherit the container's external prerequisites without waiting for that container's complete execution. §7 uses this for OrderAllows, barriers, and error closure.

Replay of a static perform at the same dynamic path reuses CallID; distinct sites do not collide. Pure fn have no perform sites. Equal arguments never merge identities; fingerprints are not identity. Different call sites, graph-loop iterations, carry/comprehension elements give distinct child/join/perform identities. Definition order does not change instance order: compare invocation prefix first. Programs without children preserve existing identities/wire. Blank/comment edits may move ordinals; identities are revision-local, never business keys across revisions/runs.

Single-source programs without import lip use SourceHash(root) as revision. Multi-source revision is `lowerhex(SHA256(C(["lip-source-set", "1.0", root_source_id, SourceTable, DependencyLock])))`. C/SHA256/lowerhex follow §16.1. Include all sources/versions/edges/content/comments, not only reachable declarations. Changed dependencies cannot reuse a revision. Language version, root SourceID, lock, and selected entry are also fixed run metadata; changes cannot masquerade as resume. Never use private JSON/absolute paths/directory iteration order for identity. Equal frozen inputs/locks MUST produce equal positions. Resume retains run_id; changed programs cannot resume old unfinished runs under new positions. Starting a new run is distinct from resuming.

### 6.3. Structured child execution and completion

A Flow is a graph template; collecting declarations/signatures neither evaluates bodies nor creates stores/Host calls. The static call graph includes all possible calls, even unselected branches. Direct/indirect Flow recursion is E-FLOW-CYCLE; pure fn recursion remains legal. Repeat effectful workflows through for/carry. No dynamic Flow values/target replacement exist.

After §2.1/5 input fixing/validation, instantiate a graph with immutable Data parameters. It sees its parameters, locals, defining module data/fn/Flows/import contracts, never caller locals/live stores. Pass context explicitly. Every invocation/iteration has isolated names/stores/identity.

Children share root run_id, registry, capabilities, scheduler, journal, resource permits, budgets, and cancellation. Charge real instantiation/evaluation cost without resetting steps/nodes/time or creating private worker pools. Every perform uses §10. Inputs/gates must be ready before entering; unselected calls evaluate no arguments/body and produce no calls/contributions, apart from normal static/metadata costs.

An instance has parameter entry, body region, and completion boundary. Success requires all selected nodes Completed/normal Skipped, all nested regions settled, and valid return annotation/Data. Early return readiness is insufficient; unused audit/write/join work cannot be dropped. Valued results publish only after complete success, and void calls also wait. Pipeline later arguments/body cannot overlap previous remaining work.

Independent Flow graph nodes, including graph if/for blocks, may overlap under dependencies/contracts. Join consumers wait for every input invocation to finish. Calls in value blocks/carry/comprehensions/ordinary arguments preserve the caller's left-to-right local cursor: no subsequent local expression/statement executes before child completion, and local bindings cannot become independent nodes. Internal subgraphs use §7 and the parent concurrency limit. A cursor waiting for a child stays Running but yields WorkerAdmission; resumption reacquires it. Parallelism 1 MUST progress, without private blocking schedulers.

Child failures preserve original Position/EvalPath/error_site/root cause; boundaries/consumers propagate rather than invent smaller caller errors. Argument evaluation errors belong to their original token; boundary annotation/Data errors use invocation_site. Pipeline stage argument/result boundary checks use the stage target's first token, while fn body errors retain their body token. A child's own return error always belongs to its return node, even if discarded. F/K span parent/child completion dependencies without separate fail-fast behavior. Cancellation reaches all unfinished instances; started calls/uncertainty follow §10/11. No implicit rollback/compensation/retry occurs. Business `{ok:false}` or 401/403 Data does not fail regions or close stages; control it with if/match.

Select Main explicitly in this example; Left/Right may overlap, SumFields waits for both:

```lip
flow Left(value:number) -> number { return value + 1 }
flow Right(value:number) -> number { return value * 2 }
flow SumFields(parts:object) -> number { return parts.left + parts.right }
flow Main(value:number) -> object {
    left = value |> Left
    right = value |> Right
    total = {left:left, right:right} |> SumFields
    return {left:left, right:right, total:total}
}
```

value=3 yields `{left:4,right:6,total:10}`. Linear `value |> Left |> Right` yields 8, without implicit branching/aggregation. Replacing a stage with external work retains effect/barrier restrictions.

## 7. Graph scheduling and errors

Node states are Pending, Running (including waiting for children/resources), Completed, Skipped, Error, and Cancelled. The last four terminate the current attempt. Skipped distinguishes normal gates/break/continue from failure/cancellation cascades. Ordinary data dependencies require Completed values; being terminal does not create null.

For Pending n, absent constraints count as true:

| Predicate | Requirement |
|---|---|
| DataDepsCompleted(n) | All ordinary inputs Completed with usable values |
| GatesOpen(n) | Selected control decisions, not closed by break/continue |
| ContextActive(n) | Context active and n retained by error truncation |
| AfterSatisfied(n) | Host completion constraints satisfied |
| OrderAllows(n) | Source order/barrier/region completion permits execution |
| WorkerAdmission(n) | Available scheduler worker allowance, distinct from provider resources |

```text
Ready(n) = DataDepsCompleted(n) ∧ GatesOpen(n) ∧ ContextActive(n)
           ∧ AfterSatisfied(n) ∧ OrderAllows(n) ∧ WorkerAdmission(n)
```

WorkerAdmission counts active computation/provider calls, not all Running contexts. Waiting containers/permit waiters release allowance; resumable Running cursors compete for it once children/resources and gate/context/After/order conditions permit, without returning to Pending or repeating their prefix. Coordinate permits/admission so waiters cannot exhaust workers needed to release resources. With no errors/budgets/cancellation and eventual resource availability, scheduling and permits MUST be fair.

Sequential mode advances one leaf evaluation/cursor at a time, selecting the smallest Position among ready/resumable work; waiting ancestors do not compete or occupy workers. Multiple containers may remain Running. Every mode preserves Earlier order. Node effects conservatively combine all possible performs; Concurrent ReadOnly requires every possible operation to declare both. Write/Unknown/nonconcurrent ReadOnly nodes are barriers: wait for every earlier perform-containing node to succeed/normally skip; later perform-containing nodes cannot cross unsettled earlier barriers. Concurrent ReadOnly may overlap. Pure/join nodes are not blocked solely by external barriers.

OrderAllows MUST account for possible work in uninstantiated earlier loop/branch/child regions, not just created/Ready nodes. A barrier after a producer loop waits for completion/normal gate closure, including future iterations. Earlier barriers constrain later parallel regions. Structural prefixes/static regions establish this in all modes. Node-local performs admit left to right; conservative summaries may reduce overlap but never reorder. Child summaries constrain caller regions until complete; yielding workers does not release sibling barriers. Ancestor exclusion prevents self-completion waits while descendants inherit all outside prerequisites and local cursor order. Writes cannot pass earlier concurrent reads. Infinite earlier producers may permanently delay barriers; change graph structure rather than weaken order.

Host After constraints use §16.1 AfterTarget_wire resolved in this run's frozen metadata, rejecting foreign runs/nonexistent source objects. Legal unexpanded instances retain symbolic completion conditions. Targets may be later positions and constrain entire dynamic regions/children. Explicit After has no ancestor exemption: a descendant waiting for full ancestor completion forms a cycle. Check cycles before/after expansion. Completed targets satisfy After; normal Skipped under the same closed gate also satisfies it. Error/Cancelled/other Skipped causes `Skipped(after_failure)`. Gate closure takes precedence and never creates values. Reject mandatory cycles combining data/control/full-read/barrier dependencies statically; dynamic After/expansion cycles are E-AFTER-CYCLE/E-GRAPH-CYCLE, never endless Pending. Failed dependencies cascade appropriate Skipped/cause chains. A normally skipped data target has no value; a consumer outside the same closed gate is E-DEPENDENCY.

Root errors arise from actual selected evaluation, not propagated dependency/region failure. For a root err, use its owner Position, occurrence EvalPath, and originating token error_site (perform_site for perform errors):

```text
ErrorKey(err) = (Position(err.node), EvalPath(err), err.error_site)
```

Compare Position, then EvalPath, then integer error_site. The primary error is the minimum root ErrorKey. Error ordering retains ancestor prefixes: failed children propagate their cause without smaller wrapper roots. Caller errors before a call/after success retain their own occurrence positions, regardless of observation time.

F is the least currently active root ErrorKey, absent when none. Confirm its primary error only after every possible node with Position≤F.Position and every prerequisite settles. Infinite prefix computation may delay confirmation; timeouts do not prove absence of earlier errors. Budget/cancellation have independent termination rules.

K is this structural prefix plus its full transitive prerequisite closure: data, control, After, full-read producers, barriers, loop/child entries/completion, and unexpanded/future instances. Retain prerequisites even beyond F.Position. Implicit order edges use Earlier without false ancestor-completion prerequisites; explicit/data/completion edges remain real. Once a failed child makes caller success impossible, its no-longer-needed successful-completion dependencies may be removed, but all possible earlier errors/dependencies remain. A short caller prefix does not automatically retain every later child or bypass error_cut.

Outside K, Pending becomes Skipped(error_cut), admitting no new calls. Inside K, continue Ready work to determine earlier errors. Already-started providers record their outcomes without automatic internal-error interruption; outside-K cursors stop after their current call. Other work may cooperatively stop. Smaller errors recompute F/K; trace retains all observed errors/calls. error_cut derives from active errors and is not permanent history. Core errors remain active within the attempt; durable uncertainty follows §11.1. Failed runs may have schedule-dependent call sets/partial stores/traces because later reads may start before earlier errors appear. Cancellation is cooperative. Success requires every selected node successful/normal Skipped and valid output; return never drops unused perform work.

### 7.1. Required scheduling metadata and reference algorithm

Compilers MUST retain a region tree containing branch candidates; loop/child entry/body/completion; source ranges; potential perform/barrier/writer sites; transitive summaries; invocation origins and defining tokens. A region completes only after selected work succeeds/normally skips with no future iterations/unfinished children. Failure carries its root cause. Any implementation MUST be observably equivalent to:

```text
1. Build region summaries, full-read producers, data/control/After/order conditions; reject cycles.
2. When opening gates/expanding loops/children, add instances/positions while preserving completion tokens for unexpanded regions.
3. Propagate gate/failure skips, then update F and prerequisite closure K.
4. Select only allowed Ready/resumable work; sequential mode picks minimum Position, parallel mode is fair.
5. Admit/record each live perform; join by atomic union; update region completion conditions.
6. Repeat. Publish output on complete success, or primary error after its prefix settles, draining started calls. Durable provisional errors terminate attempts subject to §11.1 finality.
```

An earlier failed region makes barrier-dependent calls Skipped(order_failure), not permanently Pending. Later After targets remain in K. Position-only pruning without closure is nonconforming. Dependency waiters stay Pending and resource/cursor waiters Running, without extra lifecycle states.

## 8. Claim stores: join-only information

A bounded-below join-semilattice has a least element and a least upper bound for each pair. Here bottom ∅ is the empty contribution set and join is union. `facts=claim()` is allowed only at the owning Flow's top level, including child Flows. A Claim is a record; the store is the semilattice, without an arbitrary infinite-join promise.

Each `facts <+ value` atomically contributes `(JoinID,C(value))`. RHS MUST be Pure Data and may reference completed Recorded bindings. Declaration initializes ∅; every join/read/snapshot depends on declaration completion and a declared store. Ordinary names cannot be join targets; stores cannot be redeclared/reassigned. Runtime identity includes run_id, InvocationPrefix, and declaration node_site, not just name. Parent/child/sibling stores are isolated. Pass complete Data snapshots/results across calls and join them explicitly; active stores never cross boundaries and all producer constraints remain.

There is no overwrite/delete/retract/last-arrival-wins/custom merge. Revisions are new Data facts, e.g. revision kind, old snapshot.id, and replacement data, interpreted by explicit pure projections/Host contracts; projections need not be monotone.

Let A be canonical atoms `(JoinID,C(value))`, and `L=𝒫_fin(A)` the finite subsets. x/y are states, a an atom, ⊆ inclusion, ∪ union, ⊑ information order, ⊔ least upper bound, and := internal Runtime update:

```text
bottom = ∅
x ⊑ y iff x ⊆ y
x ⊔ y = x ∪ y
join(store, a): store := store ⊔ {a}
```

Union supplies associativity, commutativity, idempotence, and least upper bounds for finite joins. Infinite execution need not reach a final state or fit in finite memory.

Same JoinID/same value is deduplicated delivery; equal values with distinct IDs are distinct events. Same ID/different value is a replay identity conflict. Keep both atoms and an irreversible conflict marker so union remains total; the introducing join and subsequent snapshot projections fail E-CLAIM-CONFLICT, never first-arrival-wins. Running contributions may still record; the Flow cannot succeed. Business disagreement across distinct IDs is ordinary data.

At one linearization point, delivery either adds an atom, acknowledges an identical atom, or records the conflicting variant/marker and fails the introducing delivery. Preserve previous successful receipts; register the root error at the JoinID source. Complete/later live reads fail, earlier snapshots stay fixed, and terminal snapshot replay returns its historical observation. Joins become visible to live reads immediately; later writer failure does not roll them back. Stores live within the run/declaring instance. Cross-run persistence is explicit Host storage/input/effect data, never automatic contamination.

## 9. Live and complete reads

Store growth alone does not make observations schedule-independent.

| Form | Result | Graph/replay contract |
|---|---|---|
| `facts` in ordinary expressions | Complete Data snapshot | Waits for all producers; then fixed |
| `perform effect claim.snapshot("facts")` | Atomic snapshot of committed atoms now | Recorded ReadOnly observation; no future-writer wait |

Snapshots are lists of `{id:string,run_id:string,position:list,value:Data}`, sorted by original Position/JoinID, never digest/arrival time. position uses §16 wire pairs and requires the run's source/revision/region metadata for interpretation. id is lowercase SHA-256 of the §16 JoinID encoding. Snapshots are immutable, not live handles. If distinct canonical identities share a digest, preserve originals and fail E-ID-COLLISION, never merge/select one. Formal comparison keys cannot store only digests.

claim.snapshot's first argument MUST be a string literal naming a top-level store in the current Flow. Resolve its declaration site statically and combine current InvocationPrefix at runtime; repeated child calls cannot reuse a name-only cached store. No access to parent/sibling/other-run/Session stores is granted. This built-in uses ordinary effect/perform without import, resolving to `["lip","claim","snapshot"]`; claim is reserved, not an alias, and lip is not an importable external module type. Its sole parameter is store; result is the record list; descriptor is Concurrent ReadOnly/Recorded without provider resources. Pure fn cannot construct store snapshots. Snapshot/join have atomic linearization points, never partial records/unions.

A complete read MUST follow every possible writer's outermost producer statement and MUST NOT occur inside a writer-containing loop; otherwise E-CLAIM-ORDER. Check every static writer, even mutually exclusive branches. Later sequential-loop reads are allowed if every writer is earlier/outside that loop. Empty stores yield `[]`; unselected/break-skipped writers contribute nothing. Writer failure/cancellation/failure-skips fail complete reads/Flows, not empty success. Preserve failed producer causes, including uninstantiated failed regions. Store use in expressions/containers/fn arguments/return means complete Data read; only declaration/join LHS/snapshot's dedicated string resolve store identity.

Live snapshots may feed if/match/fn/proposals/joins with their observation journal retained. They may change subsequent branches/calls/contributions; replay preserves the observation, not equality across live schedules. They provide no source-prefix isolation: even within sequential loops, later joins may arrive before a read because external barriers alone do not block joins. §13 excludes all live snapshots. carry explicitly represents local iteration state.

```text
import python "application" as worker

flow Collect(items: list) -> object {
    facts = claim()
    for item in items parallel {
        result = perform effect worker.process(item)
        facts <+ {input: item, output: result}
    }
    seen = perform effect claim.snapshot("facts")
    return {complete: facts, seen: seen}
}
```

Register version-1 Concurrent ReadOnly `worker.process(item:Data)->Data`. seen may finish before all writers; complete must wait. A live snapshot observes committed atoms at its linearization point, cannot predict future contributions, wait on a business predicate, or automatically reevaluate. Complete reads wait for all producers; there is no additional threshold wait or implicit fixed-point mechanism.

## 10. Go/Python call protocol

External go/python/host imports establish namespace/alias dependencies without executing code, installing packages, or granting permission. LIP source imports use §2.2 instead. Omitted aliases use the final `/` or `.` segment; require as when that is not an identifier. Aliases are unique per file and resolve independently per module. Core aliases only resolve operations; the generic profile may lower value uses to module-reference performs, never pass native modules directly into fn. Go operations use registered functions/methods/generated adapters; Python module/path/method mappings may expose installed packages without a compiler package allowlist.

CallID follows §6.2. contract_version identifies the registered contract. Arguments are fixed Data; ErrorData is `{code:string,message:string,details:Data}`:

```text
Request  = {CallID, operation, contract_version, positional: list<Data>, named: object}
Response = Result(Data) | Rejected(ErrorData) | Uncertain(ErrorData)
```

Descriptors MUST declare name/version, parameter/result schemas, ReadOnly/Write/Unknown, Concurrent, capabilities, and resource requirements. Validate before invocation; never infer purity/concurrency/idempotence/authorization from names. Named arguments follow positional ones and bind once against schema. Proposals retain declared resource references rather than native objects. Construction invokes no provider, writes no intent, and consumes no call budget.

Canonical operation identity is `[language,import_path,member_path]`, with the decoded import path and dot-joined ASCII members after the alias. Ordinary operations require an imported external alias and at least one member; `effect worker(0)` is E-OPERATION. A module is not implicitly callable. Unknown aliases/members/versions/builtins are static E-OPERATION; dynamic proposals still validate complete identity. The reserved claim namespace provides only snapshot, resolving to `["lip","claim","snapshot"]`, without alias override/extension. Contract identity uses Data strings, never function addresses/repr. Schemas cover Data subtypes, named fields, positional order, required/optional parameters, and return shapes. Reject unregistered schemas. Unanalyzable native signatures may use explicit generic Data schemas with Unknown, never default Pure. Versions are nonempty and change whenever call/recovery semantics change. SDK/transport/package/schema-file formats are Host choices; protocol semantics/Data/identity are shared.

Live perform order is: resolve proposal → registry/schema checks → capability/approval/budget/cancellation checks → atomically acquire provider resources → record intent (persist for durable) → invoke → record external fact and copy/validate result → record terminal outcome/uncertainty → release → publish Completed value on success. CallID journal execution is exclusive; duplicate concurrent deliveries wait/read one outcome. Admission rejection invokes nothing. Approval wait keeps the node Running with a cause and remains cancellable. Authorization covers operation and resource scopes.

Admit each node-local perform left to right; do not pre-acquire resources for later calls. Acquire the whole fixed `(resource_name,nonnegative_units)` vector atomically, never hold a subset while awaiting more. Release on success/failure/cancellation independently of closed gates. No-demand calls acquire no fake permits. Terminal replay acquires no provider permits. Separate worker allowance/provider permits so waiters cannot block work needed to release resources. Publish budgets/limits.

Go errors/Python exceptions designated as failure channels cause node Error. Adapters classify panic/crash as definite failure or Uncertain. Provider Rejected is E-EFFECT-REJECTED; known-completed conversion/schema faults preserve E-VALUE/E-SCHEMA; unknown outcomes are E-UNCERTAIN. Schema-valid `{ok:false,...}` remains Result. Known rejection does not prove no prior changes. Write crashes/timeouts/disconnections/cancellation/conversion failures MUST be Uncertain unless external outcome is known; never record “not executed.” Separate external completion facts from LIP validation:

```text
ProviderFact = KnownSucceeded(receipt:Data) | KnownRejected(receipt:Data)
             | Unknown(details:Data)
Validation   = Passed(Data) | Failed(ErrorData, raw_data?:Data) | NotApplicable
TerminalKind = result | rejected | known_fault
```

| External fact / validation | Language outcome | Journal |
|---|---|---|
| KnownSucceeded / Passed | Result, Completed | terminal result |
| KnownRejected / NotApplicable | Rejected, E-EFFECT-REJECTED | terminal rejected; preserve prior changes |
| KnownSucceeded / Failed | Rejected, E-SCHEMA/E-VALUE | terminal known_fault with receipt/validation error |
| Unknown / NotApplicable | Uncertain, E-UNCERTAIN | nonterminal intent + uncertainty |

known_fault travels as Rejected(ErrorData); trusted Host classification, not a provider's claimed error code, establishes it. Receipts describe completion evidence without requiring transaction IDs. Optional raw_data must already be legal Data, never pointers/mutable objects. Replay known_fault with the same error and zero provider calls; it cannot reconcile to success or redo a write to repair formatting. Compensation/correction is an explicit new operation.

Go adapters handle context, finite values, structs/string-keyed maps/lists, declared multiple returns/errors. Python workers handle the same Data/exceptions/IDs/deadlines/cancellation and bounded queues. Wire numbers are binary64 even over JSON, not Python int/float guesses. Default mapping is Go float64/Python float; integer parameters need explicit validated safe-integer contracts. bytes require declared string encoding (base64/hex) or resource references, never platform charset assumptions. Tuples may map to lists by contract; sets/native callables/arrays/DataFrames need explicit Data/reference conversion, not default str/pickle.

Resource references are unguessable Data-object tokens interpreted only by accepting schemas and bound to Host/worker generation, type, lifetime, and authorization scope. Declare application/run/session ownership (§10.1). Sessions may retain objects across successful cells without replaying old cells. Pure code only sees the Data reference. References confer no permission; reject expired/wrong-scope/restarted/unmigrated objects and provide release/end-of-life cleanup. Process-lived package initialization does not keep references valid. Durable replay must reconstruct/reject/reconcile invalid native references, not accept them as live.

```text
import go "crypto/sha256" as sha
import python "numpy" as np

flow Statistics(xs: list) -> object {
    mean = perform effect np.mean(xs)
    digest = perform effect sha.Sum256(str(xs))
    return {mean: mean, digest: digest}
}
```

This example explicitly registers version-1 Concurrent ReadOnly np.mean over finite number lists and sha.Sum256 over UTF-8 string bytes with a 32-byte result encoded as 64 lowercase hex characters. These are example-specific conversions, never generic defaults. Callback/stream/transaction/large-object contracts belong to adapters without another LIP computation model.

A generic profile such as NewNativeHost may use Go export/type information or persistent Python workers without limiting package names. Before core checking it lowers direct calls/properties/methods/indexing/resource conversions into registered Unknown proposals/performs, without making native callables pure, bypassing authorization, or inferring Concurrent. Resolve callees and available signature/arity information before arguments; when signatures are unavailable, actual invocation checks them and the adapter declares that limitation. Merely constructing effect does not load code or run native checks.

In this profile, an explicit `effect receiver.member(args)` may use any lexically bound receiver, including parameters of functions/Flows in imported modules and saved session values. The profile lowers it to a native request carrying that receiver and member path; native provenance need not be statically known. Nested member paths resolve during perform, never during proposal construction. Saved requests keep their captured receiver when its public name is unbound or reused. Ordinary calls on objects retain core pure-call semantics unless lowered through known native provenance.

Alias value uses may lower to module-reference performs. Import/completion MUST NOT initialize packages. Go export discovery may use the toolchain/module resolver; Python importlib runs at the first authorized real operation. CLI/sessions/generated programs MUST share conversion/boundary behavior.

This implementation's generic conversion contract validates Go integer ranges/exactness and uses UTF-8 for string→[]byte. Python receives safe integral numbers as int and others as float, without changing LIP number identity. Python list/tuple/string-keyed dict become Data; Go containers/structs and other native objects remain references. native_data performs a finite acyclic Data snapshot; native_release releases references without replacing business close methods. Cross-runtime values require explicit snapshots/encoding, never foreign-token interpretation. Cancellation/disconnection cannot automatically retry possibly completed writes; reset/close invalidates old references. A trailing error in multiple Go results is the failure channel; a sole error result is preserved as a native resource (nil→null), permitting error construction/comparison. Explicit contracts may choose other mappings.

Authorization/Data boundaries are not OS sandboxes. In-process Go and Python workers retain OS privileges; Hosts isolate untrusted libraries with processes/containers. effect/perform does not automatically secure native code.

### 10.1. Host ownership and run lifecycle

Ownership decides object invalidation/cleanup; provider permits limit resource quantities per call. Contracts declare scope, owner, generation, available operations, release, and cancellation as Host metadata, not new types/mutable core containers.

| Scope | Owner/end condition |
|---|---|
| application | Shared application resources such as pools; survive individual runs, cleaned at application shutdown |
| run | Root-run temporary objects/leases, shared by its children; revoke/clean at run end |
| session | Explicit interactive Host session; usable across successful cells, cleaned at session end |

HTTP requests are run-scoped, not sessions. Do not directly share a session Native Host across concurrent requests. Pools may be application-scoped, but borrowed connections/transactions/temporaries need explicit run leases. Owner/generation-bound references cannot migrate between requests and remain valid. Children may use same-run references under contract without additional permission. Recheck capabilities per perform; do not hide objects inside Data/fn.

Success/failure/cancellation/budget termination MUST trigger Host cleanup, independently of gates/finally code. First reject new resource use, then settle/isolate started actions and record outcome/credible uncertainty before releasing leases/objects. Never label ongoing writes unexecuted or reuse leases while still in use. Preserve cleanup diagnostics/external facts without reruns. Application shutdown stops new runs before draining/isolating in-flight actions.

Reference release is distinct from business close/commit/rollback. Technical ownership cleanup does not compensate writes; business transactions/compensation/confirmation require explicit operations or one registered atomic transaction contract. Flow failure does not automatically roll back databases; MemoryJournal is neither a cross-request transaction nor dedup store.

Workers/remotes use bounded queues/pools/leases. Cancelling one run cannot kill workers serving others. Disconnection/reset/new generation invalidates references and classifies unresolved writes Uncertain, without restart-and-retry. Reconstruction/reconcile follows §11 and cannot substitute a different worker's object for the original. Shared concurrency follows declared Concurrent/scope locks/permits, not method names/read-shaped results/application sharing. No global serialization is required for proven concurrent reads with available permits; Unknown stays conservative. Wait/cancel cannot exhaust resources needed for release.

### 10.2. Static Host bindings and generated programs

Interpreter, Go generation, and embedded services use the same frozen sources, entry, contracts, and checking results. Generated targets MUST contain actual Go evaluation/graph code, never feed original LIP strings to an interpreter. Module symbols/invocation origins/type/effect metadata are fixed at build time; Program metadata initializes once, then stays read-only across runs, with no request/replay AST rewriting. Application pool creation/closure is Host lifecycle. Package initialization belongs to an explicit startup contract or authorized operation, never source-import resolution, and grants no implicit permissions.

Static Go adapters register at build/startup; calls do not load plugins/download dependencies. Hosts lock Python environments/worker configuration without request-time installs/upgrades. Actual loading retains authorization/perform rules. Every execution path shares native lowering/resource/schema mapping and Unknown/permission checks.

Same canonical operations across modules MUST bind compatible identical contracts. Conflicting version/schema/effect/resource descriptors reject before runs as E-OPERATION. Services resolve all route entries/sources/contracts before requests; each request owns run/context/journal while sharing application resources/read-only Program. Packaging/deployment/CLI formats add no language execution path/syntax.

## 11. Journaling and optional durable recovery

Every run MUST record all perform argument fingerprints and result/rejection/uncertainty during that run, especially snapshots/clocks/randomness. Recorded values publish only after recording. Core does not promise crash recovery. Claiming durable requires all persistence rules here, including upstream observations rather than only writes. §16 C/wire rules apply to core too.

An intent records a request before provider invocation. A checkpoint completely snapshots recoverable nodes/gates/contributions and their journal high-water boundary. A recovery epoch identifies each exclusive execution attempt; fencing prevents superseded owners from publishing. Epoch is not logical Position/CallID. Provider DedupKey is a contract-recognized stable key, never inferred from equal arguments.

For a new run, select entry and validate §2.3 input before begin allocates/persists run_id; rejection creates no journal/intent. Durable metadata fixes language version, run_id, root SourceID, selected declaration, validated full EntryInput, exact §6.2 revision, SourceTable/DependencyLock, and Host contract versions before any intent. Dependency/version/comment changes change revision. Resume preserves entry/prefixes/run_id; changing entry/root/language is E-REPLAY-DIVERGENCE before any new provider admission. One fenced owner executes a run; old owners cannot publish after takeover. Incompatible recovery rejects, while completed Data archives remain valid independently of future sources. New runs use new revisions; Host state schema migration is separate.

Journal keys are complete CallIDs. canonical_arguments stores full `C([positional,named])`; provider facts/validation/terminal kinds follow §10:

```text
Journal[CallID] = {operation, version, argument_fingerprint,
                   canonical_arguments, intent, provider_fact?, validation?,
                   terminal_kind?, result_or_rejection?, uncertainty?}
```

| Existing record | Recovery |
|---|---|
| Different arguments/operation/version | E-REPLAY-DIVERGENCE; no new identity to conceal it |
| terminal result | Reuse value; zero provider calls |
| terminal rejection | Replay the same node Error; zero provider calls |
| terminal known_fault | Reuse completion evidence and same E-SCHEMA/E-VALUE; zero provider calls |
| intent only, ReadOnly with explicit permission | May reread and record; no prior result was published |
| intent only, Write with stable provider DedupKey | Resume invocation only under a contract proving safety |
| Other intent-only Write/Unknown | Fail closed; reconcile external facts before further action, never blindly retry |

Recoverable invocations retain original CallID. Persist results before publishing, so downstream durable writes cannot legitimately consume an unrecorded read. DedupKey does not replace CallID. Exactly-once external behavior is not a language guarantee. Uncertain remains nonterminal; automatic ReadOnly recovery also requires explicit no-write/reexecution permission. Invalid native-token terminal results may be displayed historically but cannot validate as active resources; explicitly restore the same identity/change the recovery contract or terminate.

### 11.1. Node states and journal states are separate machines

| Current-attempt node | Journal | Meaning |
|---|---|---|
| Pending/Running | absent/intent | No published value; Running does not prove no provider execution |
| Completed | terminal Result | Recorded value available to consumers |
| Error(E-EFFECT-REJECTED) | terminal Rejected | Known failure replayed identically |
| Error(E-SCHEMA/E-VALUE) | terminal known_fault | External completion with failed validation |
| Error(E-UNCERTAIN) | nonterminal intent + uncertainty | Attempt evaluation failed, external fact unresolved |

Nonterminal describes the journal, not the node's terminal Error in that attempt. After reconcile, resume creates new Pending/Running instances with unchanged logical Position/CallID, never mutates old terminal nodes into Completed. Durable storage retains root-error ledger/attempt history, distinguishing permanent errors from provisional E-UNCERTAIN tied to pending CallIDs and final failure from recoverable failed attempts.

Recovery rules:

1. Ordinary root errors, terminal rejections, and known_fault are permanent, retaining complete ErrorKey/category/details; their presence prevents eventual success.
2. E-UNCERTAIN is active only while its outcome remains unresolved. Trusted Result removes it; trusted rejection/known_fault records the appropriate permanent root at the original identity/site. Retain history.
3. Recompute F/K from active errors. Old error_cut is not a tombstone. Work cut/failure-skipped solely by resolved provisional errors may get new-epoch Pending instances under original identities, subject to other active errors. Normally unselected gates do not reopen.
4. Preserve every terminal outcome/started external fact, including later permanent rejections. Never erase calls or rewrite old terminal nodes.
5. If the F prefix/prerequisites may reevaluate after uncertainty resolution, final primary error is not confirmed. A permanent error guarantees failure, not which primary error. Confirmed final failure cannot become success; unrelated unresolved actions still require draining/reconcile.

Recovery example: four independent nodes under one run, with all earlier nodes successful and no added dependencies/barriers:

| Node | Position | Attempt state |
|---|---|---|
| uncertain_call | [(5,0)] | E-UNCERTAIN, unresolved Concurrent ReadOnly call |
| check_a | [(6,0)] | Skipped(error_cut), unevaluated Pure computation |
| check_b | [(7,0)] | Skipped(error_cut), unevaluated Pure computation |
| later_call | [(8,0)] | Concurrent ReadOnly call started before cut; later terminal Rejected/permanent root |

Reconcile uncertain_call to Result: remove its provisional root, retain later_call, and recompute F/K so check_a/check_b may evaluate in a new epoch. If both/earlier prefix succeed, later_call is primary; if check_a fails E-FAIL, it is primary. Both outcomes reuse recorded calls without provider reruns. Checkpoints MUST preserve errors/associated CallIDs and derive cuts from the active ledger, never regard provisional skips as permanently complete or forget permanent roots.

| Durable API | Minimum behavior |
|---|---|
| begin | After valid entry/input, allocate unique run_id and persist program/full input/contracts before execution |
| resume | Acquire exclusive recovery ownership of the same run; reuse observations/identities |
| status | Distinguish Running/success/failure/pending intent/abandoned and list every nonterminal intent |
| reconcile | Under authorization/audit, resolve pending CallID with trusted facts/validation to Result/Rejected/known_fault |
| abandon | Only after all intents are terminal; preserve journal without implying rollback |

begin/resume/reconcile/abandon MUST be idempotent for repeated equal requests and durably CAS/fence conflicting concurrent requests. status remains meaningful after crash. reconcile cannot overwrite terminal outcomes; contradictory evidence fails, identical evidence only confirms, and two outcomes cannot both be consumed. known_fault is terminal even without a published successful value and counts as settled for abandon.

Rebuild stores either from empty using recorded inputs/observations, or from a complete checkpoint without recomputing restored contributions. Do not combine incomplete old stores with recomputation incorrectly; normal duplicate dedup remains, and same-ID changed values MUST fail rather than be deleted. Contributions need not be persisted individually; unsuccessful partial stores are not committed transactions. Discard/recompute partial stores if desired, but never discard their journal. Checkpoints must jointly preserve nodes/gates, contributions, journal high-water, root ledger, and epoch; derive cut anew, never save only truncated producers. Rebuild complete-read caches from complete producer states, never partial snapshots.

Divergence/pending intents prevent successful output without rollback. abandon requires trusted terminal outcomes for every intent. Reissuing the same business command must explicitly account for old facts/DedupKeys, not evade unresolved writes by changing run_id.

## 12. Core scope, libraries, and Host contracts

§2/§3.1 define the complete syntax/pure functions. Static modules share fn/Data/named Flows (§2.2); structured workflows use §6.3, without runtime loading/initialization.

| Capability | Boundary |
|---|---|
| Persistent state, sessions, transactions | Host storage, with ordinary Data input/output |
| Retry | Explicit Host operation contract, or business for with `{ok:false}` |
| Iterative computation | Pure recursion/tail calls/fold; for/carry for external work |
| Reusable external workflows | Child Flows and §2.1 sequential pipelines; dependency graphs for branching/aggregation |
| Queues, long-lived events, background tasks | Host contracts/lifecycle; no core inbox/subscription/autostart |
| max/min/set/any/all/count/bag/log/latest | Pure/library projections over complete snapshots |
| Retraction / active views | New revision facts plus projection, or Host persistent stores |
| Threshold waits | Outside core; live reads once, complete reads wait for every producer |
| Trace, provenance UI, caching, approval, rate limits | Runtime policy/products respecting core contracts |
| External calls | Only effect/perform, including lowered native conveniences |

count/bag retain event identity: equal payloads with different IDs count separately, identical replays once. Logs order by Position; floating-point sums use explicit left folds without assumed associativity. No separate mutable reducer slots are needed. latest requires an explicit rule: last source-position record, or business timestamp/field with total tie-breaking, never last arrival. Uniform UTC/same-precision ISO strings may sort temporally; arbitrary timezone/precision strings do not acquire time semantics. Use keys/has/put/fold for object/set projections.

Safe object dispatch uses type patterns and short-circuit field checks:

```text
fn payload(x: any) -> any {
    match x {
        object if has(x,"kind") && x.kind == "a" && has(x,"payload") => x.payload,
        _ => null
    }
}
```

has does not prove a business field schema; later calls still check declarations. Pure for supports per-item validation/discarded-call errors; accumulation uses fold/carry, never shadowing as mutation.

Interpreter/codegen and parallelism 1+ are allowed; every provided path must pass core conformance. Parallelism 1 still requires parallel static checks, regions/dependencies/barriers/F/K/Claims/identity, not fail-fast sequential rewriting. After may default empty, but accepted constraints MUST be enforced. Core has no deterministic parallel early circuit breaker/custom lattice containers. For exactly three recoverable business failures before stopping, use sequential carry with schema-valid Result data. Parallel programs fully collect then decide, or explicitly accept live-snapshot timing semantics.

Recommended structure is parallel ReadOnly → join → complete read → sequential Write:

```text
import python "application" as worker
import go "application/storage" as storage
flow Save(items: list) -> object {
    facts = claim()
    for item in items parallel {
        result = perform effect worker.compute(item)
        facts <+ result
    }
    results = facts
    for record in results { perform effect storage.save(record.value) }
    return {saved: len(results)}
}
```

Register version-1 Concurrent ReadOnly `worker.compute(item:Data)->Data` and Write `storage.save(value:Data)->null`. Business failures must be schema-valid Results to count/branch. Runtime errors are neither implicit Claims nor caught values; adapter exception-to-business conversion requires a declared Host contract.

### 12.1. Optional HTTP Host integration

HTTP is a Host profile outside core, adding no syntax or Flow/Claim/identity/error semantics. Receive a request, convert it to entry input, and only after complete validated success commit status/headers/body. Receiving/sending are run lifecycle, not source operations; database/network/clock/log work inside a Flow still uses perform.

Entries MUST be `flow Name(request:object)->object`. Startup binds explicit-method Go ServeMux patterns (possibly parameters) to precompiled, entry-checked, conflict-checked Programs before accepting requests. One read-only Program may serve many requests. Entries are fixed at startup using the §2 defaults or an explicit configuration; requests cannot select arbitrary entries/sources/operations/registries. Children remain within the request run and produce no separate HTTP responses.

Missing explicit methods are E-HTTP. Hosts may reuse ServeMux path/parameter matching but MUST implement exact configured method dispatch and 405/Allow themselves, not inherit ServeMux method matching/405 behavior. HEAD matches only explicitly configured HEAD and MUST NOT execute GET. Without a routing redirect, a host/path match ignoring methods but no matching method yields 405; no host/path match yields 404. Neither creates runs/Flows/providers. Allow contains all explicit host/path methods, deduplicated and UTF-8 sorted, joined by `, `; GET never implies HEAD. Explicit HEAD uses its own entry/effect contract.

Accept origin-form/absolute-form targets. Absolute form uses URL path/query and server-validated authority; empty paths become `/`. Reject asterisk-form (including OPTIONS *) and authority-form as 400 before routing/runs. Go servers MUST disable/replace default general OPTIONS handling, e.g. DisableGeneralOptionsHandler=true. Split encoded paths on literal `/` before percent-decoding segments; `%2F` stays within one segment.

| Request field | Data form |
|---|---|
| method / path / host | Method, decoded path, Host UTF-8 strings |
| raw_path | Target path without query, preserving percent encoding and `/` versus `%2F` |
| params | Parameter-name→string object |
| query | Decoded key→string-list object, retaining duplicate values; reject bad encoding |
| headers | Lowercase name→string-list object; proxy headers confer no trusted identity |
| body | `{encoding:string,data:string}`; utf-8 for valid UTF-8, otherwise standard base64 |
| json | Nonempty application/json or application/*+json decoded to Data; otherwise null |

In Go use r.URL.EscapedPath(), default `/`, never possibly empty RawPath. Matching `/{x}` to `/a%2Fb` yields path `/a/b`, raw_path `/a%2Fb`, params.x `a/b`, one parameter segment.

JSON accepts UTF-8 charset only and rejects duplicate keys, nonfinite numbers, invalid Unicode, trailing content, and `$lip.native` in any nested request/response-json object. HTTP cannot import/publish native references. Applications still validate business schemas. Empty-body json is null. Keep original body plus parsed json; raw bytes count toward body limits, and base64 expansion/copies/full EntryInput memory count toward published Data/memory budgets.

Successful EntryOutput.value MUST contain only these response fields, all validated before commit:

| Field | Rule |
|---|---|
| status | Required integer number 200–599; no informational/upgrade responses |
| headers | Optional name→string/string-list object; reject case-insensitive duplicates, invalid names, control characters |
| json | Optional Data, canonical JSON; exclusive with body; default application/json |
| body | Optional string; default utf-8 and text/plain; charset=utf-8 |
| encoding | Only with body; utf-8/base64; decoded base64 defaults to application/octet-stream |

Hosts control Content-Length, Transfer-Encoding, Connection, Trailer, Upgrade, Keep-Alive, Proxy-Connection, and TE; Flow setters are forbidden. 204/205/304 cannot have nonempty bodies. Explicit HEAD computes/validates full response and sets Content-Length from that body without sending it; omit that header for 204/304. Responses are complete and bounded, without streaming/WebSocket/multipart upload; these need separate lifecycle/resource contracts. Triple quotes provide no HTML safety; template libraries escape explicitly.

After routing/conversion/EntryInput validation, each request gets fresh run_id, MemoryJournal, Claims, budgets, and cancellation, never fixed IDs/shared journals. The registry is run-fixed and shared operations obey concurrency/resource contracts. Trusted authentication middleware may attach identity to context, read by Authorize/operations; HTTP Data grants no capabilities. Embedded applications own Hosts/pools; requests do not close shared pools. Generic native bridges require §10.1 request leases/ownership/cancellation, never shared REPL Sessions.

Response 401/403 is ordinary Data, not a gate or early return. Use if/match to guard actions and Authorize per external admission. Final top-level return retains §2 completion rules.

Finite defaults: 30-second deadline, 1 MiB request and encoded response bodies each, 64 active requests across routes; per-run parallelism 4, steps 1,000,000, elements/nodes 100,000 each, non-tail frames 10,000. Positive overrides are allowed; negatives are configuration errors and zero selects defaults, not infinity. Reject capacity immediately without queues. Servers also bound read time/header size. Blocking native operations must cooperate with context; response timeout cannot interrupt arbitrary native code.

Invalid query/JSON/Data→400; oversized input→413; invalid media type/JSON charset→415; capacity→503; deadline while writable→504; server interruption while writable→503. Client disconnect/cancel stops unstarted work and records cancellation without requiring a response or synthesizing 408. E-CAPABILITY→403; other execution errors/invalid responses/oversized output→500. Public errors expose fixed categories only, never source/journal/diagnostics/internal addresses; full Reports may go to server diagnostic hooks. Profile configuration/conversion/response errors use E-HTTP without inventing core errors. HTTP statuses change no completed/Uncertain facts. Never automatically retry/claim rollback on failure/disconnect/validation error. Failed writes after response commit can only be reported, not followed by a new status. Durable support is separately declared.

### 12.2. Host messages and independent runs

Within-run reuse/waiting uses child Flows. Messaging between processes/background tasks/long-lived events belongs to Hosts. Core defines no mailbox, subscription, fire-and-forget, auto-executed receiver Flow, or pipeline delivery semantics. Send/receive/ack/status operations use registered effect/perform with Data/effect/permission/resource contracts. A receive that consumes/acks/changes queues is not Concurrent ReadOnly merely by name. Send success means only the declared delivery/receipt acknowledgement, not consumer business completion. Explicit Host status/result operations or same-run child Flows provide business waiting.

Hosts may turn events into selected-entry inputs, each with a new independent run_id/Claims/budgets/cancellation, never a pretend child of the sender. Use explicit business/correlation Data keys across runs. Delivery/dedup/idempotence/ack/retry/recovery belong to contracts; CallID/MemoryJournal guarantee neither cross-run dedup nor exactly-once business execution.

### 12.3. Reusable business-library value/effect boundaries

Validation, business Results, templates, and databases use §2.2 modules/existing values/operations, without business syntax, implicit globals, or extra effect-bypassing channels. Exported interfaces/contracts declare inputs/outputs/failure channels/ownership. Built-in libraries are not required.

| Capability | Contract |
|---|---|
| Input/schema validation | Static Data descriptions + pure fn, or explicit Host schemas; any/has do not exempt dynamic checks |
| Business Result | Ordinary `{ok:true,value:...}` / `{ok:false,error:...}` Data; no new type/catch syntax |
| Text/templates | Context-specific escaping by pure fn/library; triple quotes are plain strings without interpolation/dedentation/HTML safety |
| Database/query/write/transaction | Registered schemas/effects/resources/transactions; application pools and run leases |
| Cross-request idempotence/state | Explicit business keys + Host storage/transactions, not equal arguments/MemoryJournal |

Pure fn can construct reusable single-step proposals, whose results remain Unknown Ops. Multiple external steps use child Flows. Atomic multi-step transactions may be one explicit Host operation with legal Data results; child/pipeline sequences are not automatic transactions. Exceptions become business Results only under declared adapter conversion, otherwise normal errors. 401/403/ok:false still need explicit gates for later actions.

## 13. Determinism and guarantee boundaries

Assume correct pure primitives, honest adapter contracts, and enforced value boundaries. Fix run_id, selected entry, input, complete sources, registry/schemas/authorization, After constraints, initial journal, and a response mapping R from complete CallID plus canonical request to Host response. Exclude live claim.snapshot, cancellation, deadline/budget failure, crashes, invalid resources, and changed-value duplicate deliveries. Every selected node/call terminates finitely, scheduling is fair, and equal deliveries carry equal values. Pseudorandom scheduling tests MUST derive responses from CallID/canonical request, not consume one global arrival-order random sequence.

Under these premises, all legal schedules yield the same successful Data or primary ErrorKey/category/semantic details. Successful executions have the same evaluated CallID set. Failed executions need not have equal issued-call sets, partial stores, other traces, or timings. R is a comparison premise, not a promise that real remote services always agree. Earlier divergence may prevent primary-error confirmation. Changed cancellation/budgets/versions are changed conditions. Propagation adds no smaller wrapper roots. Closed gates and unselected expression branches evaluate nothing, create no dynamic errors/F/K/cuts, and are distinct from compile errors on invalid programs.

1. A pure small step has at most one successor; termination has a unique value, while infinite recursion may diverge.
2. A finite acyclic pure graph with fixed inputs yields the same value under fair, uninterrupted schedules.
3. For a fixed finite successful-contribution atom set B, delivery order/duplicates yield the same final store B and deterministic projection. Changed values at one identity conflict rather than selecting arrivals.
4. Programs without perform cannot call Hosts; performs without completed admission cannot call providers.
5. Durable recovery reuses journaled observations and never automatically repeats unresolved writes.

Live observations can change control/contribution set B. Journaling preserves the actual observation but does not equate different live schedules. Store growth does not guarantee monotone business projections or whole-program transactions.

Failure counterexample: independent earlier early and later late are Concurrent ReadOnly; R fixes early=Rejected and late=Result. If early fails before late starts, cut late; if late already started, both calls occurred. Both runs report early as primary, but their call sets differ. Ordering errors does not undo calls.

## 14. Conformance obligations

Every provided interpreter/generated path MUST validate these rules with shared cases when both exist (§16.4), including perturbed legal schedules:

- Immutable captures, argument/result copies, typed structural equality, strict/short-circuit evaluation, numeric boundaries.
- 10,000/100,000 direct/mutual/indirect tail calls; non-tail budgets and cancellation of divergence.
- Sequential/parallel for, break/continue, first-arm match, guard errors.
- carry empty/next/exhaustion/break/source-free budgets, prior-result arguments, replayed EvalPath.
- All §3.1 primitives, string order, guarded comprehension paths and linear construction overhead.
- Ordinary/triple string escapes, LF/CRLF, indentation, quote text, invalid controls, later diagnostic positions.
- Terminal if/match with newline/semicolon, fold callback tails, carry else-if, locals and argument-validation order.
- Named ordering, static/dynamic arity, normative Ops through projections/calls/branches, parallel acceptance.
- Multi-Flow signature collection, explicit entries, non-value Flows, fn purity, Flow-cycle rejection.
- EntryInput binding and validation before run_id/begin; zero nodes/calls on pre-run rejection; durable fixed input; EntryOutput null/void.
- Modules/exports/static Data, offline locked resolution, cycles/private names/isolation; multi-source sites/source maps/revision changes/single-source compatibility independent of discovery order.
- Unique file modes, import order, final import/data without newline, exact path matching, symlink/duplicate physical-file rejection.
- Pipeline precedence/single evaluation/first-argument injection/names/full stage completion.
- Child input checks before body and output failures retaining writes; business failures need explicit gates.
- Distinct identities/stores across call sites, graph loops, carry/comprehensions/nested calls; loop-source children distinct from iteration zero.
- Transitive summaries, unexpanded barrier/After/K regions, progress at parallelism 1, ancestor exemptions with inherited outside order, Running versus worker allowance, After wire.
- Local cursors, shared budgets/cancellation/journal, original child errors, interpreter/Go equivalence.
- Zero dynamic errors/budgeted evaluation under closed gates; no invalid constant-folded speculative errors.
- Ready-order variations, cross-parallel barriers, After terminal states/failure cascades, minimum errors observed out of order, prerequisites beyond error prefix, failure call-set counterexample.
- Position/CallID/JoinID uniqueness/order in sibling/nested loops/comprehensions.
- Duplicate atoms, changed same-ID values, equal different-ID payloads, concurrent joins/full reads.
- Legal differing live snapshots with identical replay per journal; complete-read waits/failures.
- Identical Go/Python schema/Data boundaries, no leaked exceptions/cancellation/native objects.
- Zero provider calls at effect construction/before authorization/terminal replay; intent-only crash/reconcile.
- Known-completed write conversion/schema faults preserving facts and errors without reruns.
- Uncertain-only/mixed permanent-error recovery, F/K recomputation, stable identity, newly evaluated earlier errors.
- Invalid resource references/restarted workers never accepted as live persisted objects.
- Application/run/session ownership, concurrent-request lease isolation, cancellation/failure cleanup, bounded workers.
- Multi-module static contracts, read-only Program metadata, consistent generated/interpreted schemas/resources.

HTTP claimants additionally test exact methods/HEAD without GET fallback; explicit-only Allow; encoded slash/raw paths/absolute-form; pre-run OPTIONS * rejection; empty-body json; pre-commit response validation; body/Data budgets; disconnect/timeout statuses.

| Scenario | Method and acceptance |
|---|---|
| Parallel search / three failures | Fully collect then decide; exact early stopping uses sequential carry, not deterministic parallel cancellation |
| Multi-agent missing evidence | Complete snapshot → guarded comprehensions/has/put; same successful output under schedule perturbation |
| Improve until score≥0.8 | carry+perform; next request uses prior result; no calls after threshold |
| Reusable workflows | Static children + pipeline/bindings; Data boundaries and complete results |
| Request branching/join | Independent child bindings + object/list input; effect-safe overlap and complete aggregation |
| Background messages | Host delivery/consumption + independent runs; delivery ack is not business completion |
| Crash recovery | Durable profile, fixed observation replay, no unresolved-write reruns, no abandon with pending intents |
| Provenance | Snapshot run_id/Position plus matching source revision locates joins; full causal UI belongs to Runtime |

Under §13 premises, schedule perturbations preserve successful output/primary error, and only successful runs require identical CallID sets. Finite-state models may help but do not replace parser/evaluator/Host-boundary/durable-journal implementation checks.

## 15. Diagnostics and conformance labels

Error objects differ from Skipped causes. Reject statically known invalid programs. Runtime errors never silently become null/false/empty snapshots and are not ordinary matchable values. Recoverable business failures require explicit result schemas such as `{ok:false,...}`.

| Static diagnostic | Condition |
|---|---|
| E-LEX / E-PARSE | Invalid literal/escape/token/syntax |
| E-NAME / E-DUPLICATE | Undeclared/forward value reference or duplicate declaration/field |
| E-SHADOW-CLAIM / E-RESERVED | Store shadowing or reserved-name redefinition |
| E-TYPE / E-ARITY | Statically known type/argument mismatch |
| E-ENTRY | No default or explicit entry, unknown/non-root-Flow choice; pre-run rejection |
| E-MODULE / E-MODULE-CYCLE / E-EXPORT | Missing/invalid/lock-mismatched module, module cycle, private access |
| E-FLOW-CYCLE | Direct/indirect Flow-call cycle, including constant-unselected paths |
| E-PURITY | External execution/live stores/children in fn, or prohibited work in Pure contexts |
| E-RETURN / E-CONTROL | Invalid result/return/break/continue position |
| E-PARALLEL | Write/Unknown/graph-loop control transfer in parallel |
| E-CLAIM-ORDER | Complete-read source/loop relation violates producer order |
| E-GRAPH-CYCLE / E-AFTER-CYCLE | Known dependency/After cycles |
| E-OPERATION / E-SCHEMA | Unregistered operation/version or known contract mismatch |

| Runtime Error | Condition |
|---|---|
| E-TYPE / E-ARITY / E-SCHEMA | Dynamic value/argument/input/result mismatch |
| E-VALUE / E-NUMBER / E-INDEX | Invalid native data/numeric domain/nonfinite/zero division/bounds/missing keys |
| E-FAIL / E-MATCH-NO-ARM | fail or expression match without an arm |
| E-BUDGET / E-CAPABILITY / E-OPERATION | Budget/permission/registration failure |
| E-CLAIM-CONFLICT / E-DEPENDENCY | Changed same-JoinID value or unusable dependency |
| E-GRAPH-CYCLE / E-AFTER-CYCLE | Cycles from expansion/new constraints |
| E-EFFECT-REJECTED / E-UNCERTAIN | Terminal provider rejection or unknown external outcome |
| E-REPLAY-DIVERGENCE / E-RESOURCE | Changed recovery request or expired/wrong-scope native reference |
| E-ID-COLLISION | Detected equal indexing digest for different canonical identities/arguments |

| Host diagnostic, not a node root | Condition |
|---|---|
| E-ENTRY | Pre-run selection failure, including declaration roots without Flows |
| E-SCHEMA | §2.3 EntryInput structure/binding/Data/annotation failure |
| E-HTTP | HTTP configuration/conversion/response failure; §12.1 status mapping |

A code may occur at multiple phases; diagnostics MUST identify the phase. HTTP response validation follows Flow result publication and cannot invent ErrorKeys/rewrite completed calls/nodes. Skipped causes include unselected_gate, break, continue, dependency_failure, after_failure, order_failure, error_cut, cancelled, optionally linking original errors; they are not static diagnostic codes.

Recommend W-SHADOW-LOOP when a loop shadows an outer name and RHS reads its old value; it remains new binding, not accumulation. Recommend W-PIPE-PRECEDENCE for unparenthesized mixed boolean/pipeline syntax without changing grouping/legality or weakening purity/type errors to warnings.

Child diagnostics/traces MUST connect selected entry, call token, defining location, InvocationPrefix, internal identities, and full cause chain, retaining entry/completion/failure/skip boundaries. Explain dependency/child/resource waits with causes, not extra lifecycle states. Hierarchical UI/previews/timings are optional and cannot change error order or require deterministic trace text. Fusion/lowering/codegen MUST retain source diagnostics rather than point only to generated code/temporaries/caller wrappers.

Implementations declare `LIP 1.0 core` or `LIP 1.0 core + durable profile` and publish Go/Python transports/contracts/budgets/cancellation granularity. Model-checking success, a working adapter, or a few examples do not replace §14 conformance.

## 16. Core appendices: encoding, display, and numeric vectors

### 16.1. Canonical data and identity encoding

C(v): null/false/true are bytes 0x00/0x01/0x02. number is 0x03 plus eight big-endian binary64 bytes after zero normalization. string is 0x04, eight-byte unsigned big-endian UTF-8 length, then bytes. list is 0x05, eight-byte unsigned element count, then each C(element). object is 0x06, eight-byte unsigned field count, then C(key),C(value) sorted by UTF-8 key bytes. Exceeding encoding/Host budgets MUST fail. Tuples become Data lists, never private repr. Ordinals/indices use decimal strings.

PositionPairs/EvalPathPairs encode each integer pair as two decimal strings. decimal emits unsigned ASCII without leading zeroes, zero as `"0"`. SHA256 returns 32 digest bytes; lowerhex emits two lowercase hex characters per byte. positional/named are Request arguments:

```text
JoinID_wire = ["join", run_id, [[decimal(anchor), decimal(index)], ...]]
CallID_wire = ["call", run_id, PositionPairs, EvalPathPairs, decimal(perform_site)]
JoinDigest = lowerhex(SHA256(C(JoinID_wire)))
CallDigest = lowerhex(SHA256(C(CallID_wire)))   # optional indexing digest
ArgFingerprint = lowerhex(SHA256(C([positional, named])))
```

Field counts/tags/string types MUST exactly match. Decode pair components as arbitrary-precision naturals or equivalent length/digit comparisons: position `"9"` precedes `"10"`. Ordinals/indices cannot wrap; use arbitrary precision or explicit budgets without imposing binary64 safe-integer iteration limits. Child prefixes are already in PositionPairs; no private fields are appended. Decode complete pairs with frozen revision metadata, not an assumption that all prefixes are graph loops.

After targets live in a fixed run_id/revision context without changing JoinID/CallID. Prefix/selector pairs use the same encoding:

```text
AfterTarget_wire = ["node", PositionPairs]
                 | ["region", "flow", InvocationPrefixPairs]
                 | ["region", "loop", SelectorPositionPairs]
                 | ["region", "branch", SelectorPositionPairs]
                 | ["site", kind, decimal(site)]
kind = "node" | "call" | "loop" | "branch"
```

node identifies one dynamic node; flow the entire invocation (entry prefix []); loop/branch the selector's complete region. site denotes all dynamic instances of that static site, including unexpanded ones; call uses invocation_site, other kinds their node/selector sites. Infinite regions may wait forever; closure/failure follows §7. Validate dynamic paths from frozen call/loop metadata, not callee names/arguments. Reject nonexistent targets; do not instantiate unselected work merely to satisfy a constraint. Resolve dynamic index existence as source/gates become known; normal closure uses Skipped rules, other provably nonexistent instances reject rather than leave phantom permanent Pending conditions.

snapshot.id is JoinDigest; argument_fingerprint is ArgFingerprint. CallID transport is CallID_wire; digests use C only, never JSON/repr/private concatenation. Identity equality compares full canonical structure/equivalent C bytes, not CallDigest; argument equality compares full C([positional,named]), not fingerprint. Compare operation/version separately. Computing CallDigest is optional without an index. Any detected digest collision MUST preserve original bytes and fail E-ID-COLLISION, never merge or arbitrarily choose.

| v | C(v), hex |
|---|---|
| null / false / true | 00 / 01 / 02 |
| 1 | 033ff0000000000000 |
| "a" | 04000000000000000161 |
| [] / {} | 050000000000000000 / 060000000000000000 |

### 16.2. Display vectors and differences from JCS

§3 is normative; these results are required:

| Input | str/order |
|---|---|
| -0 / 1e3 / 1e21 | `0` / `1000` / `1e21` |
| 1e-6 / 1e-7 / 5e-324 | `0.000001` / `1e-7` / `5e-324` |
| `["\n","\t"]` | `["\u000a","\u0009"]` |
| Object keys U+E000 / U+10000 | U+E000 first; opposite UTF-16/JCS order |
| Top-level string "1" / number 1 | Both str=`1`, different C; display does not establish identity |

Ordinary JSON decoders can read these outputs. Canonical display uses UTF-8 key order, no plus sign in exponents, and uniform long control escapes. JCS/JSON.stringify defaults are not canonical-byte oracles.

### 16.3. Numeric vectors

a/b below are stored binary64 operands from §2 literals. Floor division computes the exact mathematical quotient's floor before binary64 conversion. Remainder computes its exact mathematical remainder, rounds nearest/ties-to-even, then corrects the interval. This differs from Python float %/IEEE fmod. Correction need not preserve `a == (a//b)*b + a%b`. Proven-equivalent fast paths may use integer/floating operations with binary64 dyadic/big-integer fallback, never ordinary floating floor(a/b) instead of exact quotient. Cost need not equal one hardware division; budgets/performance account for fallback. Hex float notation describes exact results, not LIP literals. bits=1 denotes the smallest positive subnormal.

| a | b | a // b | a % b |
|---|---|---|---|
| -5 | 3 | -2 | 1 |
| 5 | -3 | -2 | -1 |
| 1 | 0.1 | 9 | binary64 `0x1.9999999999996p-4` |
| -1e-17 | 1 | -1 | nextafter(1,0) |
| 1e-17 | -1 | -1 | nextafter(-1,0) |
| 5e-324 | 1 | 0 | 5e-324 |
| -5e-324 | 5e-324 | -1 | 0 |

Literal 2e-324 rounds to zero, 3e-324 to bits=1. Basic arithmetic MUST NOT flush subnormals, and arithmetic/literals normalize zero. Zero divisors/nonfinite quotients are E-NUMBER. Interpreter/generated Go/boundary codecs MUST agree.

### 16.4. Core conformance cases

Complete files parse independently. Expression fragments evaluate as one node in the declared environment; static fragments remain subject to syntax/normative Ops checking even with dynamic callees. core/durable indicate applicable profiles, not required internal testing APIs.

The following complete file, including its terminal newline, MUST parse terminal if/match and yield `{sum:55,odd:[1,3],tag:1}`:

```text
fn sum_to(n: number, acc: number) -> number {
    if n == 0 { acc } else { sum_to(n - 1, acc + n) }
}
fn filter_fold(xs: list, p: function) -> list {
    fold(xs, [], fn(acc: list, x: any) -> list {
        if p(x) { concat(acc, [x]) } else { acc }
    })
}
fn tag(x: any) -> number {
    match x { number => 1, _ => 0 }
}
flow Main() -> object {
    odd = filter_fold([1,2,3], fn(x: number) -> bool { x % 2 == 1 })
    return {sum:sum_to(10,0), odd:odd, tag:tag(0)}
}
```

Carry tail classification, else-if, and local break MUST parse this complete file and yield 6:

```text
flow Main() -> number {
    answer = for x in [1,2,3,4] with s = 0 {
        for unused in [0] { break }
        if x == 3 { break s + x }
        else if x == 4 { break 999 }
        else { next s + x }
    }
    return answer
}
```

For each independent row below, reset bindings/journal. worker aliases `import python "example.worker" as worker`, with version-1 Concurrent ReadOnly read(value:number)->number returning its input and text()->string returning "a"; unknown is unregistered. f has only static type function but actually is `fn(x:number)->number{x}`; known_fn is separately named. store aliases `import host "example.store" as store` with version-1 Write commit()->number, whose fault provider completes a write, returns legal Data string "invalid", and receipt `{done:true}`. “Recovery graph” means §11.1. The prerequisite graph has Pure earlier_node [(1,0)], permanent E-FAIL failed_node [(5,0)], and provisional uncertain Concurrent ReadOnly dependency_node [(9,0)]; After requires earlier_node to wait for dependency_node, with all other prefix work successful and no additional dependencies/barriers.

| Input / environment | Required result | Profile |
|---|---|---|
| `range(5)[[-1,0,-1]]` / `range(5)[::-2]` | `[4,0,4]` / `[4,2,0]` | core evaluation/generated |
| `range(5)[:-1:-1]` / `slice(range(5),null,null,-1)` | `[]` / `[4,3,2,1,0]` | core evaluation/generated |
| `[0][[0,1]]` / `[0][::0]` | E-INDEX / E-VALUE, no partial result | core evaluation/generated |
| Gather/slice then project a function/effect list | Preserve values and Unknown Ops; cannot authorize parallel | core static/dynamic |
| `{len([0]);}` / `{len([0]);null}` | 1 / null; terminal semicolon is padding | core |
| `if false {1/0} else {7}` | 7, no E-NUMBER or execution budget for unselected branch | core, varied Ready order |
| `flow Main()->number {if false {x=1/0};return 7}` | x is unselected_gate, no root/F/cut, output 7 | core scheduling |
| `[1/x for x in [0,2] if x!=0]` | [0.5], skip body for zero | core |
| `[x for x in [1] if 1/0==0]` | Guard E-NUMBER, not false | core |
| `[perform effect worker.read(x) for x in [0,1,2] if x>0]` | [1,2]; EvalPath indices 1/2, not 0/1 | core identity |
| `"10"<"9"` / `"a"<"aa"` / `1<"2"` | true / true / E-TYPE, static when known | core |
| `parse_number("+1e-3")` / `parse_number("-0")` / `parse_number("3e-324")` | 0.001 / 0 / bits=1 | core |
| parse_number of `" 1"`, `"01"`, `"1x"`, `"NaN"`, `"1e309"` | E-VALUE for each | core |
| `f(x=1,2)` / `f(x=1,x=2)` | Static E-PARSE / E-ARITY | core |
| `known_fn(y=1)` for declared x:number / `len(value=[])` | Static E-ARITY | core |
| `f(perform effect worker.read(0),perform effect worker.read(1))` | Arity fails before arguments, zero providers | core dynamic call |
| `f(perform effect worker.text())` | One argument call, E-TYPE before f body | core dynamic call |
| `p=[effect worker.read(0)];q=p[0];r=perform q` | r=0 with Unknown barrier normally; E-PARALLEL in parallel | core Ops |
| `for item in [0] parallel {out=for x in [item] with s=0 {v=perform effect worker.read(x);break v}}` | Legal; carry break does not exit graph loop | core |
| `effect worker(0)` / `effect worker.unknown(0)` | Static E-OPERATION, zero providers | core |
| §12 payload on `{}` / `{kind:"a",payload:5}` / 0 | null / 5 / null | core |
| Position wire `[["9","0"]]` vs `[["10","0"]]` | First sorts earlier in snapshots | core identity |
| Distinct canonical identities with injected equal digest | E-ID-COLLISION, preserve originals | core index fault |
| Faulty `perform effect store.commit()` | terminal known_fault/E-SCHEMA with receipt/validation, no success | core Host / durable |
| Replay same commit ID; reconcile its known_fault to Result | Same E-SCHEMA, zero calls; reject terminal overwrite | durable |
| Two-node uncertain_call/check_a recovery graph, reconcile Result | Remove provisional root, restore check_a Pending, preserve identities | durable |
| Full recovery graph, reconcile Result, checks succeed | Retain later_call permanently as primary; no provider reruns | durable |
| Same graph, check_a fails E-FAIL | check_a primary, retain later_call root | durable |
| Prerequisite graph, dependency unresolved then reconciled Result | K always retains dependency; defer primary confirmation and then evaluate earlier_node | durable |

The recovery graph fixes later_call's pre-cut start/terminal rejection as history, without requiring every initial schedule to start it. Compare categories/semantic details, not timing/trace text, and count actual provider calls separately.

With the same worker contract and Main explicitly selected, this complete file yields `{a:5,b:2}` with distinct Read CallIDs; declaration order changes neither resolution nor parent ordering:

```lip
import python "example.worker" as worker
flow Main() -> object {
    a = 2 |> Read |> Adjust(delta=3)
    b = Read(2)
    return {a:a, b:b}
}
flow Read(value:number) -> number {
    return perform effect worker.read(value)
}
flow Adjust(value:number, delta:number) -> number {
    return value + delta
}
```

The next independent rows occur inside legal Flows, using Read/Adjust above; additional fn/fault/Claim children are declared per row. Durable applies only where marked.

| Input / environment | Required result | Profile |
|---|---|---|
| Multiple root Flows including Main, no explicit entry | Select Main regardless of declaration order; default and explicit Main have identical build identity | core entry |
| Multiple roots without Main, or unknown/non-Flow explicit choice | E-ENTRY, zero providers; no fallback for an invalid explicit choice | core entry |
| Root has Main/main; explicit other root; only an imported module has Main | Only root Main has priority; explicit selection overrides it; imported Main never participates | core entry |
| Main(x:number,y:string), `{positional:[2],named:{y:"a"}}` | x=2, y="a"; fix complete run input | core entry |
| Missing y, duplicate x, unknown name, or string x | Host pre-run E-SCHEMA; no root/Claims/providers | core entry |
| Invalid then valid durable EntryInput | Invalid rejects before begin/ID/journal; valid persists full input before work | durable entry |
| `flow Main()->null{return null}`; binding a void call | First succeeds with null; second static E-TYPE | core type |
| Null-valued entry / explicit void / empty implicit main | `{kind:"value",value:null}` / `{kind:"void"}` / `{kind:"void"}`, after complete graph | core output |
| Final `return Read(2)` | Legal; await whole child then validate caller result | core |
| `perform effect worker.read(2) \|> Adjust(delta=3) \|> Adjust(delta=1)` | 6, initial read once, sequential full completion | core pipeline |
| `perform effect worker.read(2) \|> Read`; `2 \|> Read \|> Adjust(delta=3)` | 2 / 5; fixed stage inputs | core |
| `2 \|> Adjust(value=2,delta=3)` / `2 \|> Adjust()` | Static E-ARITY for duplicate first / missing delta | core |
| list xs `\|> len` / `\|> map()` | len(xs) / E-ARITY for missing callback | core |
| `ok && x \|> F`; `x \|> F == y`; `(x \|> F)==y` | `(ok && x) \|> F` / E-PARSE / compare stage result | core syntax |
| Guard uses `xs \|> len` / `2 \|> Adjust(delta=3)` | Pure bool composition allowed / E-PURITY even if Pure summary | core |
| Ordinary number fn f: `f(Read(2))`; local function value f as `2 \|> f` | Legal local cursor / E-TYPE nonstatic stage | core |
| `Read(perform effect worker.read(2))` / `Adjust(Read(2),delta=3)` | Static E-PURITY; use prior bindings/pipeline | core |
| fn calls Read or Pure Adjust; map receives Read | E-PURITY / E-PURITY / E-TYPE Flow non-value | core |
| F→G and G→F under constant false | Static E-FLOW-CYCLE; fn mutual recursion remains legal | core |
| Dynamic non-number argument to Read | E-SCHEMA before child body/provider | core boundary |
| Dynamic stage argument mismatch / child non-Data/wrong result | Owner/stage target token / child return node | core diagnostic |
| `if false {ignored=Read(2)}` | No argument/child semantic evaluation/provider | core gate |
| Child return ready but selected audit pending | No success publication/later stage arguments | core completion |
| Child returns valid ok:false followed by next stage / guarded next stage | Continues normally / executes only selected branch | core business |
| Child faulty commit after completed write | Preserve known_fault/receipt/internal perform error; skip downstream, no rerun | core / durable |
| Child performs fn-returned proposal inside parallel | Unknown Ops remains E-PARALLEL regardless of Data output | core summary |
| Same Claim child at two sites, graph iterations, carry iterations | Isolated stores/IDs, full producer checks | core identity |
| Earlier child failure, unnecessary later unstarted node | F/K cuts later work; no wrapper root/forced descendants | core error |
| Worker allowance 1, child in local value block | Progress without deadlock; later local statement waits | core scheduling |
| Running Write/Unknown owner with internal Write barrier | No wait for owner completion; waiting ancestor yields workers | core scheduling |
| Earlier external barrier before owner, later sibling Write | Child inherits earlier barrier; sibling waits for full owner | core order |
| After distinguishes two flow prefixes; child waits for own owner | Exact full-region waits / E-AFTER-CYCLE | core After |
| Parent budget/cancel with completed/pending child actions | Shared budget/cancel, preserve records/uncertainty, no rollback promise | core lifecycle |
| Resume with changed entry/language/lock/child source | E-REPLAY-DIVERGENCE before new providers | durable |

Fixed identity vectors: owner Position `[(20,0)]`, call sites 25/40, child node 3 produce `[(20,0),(25,0),(3,0)]` / `[(20,0),(40,0),(3,0)]`, in that order. At local loop site 22 iterations 0/1, call 25 produces `[(20,0),(22,0),(25,0),(3,0)]` / `[(20,0),(22,1),(25,0),(3,0)]`; child EvalPath does not repeat caller `(22,index)`. Use decimal-string wire pairs. The second child completion target is `["region","flow",[["20","0"],["22","1"],["25","0"]]]`, distinct from its node target `["node",[["20","0"],["22","1"],["25","0"],["3","0"]]]`. If owner is in graph loop site 10 iteration 2, prefix all with `(10,2)` from owner Position without repeating it in EvalPath. Deeper calls retain full parent owner Position, not root/callee-site resets.

Independent loop-source vector: graph for_site=10, source Read invocation_site=12, child node=3, body node=20; Read returns a list. Selector is `[(10,0)]`; source child prefix `[(10,0),(12,0)]`, child node `[(10,0),(12,0),(3,0)]`; body iterations 0/1 are `[(10,0),(20,0)]` / `[(10,1),(20,0)]`. Source evaluation adds no own iteration index. Shared `(10,0)` does not merge source/selector/body: metadata and tokens distinguish them. All source child nodes precede body nodes because 12<20. Selector completion is not loop completion.

Modules/resources use frozen build inputs and both interpreter/generated execution. Source order is identity order, not completion/filesystem discovery order.

| Input / environment | Required result | Profile |
|---|---|---|
| Root imports modules with private same-name helper | Isolated names; only root Flows count for entry | core modules |
| Imports/fn-only root / dependency; export-fn-only or data-only root | Implicit empty main / declarations only / E-ENTRY | core file mode |
| import after exported data | E-PARSE, imports must come first | core syntax |
| Main returns LIMIT, newline, `LIMIT:number=2` ending at 2 | Legal without final newline, result 2 | core syntax |
| Final provided import without newline | Legal; root may create implicit main, dependency never does | core syntax |
| import/declaration or adjacent data declarations without sep before non-EOF | E-PARSE, EOF exemption only at file end | core syntax |
| Private pricing member / alias passed to fn | E-EXPORT / E-TYPE, no Host execution | core names |
| Dependency has top-level perform / static initializer calls fn | E-MODULE / E-PARSE, no external initialization | core |
| Unused A→B→A imports | E-MODULE-CYCLE despite unused aliases | core build |
| Missing/escaping/lock-mismatched dependency | E-MODULE before run/provider | core build |
| Case/Unicode filename mismatch, symlink, duplicate physical source | E-MODULE; no permissive filesystem alias identity | core local resolution |
| Same source set discovered/compiled in different orders | Identical SourceTable/lock/sites/revision | core reproducibility |
| Only dependency comment/content/version changes | New revision; no old-run resume with new sources | core / durable |
| Disk changes after snapshot, then compiled Program runs | Frozen sources retained; rebuild for changes | core freeze |
| Exported fn proposal performed in parallel | Unknown Ops, E-PARALLEL | core effect |
| Two HTTP runs share application pool, own run objects/leases; cancel one | Clean only its leases, retain pool/other run | core Host |
| Old run reference in another run / old worker generation | E-RESOURCE before provider actions, no implicit recreation | core resources |
| Cancel unclear write then lifecycle cleanup | Preserve Uncertain, isolate/settle lease, no restart retry | core / durable |
| Concurrent runs of one read-only Program | No static mutations; isolated runs/journals/Claims/cancel | core concurrency |

Fixed multi-source sites: root token_count=40, two UTF-8-sorted dependencies token_count=20/10. Local ordinal 3 maps to global sites 3/43/63. Prefixes plus these global sites distinguish definitions, never merge local ordinal 3. Single-source ordinal 3 stays site 3 with unchanged wire.

HTTP cases apply only to implementations claiming that optional profile:

| Input / configuration | Required result |
|---|---|
| Only GET /items containing Write; HEAD /items | 405, Allow: GET, zero runs/providers; no automatic fallback |
| Only GET /items; DELETE | 405, Allow: GET without HEAD, zero runs/providers |
| Explicit GET and HEAD /items; DELETE | 405, Allow: GET, HEAD, zero runs/providers |
| Only POST /items; HEAD /items / GET /missing | 405 Allow: POST / 404; zero runs/providers |
| Explicit HEAD, valid body | Fully evaluate/validate/compute length; send no body |
| Missing route method / Host-controlled response header | E-HTTP before startup / 500 before commit |
| GET /{x}, /a%2Fb, empty body | path /a/b, raw_path /a%2Fb, params.x a/b, json null |
| GET /{x}, /A and /%41 | Same path /A and x=A; raw_path /A versus /%41 even if RawPath empty |
| Absolute `http://lip.test/a%2Fb?q=1` | host lip.test, path /a/b, raw_path /a%2Fb, x=a/b, query `{q:["1"]}` |
| OPTIONS * / authority-form | 400 before routing/runs/providers |
| Disconnected request / writable deadline expiry | Cancellation without synthetic 408 / 504; no automatic retries |

### 16.5. Fixed numerical algorithms for power and logarithms

This normative reference is shared by interpreter/codegen/independent implementations and is not a dependency on a Go release. Go-style pseudocode is not LIP source. float64 means binary64; every numericAdd/Sub/Mul/Div independently rounds nearest/ties-to-even without reassociation/FMA. Hex constants specify exact values. Assignments/comparisons/booleans/loops run in code order. int is a signed integer sufficient for the exponents, uint64 is unsigned 64-bit, and >>/& are descriptive integer operations, not new LIP operators.

math.Abs is absolute value; Floor is mathematical floor; Frexp gives exact `(m,k)` with x=m·2^k and 0.5≤|m|<1 for nonzero x. Sqrt is correctly rounded nearest/ties-to-even square root. Ldexp rounds m·2^k to binary64, including subnormals/zero/overflow. Inf(1) is an internal sentinel rejected as E-NUMBER before becoming a LIP value. Conversions follow code; loop indices are exactly representable. Proven bit-equivalent optimizations are permitted, never tolerance-based acceptance of different bits.

Apply §3 domain checks first. corePower accepts validated finite operands, coreLog positive finite operands, and coreExp is internal to corePower. `**` uses corePower; `*/` handles §3 exact cases then numericDiv(coreLog(a),coreLog(b)). Reject final nonfinite values and normalize zero. Log uses fixed 24-term series/exponent decomposition; exp uses a fixed 24-term series/power-of-two scale; power uses scaled exponentiation by squaring and square root/reciprocal for ±0.5. None become source builtins.

```go
func numericAdd(a, b float64) float64 { return float64(a + b) }
func numericSub(a, b float64) float64 { return float64(a - b) }
func numericMul(a, b float64) float64 { return float64(a * b) }
func numericDiv(a, b float64) float64 { return float64(a / b) }

const numericLn2Hi = 0x1.62e42fee00000p-1
const numericLn2Lo = 0x1.a39ef35793c76p-33

func coreLog(x float64) float64 {
	m, k := math.Frexp(x)
	if m < 0x1.6a09e667f3bcdp-1 {
		m = numericMul(m, 2)
		k--
	}
	z := numericDiv(numericSub(m, 1), numericAdd(m, 1))
	z2 := numericMul(z, z)
	term, sum := z, z
	for j := 1; j < 24; j++ {
		term = numericMul(term, z2)
		sum = numericAdd(sum, numericDiv(term, float64(2*j+1)))
	}
	return numericAdd(numericMul(float64(k), numericLn2Hi),
		numericAdd(numericMul(2, sum), numericMul(float64(k), numericLn2Lo)))
}

func coreExp(x float64) float64 {
	if x > 0x1.62e42fefa39efp+9 {
		return math.Inf(1)
	}
	if x < -0x1.74910d52d3052p+9 {
		return 0
	}
	ln2 := numericAdd(numericLn2Hi, numericLn2Lo)
	k := int(math.Floor(numericAdd(numericDiv(x, ln2), 0.5)))
	r := numericSub(numericSub(x, numericMul(float64(k), numericLn2Hi)),
		numericMul(float64(k), numericLn2Lo))
	term, sum := 1.0, 1.0
	for j := 1; j <= 24; j++ {
		term = numericDiv(numericMul(term, r), float64(j))
		sum = numericAdd(sum, term)
	}
	return math.Ldexp(sum, k)
}

func corePower(x, y float64) float64 {
	if y == 0 || x == 1 {
		return 1
	}
	if y == 1 || x == 0 {
		return x
	}
	if y == 0.5 {
		return math.Sqrt(x)
	}
	if y == -0.5 {
		return numericDiv(1, math.Sqrt(x))
	}
	negative := x < 0 && math.Abs(y) < 0x1p53 && int64(math.Abs(y))&1 != 0
	x = math.Abs(x)
	if math.Abs(y) >= 0x1p63 {
		if x == 1 {
			return 1
		}
		if (x < 1) == (y > 0) {
			return 0
		}
		return math.Inf(1)
	}
	whole := math.Floor(math.Abs(y))
	fraction := numericSub(math.Abs(y), whole)
	if fraction > 0.5 {
		fraction = numericSub(fraction, 1)
		whole = numericAdd(whole, 1)
	}
	a, scale := 1.0, 0
	if fraction != 0 {
		a = coreExp(numericMul(fraction, coreLog(x)))
	}
	m, e := math.Frexp(x)
	for n := uint64(whole); n != 0; n >>= 1 {
		if e < -8192 || e > 8192 {
			scale += e
			break
		}
		if n&1 != 0 {
			a = numericMul(a, m)
			scale += e
		}
		m = numericMul(m, m)
		e *= 2
		if m < 0.5 {
			m = numericMul(m, 2)
			e--
		}
	}
	if y < 0 {
		a = numericDiv(1, a)
		scale = -scale
	}
	a = math.Ldexp(a, scale)
	if negative {
		a = -a
	}
	return a
}
```

All paths MUST match these outcomes; bits denotes normalized unsigned binary64 encoding. Domain errors produce no successful value.

| Expression | Required result |
|---|---|
| `0 ** 0`, `(-1) ** 9007199254740992` | 1 |
| `(-2) ** 3` | -8 |
| `2 ** -1074` | bits=1, smallest positive subnormal |
| `2 ** -1075`, `(-2) ** -1075` | 0 |
| `2 ** 1024`, `0 ** -1`, `(-2) ** 0.5` | E-NUMBER |
| `0 */ 2`, `2 */ 1`, `2 */ 0`, `2 */ -2` | E-NUMBER |
| `0.25 */ 2`, `8 */ 0.5` | -2, -3 |
| `2 ** 0.5`, `4 ** -0.5` | bits=0x3ff6a09e667f3bcd, 0.5 |
| `5e-324 ** 0.5` | Exact 2^-537, bits=0x1e60000000000000 |
| `27 ** (1/3)` | 3 |
| `2 */ 3` | bits=0x3fe430939835353d |
| `10 */ 2` | bits=0x400a934f0979a372 |
| `"2" ** 3`, `8 */ true` | Static E-TYPE; dynamic any also E-TYPE |
| `if false {(-2) ** 0.5} else {7}` | 7, unselected branch not evaluated |

These exact algorithm bits cannot be replaced by platform pow/log or approximate decimal equality. parse_number/display round trips follow §3/3.1 and MUST accept all canonical numeric spellings without requiring JCS spelling.

| Expression | binary64 bits |
|---|---|
| `2 ** 0.5` | `0x3ff6a09e667f3bcd` |
| `4 ** -0.5` | `0x3fe0000000000000` |
| `5e-324 ** 0.5` | `0x1e60000000000000` |
| `27 ** (1/3)` | `0x4008000000000000` |
| `0.25 */ 2` | `0xc000000000000000` |
| `8 */ 0.5` | `0xc008000000000000` |
| `8 */ 2` | `0x4008000000000000` |
| `2 */ 3` | `0x3fe430939835353d` |
| `10 */ 2` | `0x400a934f0979a372` |

## 17. Non-normative notes: repository implementation status

This section records repository status as of the date above, without changing contracts or itself declaring core conformance. Interpreter/CLI/generated Go support static Data, multiple Flows/explicit selection, EntryInput/EntryOutput, children, pipelines, and static modules. Unselected declarations are checked. Input validation precedes run_id/MemoryJournal; CLI conveniences convert to the same EntryInput/diagnostics. Outputs await whole-graph success and distinguish null/void. Parent/child scheduling shares workers, yielding while waiting for children/resources, with instance-isolated Claims. Build snapshots expose SourceTable/SourceHash/DependencyLock/multi-source revision; sites use root-first order while SourceTable uses SourceID order. After wire covers dynamic nodes, Flow/loop/branch regions, and static completion sets. Expansion/prerequisite closure/codegen share one runtime. See [implementation coverage](../IMPLEMENTATION.en.md).

The runtime accepts any positive `int` leaf allowance without preallocating workers, with bounded event storage and shared resource permits. Completed lexical scopes release local payloads while preserving returned Data, frozen captures, and After/diagnostic metadata. This is scope cleanup rather than exact last-use analysis; retained graph history and journals still grow with work. Non-tail calls default to 10,000 active frames per leaf, configurable through `Options.MaxFrames` or `-frames`; exhaustion reports E-BUDGET without a fabricated success value.

HTTP CLI entries/module snapshots and per-route entry choices are supported. Host method/405/Allow dispatch does not fall HEAD back to GET; raw_path uses EscapedPath. Conversion/validation/isolation/cancellation retain core boundaries. HTTP supports explicit contracts, not a request-scoped generic native bridge. The repository provides only in-memory journaling and does not claim durable support. REPL supports script declarations/statements, retains successful values, fn/Flow definitions and closures, and supports cross-cell named calls/pipelines. Static Data, exports, frozen LIP imports, and cell-local Claims use the shared compiler/runtime; :begin/:end groups complete scripts and forward declarations. Flow invocations have fresh stores; completed Claim Data snapshots may persist. :run provides independent file execution with entry/input/project options. Bundled entry/Flow/module lessons use the same compilation path. Native helper lowering respects lexical module/local scopes, with pipeline targets still checked normally. See the [English README](../README.en.md) for commands/limits and implementation notes for evidence.

The REPL's `:del name[,name...]` command unbinds names independently of their contents and allows reuse in later cells. Batch validation precedes any removal. Existing saved closures and Flows keep their original references; deletion does not replay actions or close resources. This is session tooling, not additional LIP source syntax.

Regenerate §16.5 bits with `go run ./tests/reference_numeric` or check using `go run ./tests/reference_numeric -check`. The tool extracts reference code directly rather than calling the interpreter implementation. make test includes TestSpecNumericReference; separate vectors exercise interpreted/generated results. Independent implementations need not use these repository tools.
