package script

import (
	"context"
	"fmt"
	"go/types"
	"strings"
)

// Native provenance describes the value at each container position. It is
// independent of normative proposal Ops and never makes a perform concurrent.
func nativeShapePointer(s nativeShape) *nativeShape { return &s }

func (s nativeShape) element() nativeShape {
	out := nativeShape{native: s.native}
	if s.item != nil {
		out = mergeNativeShape(out, *s.item)
	}
	for _, x := range s.items {
		out = mergeNativeShape(out, x)
	}
	for _, x := range s.fields {
		out = mergeNativeShape(out, x)
	}
	return out
}

func mergeNativeShape(a, b nativeShape) nativeShape {
	out := nativeShape{native: a.native || b.native}
	if a.fields != nil || b.fields != nil {
		out.fields = map[string]nativeShape{}
		for k, v := range a.fields {
			out.fields[k] = v
		}
		for k, v := range b.fields {
			out.fields[k] = mergeNativeShape(out.fields[k], v)
		}
	}
	if a.items != nil || b.items != nil || a.item != nil || b.item != nil {
		x := nativeShape{}
		for _, s := range []nativeShape{a, b} {
			if s.item != nil {
				x = mergeNativeShape(x, *s.item)
			}
			for _, v := range s.items {
				x = mergeNativeShape(x, v)
			}
		}
		out.item = &x
	}
	if a.result != nil || b.result != nil {
		x := nativeShape{}
		for _, s := range []nativeShape{a, b} {
			if s.result != nil {
				x = mergeNativeShape(x, *s.result)
			}
		}
		out.result = &x
	}
	return out
}

func (s nativeShape) empty() bool {
	return !s.native && len(s.fields) == 0 && len(s.items) == 0 && s.item == nil && s.result == nil
}

// Runtime function metadata contains provenance only. Lexical initializer
// syntax belongs to the compiler and must not extend a closure's captures.
func nativeRuntimeShape(s nativeShape) nativeShape {
	out := nativeShape{native: s.native}
	for name, field := range s.fields {
		if field = nativeRuntimeShape(field); !field.empty() {
			if out.fields == nil {
				out.fields = map[string]nativeShape{}
			}
			out.fields[name] = field
		}
	}
	for i, item := range s.items {
		if item = nativeRuntimeShape(item); !item.empty() {
			if out.items == nil {
				out.items = make([]nativeShape, len(s.items))
			}
			out.items[i] = item
		}
	}
	if s.item != nil {
		out.item = nativeShapePointer(nativeRuntimeShape(*s.item))
	}
	if s.result != nil {
		out.result = nativeShapePointer(nativeRuntimeShape(*s.result))
	}
	return out
}
func nativeValueShape(v Value) nativeShape {
	if _, ok := NativeDescription(v); ok {
		return nativeShape{native: true}
	}
	s := nativeShape{}
	if v.k == Function && v.f != nil {
		s.result = v.f.nativeResult
	}
	if v.k == Effect && v.p != nil {
		s.result = v.p.nativeResult
	}
	if v.k == Object {
		for k, x := range v.o {
			if shape := nativeValueShape(x); !shape.empty() {
				if s.fields == nil {
					s.fields = map[string]nativeShape{}
				}
				s.fields[k] = shape
			}
		}
	}
	if v.k == List {
		for i, x := range v.l {
			if shape := nativeValueShape(x); !shape.empty() {
				if s.items == nil {
					s.items = make([]nativeShape, len(v.l))
				}
				s.items[i] = shape
			}
		}
	}
	return s
}

func nativeSchemaShape(schema Schema) nativeShape {
	switch strings.TrimSuffix(schema.Type, "?") {
	case "null", "bool", "number", "string":
		return nativeShape{}
	case "list":
		item := nativeShape{native: true}
		if schema.Items != nil {
			item = nativeSchemaShape(*schema.Items)
		}
		return nativeShape{item: &item}
	}
	return nativeShape{native: true}
}
func nativeGoResultShape(sig *types.Signature) nativeShape {
	count := sig.Results().Len()
	if count > 1 && types.Identical(sig.Results().At(count-1).Type(), types.Universe.Lookup("error").Type()) {
		count--
	}
	var value func(types.Type) nativeShape
	value = func(t types.Type) nativeShape {
		if basic, ok := t.Underlying().(*types.Basic); ok && basic.Info()&(types.IsBoolean|types.IsString|types.IsInteger|types.IsFloat) != 0 {
			return nativeShape{}
		}
		return nativeShape{native: true}
	}
	if count == 0 {
		return nativeShape{}
	}
	if count == 1 {
		return value(sig.Results().At(0).Type())
	}
	out := nativeShape{items: make([]nativeShape, count)}
	for i := range out.items {
		out.items[i] = value(sig.Results().At(i).Type())
	}
	return out
}

// Library syntax is lowered to the same registered proposal/perform protocol
// as application adapters. No foreign callable is a LIP pure function.
func lowerNative(ctx context.Context, s *syntax, host Host, saved map[string]Value, avoid map[string]bool) (Host, error) {
	if host.Native == nil {
		return host, nil
	}
	host.Contracts = append([]Contract(nil), host.Contracts...)
	s.nativeOrigins = map[*expression]*expression{}
	aliases := map[string]importDecl{}
	registered := map[string]Contract{}
	for _, c := range host.Contracts {
		registered[identityKey(c.Operation.Wire())] = c
	}
	for _, im := range s.imports {
		aliases[im.alias] = im
		if im.language == "go" {
			if _, err := host.Native.discover(ctx, im.path); err != nil {
				explicit := false
				for _, c := range host.Contracts {
					explicit = explicit || c.Operation.Language == "go" && c.Operation.Path == im.path && c.Version != "lip-native-1"
				}
				if !explicit {
					return host, at(err, im.tok)
				}
			}
		}
	}
	names := map[string]bool{}
	for name := range avoid {
		names[name] = true
	}
	mapSyntaxTokens(s, func(t *token) {
		if t.kind == "id" {
			names[t.text] = true
		}
	})
	for name := range saved {
		names[name] = true
	}
	for name := range aliases {
		names[name] = true
	}
	fresh := func() string {
		for i := 0; ; i++ {
			name := fmt.Sprintf("__lip_native_%d", i)
			if !names[name] {
				names[name] = true
				return name
			}
		}
	}
	bridgeAlias := fresh()
	bridgeImported := false
	add := func(op Operation, action string) string {
		key := identityKey(op.Wire())
		if _, ok := registered[key]; !ok {
			c := host.Native.contract(op, action)
			host.Contracts = append(host.Contracts, c)
			registered[key] = c
		}
		if op.Language == "host" {
			if !bridgeImported {
				s.imports = append(s.imports, importDecl{language: "host", path: "lip.native", alias: bridgeAlias})
				bridgeImported = true
			}
			return bridgeAlias + "." + op.Member
		}
		return ""
	}
	foreign := map[*expression]nativeShape{}
	var failure error
	var lower func(*expression, map[string]nativeShape)
	var blockLower func(*block, map[string]nativeShape)
	var stmtLower func(*statement, map[string]nativeShape)
	var exitNative func(*statement, map[string]nativeShape) nativeShape
	child := func(scope map[string]nativeShape) map[string]nativeShape {
		out := map[string]nativeShape{}
		for k, v := range scope {
			out[k] = v
		}
		return out
	}
	shape := func(e *expression, scope map[string]nativeShape) nativeShape {
		if e == nil {
			return nativeShape{}
		}
		if e.tag == "id" {
			return scope[e.name]
		}
		return foreign[e]
	}
	isNative := func(e *expression, scope map[string]nativeShape) bool { return shape(e, scope).native }
	exitNative = func(s *statement, scope map[string]nativeShape) nativeShape {
		if s == nil {
			return nativeShape{}
		}
		out := nativeShape{}
		if s.tag == "next" || s.tag == "break" {
			out = shape(s.expr, scope)
		}
		for _, b := range []*block{s.body, s.otherwise} {
			if b != nil {
				out = mergeNativeShape(out, exitNative(b.exit, scope))
			}
		}
		out = mergeNativeShape(out, exitNative(s.other, scope))
		for _, a := range s.arms {
			if a.body != nil {
				out = mergeNativeShape(out, exitNative(a.body.exit, scope))
			}
		}
		return out
	}
	pack := func(args []argument, t token) (*expression, *expression) {
		pos := &expression{tag: "list", tok: t}
		named := &expression{tag: "object", tok: t}
		for _, a := range args {
			if a.name == "" {
				pos.elems = append(pos.elems, a.expr)
			} else {
				named.fields = append(named.fields, field{name: a.name, expr: a.expr, tok: a.expr.tok})
			}
		}
		return pos, named
	}
	// Receiver and arguments are fixed in source order before a pure proposal.
	performCall := func(e *expression, name string, args []argument, packed bool) {
		b := &block{tok: e.tok}
		for i, a := range args {
			local := fresh()
			b.items = append(b.items, &statement{tag: "bind", tok: a.expr.tok, name: local, expr: a.expr})
			args[i].expr = &expression{tag: "id", tok: a.expr.tok, name: local}
			s.nativeOrigins[args[i].expr] = a.expr
		}
		if packed {
			pos, named := pack(args, e.tok)
			args = []argument{{expr: pos}, {expr: named}}
		}
		b.tail = &expression{tag: "perform", tok: e.tok, x: &expression{tag: "proposal", tok: e.tok, name: name, args: args}}
		*e = expression{tag: "block", tok: e.tok, body: b}
		foreign[e] = nativeShape{native: true}
	}
	perform := func(e *expression, name string, args []argument) { performCall(e, name, args, true) }
	prepare := func(t token, name string, receiver *expression, args []argument) *expression {
		b := &block{tok: t}
		placeholders := []argument{}
		if receiver != nil {
			local := fresh()
			b.items = append(b.items, &statement{tag: "bind", tok: receiver.tok, name: local, expr: receiver})
			placeholders = append(placeholders, argument{expr: &expression{tag: "id", tok: t, name: local}})
		}
		for _, arg := range args {
			placeholders = append(placeholders, argument{name: arg.name, expr: &expression{tag: "lit", tok: t}})
		}
		pos, named := pack(placeholders, t)
		b.tail = &expression{tag: "perform", tok: t, x: &expression{tag: "proposal", tok: t, name: name, args: []argument{{expr: pos}, {expr: named}}}}
		return &expression{tag: "block", tok: t, body: b}
	}
	moduleTarget := func(e *expression) (importDecl, string, bool) {
		name, ok := memberName(e)
		if !ok {
			return importDecl{}, "", false
		}
		parts := strings.SplitN(name, ".", 2)
		im, ok := aliases[parts[0]]
		if !ok || im.language != "go" && im.language != "python" {
			return im, "", false
		}
		member := ""
		if len(parts) == 2 {
			member = parts[1]
		}
		return im, member, true
	}
	stmtLower = func(stmt *statement, scope map[string]nativeShape) {
		if stmt == nil {
			return
		}
		lower(stmt.expr, scope)
		local := child(scope)
		if stmt.tag == "for" {
			local[stmt.name] = shape(stmt.expr, scope).element()
		}
		blockLower(stmt.body, local)
		blockLower(stmt.otherwise, child(scope))
		stmtLower(stmt.other, child(scope))
		for _, a := range stmt.arms {
			lower(a.guard, scope)
			blockLower(a.body, child(scope))
		}
		if stmt.tag == "bind" {
			info := shape(stmt.expr, scope)
			info.source = stmt.expr
			scope[stmt.name] = info
		} else if stmt.tag == "claim" {
			scope[stmt.name] = nativeShape{}
		}
	}
	blockLower = func(b *block, scope map[string]nativeShape) {
		if b == nil {
			return
		}
		for _, stmt := range b.items {
			stmtLower(stmt, scope)
		}
		lower(b.tail, scope)
		stmtLower(b.exit, scope)
	}
	lower = func(e *expression, scope map[string]nativeShape) {
		if e == nil || failure != nil {
			return
		}
		if e.tag == "id" {
			if source := scope[e.name].source; source != nil {
				s.nativeOrigins[e] = source
			}
		}
		if e.tag == "pipe" {
			// A stage is a static LIP declaration, never a native callable or a
			// bridge helper. Preserve its callee for the ordinary stage checker.
			lower(e.x, scope)
			foreign[e] = shape(e.x, scope)
			for _, arg := range e.y.args[1:] {
				lower(arg.expr, scope)
				foreign[e] = mergeNativeShape(foreign[e], shape(arg.expr, scope))
			}
			return
		}
		// Capture a module path before lowering its receiver. Namespace lookup is
		// not an extra native call and must not invoke a function being referenced.
		if im, member, ok := moduleTarget(e); ok && (e.tag == "field" || e.tag == "id") {
			// A reserved source keyword separates adapter operations from real
			// library paths such as Python's get.member or prepare.member.
			op := Operation{im.language, im.path, "perform.get." + member}
			if member == "" {
				op.Member = "perform.get"
			}
			add(op, "get")
			perform(e, im.alias+"."+op.Member, nil)
			return
		}
		if e.tag == "proposal" {
			resultShape := nativeShape{native: true}
			parts := strings.SplitN(e.name, ".", 2)
			im, ok := aliases[parts[0]]
			if ok && len(parts) == 2 && (im.language == "go" || im.language == "python") {
				op := Operation{im.language, im.path, parts[1]}
				key := identityKey(op.Wire())
				if c, ok := registered[key]; ok && c.Version != "lip-native-1" {
					for _, a := range e.args {
						lower(a.expr, scope)
					}
					e.nativeResult = nativeShapePointer(nativeSchemaShape(c.Result))
					foreign[e] = nativeShape{result: e.nativeResult}
					return
				}
				if im.language == "go" {
					pkg, err := host.Native.discover(ctx, im.path)
					if err != nil {
						failure = at(err, e.tok)
						return
					}
					root := strings.Split(parts[1], ".")[0]
					if _, ok := pkg.members[root]; !ok {
						failure = at(diagnostic("E-OPERATION", "no exported Go symbol "+e.name), e.tok)
						return
					}
					if sig := pkg.signatures[parts[1]]; sig != nil {
						resultShape = nativeGoResultShape(sig)
						var pos []any
						named := map[string]any{}
						for _, a := range e.args {
							if a.name == "" {
								pos = append(pos, nil)
							} else {
								named[a.name] = []any{}
							}
						}
						if _, err := nativeGoArguments(sig, pos, named); err != nil {
							failure = at(diagnostic("E-ARITY", err.Error()), e.tok)
							return
						}
					} else if !strings.Contains(parts[1], ".") {
						count, named := 0, false
						for _, a := range e.args {
							if a.name == "" {
								count++
							} else {
								named = true
							}
						}
						var err error
						if pkg.manual {
							err = host.Native.Go.checkPackageCall(im.path, parts[1], count, named)
						} else if _, ok := pkg.pkg.Scope().Lookup(parts[1]).(*types.TypeName); ok {
							if named || count > 1 {
								err = diagnostic("E-ARITY", "type constructor accepts zero or one positional argument")
							}
						} else {
							err = diagnostic("E-TYPE", "callee is not callable")
						}
						if err != nil {
							failure = at(err, e.tok)
							return
						}
					}
				}
				for _, a := range e.args {
					lower(a.expr, scope)
				}
				add(op, "call")
				pos, named := pack(e.args, e.tok)
				e.args = []argument{{expr: pos}, {expr: named}}
				e.nativeResult = &resultShape
				foreign[e] = nativeShape{result: e.nativeResult}
				return
			}
			// Explicit effects may target any bound receiver, including Flow
			// parameters whose native provenance is only known at runtime.
			_, bound := scope[parts[0]]
			if len(parts) == 2 && bound && !ok {
				for _, a := range e.args {
					lower(a.expr, scope)
				}
				op := Operation{"host", "lip.native", "call." + parts[1]}
				name := add(op, "call")
				args := append([]argument{{expr: &expression{tag: "id", tok: e.tok, name: parts[0]}}}, e.args...)
				pos, named := pack(args, e.tok)
				e.name = name
				e.args = []argument{{expr: pos}, {expr: named}}
				e.nativeResult = nativeShapePointer(nativeShape{native: true})
				foreign[e] = nativeShape{result: e.nativeResult}
				return
			}
		}
		if e.tag == "call" {
			if e.x.tag == "id" && len(e.args) == 1 {
				action := ""
				switch e.x.name {
				case "$native_data":
					action = "snapshot"
				case "$native_release":
					action = "release"
				case "native_data", "native_release":
					if _, bound := scope[e.x.name]; !bound {
						action = "snapshot"
						if e.x.name == "native_release" {
							action = "release"
						}
					}
				case "len":
					action = "length"
				case "str":
					action = "string"
				}
				if action != "" {
					lower(e.args[0].expr, scope)
					if action == "snapshot" || action == "release" || isNative(e.args[0].expr, scope) {
						perform(e, add(Operation{"host", "lip.native", action}, action), e.args)
						foreign[e] = nativeShape{}
						return
					}
				}
			}
			if im, member, ok := moduleTarget(e.x); ok && member != "" {
				op := Operation{im.language, im.path, member}
				if c, ok := registered[identityKey(op.Wire())]; ok && c.Version != "lip-native-1" {
					for _, arg := range e.args {
						lower(arg.expr, scope)
					}
					performCall(e, im.alias+"."+member, e.args, false)
					foreign[e] = nativeSchemaShape(c.Result)
					return
				}
				add(op, "call")
				if im.language == "python" || strings.Contains(member, ".") {
					for _, arg := range e.args {
						lower(arg.expr, scope)
					}
					prep := Operation{im.language, im.path, "perform.prepare." + member}
					add(prep, "prepare")
					callee := prepare(e.x.tok, im.alias+"."+prep.Member, nil, e.args)
					perform(e, add(Operation{"host", "lip.native", "call"}, "call"), append([]argument{{expr: callee}}, e.args...))
					return
				}
				probe := &expression{tag: "proposal", tok: e.x.tok, name: im.alias + "." + member, args: append([]argument(nil), e.args...)}
				lower(probe, scope)
				if failure != nil {
					return
				}
				perform(e, im.alias+"."+member, e.args)
				if result := foreign[probe].result; result != nil {
					foreign[e] = *result
				}
				return
			}
			// A method is resolved by the provider, not by pure object projection.
			if e.x.tag == "field" {
				lower(e.x.x, scope)
				if isNative(e.x.x, scope) {
					for _, a := range e.args {
						lower(a.expr, scope)
					}
					name := add(Operation{"host", "lip.native", "prepare." + e.x.name}, "prepare")
					callee := prepare(e.x.tok, name, e.x.x, e.args)
					perform(e, add(Operation{"host", "lip.native", "call"}, "call"), append([]argument{{expr: callee}}, e.args...))
					return
				}
			}
		}
		local := scope
		if e.tag == "fn" || e.tag == "carry" || e.tag == "comprehension" {
			local = child(scope)
		}
		if e.tag == "fn" {
			for _, p := range e.params {
				local[p.name] = nativeShape{}
			}
		}
		if e.tag == "comprehension" {
			lower(e.y, scope)
			local[e.name] = shape(e.y, scope).element()
			lower(e.x, local)
		} else {
			lower(e.x, local)
			lower(e.y, scope)
		}
		lower(e.z, local)
		for _, a := range e.args {
			lower(a.expr, local)
		}
		for _, x := range e.elems {
			lower(x, local)
		}
		for _, f := range e.fields {
			lower(f.expr, local)
		}
		bodyScope := child(local)
		if e.tag == "carry" {
			bodyScope[e.state] = shape(e.y, scope)
			bodyScope[e.name] = shape(e.x, scope).element()
		}
		blockLower(e.body, bodyScope)
		for _, a := range e.arms {
			lower(a.guard, local)
			lower(a.result, local)
			blockLower(a.body, child(local))
		}
		switch e.tag {
		case "fn":
			foreign[e] = nativeShape{result: nativeShapePointer(nativeRuntimeShape(shape(e.body.tail, bodyScope)))}
			e.nativeResult = foreign[e].result
		case "perform":
			if result := shape(e.x, local).result; result != nil {
				foreign[e] = *result
			}
		case "block":
			foreign[e] = shape(e.body.tail, bodyScope)
		case "if":
			foreign[e] = mergeNativeShape(shape(e.body.tail, bodyScope), shape(e.y, local))
		case "match":
			for _, a := range e.arms {
				foreign[e] = mergeNativeShape(foreign[e], shape(a.result, local))
			}
		case "carry":
			foreign[e] = mergeNativeShape(shape(e.y, scope), exitNative(e.body.exit, bodyScope))
		case "comprehension":
			foreign[e] = nativeShape{item: nativeShapePointer(shape(e.x, local))}
		case "list":
			out := nativeShape{items: make([]nativeShape, len(e.elems))}
			for i, x := range e.elems {
				out.items[i] = shape(x, local)
			}
			foreign[e] = out
		case "object":
			out := nativeShape{fields: map[string]nativeShape{}}
			for _, f := range e.fields {
				out.fields[f.name] = shape(f.expr, local)
			}
			foreign[e] = out
		case "call":
			if isNative(e.x, local) {
				callee := prepare(e.x.tok, add(Operation{"host", "lip.native", "prepare"}, "prepare"), e.x, e.args)
				perform(e, add(Operation{"host", "lip.native", "call"}, "call"), append([]argument{{expr: callee}}, e.args...))
			} else {
				if result := shape(e.x, local).result; result != nil {
					foreign[e] = *result
				}
				for _, a := range e.args {
					foreign[e] = mergeNativeShape(foreign[e], shape(a.expr, local))
				}
			}
		case "field":
			if isNative(e.x, local) {
				perform(e, add(Operation{"host", "lip.native", "get." + e.name}, "get"), []argument{{expr: e.x}})
			} else {
				foreign[e] = shape(e.x, local).fields[e.name]
			}
		case "index":
			if isNative(e.x, local) {
				perform(e, add(Operation{"host", "lip.native", "index"}, "index"), []argument{{expr: e.x}, {expr: e.y}})
			} else {
				receiver := shape(e.x, local)
				if e.y.tag == "lit" && e.y.value.k == String {
					foreign[e] = receiver.fields[e.y.value.s]
				} else if e.y.tag == "lit" && e.y.value.k == Number && e.y.value.n >= 0 && e.y.value.n < float64(len(receiver.items)) && e.y.value.n == float64(int(e.y.value.n)) {
					foreign[e] = receiver.items[int(e.y.value.n)]
				} else {
					foreign[e] = receiver.element()
				}
			}
		}
	}
	scope := map[string]nativeShape{}
	for alias := range aliases {
		scope[alias] = nativeShape{}
	}
	for name := range s.functions {
		scope[name] = nativeShape{}
	}
	for name := range s.flows {
		scope[name] = nativeShape{}
	}
	for name := range s.data {
		scope[name] = nativeShape{}
	}
	for name, value := range saved {
		scope[name] = nativeValueShape(value)
	}
	for _, name := range sortedFunctionNames(s.functions) {
		lower(s.functions[name], scope)
		scope[name] = shape(s.functions[name], scope)
	}
	if len(s.flows) == 0 || s.cell {
		blockLower(s.body, scope)
	}
	if len(s.flows) != 0 {
		for _, name := range sortedFlowNames(s.flows) {
			local := child(scope)
			for _, param := range s.flows[name].params {
				local[param.name] = nativeShape{}
			}
			blockLower(s.flows[name].body, local)
		}
	}
	return host, failure
}
