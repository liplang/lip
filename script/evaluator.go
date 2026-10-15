package script

import (
	"context"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

type environment struct {
	parent *environment
	values map[string]Value
	refs   map[string]*node
	stores map[string]*claimStore
}

func newEnvironment(parent *environment) *environment {
	return &environment{parent: parent, values: map[string]Value{}, refs: map[string]*node{}}
}
func (e *environment) lookup(name string) (Value, error) {
	for e != nil {
		if v, ok := e.values[name]; ok {
			return v, nil
		}
		if n, ok := e.refs[name]; ok {
			if n.status != Completed {
				return Value{}, diagnostic("E-DEPENDENCY", "binding unavailable: "+name)
			}
			return n.value, nil
		}
		e = e.parent
	}
	return Value{}, diagnostic("E-NAME", "unbound name "+name)
}
func (e *environment) frozen(names map[string]bool) (*environment, error) {
	out := newEnvironment(nil)
	for name := range names {
		value, err := e.lookup(name)
		if err != nil {
			return nil, err
		}
		out.values[name] = value
	}
	return out, nil
}

type tailCall struct {
	fn   *function
	args []Value
}
type evalResult struct {
	value Value
	tail  *tailCall
}
type evaluator struct {
	run   *runState
	node  *node
	ctx   context.Context
	path  Position
	depth int
}

func (v *evaluator) step(t token) error {
	if v.node != nil && v.node.cut.Load() {
		return errorCut
	}
	if err := v.ctx.Err(); err != nil {
		return err
	}
	if v.run != nil {
		if err := v.run.step(); err != nil {
			return at(err, t)
		}
	}
	return nil
}
func (v *evaluator) result(e *expression, env *environment) (Value, error) {
	r, err := v.eval(e, env, false)
	return r.value, err
}
func (v *evaluator) eval(e *expression, env *environment, tail bool) (r evalResult, err error) {
	if e == nil {
		return r, nil
	}
	if e.native != nil {
		return e.native(v, env, tail)
	}
	if err = v.step(e.tok); err != nil {
		return r, err
	}
	defer func() {
		if err != nil && err != errorCut {
			err = at(err, e.tok)
			if d, ok := err.(*Diagnostic); ok && len(d.Position) == 0 && len(d.EvalPath) == 0 {
				d.EvalPath = v.path.Copy()
			}
		}
	}()
	get := func(x *expression) (Value, error) { return v.result(x, env) }
	switch e.tag {
	case "lit":
		r.value = e.value
	case "id":
		if store := findStore(env, e.name); store != nil {
			value, failure := store.read()
			return evalResult{value: value}, failure
		}
		r.value, err = env.lookup(e.name)
	case "unary":
		var x Value
		x, err = get(e.x)
		if err != nil {
			break
		}
		switch e.name {
		case "!":
			if x.k != Bool {
				err = diagnostic("E-TYPE", "! requires bool")
			} else {
				r.value = BoolValue(!x.b)
			}
		default:
			if x.k != Number {
				err = diagnostic("E-TYPE", "unary sign requires number")
			} else if e.name == "-" {
				r.value, err = NumberValue(-x.n)
			} else {
				r.value = x
			}
		}
	case "binary":
		var a, b Value
		a, err = get(e.x)
		if err != nil {
			break
		}
		if e.name == "&&" || e.name == "||" {
			if a.k != Bool {
				err = diagnostic("E-TYPE", "logical operand must be bool")
				break
			}
			if e.name == "&&" && !a.b || e.name == "||" && a.b {
				r.value = a
				break
			}
		}
		b, err = get(e.y)
		if err != nil {
			break
		}
		switch e.name {
		case "&&", "||":
			if b.k != Bool {
				err = diagnostic("E-TYPE", "logical operand must be bool")
			} else {
				r.value = b
			}
		case "==", "!=":
			var eq bool
			eq, err = Equal(a, b)
			if e.name == "!=" {
				eq = !eq
			}
			r.value = BoolValue(eq)
		case "<", "<=", ">", ">=":
			var less, equal bool
			if a.k == Number && b.k == Number {
				less, equal = a.n < b.n, a.n == b.n
			} else if a.k == String && b.k == String {
				less, equal = a.s < b.s, a.s == b.s
			} else {
				err = diagnostic("E-TYPE", "comparison requires matching number/string")
				break
			}
			switch e.name {
			case "<":
				r.value = BoolValue(less)
			case "<=":
				r.value = BoolValue(less || equal)
			case ">":
				r.value = BoolValue(!less && !equal)
			case ">=":
				r.value = BoolValue(!less)
			}
		default:
			r.value, err = arithmetic(e.name, a, b)
		}
	case "list":
		if err = v.run.reserveElements(int64(len(e.elems))); err != nil {
			break
		}
		xs := make([]Value, 0, len(e.elems))
		for _, x := range e.elems {
			var z Value
			z, err = get(x)
			if err != nil {
				break
			}
			xs = append(xs, z)
		}
		r.value = ListValue(xs...)
	case "object":
		o := map[string]Value{}
		for _, f := range e.fields {
			var x Value
			x, err = get(f.expr)
			if err != nil {
				break
			}
			o[f.name] = x
		}
		r.value = object(o)
	case "field", "index":
		var x, index Value
		x, err = get(e.x)
		if err != nil {
			break
		}
		if e.tag == "field" {
			index = textValue(e.name)
		} else {
			index, err = get(e.y)
			if err != nil {
				break
			}
		}
		if x.k == Object {
			if index.k != String {
				err = diagnostic("E-TYPE", "object index requires string")
				break
			}
			var ok bool
			r.value, ok = x.o[index.s]
			if !ok {
				err = diagnostic("E-INDEX", "missing key "+index.s)
			}
		} else if x.k == List || x.k == String {
			var i int
			i, err = nonnegativeIndex(index)
			if err != nil {
				break
			}
			if x.k == List {
				if i >= len(x.l) {
					err = diagnostic("E-INDEX", "index out of bounds")
				} else {
					r.value = x.l[i]
				}
			} else {
				rs := []rune(x.s)
				if i >= len(rs) {
					err = diagnostic("E-INDEX", "index out of bounds")
				} else {
					r.value = textValue(string(rs[i]))
				}
			}
		} else {
			err = diagnostic("E-TYPE", "value cannot be indexed")
		}
	case "fn":
		var captures *environment
		captures, err = env.frozen(freeNames(e))
		if err == nil {
			r.value = Value{k: Function, f: &function{params: e.params, typ: e.typ, body: e.body, captures: captures, nativeResult: e.nativeResult}}
		}
	case "block":
		return v.block(e.body, newEnvironment(env), tail)
	case "if":
		var g Value
		g, err = get(e.x)
		if err != nil {
			break
		}
		if g.k != Bool {
			err = diagnostic("E-TYPE", "if condition must be bool")
			break
		}
		if g.b {
			return v.block(e.body, newEnvironment(env), tail)
		}
		return v.eval(e.y, env, tail)
	case "match":
		var x Value
		x, err = get(e.x)
		if err != nil {
			break
		}
		var a *arm
		a, err = v.selectArm(e.arms, x, env)
		if err != nil {
			break
		}
		if a == nil {
			err = diagnostic("E-MATCH-NO-ARM", "no matching arm")
		} else {
			return v.eval(a.result, env, tail)
		}
	case "pipe":
		var value Value
		value, err = get(e.x)
		if err != nil {
			break
		}
		local := newEnvironment(env)
		local.values["$pipe"] = value
		return v.eval(e.y, local, tail)
	case "flow":
		r.value, err = v.invokeFlow(e, env)
	case "call":
		var callable Value
		callable, err = get(e.x)
		if err != nil {
			break
		}
		if callable.k != Function || callable.f == nil {
			err = diagnostic("E-TYPE", "callee is not a language function")
			break
		}
		fn := callable.f
		var order []int
		order, err = argumentOrder(fn, e.args)
		if err != nil {
			break
		}
		args := make([]Value, len(e.args))
		for i, arg := range e.args {
			var x Value
			x, err = get(arg.expr)
			if err != nil {
				break
			}
			args[order[i]] = x
		}
		if err != nil {
			break
		}
		if e.state == "stage" {
			for i := 1; i < len(e.args); i++ {
				if !args[order[i]].IsData() {
					err = diagnostic("E-SCHEMA", "pipeline named arguments must be Data")
					break
				}
			}
			if err != nil {
				break
			}
		}
		if fn.builtin != "" {
			r.value, err = v.builtin(fn.builtin, args)
			break
		}
		for i, p := range fn.params {
			if err = checkType(args[i], p.typ); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		if tail {
			r.tail = &tailCall{fn, args}
		} else {
			r.value, err = v.invoke(fn, args)
		}
	case "proposal":
		contract, _ := v.run.program.check.resolve(e)
		pos := []Value{}
		named := map[string]Value{}
		for _, arg := range e.args {
			var x Value
			x, err = get(arg.expr)
			if err != nil {
				break
			}
			if !x.IsData() {
				err = diagnostic("E-SCHEMA", "proposal argument must be Data")
				break
			}
			if arg.name == "" {
				pos = append(pos, x)
			} else {
				named[arg.name] = x
			}
		}
		if err != nil {
			break
		}
		_, err = bindContract(contract, pos, named)
		if err == nil {
			r.value = Value{k: Effect, p: &proposal{contract: contract, positional: pos, named: named, nativeResult: e.nativeResult}}
		}
	case "perform":
		var p Value
		p, err = get(e.x)
		if err != nil {
			break
		}
		if p.k != Effect || p.p == nil {
			err = diagnostic("E-TYPE", "perform requires proposal")
			break
		}
		r.value, err = v.run.perform(v, e.tok, p.p)
	case "comprehension":
		var source Value
		source, err = get(e.y)
		if err != nil {
			break
		}
		if source.k != List {
			err = diagnostic("E-TYPE", "comprehension requires list")
			break
		}
		if err = v.run.reserveElements(int64(len(source.l))); err != nil {
			break
		}
		v.recordRange(e.tok.site, len(source.l))
		xs := make([]Value, 0, len(source.l))
		saved := v.path.Copy()
		defer func() { v.path = saved }()
		for i, x := range source.l {
			local := newEnvironment(env)
			local.values[e.name] = x
			v.path = append(saved.Copy(), Pair{e.tok.site, strconv.Itoa(i)})
			if e.z != nil {
				var g Value
				g, err = v.result(e.z, local)
				if err != nil {
					break
				}
				if g.k != Bool {
					err = diagnostic("E-TYPE", "guard requires bool")
					break
				}
				if !g.b {
					continue
				}
			}
			var item Value
			item, err = v.result(e.x, local)
			if err != nil {
				break
			}
			xs = append(xs, item)
		}
		r.value = ListValue(xs...)
	case "carry":
		var source, acc Value
		if e.x != nil {
			source, err = get(e.x)
			if err != nil {
				break
			}
			if source.k != List {
				err = diagnostic("E-TYPE", "carry requires list")
				break
			}
		}
		if e.x != nil {
			v.recordRange(e.tok.site, len(source.l))
		}
		acc, err = get(e.y)
		if err != nil {
			break
		}
		saved := v.path.Copy()
		defer func() { v.path = saved }()
		index := new(big.Int)
		for e.x == nil || index.IsInt64() && index.Int64() < int64(len(source.l)) {
			local := newEnvironment(env)
			local.values[e.state] = acc
			if e.name != "" {
				local.values[e.name] = source.l[index.Int64()]
			}
			v.path = append(saved.Copy(), Pair{e.tok.site, index.String()})
			var control string
			acc, control, err = v.carryBlock(e.body, local)
			if err != nil || control == "break" {
				break
			}
			index.Add(index, big.NewInt(1))
		}
		r.value = acc
	}
	return r, err
}
func (v *evaluator) invoke(fn *function, args []Value) (Value, error) {
	if v.depth >= v.run.opts.MaxFrames {
		return Value{}, diagnostic("E-BUDGET", "non-tail call frame budget exhausted (limit: "+strconv.Itoa(v.run.opts.MaxFrames)+")")
	}
	v.depth++
	defer func() { v.depth-- }()
	// A tail chain must validate the innermost return before its callers.
	// Keep each type only at its last occurrence: passed duplicate checks are
	// redundant, and the finite type set keeps proper tail calls bounded.
	checks := []string{}
	for {
		typ := normalizeType(fn.typ)
		for i, previous := range checks {
			if previous == typ {
				checks = append(checks[:i], checks[i+1:]...)
				break
			}
		}
		checks = append(checks, typ)
		env := newEnvironment(fn.captures)
		for i, p := range fn.params {
			env.values[p.name] = args[i]
		}
		r, err := v.block(fn.body, env, true)
		if err != nil {
			return Value{}, err
		}
		if r.tail != nil {
			fn, args = r.tail.fn, r.tail.args
			continue
		}
		for i := len(checks) - 1; i >= 0; i-- {
			if err := checkType(r.value, checks[i]); err != nil {
				return Value{}, err
			}
		}
		return r.value, nil
	}
}
func (v *evaluator) block(b *block, env *environment, tail bool) (evalResult, error) {
	for _, s := range b.items {
		control, err := v.localStatement(s, env)
		if err != nil {
			return evalResult{}, err
		}
		if control != "" {
			return evalResult{}, diagnostic("E-CONTROL", "control escaped local block")
		}
	}
	return v.eval(b.tail, env, tail)
}
func (v *evaluator) localStatement(s *statement, env *environment) (string, error) {
	if s.native != nil {
		return s.native(v, env)
	}
	if err := v.step(s.tok); err != nil {
		return "", err
	}
	switch s.tag {
	case "bind":
		x, err := v.result(s.expr, env)
		if err == nil {
			env.values[s.name] = x
		}
		return "", err
	case "eval":
		_, err := v.result(s.expr, env)
		return "", err
	case "break", "continue":
		return s.tag, nil
	case "if":
		g, err := v.result(s.expr, env)
		if err != nil {
			return "", err
		}
		if g.k != Bool {
			return "", diagnostic("E-TYPE", "if requires bool")
		}
		if g.b {
			return v.localStatements(s.body, newEnvironment(env))
		}
		if s.other != nil {
			return v.localStatement(s.other, newEnvironment(env))
		}
		if s.otherwise != nil {
			return v.localStatements(s.otherwise, newEnvironment(env))
		}
	case "match":
		x, err := v.result(s.expr, env)
		if err != nil {
			return "", err
		}
		a, err := v.selectArm(s.arms, x, env)
		if err != nil {
			return "", err
		}
		if a != nil {
			return v.localStatements(a.body, newEnvironment(env))
		}
	case "for":
		var source Value
		var err error
		if s.expr != nil {
			source, err = v.result(s.expr, env)
			if err != nil {
				return "", err
			}
			if source.k != List {
				return "", diagnostic("E-TYPE", "for requires list")
			}
		}
		if s.expr != nil {
			v.recordRange(s.tok.site, len(source.l))
		}
		saved := v.path.Copy()
		defer func() { v.path = saved }()
		i := new(big.Int)
		for s.expr == nil || i.IsInt64() && i.Int64() < int64(len(source.l)) {
			if err := v.step(s.tok); err != nil {
				return "", err
			}
			v.path = append(saved.Copy(), Pair{s.tok.site, i.String()})
			local := newEnvironment(env)
			if s.name != "" {
				local.values[s.name] = source.l[i.Int64()]
			}
			control, e := v.localStatements(s.body, local)
			if e != nil {
				return "", e
			}
			if control == "break" {
				break
			}
			i.Add(i, big.NewInt(1))
		}
	default:
		return "", diagnostic("E-PURITY", "operation forbidden in a node-local block")
	}
	return "", nil
}
func (v *evaluator) localStatements(b *block, env *environment) (string, error) {
	for _, s := range b.items {
		control, err := v.localStatement(s, env)
		if err != nil || control != "" {
			return control, err
		}
	}
	return "", nil
}
func (v *evaluator) selectArm(arms []arm, x Value, env *environment) (*arm, error) {
	for i := range arms {
		a := &arms[i]
		matched := false
		for _, p := range a.patterns {
			if p.wildcard || p.typ != "" && x.k.String() == p.typ {
				matched = true
			} else if p.typ == "" && !p.wildcard && x.k == p.value.k {
				eq, err := Equal(x, p.value)
				if err != nil {
					return nil, err
				}
				matched = matched || eq
			}
		}
		if !matched {
			continue
		}
		if a.guard != nil {
			g, err := v.result(a.guard, env)
			if err != nil {
				return nil, err
			}
			if g.k != Bool {
				return nil, diagnostic("E-TYPE", "guard requires bool")
			}
			if !g.b {
				continue
			}
		}
		return a, nil
	}
	return nil, nil
}
func (v *evaluator) carryBlock(b *block, env *environment) (Value, string, error) {
	if _, err := v.localStatements(b, env); err != nil {
		return Value{}, "", err
	}
	s := b.exit
	if err := v.step(s.tok); err != nil {
		return Value{}, "", err
	}
	if s.tag == "next" || s.tag == "break" {
		x, err := v.result(s.expr, env)
		return x, s.tag, err
	}
	x, err := v.result(s.expr, env)
	if err != nil {
		return Value{}, "", err
	}
	if s.tag == "if" {
		if x.k != Bool {
			return Value{}, "", diagnostic("E-TYPE", "if requires bool")
		}
		if x.b {
			return v.carryBlock(s.body, newEnvironment(env))
		}
		if s.other != nil {
			return v.carryBlock(&block{exit: s.other}, newEnvironment(env))
		}
		return v.carryBlock(s.otherwise, newEnvironment(env))
	}
	a, err := v.selectArm(s.arms, x, env)
	if err != nil {
		return Value{}, "", err
	}
	if a == nil {
		return Value{}, "", diagnostic("E-MATCH-NO-ARM", "carry match has no arm")
	}
	return v.carryBlock(a.body, newEnvironment(env))
}
func argumentOrder(fn *function, args []argument) ([]int, error) {
	order := make([]int, len(args))
	if fn.builtin != "" {
		valid := false
		for _, n := range builtinArity[fn.builtin] {
			valid = valid || n == len(args)
		}
		if !valid {
			return nil, diagnostic("E-ARITY", "wrong core arity")
		}
		for i, a := range args {
			if a.name != "" {
				return nil, diagnostic("E-ARITY", "core function rejects named arguments")
			}
			order[i] = i
		}
		return order, nil
	}
	if len(args) != len(fn.params) {
		return nil, diagnostic("E-ARITY", "wrong function arity")
	}
	assigned := map[int]bool{}
	next := 0
	for i, a := range args {
		index := next
		if a.name == "" {
			next++
		} else {
			index = -1
			for j, p := range fn.params {
				if p.name == a.name {
					index = j
					break
				}
			}
		}
		if index < 0 || index >= len(fn.params) || assigned[index] {
			return nil, diagnostic("E-ARITY", "unknown/duplicate parameter")
		}
		assigned[index] = true
		order[i] = index
	}
	return order, nil
}
func nonnegativeIndex(x Value) (int, error) {
	if x.k != Number {
		return 0, diagnostic("E-TYPE", "index requires number")
	}
	if x.n < 0 || x.n > 9007199254740991 || math.Trunc(x.n) != x.n || x.n > float64(int(^uint(0)>>1)) {
		return 0, diagnostic("E-INDEX", "index must be a safe nonnegative integer")
	}
	return int(x.n), nil
}
func (v *evaluator) builtin(name string, xs []Value) (Value, error) {
	wrong := func() (Value, error) { return Value{}, diagnostic("E-TYPE", "invalid "+name+" arguments") }
	switch name {
	case "len":
		switch xs[0].k {
		case String:
			return number(float64(utf8.RuneCountInString(xs[0].s))), nil
		case List:
			return number(float64(len(xs[0].l))), nil
		case Object:
			return number(float64(len(xs[0].o))), nil
		}
		return wrong()
	case "str":
		s, e := Display(xs[0])
		return textValue(s), e
	case "fail":
		if xs[0].k != String {
			return wrong()
		}
		return Value{}, diagnostic("E-FAIL", xs[0].s)
	case "concat":
		if xs[0].k != List || xs[1].k != List {
			return wrong()
		}
		if err := v.run.reserveElements(int64(len(xs[0].l)) + int64(len(xs[1].l))); err != nil {
			return Value{}, err
		}
		out := make([]Value, 0, len(xs[0].l)+len(xs[1].l))
		out = append(out, xs[0].l...)
		out = append(out, xs[1].l...)
		return ListValue(out...), nil
	case "keys":
		if xs[0].k != Object {
			return wrong()
		}
		if err := v.run.reserveElements(int64(len(xs[0].o))); err != nil {
			return Value{}, err
		}
		out := []Value{}
		for _, k := range sortedKeys(xs[0].o) {
			out = append(out, textValue(k))
		}
		return ListValue(out...), nil
	case "has":
		if xs[0].k != Object || xs[1].k != String {
			return wrong()
		}
		_, ok := xs[0].o[xs[1].s]
		return BoolValue(ok), nil
	case "put":
		if xs[0].k != Object || xs[1].k != String {
			return wrong()
		}
		o, _ := xs[0].Fields()
		o[xs[1].s] = xs[2]
		return object(o), nil
	case "split":
		if xs[0].k != String || xs[1].k != String {
			return wrong()
		}
		if xs[1].s == "" {
			return Value{}, diagnostic("E-VALUE", "separator cannot be empty")
		}
		if err := v.run.reserveElements(int64(strings.Count(xs[0].s, xs[1].s)) + 1); err != nil {
			return Value{}, err
		}
		parts := strings.Split(xs[0].s, xs[1].s)
		out := make([]Value, len(parts))
		for i, s := range parts {
			out[i] = textValue(s)
		}
		return ListValue(out...), nil
	case "slice":
		start, err := nonnegativeIndex(xs[1])
		if err != nil {
			return Value{}, err
		}
		end, err := nonnegativeIndex(xs[2])
		if err != nil {
			return Value{}, err
		}
		if end < start {
			return Value{}, diagnostic("E-INDEX", "reversed slice")
		}
		if xs[0].k == List {
			if end > len(xs[0].l) {
				return Value{}, diagnostic("E-INDEX", "slice out of bounds")
			}
			if err := v.run.reserveElements(int64(end - start)); err != nil {
				return Value{}, err
			}
			return ListValue(xs[0].l[start:end]...), nil
		}
		if xs[0].k == String {
			rs := []rune(xs[0].s)
			if end > len(rs) {
				return Value{}, diagnostic("E-INDEX", "slice out of bounds")
			}
			return textValue(string(rs[start:end])), nil
		}
		return wrong()
	case "parse_number":
		if xs[0].k != String {
			return wrong()
		}
		return parseNumericText(xs[0].s)
	case "range":
		start, end, step := int64(0), int64(0), int64(1)
		numbers := make([]int64, len(xs))
		for i, x := range xs {
			if x.k != Number || math.Trunc(x.n) != x.n || math.Abs(x.n) > 9007199254740991 {
				return wrong()
			}
			numbers[i] = int64(x.n)
		}
		if len(xs) == 1 {
			end = numbers[0]
		} else {
			start, end = numbers[0], numbers[1]
		}
		if len(xs) == 3 {
			step = numbers[2]
		}
		if step == 0 {
			return Value{}, diagnostic("E-VALUE", "range step is zero")
		}
		count := int64(0)
		if step > 0 && start < end {
			count = (end-start-1)/step + 1
		} else if step < 0 && start > end {
			count = (start-end-1)/(-step) + 1
		}
		if err := v.run.reserveElements(count); err != nil {
			return Value{}, err
		}
		out := make([]Value, int(count))
		for i := range out {
			out[i] = number(float64(start + int64(i)*step))
		}
		return ListValue(out...), nil
	case "map", "fold":
		if xs[0].k != List {
			return wrong()
		}
		f := xs[1]
		seed := Value{}
		if name == "fold" {
			seed = xs[1]
			f = xs[2]
		}
		if f.k != Function {
			return wrong()
		}
		arity := 1
		if name == "fold" {
			arity = 2
		}
		if _, err := argumentOrder(f.f, make([]argument, arity)); err != nil {
			return Value{}, err
		}
		if name == "map" {
			if err := v.run.reserveElements(int64(len(xs[0].l))); err != nil {
				return Value{}, err
			}
		}
		out := make([]Value, 0, len(xs[0].l))
		for _, x := range xs[0].l {
			if err := v.step(v.node.stmt.tok); err != nil {
				return Value{}, err
			}
			args := []Value{x}
			if name == "fold" {
				args = []Value{seed, x}
			}
			as := make([]argument, len(args))
			if _, err := argumentOrder(f.f, as); err != nil {
				return Value{}, err
			}
			if f.f.builtin == "" {
				for i, p := range f.f.params {
					if err := checkType(args[i], p.typ); err != nil {
						return Value{}, err
					}
				}
			}
			var value Value
			var err error
			if f.f.builtin != "" {
				value, err = v.builtin(f.f.builtin, args)
			} else {
				value, err = v.invoke(f.f, args)
			}
			if err != nil {
				return Value{}, err
			}
			if name == "fold" {
				seed = value
			} else {
				out = append(out, value)
			}
		}
		if name == "fold" {
			return seed, nil
		}
		return ListValue(out...), nil
	}
	return wrong()
}
