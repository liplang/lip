package script

import (
	"sort"
	"strings"
)

var builtinArity = map[string][]int{"len": {1}, "str": {1}, "range": {1, 2, 3}, "map": {2}, "fold": {3}, "fail": {1}, "concat": {2}, "slice": {3, 4}, "keys": {1}, "has": {2}, "put": {3}, "split": {2}, "parse_number": {1}, "print": {1}, "log": {2}, "telemetry": {2}}

type bindingInfo struct {
	info    analysis
	fn      *expression
	builtin string
	static  bool
}
type scope struct {
	parent   *scope
	bindings map[string]bindingInfo
}

func (s *scope) child() *scope { return &scope{parent: s, bindings: map[string]bindingInfo{}} }
func (s *scope) find(name string) (bindingInfo, bool) {
	for s != nil {
		if x, ok := s.bindings[name]; ok {
			return x, true
		}
		s = s.parent
	}
	return bindingInfo{}, false
}

type checker struct {
	syntax    *syntax
	contracts map[string]Contract
	aliases   map[string]importDecl
	stores    map[string]*statement
	writers   map[string]int
	err       error
	top       int
	local     int
	voidOK    *expression
	returns   map[*expression]*operationSummary
}

func unionOperations(a, b *operationSummary) *operationSummary {
	if a == nil || b == nil {
		return nil
	}
	return &operationSummary{sources: []*operationSummary{a, b}}
}

// Reachability computes the least union fixed point, including recursive
// components. Empty intermediate cells are bottom, never an unknown leaf.
// No function is evaluated and no operation is inferred from a runtime value.
func resolvedOperations(root *operationSummary) ([]string, bool) {
	seen := map[*operationSummary]bool{}
	ops := map[string]bool{}
	pending := []*operationSummary{root}
	for len(pending) != 0 {
		s := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if s == nil {
			return nil, false
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		if s.operation != "" {
			ops[s.operation] = true
		}
		pending = append(pending, s.sources...)
	}
	keys := make([]string, 0, len(ops))
	for op := range ops {
		keys = append(keys, op)
	}
	sort.Strings(keys)
	return keys, len(keys) != 0
}

func pureInfo(typ string) analysis {
	return analysis{typ: normalizeType(typ), effect: PureEffect, concurrent: true}
}
func mergeEffect(a, b analysis) analysis {
	if b.effect > a.effect {
		a.effect = b.effect
	}
	a.concurrent = a.concurrent && b.concurrent
	a.hasFlow = a.hasFlow || b.hasFlow
	return a
}
func unify(a, b string) string {
	a, b = normalizeType(a), normalizeType(b)
	if a == b {
		return a
	}
	if a == "null" {
		if b == "any" || strings.HasSuffix(b, "?") {
			return b
		}
		return b + "?"
	}
	if b == "null" {
		return unify(b, a)
	}
	return "any"
}
func (c *checker) error(t token, code, msg string) {
	if c.err == nil {
		c.err = at(diagnostic(code, msg), t)
	}
}
func reserved(name string) bool {
	_, b := builtinArity[name]
	return b || name == "claim" || strings.Contains(" import as flow fn return if else match for in parallel break continue with next effect perform export true false null void ", " "+name+" ")
}
func (c *checker) declare(s *scope, name string, info bindingInfo, t token) {
	if reserved(name) {
		c.error(t, "E-RESERVED", "reserved name "+name)
	}
	if _, ok := c.stores[name]; ok {
		c.error(t, "E-SHADOW-CLAIM", "Claim name cannot be shadowed")
	}
	if _, ok := c.aliases[name]; ok {
		c.error(t, "E-RESERVED", "import alias cannot be rebound")
	}
	if _, ok := s.bindings[name]; ok {
		c.error(t, "E-DUPLICATE", "duplicate binding "+name)
	}
	s.bindings[name] = info
}
func (c *checker) require(t token, a analysis, typ string) {
	if a.typ != "any" && a.typ != typ {
		c.error(t, "E-TYPE", "expected "+typ+", got "+a.typ)
	}
}
func analyze(s *syntax, host Host) (*checker, error) {
	if len(s.flows) == 0 {
		return analyzeBindings(s, host, nil)
	}
	for _, name := range sortedFlowNames(s.flows) {
		if _, err := checkFlow(s, host, name); err != nil {
			return nil, err
		}
	}
	return s.flows[s.flowName].check, nil
}
func checkFlow(s *syntax, host Host, name string) (*checker, error) {
	f := s.flows[name]
	if f.checking {
		return nil, at(diagnostic("E-FLOW-CYCLE", "recursive Flow call "+name), f.tok)
	}
	if f.check != nil {
		return f.check, nil
	}
	f.checking = true
	defer func() { f.checking = false }()
	view := *s
	view.flowName, view.result, view.params, view.body = f.name, f.result, f.params, f.body
	view.cell = false
	c, err := analyzeBindings(&view, host, s.initial)
	if err != nil {
		return nil, err
	}
	summary := pureInfo(f.result)
	walkBlockExpressions(f.body, func(e *expression) { summary = mergeEffect(summary, e.info) })
	if f.result != "void" && len(f.body.items) != 0 {
		summary.ops = f.body.items[len(f.body.items)-1].expr.info.ops
	}
	f.summary, f.check = summary, c
	return c, nil
}

// Saved interactive values enter the same checker as ordinary source values.
// This is a tooling entry point; Compile always starts with an empty scope.
func analyzeBindings(s *syntax, host Host, initial map[string]bindingInfo) (*checker, error) {
	c := &checker{syntax: s, contracts: map[string]Contract{}, aliases: map[string]importDecl{}, stores: map[string]*statement{}, writers: map[string]int{}, returns: map[*expression]*operationSummary{}}
	for _, contract := range host.Contracts {
		if contract.Version == "" || contract.Result.Type == "" || contract.Invoke == nil || contract.Effect == PureEffect {
			return nil, diagnostic("E-OPERATION", "invalid operation contract")
		}
		key := identityKey(contract.Operation.Wire())
		if _, dup := c.contracts[key]; dup {
			return nil, diagnostic("E-OPERATION", "duplicate operation contract")
		}
		c.contracts[key] = contract
	}
	for _, d := range s.imports {
		if reserved(d.alias) {
			c.error(d.tok, "E-RESERVED", "reserved alias")
		}
		if _, dup := c.aliases[d.alias]; dup {
			c.error(d.tok, "E-DUPLICATE", "duplicate alias")
		}
		c.aliases[d.alias] = d
	}
	env := &scope{bindings: map[string]bindingInfo{}}
	for name := range builtinArity {
		env.bindings[name] = bindingInfo{info: pureInfo("function"), builtin: name, static: true}
	}
	dataNames := make([]string, 0, len(s.data))
	for name := range s.data {
		dataNames = append(dataNames, name)
	}
	sort.Slice(dataNames, func(i, j int) bool { return s.data[dataNames[i]].tok.site < s.data[dataNames[j]].tok.site })
	for _, name := range dataNames {
		d := s.data[name]
		if err := checkType(d.value, d.typ); err != nil {
			c.error(d.tok, "E-TYPE", "static data "+name+": "+err.Error())
		}
		c.declare(env, name, bindingInfo{info: pureInfo(d.typ)}, d.tok)
	}
	for _, name := range sortedFlowNames(s.flows) {
		f := s.flows[name]
		if reserved(name) {
			c.error(f.tok, "E-RESERVED", "reserved Flow name "+name)
		}
		if _, conflict := c.aliases[name]; conflict {
			c.error(f.tok, "E-RESERVED", "Flow/import name conflict")
		}
	}
	functions := make([]string, 0, len(s.functions))
	for name := range s.functions {
		functions = append(functions, name)
	}
	sort.Slice(functions, func(i, j int) bool { return s.functions[functions[i]].tok.site < s.functions[functions[j]].tok.site })
	for _, name := range functions {
		f := s.functions[name]
		cell := &operationSummary{}
		c.returns[f] = cell
		info := pureInfo("function")
		info.returns = cell
		c.declare(env, name, bindingInfo{info: info, fn: f, static: true}, f.tok)
	}
	for name, info := range initial {
		if s.functions[name] != nil {
			continue
		}
		if _, declared := s.data[name]; declared {
			continue
		}
		c.declare(env, name, info, token{site: -1})
	}
	for _, name := range functions {
		f := s.functions[name]
		c.function(f, env)
	}
	// Module-level functions capture declarations, never a caller Flow's stores.
	for i, stmt := range s.body.items {
		if stmt.tag == "claim" {
			if reserved(stmt.name) {
				c.error(stmt.tok, "E-RESERVED", "reserved store name")
			}
			if _, dup := c.stores[stmt.name]; dup {
				c.error(stmt.tok, "E-DUPLICATE", "duplicate Claim")
			}
			if _, dup := c.aliases[stmt.name]; dup {
				c.error(stmt.tok, "E-RESERVED", "store/import conflict")
			}
			c.stores[stmt.name] = stmt
		}
		c.collectWriters(stmt, i)
	}
	flow := env.child()
	if s.cell {
		for name, info := range initial {
			flow.bindings[name] = info
		}
	}
	for _, p := range s.params {
		c.declare(flow, p.name, bindingInfo{info: pureInfo(p.typ)}, p.tok)
	}
	returns := 0
	for i, stmt := range s.body.items {
		c.top = i
		if stmt.tag == "return" {
			returns++
			if i != len(s.body.items)-1 {
				c.error(stmt.tok, "E-RETURN", "Flow return must be last")
			}
			if s.result != "void" && stmt.expr == nil {
				c.error(stmt.tok, "E-RETURN", "missing return value")
			}
			if s.result == "void" && stmt.expr != nil {
				c.error(stmt.tok, "E-RETURN", "void Flow cannot return a value")
			}
		}
		c.statement(stmt, flow, false, false, 0, true)
		if stmt.tag == "return" && stmt.expr != nil {
			c.assignType(stmt.expr.tok, stmt.expr.info.typ, s.result)
		}
	}
	if s.result != "void" && returns != 1 {
		c.error(s.body.tok, "E-RETURN", "Flow requires exactly one final return")
	}
	if returns > 1 {
		c.error(s.body.tok, "E-RETURN", "multiple returns")
	}
	return c, c.err
}
func (c *checker) collectWriters(s *statement, top int) {
	if s.tag == "join" {
		c.writers[s.name] = top
	}
	if s.body != nil {
		for _, x := range s.body.items {
			c.collectWriters(x, top)
		}
	}
	if s.otherwise != nil {
		for _, x := range s.otherwise.items {
			c.collectWriters(x, top)
		}
	}
	if s.other != nil {
		c.collectWriters(s.other, top)
	}
	for _, a := range s.arms {
		if a.body != nil {
			for _, x := range a.body.items {
				c.collectWriters(x, top)
			}
		}
	}
}
func (c *checker) assignType(t token, actual, expected string) {
	actual, expected = normalizeType(actual), normalizeType(expected)
	if actual == "any" || expected == "any" || actual == expected || strings.HasSuffix(expected, "?") && (actual == "null" || actual == strings.TrimSuffix(expected, "?")) {
		return
	}
	c.error(t, "E-TYPE", "expected "+expected+", got "+actual)
}
func (c *checker) function(e *expression, env *scope) analysis {
	c.local++
	defer func() { c.local-- }()
	local := env.child()
	for _, p := range e.params {
		c.declare(local, p.name, bindingInfo{info: pureInfo(p.typ)}, p.tok)
	}
	a := c.localBlock(e.body, local, true, false, 0)
	c.assignType(e.tok, a.typ, e.typ)
	cell := c.returns[e]
	if cell == nil {
		cell = &operationSummary{}
	}
	cell.sources = []*operationSummary{a.ops}
	e.info = pureInfo("function")
	e.info.returns = cell
	return e.info
}
func (c *checker) localBlock(b *block, env *scope, pure, parallel bool, loops int) analysis {
	a := pureInfo("any")
	for _, s := range b.items {
		a = mergeEffect(a, c.statement(s, env, pure, parallel, loops, false))
	}
	if b.tail != nil {
		tail := c.expression(b.tail, env, pure, parallel)
		a = mergeEffect(a, tail)
		a.typ = tail.typ
		a.ops = tail.ops
	}
	if b.exit != nil {
		a = mergeEffect(a, c.carryExit(b.exit, env, pure, parallel, loops))
	}
	return a
}
func (c *checker) statement(s *statement, env *scope, pure, parallel bool, loops int, top bool) analysis {
	a := pureInfo("any")
	if s.expr != nil {
		saved := c.voidOK
		if s.tag == "eval" {
			c.voidOK = s.expr
		}
		a = c.expression(s.expr, env, pure, parallel)
		c.voidOK = saved
	}
	switch s.tag {
	case "claim":
		if !top || pure {
			c.error(s.tok, "E-PURITY", "Claim declarations require Flow top level")
		}
		if _, dup := env.bindings[s.name]; dup {
			c.error(s.tok, "E-DUPLICATE", "duplicate Claim binding")
		}
		env.bindings[s.name] = bindingInfo{info: pureInfo("list")}
	case "bind":
		info := bindingInfo{info: a}
		if s.expr.tag == "fn" {
			info.fn = s.expr
		} else if s.expr.tag == "id" {
			if prior, ok := env.find(s.expr.name); ok {
				info.fn, info.builtin = prior.fn, prior.builtin
			}
		}
		c.declare(env, s.name, info, s.tok)
	case "join":
		if pure || c.local > 0 {
			c.error(s.tok, "E-PURITY", "join requires a Flow graph statement")
		}
		if _, ok := c.stores[s.name]; !ok {
			c.error(s.tok, "E-NAME", "join target is not a store")
		}
		if a.effect != PureEffect || a.hasFlow {
			c.error(s.tok, "E-PURITY", "join RHS must be pure")
		}
	case "return":
		if !top {
			c.error(s.tok, "E-RETURN", "return only at Flow tail")
		}
	case "break", "continue":
		if loops == 0 {
			c.error(s.tok, "E-CONTROL", "loop control outside a sequential loop")
		}
	case "if":
		c.require(s.tok, a, "bool")
		a = mergeEffect(a, c.localBlock(s.body, env.child(), pure, parallel, loops))
		if s.otherwise != nil {
			a = mergeEffect(a, c.localBlock(s.otherwise, env.child(), pure, parallel, loops))
		}
		if s.other != nil {
			a = mergeEffect(a, c.statement(s.other, env.child(), pure, parallel, loops, false))
		}
	case "match":
		for _, arm := range s.arms {
			c.patterns(arm)
			if arm.guard != nil {
				g := c.expression(arm.guard, env, pure, parallel)
				c.require(arm.guard.tok, g, "bool")
				if g.effect != PureEffect || g.hasFlow {
					c.error(arm.guard.tok, "E-PURITY", "guard must be pure")
				}
			}
			a = mergeEffect(a, c.localBlock(arm.body, env.child(), pure, parallel, loops))
		}
	case "for":
		if s.expr != nil {
			c.require(s.tok, a, "list")
		}
		if s.parallel && (pure || c.local > 0) {
			c.error(s.tok, "E-PARALLEL", "fn loop cannot be parallel")
		}
		child := env.child()
		if s.name != "" {
			c.declare(child, s.name, bindingInfo{info: pureInfo("any")}, s.tok)
		}
		nextLoops := loops + 1
		if s.parallel {
			nextLoops = 0
		}
		a = mergeEffect(a, c.localBlock(s.body, child, pure, parallel || s.parallel, nextLoops))
	}
	if parallel && a.effect != PureEffect && (a.effect != ReadOnlyEffect || !a.concurrent) {
		c.error(s.tok, "E-PARALLEL", "parallel requires Concurrent ReadOnly")
	}
	return a
}
func (c *checker) patterns(a arm) {
	for _, p := range a.patterns {
		if p.wildcard && len(a.patterns) != 1 {
			c.error(p.tok, "E-PARSE", "wildcard must be the only pattern")
		}
	}
}
func (c *checker) carryExit(s *statement, env *scope, pure, parallel bool, loops int) analysis {
	a := c.expression(s.expr, env, pure, parallel)
	if s.tag == "next" || s.tag == "break" {
		if a.effect != PureEffect || a.hasFlow {
			c.error(s.tok, "E-PURITY", "carry exit must be pure")
		}
		return a
	}
	if s.tag == "if" {
		c.require(s.tok, a, "bool")
		a = mergeEffect(a, c.localBlock(s.body, env.child(), pure, parallel, loops))
		if s.other != nil {
			a = mergeEffect(a, c.carryExit(s.other, env.child(), pure, parallel, loops))
		}
		if s.otherwise != nil {
			a = mergeEffect(a, c.localBlock(s.otherwise, env.child(), pure, parallel, loops))
		}
	}
	if s.tag == "match" {
		for _, arm := range s.arms {
			c.patterns(arm)
			if arm.guard != nil {
				g := c.expression(arm.guard, env, pure, parallel)
				c.require(arm.guard.tok, g, "bool")
				if g.effect != PureEffect || g.hasFlow {
					c.error(arm.guard.tok, "E-PURITY", "guard must be pure")
				}
			}
			a = mergeEffect(a, c.localBlock(arm.body, env.child(), pure, parallel, loops))
		}
	}
	return a
}
func (c *checker) resolve(e *expression) (Contract, bool) {
	if e.name == "claim.snapshot" {
		return Contract{Operation: Operation{"lip", "claim", "snapshot"}, Version: "0.9", Params: []Parameter{{Name: "store", Schema: Schema{Type: "string"}}}, Result: Schema{Type: "list"}, Effect: ReadOnlyEffect, Concurrent: true}, true
	}
	parts := strings.SplitN(e.name, ".", 2)
	d, ok := c.aliases[parts[0]]
	if !ok || len(parts) != 2 {
		c.error(e.tok, "E-OPERATION", "operation requires imported alias and member")
		return Contract{}, false
	}
	key := identityKey(Operation{d.language, d.path, parts[1]}.Wire())
	contract, ok := c.contracts[key]
	if !ok {
		c.error(e.tok, "E-OPERATION", "unregistered operation "+e.name)
	}
	return contract, ok
}
func (c *checker) expression(e *expression, env *scope, pure, parallel bool) analysis {
	if e == nil {
		return pureInfo("any")
	}
	a := pureInfo("any")
	sub := func(x *expression) analysis {
		v := c.expression(x, env, pure, parallel)
		a = mergeEffect(a, v)
		return v
	}
	switch e.tag {
	case "lit":
		a.typ = e.value.k.String()
	case "id":
		if store, ok := c.stores[e.name]; ok {
			if pure {
				c.error(e.tok, "E-PURITY", "fn cannot capture/read active Claim")
			}
			if _, declared := env.find(e.name); !declared || store.tok.site > e.tok.site {
				c.error(e.tok, "E-NAME", "store not yet declared")
			}
			if top, writer := c.writers[e.name]; writer && c.top <= top {
				c.error(e.tok, "E-CLAIM-ORDER", "complete read must follow every writer region")
			}
			a.typ = "list"
		} else if x, ok := env.find(e.name); ok {
			a = x.info
			a.effect, a.concurrent, a.hasFlow = PureEffect, true, false
		} else if _, ok := c.syntax.flows[e.name]; ok {
			c.error(e.tok, "E-TYPE", "Flow declarations are not values")
		} else {
			c.error(e.tok, "E-NAME", "unbound name "+e.name)
		}
	case "unary":
		x := sub(e.x)
		want := "number"
		if e.name == "!" {
			want = "bool"
		}
		c.require(e.tok, x, want)
		a.typ = want
	case "binary":
		x, y := sub(e.x), sub(e.y)
		switch e.name {
		case "&&", "||":
			c.require(e.tok, x, "bool")
			c.require(e.tok, y, "bool")
			a.typ = "bool"
		case "==", "!=":
			if x.typ == "function" || x.typ == "effect" || y.typ == "function" || y.typ == "effect" {
				c.error(e.tok, "E-TYPE", "function/effect equality is undefined")
			}
			a.typ = "bool"
		case "<", "<=", ">", ">=":
			if x.typ != "any" && y.typ != "any" && (x.typ != y.typ || x.typ != "number" && x.typ != "string") {
				c.error(e.tok, "E-TYPE", "comparison requires two numbers or two strings")
			}
			a.typ = "bool"
		default:
			if e.name == "+" && (x.typ == "string" && (y.typ == "string" || y.typ == "any") || y.typ == "string" && x.typ == "any") {
				a.typ = "string"
			} else if e.name == "+" && x.typ == "any" && y.typ == "any" {
				a.typ = "any"
			} else {
				c.require(e.tok, x, "number")
				c.require(e.tok, y, "number")
				a.typ = "number"
			}
		}
	case "list":
		for _, x := range e.elems {
			sub(x)
		}
		a.typ = "list"
	case "object":
		names := map[string]bool{}
		for _, f := range e.fields {
			if names[f.name] {
				c.error(f.tok, "E-DUPLICATE", "duplicate object field")
			}
			names[f.name] = true
			sub(f.expr)
		}
		a.typ = "object"
	case "field", "index":
		x := sub(e.x)
		if e.tag == "field" {
			c.require(e.tok, x, "object")
		} else if x.typ != "any" && x.typ != "list" && x.typ != "string" && x.typ != "object" {
			c.error(e.tok, "E-TYPE", "value cannot be indexed")
		}
		if e.y != nil {
			y := sub(e.y)
			if x.typ == "object" {
				c.require(e.tok, y, "string")
			} else if x.typ == "list" {
				if y.typ != "any" && y.typ != "number" && y.typ != "list" {
					c.error(e.tok, "E-TYPE", "list index requires number or list")
				}
				if y.typ == "list" {
					a.typ = "list"
					if e.y.tag == "list" {
						for _, item := range e.y.elems {
							c.require(item.tok, item.info, "number")
						}
					}
				}
			} else if x.typ == "string" {
				c.require(e.tok, y, "number")
				a.typ = "string"
			}
		}
		a.ops = nil
	case "native_receiver":
		a = sub(e.x)
		a.ops = nil
	case "slice":
		x := sub(e.x)
		if x.typ != "any" && x.typ != "list" && x.typ != "string" {
			c.error(e.tok, "E-TYPE", "slice requires list or string")
		}
		for _, arg := range e.args {
			y := sub(arg.expr)
			c.assignType(arg.expr.tok, y.typ, "number?")
		}
		a.typ, a.ops = x.typ, nil
	case "fn":
		return c.function(e, env)
	case "block":
		c.local++
		a = c.localBlock(e.body, env.child(), pure, parallel, 0)
		c.local--
	case "if":
		g := sub(e.x)
		c.require(e.tok, g, "bool")
		c.local++
		x := c.localBlock(e.body, env.child(), pure, parallel, 0)
		c.local--
		y := sub(e.y)
		a = mergeEffect(a, x)
		a.typ = unify(x.typ, y.typ)
		a.ops = unionOperations(x.ops, y.ops)
	case "match":
		sub(e.x)
		first := true
		for _, arm := range e.arms {
			c.patterns(arm)
			if arm.guard != nil {
				g := sub(arm.guard)
				c.require(arm.guard.tok, g, "bool")
				if g.effect != PureEffect || g.hasFlow {
					c.error(arm.guard.tok, "E-PURITY", "guard must be pure")
				}
			}
			x := sub(arm.result)
			if first {
				a.typ, a.ops = x.typ, x.ops
				first = false
			} else {
				a.typ = unify(a.typ, x.typ)
				a.ops = unionOperations(a.ops, x.ops)
			}
		}
	case "proposal":
		contract, ok := c.resolve(e)
		a.typ = "effect"
		if ok {
			a.ops = &operationSummary{operation: identityKey(contract.Operation.Wire())}
		}
		for _, arg := range e.args {
			x := sub(arg.expr)
			if x.effect != PureEffect || x.hasFlow {
				c.error(e.tok, "E-PURITY", "proposal arguments must be pure")
			}
			if x.typ == "function" || x.typ == "effect" {
				c.error(e.tok, "E-SCHEMA", "proposal argument must be Data")
			}
		}
		if ok {
			c.contractArguments(e, contract)
		}
		if e.name == "claim.snapshot" {
			if pure {
				c.error(e.tok, "E-PURITY", "fn cannot construct store snapshot")
			}
			if len(e.args) != 1 || e.args[0].expr.tag != "lit" || e.args[0].expr.value.k != String {
				c.error(e.tok, "E-SCHEMA", "snapshot requires a literal store name")
			} else {
				name := e.args[0].expr.value.s
				store, ok := c.stores[name]
				if !ok || store.tok.site >= e.tok.site {
					c.error(e.tok, "E-NAME", "unknown or undeclared snapshot store")
				}
			}
		}
	case "perform":
		x := sub(e.x)
		c.require(e.tok, x, "effect")
		if pure {
			c.error(e.tok, "E-PURITY", "fn cannot perform")
		}
		a.typ = "any"
		a.ops = nil
		ops, known := resolvedOperations(x.ops)
		if !known {
			a.effect = UnknownEffect
			a.concurrent = false
		} else {
			a.typ = ""
			for _, key := range ops {
				contract, ok := c.contracts[key]
				if !ok {
					contract = Contract{Result: Schema{Type: "list"}, Effect: ReadOnlyEffect, Concurrent: true}
				}
				if a.typ == "" {
					a.typ = normalizeType(contract.Result.Type)
				} else {
					a.typ = unify(a.typ, contract.Result.Type)
				}
				a = mergeEffect(a, analysis{effect: contract.Effect, concurrent: contract.Concurrent})
			}
		}
	case "pipe":
		first := sub(e.x)
		local := env.child()
		local.bindings["$pipe"] = bindingInfo{info: first}
		target := e.y.name
		if e.y.tag != "flow" && e.y.x.tag != "id" {
			c.error(e.tok, "E-TYPE", "pipeline target must be a static declaration")
		} else if e.y.tag != "flow" {
			target = e.y.x.name
		}
		if binding, ok := env.find(target); ok && !binding.static {
			c.error(e.tok, "E-TYPE", "pipeline target must be a static declaration")
		}
		saved := c.voidOK
		if saved == e {
			c.voidOK = e.y
		}
		stage := c.expression(e.y, local, pure, parallel)
		c.voidOK = saved
		a = mergeEffect(first, stage)
		a.typ, a.ops = stage.typ, stage.ops
	case "flow":
		a = c.flowCall(e, env, pure, parallel)
	case "call":
		if e.x.tag == "id" {
			if _, shadow := env.find(e.x.name); !shadow {
				if _, ok := c.syntax.flows[e.x.name]; ok {
					e.tag, e.name, e.tok = "flow", e.x.name, e.x.tok
					e.x = nil
					a = c.flowCall(e, env, pure, parallel)
					break
				}
			}
		}
		callee := sub(e.x)
		c.require(e.tok, callee, "function")
		for _, arg := range e.args {
			sub(arg.expr)
		}
		a.ops = callee.returns
		a.typ = "any"
		if e.x.tag == "fn" {
			c.checkArity(e, "", e.x, env)
			a.typ = e.x.typ
		}
		if e.x.tag == "id" {
			if binding, ok := env.find(e.x.name); ok {
				if binding.builtin != "" {
					c.checkArity(e, binding.builtin, nil, env)
					switch binding.builtin {
					case "len", "parse_number":
						a.typ = "number"
					case "str":
						a.typ = "string"
					case "print", "log", "telemetry":
						a.typ = "null"
					case "has":
						a.typ = "bool"
					case "range", "map", "concat", "keys", "split":
						a.typ = "list"
					case "put":
						a.typ = "object"
					case "slice":
						if len(e.args) > 0 {
							a.typ = e.args[0].expr.info.typ
						}
					}
				} else if binding.fn != nil {
					c.checkArity(e, "", binding.fn, env)
					a.typ = binding.fn.typ
				}
			}
		}
	case "comprehension":
		source := sub(e.y)
		c.require(e.tok, source, "list")
		local := env.child()
		c.declare(local, e.name, bindingInfo{info: pureInfo("any")}, e.tok)
		if e.z != nil {
			g := c.expression(e.z, local, pure, parallel)
			c.require(e.z.tok, g, "bool")
			if g.effect != PureEffect || g.hasFlow {
				c.error(e.z.tok, "E-PURITY", "comprehension guard must be pure")
			}
			a = mergeEffect(a, g)
		}
		a = mergeEffect(a, c.expression(e.x, local, pure, parallel))
		a.typ = "list"
	case "carry":
		if e.x != nil {
			source := sub(e.x)
			c.require(e.tok, source, "list")
		}
		sub(e.y)
		local := env.child()
		if e.name != "" {
			c.declare(local, e.name, bindingInfo{info: pureInfo("any")}, e.tok)
		}
		c.declare(local, e.state, bindingInfo{info: pureInfo("any")}, e.tok)
		c.local++
		a = mergeEffect(a, c.localBlock(e.body, local, pure, parallel, 0))
		c.local--
		a.typ = "any"
		a.ops = nil
	}
	if parallel && a.effect != PureEffect && (a.effect != ReadOnlyEffect || !a.concurrent) {
		c.error(e.tok, "E-PARALLEL", "perform is not provably Concurrent ReadOnly")
	}
	e.info = a
	return a
}
func (c *checker) checkArity(e *expression, builtin string, f *expression, env *scope) {
	if builtin != "" {
		for _, arg := range e.args {
			if arg.name != "" {
				c.error(e.tok, "E-ARITY", "core functions reject named arguments")
			}
		}
		valid := false
		for _, n := range builtinArity[builtin] {
			valid = valid || n == len(e.args)
		}
		if !valid {
			c.error(e.tok, "E-ARITY", "wrong core arity")
		}
		if valid {
			c.builtinTypes(e, builtin)
			if builtin == "map" || builtin == "fold" {
				index, arity := 1, 1
				if builtin == "fold" {
					index, arity = 2, 2
				}
				callback := e.args[index].expr
				info := bindingInfo{}
				if callback.tag == "fn" {
					info.fn = callback
				} else if callback.tag == "id" {
					info, _ = env.find(callback.name)
				}
				if info.fn != nil && len(info.fn.params) != arity {
					c.error(callback.tok, "E-ARITY", "wrong callback arity")
				}
				if info.builtin != "" {
					valid := false
					for _, n := range builtinArity[info.builtin] {
						valid = valid || n == arity
					}
					if !valid {
						c.error(callback.tok, "E-ARITY", "wrong core callback arity")
					}
				}
				if builtin == "fold" && info.fn != nil && len(info.fn.params) == 2 {
					c.assignType(e.args[1].expr.tok, e.args[1].expr.info.typ, info.fn.params[0].typ)
				}
				if source := e.args[0].expr; source.tag == "list" && info.fn != nil && len(info.fn.params) == arity {
					for _, element := range source.elems {
						c.assignType(element.tok, element.info.typ, info.fn.params[arity-1].typ)
					}
				}
			}
		}
		return
	}
	seen := map[string]bool{}
	next := 0
	for _, arg := range e.args {
		name := arg.name
		if name == "" {
			if next >= len(f.params) {
				c.error(e.tok, "E-ARITY", "too many arguments")
				continue
			}
			name = f.params[next].name
			next++
		}
		found := false
		for _, p := range f.params {
			if p.name == name {
				found = true
				c.assignType(arg.expr.tok, arg.expr.info.typ, p.typ)
			}
		}
		if !found || seen[name] {
			c.error(e.tok, "E-ARITY", "unknown/duplicate parameter")
		}
		seen[name] = true
	}
	if len(seen) != len(f.params) {
		c.error(e.tok, "E-ARITY", "missing arguments")
	}
}

func (c *checker) builtinTypes(e *expression, name string) {
	types := map[string][]string{"range": {"number"}, "map": {"list", "function"}, "fold": {"list", "any", "function"}, "fail": {"string"}, "concat": {"list", "list"}, "keys": {"object"}, "has": {"object", "string"}, "put": {"object", "string", "any"}, "split": {"string", "string"}, "parse_number": {"string"}, "log": {"string", "any"}, "telemetry": {"string", "object"}}
	if name == "len" || name == "slice" {
		typ := e.args[0].expr.info.typ
		if typ != "any" && typ != "string" && typ != "list" && (name == "slice" || typ != "object") {
			c.error(e.tok, "E-TYPE", "invalid "+name+" argument")
		}
	}
	if name == "str" && (e.args[0].expr.info.typ == "function" || e.args[0].expr.info.typ == "effect") {
		c.error(e.tok, "E-TYPE", "str requires Data")
	}
	if name == "slice" {
		for _, arg := range e.args[1:] {
			c.assignType(arg.expr.tok, arg.expr.info.typ, "number?")
		}
	}
	for i, typ := range types[name] {
		if name == "range" {
			for _, arg := range e.args {
				c.require(arg.expr.tok, arg.expr.info, "number")
			}
			break
		}
		if typ != "any" {
			c.require(e.args[i].expr.tok, e.args[i].expr.info, typ)
		}
	}
}
func (c *checker) contractArguments(e *expression, contract Contract) {
	assigned := make([]bool, len(contract.Params))
	next := 0
	for _, arg := range e.args {
		index := next
		if arg.name == "" {
			next++
		} else {
			index = -1
			for i, p := range contract.Params {
				if p.Name == arg.name {
					index = i
					break
				}
			}
		}
		if index < 0 || index >= len(contract.Params) || assigned[index] {
			c.error(e.tok, "E-ARITY", "unknown/duplicate operation argument")
			continue
		}
		assigned[index] = true
		actual, expected := arg.expr.info.typ, normalizeType(contract.Params[index].Schema.Type)
		if actual != "any" && expected != "any" && actual != expected && !(strings.HasSuffix(expected, "?") && (actual == "null" || actual == strings.TrimSuffix(expected, "?"))) {
			c.error(arg.expr.tok, "E-SCHEMA", "operation argument schema mismatch")
		}
	}
	for i, p := range contract.Params {
		if !assigned[i] && !p.Optional {
			c.error(e.tok, "E-ARITY", "missing operation argument "+p.Name)
		}
	}
}

func (c *checker) flowCall(e *expression, env *scope, pure, parallel bool) analysis {
	f := c.syntax.flows[e.name]
	if pure {
		c.error(e.tok, "E-PURITY", "fn cannot invoke Flow")
		return pureInfo(f.result)
	}
	// Arguments run in the caller's cursor before the child graph starts.
	// Their effects must remain visible even when the callee itself is Pure.
	arguments := pureInfo("any")
	for _, arg := range e.args {
		x := c.expression(arg.expr, env, false, parallel)
		arguments = mergeEffect(arguments, x)
	}
	c.checkArity(e, "", &expression{params: f.params}, env)
	if f.result == "void" && c.voidOK != e {
		c.error(e.tok, "E-TYPE", "void Flow cannot be used as a value")
	}
	_, err := checkFlow(c.syntax, Host{Contracts: contractValues(c.contracts)}, e.name)
	if err != nil && c.err == nil {
		if d, ok := err.(*Diagnostic); ok {
			c.err = d
		} else {
			c.error(e.tok, "E-TYPE", err.Error())
		}
	}
	a := mergeEffect(f.summary, arguments)
	a.typ = f.result
	a.hasFlow = true
	return a
}
func contractValues(contracts map[string]Contract) []Contract {
	out := make([]Contract, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, contract)
	}
	return out
}
