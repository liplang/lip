package compiler

import (
	"fmt"
	"strings"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/token"
)

func dependencyAliases(program *ast.Program) (map[string]string, error) {
	aliases := map[string]string{}
	for _, dependency := range program.Dependencies {
		name := dependency.Alias
		if name == "" {
			continue
		}
		fail := func(message string) (map[string]string, error) {
			return nil, fmt.Errorf("%d:%d: %s", dependency.Pos.Line, dependency.Pos.Column, message)
		}
		if dependency.Kind != "python" {
			return fail("module aliases are only supported by import python")
		}
		tokens, err := lexer.New(name).Lex()
		if err != nil || len(tokens) != 2 || tokens[0].Kind != token.Ident || tokens[0].Text != name {
			return fail(fmt.Sprintf("invalid module alias %q; use one identifier after as", name))
		}
		if reservedName(name) || builtinName(name) || validTypeName(name) || name == "python" || name == "void" {
			return fail(fmt.Sprintf("module alias %q is reserved; choose a distinct module name", name))
		}
		if _, exists := aliases[name]; exists {
			return fail(fmt.Sprintf("duplicate module alias %q; each alias must name one module", name))
		}
		aliases[name] = dependencyRoot(dependency.Spec)
	}
	for _, dependency := range program.Dependencies {
		if dependency.Kind != "python" || dependency.Alias != "" {
			continue
		}
		root := strings.SplitN(dependencyRoot(dependency.Spec), ".", 2)[0]
		if target, exists := aliases[root]; exists && target != root {
			return nil, fmt.Errorf("%d:%d: module alias %q conflicts with the Python module %q", dependency.Pos.Line, dependency.Pos.Column, root, root)
		}
	}
	for _, fn := range program.Functions {
		if _, exists := aliases[fn.Name]; exists {
			return nil, fmt.Errorf("%d:%d: function %q conflicts with a module alias", fn.Pos.Line, fn.Pos.Column, fn.Name)
		}
		for _, param := range fn.Params {
			if _, exists := aliases[param]; exists {
				return nil, fmt.Errorf("%d:%d: parameter %q conflicts with a module alias", fn.Pos.Line, fn.Pos.Column, param)
			}
		}
	}
	return aliases, nil
}

// Resolve calls in a copy: source ASTs and REPL declarations retain their
// spelling and positions, while the graph and hosts use real operation names.
func resolveAliases(program *ast.Program, aliases map[string]string) (*ast.Program, error) {
	var pythonModules []string
	for _, dependency := range program.Dependencies {
		if dependency.Kind == "python" && dependency.Alias == "" {
			pythonModules = append(pythonModules, dependencyRoot(dependency.Spec))
		}
	}
	if len(aliases) == 0 && len(pythonModules) == 0 {
		return program, nil
	}
	var aliasError error
	var expression func(ast.Expr) ast.Expr
	expression = func(expr ast.Expr) ast.Expr {
		switch value := expr.(type) {
		case *ast.CallExpr:
			copy := *value
			root, rest, dotted := strings.Cut(value.Name, ".")
			if module, exists := aliases[root]; exists {
				copy.Python = true
				if !dotted && aliasError == nil {
					aliasError = fmt.Errorf("%d:%d: module alias %q is not a function; call %s.operation(...) instead", value.Pos.Line, value.Pos.Column, root, root)
				}
				copy.Name = module
				if dotted {
					copy.Name += "." + rest
				}
			}
			if !copy.Python {
				for _, module := range pythonModules {
					if copy.Name == module || strings.HasPrefix(copy.Name, module+".") {
						copy.Python = true
						break
					}
				}
			}
			if copy.Python && !strings.Contains(copy.Name, ".") && aliasError == nil {
				aliasError = fmt.Errorf("%d:%d: Python module %q is not a function; call an operation inside it", value.Pos.Line, value.Pos.Column, root)
			}
			copy.Args = make([]ast.Expr, len(value.Args))
			for i, arg := range value.Args {
				copy.Args[i] = expression(arg)
			}
			return &copy
		case *ast.UnaryExpr:
			copy := *value
			copy.Operand = expression(value.Operand)
			return &copy
		case *ast.LambdaExpr:
			for _, param := range value.Params {
				if _, exists := aliases[param]; exists && aliasError == nil {
					aliasError = fmt.Errorf("%d:%d: callback parameter %q conflicts with a module alias", value.Pos.Line, value.Pos.Column, param)
				}
			}
			copy := *value
			copy.Return = expression(value.Return)
			return &copy
		case *ast.BinaryExpr:
			copy := *value
			copy.Left, copy.Right = expression(value.Left), expression(value.Right)
			return &copy
		case *ast.IfExpr:
			copy := *value
			copy.Cond, copy.Then, copy.Else = expression(value.Cond), expression(value.Then), expression(value.Else)
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
			copy := *value
			copy.Object = expression(value.Object)
			return &copy
		case *ast.IndexExpr:
			copy := *value
			copy.Object, copy.Index = expression(value.Object), expression(value.Index)
			return &copy
		case *ast.ComprehensionExpr:
			if _, exists := aliases[value.Variable]; exists && aliasError == nil {
				aliasError = fmt.Errorf("%d:%d: Map variable %q conflicts with a module alias", value.Pos.Line, value.Pos.Column, value.Variable)
			}
			copy := *value
			copy.Source, copy.Element = expression(value.Source), expression(value.Element)
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
			case *ast.ExprStmt:
				copy := *value
				copy.Expr = expression(value.Expr)
				result[i] = &copy
			case *ast.ReturnStmt:
				copy := *value
				copy.Expr = expression(value.Expr)
				result[i] = &copy
			case *ast.WhenStmt:
				copy := *value
				copy.Cond, copy.Body = expression(value.Cond), statements(value.Body)
				result[i] = &copy
			default:
				result[i] = statement
			}
		}
		return result
	}
	copy := *program
	flow := *program.Flow
	flow.Body = statements(flow.Body)
	copy.Flow = &flow
	copy.Functions = make([]*ast.Function, len(program.Functions))
	for i, fn := range program.Functions {
		clone := *fn
		clone.Return = expression(fn.Return)
		copy.Functions[i] = &clone
	}
	return &copy, aliasError
}
