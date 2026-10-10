package compiler

import (
	"fmt"
	"strings"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/token"
)

type importAlias struct {
	Kind      string
	Target    string
	Namespace bool
}

func aliasTarget(dependency ast.Dependency) importAlias {
	target := dependencyRoot(dependency.Spec)
	namespace := dependency.Kind != "host" || strings.HasSuffix(target, ".*")
	return importAlias{Kind: dependency.Kind, Target: strings.TrimSuffix(target, ".*"), Namespace: namespace}
}

func externalCall(call *ast.CallExpr) bool { return call.Python || call.Host }

func dependencyAliases(program *ast.Program) (map[string]importAlias, error) {
	aliases := map[string]importAlias{}
	for _, dependency := range program.Dependencies {
		name := dependency.Alias
		if name == "" {
			continue
		}
		fail := func(message string) (map[string]importAlias, error) {
			return nil, fmt.Errorf("%d:%d: %s", dependency.Pos.Line, dependency.Pos.Column, message)
		}
		tokens, err := lexer.New(name).Lex()
		if err != nil || len(tokens) != 2 || tokens[0].Kind != token.Ident || tokens[0].Text != name {
			return fail(fmt.Sprintf("invalid module alias %q; use one identifier after as", name))
		}
		if reservedName(name) || name == "python" {
			return fail(fmt.Sprintf("module alias %q is reserved; choose a distinct module name", name))
		}
		if _, exists := aliases[name]; exists {
			return fail(fmt.Sprintf("duplicate module alias %q; each alias must name one module", name))
		}
		aliases[name] = aliasTarget(dependency)
	}
	for _, dependency := range program.Dependencies {
		if dependency.Alias != "" || dependency.Kind == "go" && strings.Contains(dependency.Spec, "/") {
			continue
		}
		root := strings.SplitN(dependencyRoot(dependency.Spec), ".", 2)[0]
		if target, exists := aliases[root]; exists && target.Namespace && dependency.Kind == "host" && dependency.Spec == root {
			// root(...) and root.operation(...) occupy different call forms.
			continue
		}
		if target, exists := aliases[root]; exists && (target.Kind != dependency.Kind || target.Target != root && target.Target != dependency.Spec) {
			return nil, fmt.Errorf("%d:%d: module alias %q conflicts with the %s import %q", dependency.Pos.Line, dependency.Pos.Column, root, dependency.Kind, dependency.Spec)
		}
	}
	for _, fn := range program.Functions {
		if target, exists := aliases[fn.Name]; exists && !target.Namespace {
			return nil, fmt.Errorf("%d:%d: function %q conflicts with a module alias", fn.Pos.Line, fn.Pos.Column, fn.Name)
		}
	}
	return aliases, nil
}

// Resolve module references in a copy: source ASTs and REPL declarations retain their
// spelling and positions, while the graph and hosts use real operation names.
func resolveAliases(program *ast.Program, aliases map[string]importAlias) (*ast.Program, error) {
	functions := map[string]bool{}
	for _, fn := range program.Functions {
		functions[fn.Name] = true
	}
	var pythonModules []string
	var allPythonModules []string
	var goModules []string
	var hostModules []string
	for _, dependency := range program.Dependencies {
		if dependency.Kind == "python" {
			module := dependencyRoot(dependency.Spec)
			allPythonModules = append(allPythonModules, module)
			if dependency.Alias == "" {
				pythonModules = append(pythonModules, module)
			}
		}
		if dependency.Kind == "go" && dependency.Alias == "" {
			goModules = append(goModules, dependency.Spec)
		}
		if dependency.Kind == "host" && dependency.Alias == "" {
			hostModules = append(hostModules, dependency.Spec)
		}
	}
	bound := map[string]bool{}
	moduleReference := func(name string, pos token.Pos) ast.Expr {
		if functions[name] {
			return nil
		}
		root, rest, dotted := strings.Cut(name, ".")
		if bound[root] {
			return nil
		}
		if module, exists := aliases[root]; exists {
			if module.Kind != "python" {
				return nil
			}
			name = module.Target
			if dotted {
				name += "." + rest
			}
		} else {
			// Ordinary object fields still belong to local values, including
			// parameters and comprehension variables named after a module.
			if bound[root] {
				return nil
			}
			declared := false
			for _, module := range allPythonModules {
				if name == module || strings.HasPrefix(name, module+".") {
					declared = true
					break
				}
			}
			if !declared {
				return nil
			}
		}
		return &ast.CallExpr{Name: name, Pos: pos, Python: true, PythonAttribute: true}
	}
	var aliasError error
	operationName := func(name string, pos token.Pos) (string, *importAlias) {
		root, rest, dotted := strings.Cut(name, ".")
		if bound[root] {
			_, alias := aliases[root]
			imported := alias
			for _, dependency := range program.Dependencies {
				spec := dependencyRoot(dependency.Spec)
				if name == spec || strings.HasPrefix(name, strings.TrimSuffix(spec, ".*")+".") {
					imported = true
				}
			}
			if imported && !functions[name] && aliasError == nil {
				aliasError = fmt.Errorf("%d:%d: cannot call %q through value %q, which shadows an import; use a different import alias", pos.Line, pos.Column, name, root)
			}
			return name, nil
		}
		module, exists := aliases[root]
		if exists && module.Namespace && !dotted && (builtinName(name) || functions[name]) {
			// A namespace alias applies to qualified calls; it does not hide a
			// bare core/local call with the same spelling.
			exists = false
		}
		if exists && !module.Namespace && dotted && builtinName(name) {
			// A bare operation alias does not hide core namespace members.
			exists = false
		}
		if !exists {
			if functions[name] {
				return name, &importAlias{Kind: "fn", Target: name}
			}
			kinds := map[string]bool{}
			for _, spec := range goModules {
				if strings.HasPrefix(name, spec+".") {
					kinds["go"] = true
				}
			}
			for _, spec := range pythonModules {
				if name == spec || strings.HasPrefix(name, spec+".") {
					kinds["python"] = true
				}
			}
			for _, spec := range hostModules {
				if strings.HasSuffix(spec, ".*") && name == strings.TrimSuffix(spec, ".*") && aliasError == nil {
					aliasError = fmt.Errorf("%d:%d: Host namespace %q is not a function; call %s.operation(...) instead", pos.Line, pos.Column, name, name)
				}
				if name == spec || strings.HasSuffix(spec, ".*") && strings.HasPrefix(name, strings.TrimSuffix(spec, "*")) {
					kinds["host"] = true
				}
			}
			// Canonical external names remain usable after as. Aliased imports
			// leave core operations available through their standard names.
			if !builtinName(name) {
				for _, dependency := range program.Dependencies {
					spec := dependencyRoot(dependency.Spec)
					switch dependency.Kind {
					case "go":
						if strings.HasPrefix(name, spec+".") {
							kinds["go"] = true
						}
					case "python":
						if name == spec || strings.HasPrefix(name, spec+".") {
							kinds["python"] = true
						}
					case "host":
						if strings.HasSuffix(spec, ".*") && name == strings.TrimSuffix(spec, ".*") && aliasError == nil {
							aliasError = fmt.Errorf("%d:%d: Host namespace %q is not a function; call %s.operation(...) instead", pos.Line, pos.Column, name, name)
						}
						if name == spec || strings.HasSuffix(spec, ".*") && strings.HasPrefix(name, strings.TrimSuffix(spec, "*")) {
							kinds["host"] = true
						}
					}
				}
			}
			if len(kinds) > 1 && aliasError == nil {
				aliasError = fmt.Errorf("%d:%d: operation %q matches imports from multiple backends; use distinct as aliases", pos.Line, pos.Column, name)
			}
			for _, kind := range []string{"host", "go", "python"} {
				if kinds[kind] {
					return name, &importAlias{Kind: kind, Target: name}
				}
			}
			return name, nil
		}
		if module.Namespace != dotted && aliasError == nil {
			if module.Namespace {
				aliasError = fmt.Errorf("%d:%d: module alias %q is not a function; call %s.operation(...) instead", pos.Line, pos.Column, root, root)
			} else {
				aliasError = fmt.Errorf("%d:%d: operation alias %q names %q; call %s(...) directly", pos.Line, pos.Column, root, module.Target, root)
			}
		}
		name = module.Target
		if dotted {
			name += "." + rest
		}
		return name, &module
	}
	var expression func(ast.Expr) ast.Expr
	expression = func(expr ast.Expr) ast.Expr {
		switch value := expr.(type) {
		case *ast.IdentExpr:
			if reference := moduleReference(value.Name, value.Pos); reference != nil {
				return reference
			}
			return expr
		case *ast.CallExpr:
			copy := *value
			root, _, _ := strings.Cut(value.Name, ".")
			var module *importAlias
			copy.Name, module = operationName(value.Name, value.Pos)
			if module != nil {
				copy.Python, copy.Host = module.Kind == "python", module.Kind == "host" || module.Kind == "go"
				copy.Local = module.Kind == "fn"
			}
			if !externalCall(&copy) && functions[copy.Name] {
				copy.Local = true
			}
			// Disambiguate a named comparator from a bool value before generic
			// callback traversal. Bound variables always retain their value role.
			if !externalCall(&copy) && !copy.Local && (copy.Name == "sort" || copy.Name == "list.sort") && len(value.Args) == 2 {
				if fn, ok := value.Args[1].(*ast.IdentExpr); ok && functions[fn.Name] && !bound[fn.Name] && (len(value.ArgNames) < 2 || value.ArgNames[1] == "") {
					copy.Name = "list.sort_with"
				}
			}
			if copy.Python && !strings.Contains(copy.Name, ".") && aliasError == nil {
				aliasError = fmt.Errorf("%d:%d: Python module %q is not a function; call an operation inside it", value.Pos.Line, value.Pos.Column, root)
			}
			copy.Args = make([]ast.Expr, len(value.Args))
			for i, arg := range value.Args {
				if copy.Name == "feedback" && !externalCall(&copy) && (i == 1 || i == 2) {
					if name, ok := moduleReferenceName(arg); ok {
						resolved, target := operationName(name, value.Pos)
						kind := ""
						if target != nil {
							kind = target.Kind
						}
						copy.Args[i] = &ast.IdentExpr{Name: resolved, Pos: value.Pos, OperationKind: kind}
						continue
					}
				}
				copy.Args[i] = expression(arg)
			}
			return &copy
		case *ast.UnaryExpr:
			copy := *value
			copy.Operand = expression(value.Operand)
			return &copy
		case *ast.LambdaExpr:
			copy := *value
			previous := bound
			bound = cloneBoolMap(bound)
			for _, param := range value.Params {
				bound[param] = true
			}
			copy.Return = expression(value.Return)
			bound = previous
			return &copy
		case *ast.BinaryExpr:
			copy := *value
			copy.Left, copy.Right = expression(value.Left), expression(value.Right)
			return &copy
		case *ast.IfExpr:
			copy := *value
			copy.Cond, copy.Then, copy.Else = expression(value.Cond), expression(value.Then), expression(value.Else)
			return &copy
		case *ast.MatchExpr:
			copy := *value
			copy.Value = expression(value.Value)
			copy.Arms = append([]ast.MatchArm{}, value.Arms...)
			for i := range copy.Arms {
				copy.Arms[i].Guard = expression(copy.Arms[i].Guard)
				copy.Arms[i].Expr = expression(copy.Arms[i].Expr)
			}
			return &copy
		case *ast.ListExpr:
			copy := *value
			copy.Items = make([]ast.Expr, len(value.Items))
			for i, item := range value.Items {
				copy.Items[i] = expression(item)
			}
			return &copy
		case *ast.ObjectExpr:
			copy := *value
			copy.Fields = append([]ast.ObjectField(nil), value.Fields...)
			for i := range copy.Fields {
				copy.Fields[i].Value = expression(copy.Fields[i].Value)
			}
			return &copy
		case *ast.FieldExpr:
			if name, ok := moduleReferenceName(value); ok {
				if reference := moduleReference(name, value.Pos); reference != nil {
					return reference
				}
			}
			copy := *value
			copy.Object = expression(value.Object)
			return &copy
		case *ast.IndexExpr:
			copy := *value
			copy.Object, copy.Index = expression(value.Object), expression(value.Index)
			return &copy
		case *ast.ComprehensionExpr:
			copy := *value
			copy.Source = expression(value.Source)
			previous := bound
			bound = cloneBoolMap(bound)
			bound[value.Variable] = true
			copy.Element = expression(value.Element)
			bound = previous
			return &copy
		default:
			return expr
		}
	}
	var statements func([]ast.Stmt) []ast.Stmt
	statements = func(body []ast.Stmt) []ast.Stmt {
		result := make([]ast.Stmt, len(body))
		for i, statement := range body {
			switch value := statement.(type) {
			case *ast.BindStmt:
				copy := *value
				copy.Expr = expression(value.Expr)
				result[i] = &copy
				bound[value.Name] = true
			case *ast.ExprStmt:
				copy := *value
				copy.Expr = expression(value.Expr)
				result[i] = &copy
			case *ast.ReturnStmt:
				copy := *value
				copy.Expr = expression(value.Expr)
				result[i] = &copy
			case *ast.MatchStmt:
				copy := *value
				copy.Value = expression(value.Value)
				copy.Arms = append([]ast.MatchArm{}, value.Arms...)
				for i := range copy.Arms {
					previous := bound
					bound = cloneBoolMap(bound)
					copy.Arms[i].Guard = expression(copy.Arms[i].Guard)
					copy.Arms[i].Body = statements(copy.Arms[i].Body)
					bound = previous
				}
				result[i] = &copy
			case *ast.ForStmt:
				copy := *value
				copy.Source = expression(value.Source)
				previous := bound
				bound = cloneBoolMap(bound)
				bound[value.Variable] = true
				copy.Body = statements(value.Body)
				bound = previous
				result[i] = &copy
			default:
				result[i] = statement
			}
		}
		return result
	}
	copy := *program
	flow := *program.Flow
	for _, param := range flow.Params {
		bound[param] = true
	}
	flow.Body = statements(flow.Body)
	copy.Flow = &flow
	copy.Functions = make([]*ast.Function, len(program.Functions))
	for i, fn := range program.Functions {
		clone := *fn
		bound = map[string]bool{}
		for _, param := range fn.Params {
			bound[param] = true
		}
		clone.Return = expression(fn.Return)
		copy.Functions[i] = &clone
	}
	return &copy, aliasError
}

func moduleReferenceName(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.IdentExpr:
		return value.Name, true
	case *ast.FieldExpr:
		if prefix, ok := moduleReferenceName(value.Object); ok {
			return prefix + "." + value.Name, true
		}
	}
	return "", false
}
