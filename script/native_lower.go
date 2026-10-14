package script

import (
	"context"
	"fmt"
	"go/types"
	"strings"
)

// Library syntax is lowered to the same registered proposal/perform protocol
// as application adapters. No foreign callable is a LIP pure function.
func lowerNative(ctx context.Context, s *syntax, host Host, saved map[string]Value) (Host, error) {
	if host.Native == nil {
		return host, nil
	}
	host.Contracts = append([]Contract(nil), host.Contracts...)
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
	ts, _ := lex(s.source)
	for _, t := range ts {
		if t.kind == "id" {
			names[t.text] = true
		}
	}
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
	foreign := map[*expression]bool{}
	var failure error
	var lower func(*expression, map[string]bool)
	var blockLower func(*block, map[string]bool)
	var stmtLower func(*statement, map[string]bool)
	var exitNative func(*statement, map[string]bool) bool
	child := func(scope map[string]bool) map[string]bool {
		out := map[string]bool{}
		for k, v := range scope {
			out[k] = v
		}
		return out
	}
	isNative := func(e *expression, scope map[string]bool) bool {
		return e != nil && (foreign[e] || e.tag == "id" && scope[e.name])
	}
	exitNative = func(s *statement, scope map[string]bool) bool {
		if s == nil {
			return false
		}
		if isNative(s.expr, scope) && (s.tag == "next" || s.tag == "break") {
			return true
		}
		for _, b := range []*block{s.body, s.otherwise} {
			if b != nil && exitNative(b.exit, scope) {
				return true
			}
		}
		if exitNative(s.other, scope) {
			return true
		}
		for _, a := range s.arms {
			if a.body != nil && exitNative(a.body.exit, scope) {
				return true
			}
		}
		return false
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
		}
		if packed {
			pos, named := pack(args, e.tok)
			args = []argument{{expr: pos}, {expr: named}}
		}
		b.tail = &expression{tag: "perform", tok: e.tok, x: &expression{tag: "proposal", tok: e.tok, name: name, args: args}}
		*e = expression{tag: "block", tok: e.tok, body: b}
		foreign[e] = true
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
	stmtLower = func(stmt *statement, scope map[string]bool) {
		if stmt == nil {
			return
		}
		lower(stmt.expr, scope)
		local := child(scope)
		if stmt.tag == "for" {
			local[stmt.name] = isNative(stmt.expr, scope)
		}
		blockLower(stmt.body, local)
		blockLower(stmt.otherwise, child(scope))
		stmtLower(stmt.other, child(scope))
		for _, a := range stmt.arms {
			lower(a.guard, scope)
			blockLower(a.body, child(scope))
		}
		if stmt.tag == "bind" {
			scope[stmt.name] = isNative(stmt.expr, scope)
		}
	}
	blockLower = func(b *block, scope map[string]bool) {
		if b == nil {
			return
		}
		for _, stmt := range b.items {
			stmtLower(stmt, scope)
		}
		lower(b.tail, scope)
		stmtLower(b.exit, scope)
	}
	lower = func(e *expression, scope map[string]bool) {
		if e == nil || failure != nil {
			return
		}
		// Capture a module path before lowering its receiver. Namespace lookup is
		// not an extra native call and must not invoke a function being referenced.
		if im, member, ok := moduleTarget(e); ok && (e.tag == "field" || e.tag == "id") {
			op := Operation{im.language, im.path, "get." + member}
			if member == "" {
				op.Member = "get"
			}
			add(op, "get")
			perform(e, im.alias+"."+op.Member, nil)
			return
		}
		if e.tag == "proposal" {
			parts := strings.SplitN(e.name, ".", 2)
			im, ok := aliases[parts[0]]
			if ok && len(parts) == 2 && (im.language == "go" || im.language == "python") {
				op := Operation{im.language, im.path, parts[1]}
				key := identityKey(op.Wire())
				if c, ok := registered[key]; ok && c.Version != "lip-native-1" {
					for _, a := range e.args {
						lower(a.expr, scope)
					}
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
				foreign[e] = true
				return
			}
			if len(parts) == 2 && scope[parts[0]] {
				for _, a := range e.args {
					lower(a.expr, scope)
				}
				op := Operation{"host", "lip.native", "call." + parts[1]}
				name := add(op, "call")
				args := append([]argument{{expr: &expression{tag: "id", tok: e.tok, name: parts[0]}}}, e.args...)
				pos, named := pack(args, e.tok)
				e.name = name
				e.args = []argument{{expr: pos}, {expr: named}}
				foreign[e] = true
				return
			}
		}
		if e.tag == "call" {
			if e.x.tag == "id" && len(e.args) == 1 {
				action := ""
				switch e.x.name {
				case "native_data":
					action = "snapshot"
				case "native_release":
					action = "release"
				case "len":
					action = "length"
				case "str":
					action = "string"
				}
				if action != "" {
					lower(e.args[0].expr, scope)
					if e.x.name == "native_data" || e.x.name == "native_release" || isNative(e.args[0].expr, scope) {
						perform(e, add(Operation{"host", "lip.native", action}, action), e.args)
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
					return
				}
				add(op, "call")
				if im.language == "python" || strings.Contains(member, ".") {
					for _, arg := range e.args {
						lower(arg.expr, scope)
					}
					prep := Operation{im.language, im.path, "prepare." + member}
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
				local[p.name] = false
			}
		}
		if e.tag == "comprehension" {
			lower(e.y, scope)
			local[e.name] = isNative(e.y, scope)
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
			bodyScope[e.state] = isNative(e.y, scope)
			bodyScope[e.name] = isNative(e.x, scope)
		}
		blockLower(e.body, bodyScope)
		for _, a := range e.arms {
			lower(a.guard, local)
			lower(a.result, local)
			blockLower(a.body, child(local))
		}
		switch e.tag {
		case "perform":
			foreign[e] = isNative(e.x, local)
		case "block":
			foreign[e] = isNative(e.body.tail, bodyScope)
		case "if":
			foreign[e] = isNative(e.body.tail, bodyScope) || isNative(e.y, local)
		case "match":
			for _, a := range e.arms {
				foreign[e] = foreign[e] || isNative(a.result, local)
			}
		case "carry":
			foreign[e] = isNative(e.y, scope) || exitNative(e.body.exit, bodyScope)
		case "comprehension":
			foreign[e] = isNative(e.x, local)
		case "list":
			for _, x := range e.elems {
				foreign[e] = foreign[e] || isNative(x, local)
			}
		case "object":
			for _, f := range e.fields {
				foreign[e] = foreign[e] || isNative(f.expr, local)
			}
		case "call":
			if isNative(e.x, local) {
				callee := prepare(e.x.tok, add(Operation{"host", "lip.native", "prepare"}, "prepare"), e.x, e.args)
				perform(e, add(Operation{"host", "lip.native", "call"}, "call"), append([]argument{{expr: callee}}, e.args...))
			} else {
				for _, a := range e.args {
					foreign[e] = foreign[e] || isNative(a.expr, local)
				}
			}
		case "field":
			if isNative(e.x, local) {
				perform(e, add(Operation{"host", "lip.native", "get." + e.name}, "get"), []argument{{expr: e.x}})
			}
		case "index":
			if isNative(e.x, local) {
				perform(e, add(Operation{"host", "lip.native", "index"}, "index"), []argument{{expr: e.x}, {expr: e.y}})
			}
		}
	}
	scope := map[string]bool{}
	for name, value := range saved {
		scope[name] = containsNative(value)
	}
	for _, name := range sortedFunctionNames(s.functions) {
		lower(s.functions[name], scope)
	}
	blockLower(s.body, scope)
	return host, failure
}
