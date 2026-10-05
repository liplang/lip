package compiler

import (
	"fmt"
	"strings"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
	"lipalpha/compiler/token"
)

type Graph struct {
	Flow       string
	Params     []string
	ParamTypes map[string]string
	Functions  []*ast.Function
	Nodes      []Node
}

type Node struct {
	Name   string
	Deps   []string
	Gates  []string
	Expr   ast.Expr
	Output bool
	Pos    token.Pos
}

func ParseAndBuild(src string) (*Graph, error) {
	tokens, err := lexer.New(src).Lex()
	if err != nil {
		return nil, err
	}
	program, err := parser.New(tokens).Parse()
	if err != nil {
		return nil, err
	}
	return Build(program)
}

func Build(program *ast.Program) (*Graph, error) {
	if program == nil || program.Flow == nil {
		return nil, fmt.Errorf("program has no flow")
	}
	f := program.Flow
	g := &Graph{Flow: f.Name, Params: append([]string(nil), f.Params...), ParamTypes: f.ParamTypes, Functions: program.Functions}
	functionNames := make(map[string]bool)
	for _, fn := range program.Functions {
		if functionNames[fn.Name] {
			return nil, fmt.Errorf("%d:%d: duplicate function %q", fn.Pos.Line, fn.Pos.Column, fn.Name)
		}
		functionNames[fn.Name] = true
		if err := validateFunction(fn); err != nil {
			return nil, fmt.Errorf("function %s: %w", fn.Name, err)
		}
	}
	known := make(map[string]bool)
	allNames := make(map[string]bool)
	types := make(map[string]string)
	for _, p := range f.Params {
		if reservedName(p) {
			return nil, fmt.Errorf("%d:%d: identifier %q is reserved for generated graph nodes", f.Pos.Line, f.Pos.Column, p)
		}
		if allNames[p] {
			return nil, fmt.Errorf("%d:%d: duplicate parameter %q", f.Pos.Line, f.Pos.Column, p)
		}
		known[p], allNames[p] = true, true
		if typ := f.ParamTypes[p]; typ != "" && !validTypeName(typ) {
			return nil, fmt.Errorf("%d:%d: unknown type %q for parameter %q", f.Pos.Line, f.Pos.Column, typ, p)
		}
		types[p] = paramType(f.ParamTypes, p)
	}
	fnTypes := make(map[string]string, len(program.Functions))
	fnParams := make(map[string][]string, len(program.Functions))
	for _, fn := range program.Functions {
		paramTypes := make([]string, len(fn.Params))
		for i, param := range fn.Params {
			paramTypes[i] = paramType(fn.ParamTypes, param)
		}
		fnParams[fn.Name] = paramTypes
	}
	// Infer local function result types to a small fixed point. A function may
	// refer to another local function declared later in the file; doing only a
	// single source-order pass would unnecessarily lose that type information
	// and weaken checks in the Flow that follows. Recursive or Host-backed
	// results remain "any" after the bounded pass.
	for pass := 0; pass <= len(program.Functions); pass++ {
		changed := false
		for _, fn := range program.Functions {
			env := make(map[string]string, len(fn.Params))
			for _, param := range fn.Params {
				env[param] = paramType(fn.ParamTypes, param)
			}
			typ, err := inferExprType(fn.Return, env, fnTypes, fnParams)
			if err != nil {
				return nil, fmt.Errorf("function %s: %w", fn.Name, err)
			}
			if typ != "any" && fnTypes[fn.Name] != typ {
				fnTypes[fn.Name] = typ
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	b := builder{graph: g, known: known, allNames: allNames, types: types, fnTypes: fnTypes, fnParams: fnParams, functionNames: functionNames}
	if err := b.stmts(f.Body, nil); err != nil {
		return nil, err
	}
	if len(g.Nodes) == 0 {
		return nil, fmt.Errorf("flow %q has no executable statements", f.Name)
	}
	return g, nil
}

type builder struct {
	graph         *Graph
	known         map[string]bool
	allNames      map[string]bool
	types         map[string]string
	fnTypes       map[string]string
	fnParams      map[string][]string
	functionNames map[string]bool
	outputID      int
	exprID        int
}

func (b *builder) stmts(stmts []ast.Stmt, gates []string) error {
	terminal := false
	for _, stmt := range stmts {
		if terminal {
			return fmt.Errorf("statements after return are not allowed")
		}
		switch s := stmt.(type) {
		case *ast.BindStmt:
			if reservedName(s.Name) {
				return b.err(s.Pos, "identifier %q is reserved for generated graph nodes", s.Name)
			}
			if b.allNames[s.Name] {
				return b.err(s.Pos, "duplicate binding %q", s.Name)
			}
			if err := validateExpr(s.Expr); err != nil {
				return b.err(s.Pos, "%v", err)
			}
			refs := refsOf(s.Expr)
			if err := b.checkRefs(refs, s.Pos); err != nil {
				return err
			}
			typ, err := inferExprType(s.Expr, b.types, b.fnTypes, b.fnParams)
			if err != nil {
				return b.err(s.Pos, "%v", err)
			}
			b.graph.Nodes = append(b.graph.Nodes, Node{Name: s.Name, Deps: unique(refs), Gates: unique(gates), Expr: s.Expr, Pos: s.Pos})
			b.known[s.Name], b.allNames[s.Name] = true, true
			b.types[s.Name] = typ
		case *ast.WhenStmt:
			gate, err := gateName(s.Cond)
			if err != nil {
				return b.err(s.Pos, "%v", err)
			}
			if gate != "" && !b.known[gate] {
				return b.err(s.Pos, "unknown gate %q", gate)
			}
			if typ, err := inferExprType(s.Cond, b.types, b.fnTypes, b.fnParams); err != nil {
				return b.err(s.Pos, "%v", err)
			} else if typ != "any" && typ != "bool" {
				return b.err(s.Pos, "when condition must be bool, got %s", typ)
			}
			before := cloneBoolMap(b.known)
			beforeTypes := cloneStringMap(b.types)
			nextGates := append([]string(nil), gates...)
			if gate != "" {
				nextGates = append(nextGates, gate)
			}
			if err := b.stmts(s.Body, nextGates); err != nil {
				return err
			}
			for name := range b.known {
				if !before[name] {
					delete(b.known, name)
				}
			}
			for name := range b.types {
				if _, ok := beforeTypes[name]; !ok {
					delete(b.types, name)
				}
			}
		case *ast.ReturnStmt:
			if err := validateExpr(s.Expr); err != nil {
				return b.err(s.Pos, "%v", err)
			}
			refs := refsOf(s.Expr)
			if err := b.checkRefs(refs, s.Pos); err != nil {
				return err
			}
			if _, err := inferExprType(s.Expr, b.types, b.fnTypes, b.fnParams); err != nil {
				return b.err(s.Pos, "%v", err)
			}
			name := fmt.Sprintf("__return_%d", b.outputID)
			b.outputID++
			b.graph.Nodes = append(b.graph.Nodes, Node{Name: name, Deps: unique(refs), Gates: unique(gates), Expr: s.Expr, Output: true, Pos: s.Pos})
			terminal = true
		case *ast.ExprStmt:
			if call, ok := s.Expr.(*ast.CallExpr); ok && b.functionNames[call.Name] {
				return b.err(s.Pos, "a local fn call must be assigned or returned")
			}
			if err := validateExpr(s.Expr); err != nil {
				return b.err(s.Pos, "%v", err)
			}
			refs := refsOf(s.Expr)
			if err := b.checkRefs(refs, s.Pos); err != nil {
				return err
			}
			name := fmt.Sprintf("__expr_%d", b.exprID)
			b.exprID++
			b.graph.Nodes = append(b.graph.Nodes, Node{Name: name, Deps: unique(refs), Gates: unique(gates), Expr: s.Expr, Pos: s.Pos})
		default:
			return fmt.Errorf("unsupported statement")
		}
	}
	return nil
}

func (b *builder) checkRefs(refs []string, pos token.Pos) error {
	for _, ref := range refs {
		if !b.known[ref] {
			if b.allNames[ref] {
				return b.err(pos, "binding %q is scoped to a when block", ref)
			}
			return b.err(pos, "undefined or forward reference %q", ref)
		}
	}
	return nil
}

func gateName(expr ast.Expr) (string, error) {
	if id, ok := expr.(*ast.IdentExpr); ok {
		return id.Name, nil
	}
	if lit, ok := expr.(*ast.LiteralExpr); ok {
		if v, ok := lit.Value.(bool); ok && v {
			return "", nil
		}
	}
	return "", fmt.Errorf("when condition must be an identifier or true")
}

func validateExpr(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.IdentExpr, *ast.LiteralExpr:
		return nil
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if err := validateSimpleExpr(arg); err != nil {
				return fmt.Errorf("call argument: %w", err)
			}
		}
		return nil
	case *ast.BinaryExpr:
		if err := validateSimpleExpr(e.Left); err != nil {
			return err
		}
		return validateSimpleExpr(e.Right)
	case *ast.IfExpr:
		if _, ok := e.Cond.(*ast.IdentExpr); !ok {
			return fmt.Errorf("if condition must be an identifier")
		}
		if err := validateSimpleExpr(e.Then); err != nil {
			return err
		}
		return validateSimpleExpr(e.Else)
	case *ast.ListExpr:
		for _, item := range e.Items {
			if err := validateSimpleExpr(item); err != nil {
				return err
			}
		}
		return nil
	case *ast.FieldExpr:
		return validateSimpleExpr(e.Object)
	case *ast.IndexExpr:
		if err := validateSimpleExpr(e.Object); err != nil {
			return err
		}
		return validateSimpleExpr(e.Index)
	default:
		return fmt.Errorf("unsupported expression")
	}
}

func validateSimpleExpr(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.IdentExpr, *ast.LiteralExpr:
		return nil
	case *ast.BinaryExpr:
		if err := validateSimpleExpr(e.Left); err != nil {
			return err
		}
		return validateSimpleExpr(e.Right)
	case *ast.ListExpr:
		for _, item := range e.Items {
			if err := validateSimpleExpr(item); err != nil {
				return err
			}
		}
		return nil
	case *ast.FieldExpr:
		return validateSimpleExpr(e.Object)
	case *ast.IndexExpr:
		if err := validateSimpleExpr(e.Object); err != nil {
			return err
		}
		return validateSimpleExpr(e.Index)
	default:
		if _, ok := expr.(*ast.CallExpr); ok {
			return fmt.Errorf("nested calls are not supported")
		}
		return fmt.Errorf("complex expression is not supported here")
	}
}

func refsOf(expr ast.Expr) []string {
	var refs []string
	var visit func(ast.Expr)
	visit = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.IdentExpr:
			refs = append(refs, x.Name)
		case *ast.CallExpr:
			for _, a := range x.Args {
				visit(a)
			}
		case *ast.BinaryExpr:
			visit(x.Left)
			visit(x.Right)
		case *ast.IfExpr:
			visit(x.Cond)
			visit(x.Then)
			visit(x.Else)
		case *ast.ListExpr:
			for _, item := range x.Items {
				visit(item)
			}
		case *ast.FieldExpr:
			visit(x.Object)
		case *ast.IndexExpr:
			visit(x.Object)
			visit(x.Index)
		}
	}
	visit(expr)
	return refs
}

func validateFunction(fn *ast.Function) error {
	seen := make(map[string]bool)
	for _, param := range fn.Params {
		if reservedName(param) {
			return fmt.Errorf("identifier %q is reserved for generated graph nodes", param)
		}
		if seen[param] {
			return fmt.Errorf("duplicate parameter %q", param)
		}
		seen[param] = true
		if typ := fn.ParamTypes[param]; typ != "" && !validTypeName(typ) {
			return fmt.Errorf("unknown type %q for parameter %q", typ, param)
		}
	}
	if err := validateExpr(fn.Return); err != nil {
		return err
	}
	for _, ref := range refsOf(fn.Return) {
		if !seen[ref] {
			return fmt.Errorf("unknown function parameter %q", ref)
		}
	}
	return nil
}

func unique(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func paramType(types map[string]string, name string) string {
	if typ := types[name]; typ != "" {
		return typ
	}
	return "any"
}

// inferExprType is intentionally small. It rejects contradictions that are
// visible in LIP source and leaves values returned by arbitrary Host calls as
// any; those values are checked again by runtime.Binary at execution time.
func inferExprType(expr ast.Expr, env, fnTypes map[string]string, fnParams map[string][]string) (string, error) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		return paramType(env, e.Name), nil
	case *ast.LiteralExpr:
		switch e.Value.(type) {
		case string:
			return "string", nil
		case bool:
			return "bool", nil
		default:
			return "number", nil
		}
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if _, err := inferExprType(arg, env, fnTypes, fnParams); err != nil {
				return "any", err
			}
		}
		if expected, ok := fnParams[e.Name]; ok {
			if len(expected) != len(e.Args) {
				return "any", fmt.Errorf("function %s expects %d arguments, got %d", e.Name, len(expected), len(e.Args))
			}
			for i, arg := range e.Args {
				actual, err := inferExprType(arg, env, fnTypes, fnParams)
				if err != nil {
					return "any", err
				}
				if expected[i] != "any" && actual != "any" && expected[i] != actual {
					return "any", fmt.Errorf("function %s argument %d expects %s, got %s", e.Name, i+1, expected[i], actual)
				}
			}
		}
		if typ := fnTypes[e.Name]; typ != "" {
			return typ, nil
		}
		return "any", nil
	case *ast.BinaryExpr:
		left, err := inferExprType(e.Left, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		right, err := inferExprType(e.Right, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		return inferBinaryType(e.Op, left, right)
	case *ast.IfExpr:
		cond, err := inferExprType(e.Cond, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if cond != "any" && cond != "bool" {
			return "any", fmt.Errorf("if condition must be bool, got %s", cond)
		}
		thenType, err := inferExprType(e.Then, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		elseType, err := inferExprType(e.Else, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if thenType == "any" || elseType == "any" {
			return "any", nil
		}
		if thenType != elseType {
			return "any", fmt.Errorf("if branches have incompatible types %s and %s", thenType, elseType)
		}
		return thenType, nil
	case *ast.ListExpr:
		for _, item := range e.Items {
			if _, err := inferExprType(item, env, fnTypes, fnParams); err != nil {
				return "any", err
			}
		}
		return "list", nil
	case *ast.FieldExpr:
		_, err := inferExprType(e.Object, env, fnTypes, fnParams)
		return "any", err
	case *ast.IndexExpr:
		if _, err := inferExprType(e.Object, env, fnTypes, fnParams); err != nil {
			return "any", err
		}
		if _, err := inferExprType(e.Index, env, fnTypes, fnParams); err != nil {
			return "any", err
		}
		return "any", nil
	default:
		return "any", fmt.Errorf("unsupported expression type %T", expr)
	}
}

func inferBinaryType(op, left, right string) (string, error) {
	known := func(t string) bool { return t != "any" }
	switch op {
	case "+":
		if !known(left) || !known(right) {
			return "any", nil
		}
		if left == "string" && right == "string" {
			return "string", nil
		}
		if left == "number" && right == "number" {
			return "number", nil
		}
		return "any", fmt.Errorf("operator + cannot combine %s and %s", left, right)
	case "-", "/":
		if known(left) && left != "number" || known(right) && right != "number" {
			return "any", fmt.Errorf("operator %s expects numbers, got %s and %s", op, left, right)
		}
		return "number", nil
	case "*":
		if !known(left) || !known(right) {
			return "any", nil
		}
		if left == "number" && right == "number" {
			return "number", nil
		}
		if (left == "string" && right == "number") || (left == "number" && right == "string") {
			return "string", nil
		}
		return "any", fmt.Errorf("operator * expects numbers or string and number, got %s and %s", left, right)
	case ">", ">=", "<", "<=":
		if known(left) && left != "number" || known(right) && right != "number" {
			return "any", fmt.Errorf("operator %s expects numbers, got %s and %s", op, left, right)
		}
		return "bool", nil
	case "&&", "||":
		if known(left) && left != "bool" || known(right) && right != "bool" {
			return "any", fmt.Errorf("operator %s expects booleans, got %s and %s", op, left, right)
		}
		return "bool", nil
	case "==", "!=":
		if known(left) && known(right) && left != right {
			return "any", fmt.Errorf("operator %s cannot compare %s and %s", op, left, right)
		}
		return "bool", nil
	default:
		return "any", fmt.Errorf("unsupported binary operator %q", op)
	}
}

func (b *builder) err(pos token.Pos, format string, args ...any) error {
	return fmt.Errorf("%d:%d: %s", pos.Line, pos.Column, fmt.Sprintf(format, args...))
}

func validTypeName(name string) bool {
	switch name {
	case "any", "string", "number", "bool":
		return true
	default:
		return false
	}
}

func reservedName(name string) bool {
	return strings.HasPrefix(name, "__return_") || strings.HasPrefix(name, "__expr_")
}
