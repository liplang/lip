package compiler

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
	"lipalpha/compiler/token"
	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
)

type Graph struct {
	SourceName   string
	Source       string
	Flow         string
	Params       []string
	ParamTypes   map[string]string
	ReturnType   string
	Dependencies []ast.Dependency
	Functions    []*ast.Function
	Nodes        []Node
}

type Node struct {
	Name             string
	Deps             []string
	Gates            []string
	Expr             ast.Expr
	Output           bool
	State            bool
	Type             string
	RetryAttempts    int
	FeedbackAttempts int
	FeedbackStep     string
	FeedbackVerify   string
	Pos              token.Pos
}

func ParseAndBuild(src string) (*Graph, error) {
	graph, _, err := parseSource(src)
	if graph != nil {
		graph.Source = src
	}
	return graph, err
}

func parseSource(src string) (*Graph, string, error) {
	tokens, err := lexer.New(src).Lex()
	if err != nil {
		return nil, "LIP_LEX_ERROR", err
	}
	program, err := parser.New(tokens).Parse()
	if err != nil {
		return nil, "LIP_SYNTAX_ERROR", err
	}
	graph, err := Build(program)
	return graph, "LIP_CHECK_ERROR", err
}

func Build(program *ast.Program) (*Graph, error) {
	if program == nil || program.Flow == nil {
		return nil, fmt.Errorf("program has no flow")
	}
	aliases, err := dependencyAliases(program)
	if err != nil {
		return nil, err
	}
	program, err = resolveAliases(program, aliases)
	if err != nil {
		return nil, err
	}
	f := program.Flow
	g := &Graph{Flow: f.Name, Params: append([]string(nil), f.Params...), ParamTypes: f.ParamTypes, ReturnType: f.ReturnType, Dependencies: append([]ast.Dependency(nil), program.Dependencies...), Functions: program.Functions}
	if !validOutputType(f.ReturnType) {
		return nil, fmt.Errorf("%d:%d: flow %q needs a valid explicit output type", f.Pos.Line, f.Pos.Column, f.Name)
	}
	seenDependencies := make(map[string]bool, len(g.Dependencies))
	for _, dependency := range g.Dependencies {
		if dependency.Kind != "python" && dependency.Kind != "go" && dependency.Kind != "host" {
			return nil, fmt.Errorf("%d:%d: unknown dependency kind %q", dependency.Pos.Line, dependency.Pos.Column, dependency.Kind)
		}
		if err := validateDependency(dependency); err != nil {
			return nil, err
		}
		key := dependency.Kind + "\x00" + dependency.Spec
		if seenDependencies[key] {
			return nil, fmt.Errorf("%d:%d: duplicate %s dependency %q", dependency.Pos.Line, dependency.Pos.Column, dependency.Kind, dependency.Spec)
		}
		seenDependencies[key] = true
	}
	functionNames := make(map[string]bool)
	for _, fn := range program.Functions {
		if reservedName(fn.Name) || builtinName(fn.Name) {
			return nil, fmt.Errorf("%d:%d: function name %q is reserved", fn.Pos.Line, fn.Pos.Column, fn.Name)
		}
		if functionNames[fn.Name] {
			return nil, fmt.Errorf("%d:%d: duplicate function %q", fn.Pos.Line, fn.Pos.Column, fn.Name)
		}
		functionNames[fn.Name] = true
		if err := validateFunction(fn); err != nil {
			return nil, fmt.Errorf("%d:%d: function %s: %w", fn.Pos.Line, fn.Pos.Column, fn.Name, err)
		}
	}
	if err := validateOperationDependencies(program, functionNames, g.Dependencies); err != nil {
		return nil, err
	}
	if err := validatePureFunctions(program.Functions, functionNames); err != nil {
		return nil, err
	}
	known := make(map[string]bool)
	allNames := make(map[string]bool)
	types := make(map[string]string)
	for _, p := range f.Params {
		if _, exists := aliases[p]; exists {
			return nil, fmt.Errorf("%d:%d: input %q conflicts with a module alias", f.Pos.Line, f.Pos.Column, p)
		}
		if f.ParamTypes[p] == "" {
			return nil, fmt.Errorf("%d:%d: parameter %q needs an explicit type", f.Pos.Line, f.Pos.Column, p)
		}
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
		if fn.ReturnType != "" {
			fnTypes[fn.Name] = fn.ReturnType
		}
	}
	// Infer local function result types to a small fixed point. A function may
	// refer to another local function declared later in the file; doing only a
	// single source-order pass would unnecessarily lose that type information
	// and weaken checks in the Flow that follows. Dynamic results remain any.
	for pass := 0; pass <= len(program.Functions); pass++ {
		changed := false
		for _, fn := range program.Functions {
			env := make(map[string]string, len(fn.Params))
			for _, param := range fn.Params {
				env[param] = paramType(fn.ParamTypes, param)
			}
			typ, err := inferExprType(fn.Return, env, fnTypes, fnParams)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: function %s: %w", fn.Pos.Line, fn.Pos.Column, fn.Name, err)
			}
			if fn.ReturnType == "" && typ != "any" && fnTypes[fn.Name] != typ {
				fnTypes[fn.Name] = typ
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for _, fn := range program.Functions {
		env := cloneStringMap(fn.ParamTypes)
		actual, err := inferExprType(fn.Return, env, fnTypes, fnParams)
		if err != nil {
			return nil, fmt.Errorf("%d:%d: function %s: %w", fn.Pos.Line, fn.Pos.Column, fn.Name, err)
		}
		if fn.ReturnType != "" && !compatibleType(fn.ReturnType, actual) {
			return nil, fmt.Errorf("%d:%d: function %s returns %s, declared %s", fn.Pos.Line, fn.Pos.Column, fn.Name, actual, fn.ReturnType)
		}
		if fn.ReturnType != "" {
			if err := validateDeclaredResult(fn.Return, fn.ReturnType, env, fnTypes, fnParams); err != nil {
				return nil, fmt.Errorf("%d:%d: function %s: %w", fn.Pos.Line, fn.Pos.Column, fn.Name, err)
			}
		}
		if fn.ReturnType == "" {
			fn.InferredReturnType = actual
		}
	}
	b := builder{graph: g, known: known, allNames: allNames, types: types, fnTypes: fnTypes, fnParams: fnParams, functionNames: functionNames, aliases: aliases}
	if err := b.stmts(f.Body, nil); err != nil {
		return nil, err
	}
	hasOutput := false
	for _, node := range g.Nodes {
		if node.Output {
			hasOutput = true
			break
		}
	}
	if g.ReturnType == "void" {
		if b.outputID > 1 {
			return nil, b.err(f.Pos, "flow %q must have at most one bare return", f.Name)
		}
		return g, nil
	}
	if !hasOutput {
		return nil, fmt.Errorf("%d:%d: flow %q has no return value, but declares -> %s; add return <value>, or omit the output type for effects only (-> void is also supported)", f.Pos.Line, f.Pos.Column, f.Name, g.ReturnType)
	}
	if b.outputID != 1 {
		return nil, fmt.Errorf("%d:%d: flow %q must have exactly one return; select a conditional value with if condition { value } else { other_value }", f.Pos.Line, f.Pos.Column, f.Name)
	}
	for _, n := range g.Nodes {
		if !n.Output {
			continue
		}
		if len(n.Gates) > 0 && !strings.HasSuffix(g.ReturnType, "?") {
			return nil, b.err(n.Pos, "gated return may produce no value; declare -> %s? or move return outside when", g.ReturnType)
		}
		if !compatibleType(g.ReturnType, n.Type) {
			return nil, b.err(n.Pos, "return has type %s, declared output is %s", n.Type, g.ReturnType)
		}
	}
	return g, nil
}

// validateOperationDependencies keeps a source file honest about the external
// capabilities it uses. Local fn calls and the small built-in control/value
// operations do not need a declaration. Every other bare operation is a Host
// requirement, while dotted operations default to Python module requirements.
func validateOperationDependencies(program *ast.Program, functionNames map[string]bool, dependencies []ast.Dependency) error {
	pythonRoots := make([]string, 0)
	hostOps := make([]string, 0)
	for _, dependency := range dependencies {
		switch dependency.Kind {
		case "python":
			if root := dependencyRoot(dependency.Spec); root != "" {
				pythonRoots = append(pythonRoots, root)
			}
		case "host":
			hostOps = append(hostOps, dependency.Spec)
		}
	}
	hasPython := func(name string) bool {
		for _, root := range pythonRoots {
			if name == root || strings.HasPrefix(name, root+".") {
				return true
			}
		}
		return false
	}
	hasHost := func(name string) bool {
		for _, operation := range hostOps {
			if operation == name {
				return true
			}
			if strings.HasSuffix(operation, ".*") {
				base := strings.TrimSuffix(operation, ".*")
				if name == base || strings.HasPrefix(name, base+".") {
					return true
				}
			}
		}
		return false
	}
	checkOperation := func(name string, pos token.Pos) error {
		unknown := func(message string) error {
			hints := []string{}
			if suggestion := spellingSuggestion(name, operationCandidates(functionNames)); suggestion != "" {
				hints = append(hints, fmt.Sprintf("Did you mean %s(...)? Check the operation name before adding an import.", suggestion))
			}
			if strings.HasPrefix(name, "list.") {
				hints = append(hints, "See docs/LIST-LIBRARY.md for supported list operations.")
			}
			if strings.HasPrefix(name, "string.") {
				hints = append(hints, "See docs/STRING-LIBRARY.md for supported string operations.")
			}
			return fmt.Errorf("%d:%d: %w", pos.Line, pos.Column, repairError("LIP_UNKNOWN_OPERATION", message, hints...))
		}
		if functionNames[name] || builtinName(name) {
			return nil
		}
		if strings.HasPrefix(name, "list.") {
			return unknown(fmt.Sprintf("unknown list operation %q", name))
		}
		if strings.HasPrefix(name, "string.") {
			return unknown(fmt.Sprintf("unknown string operation %q", name))
		}
		if strings.HasPrefix(name, "python.") && isPythonControlOperation(name) {
			if len(pythonRoots) > 0 || hasHost(name) {
				return nil
			}
			return fmt.Errorf("%d:%d: operation %q needs an explicit import python declaration or import host %q", pos.Line, pos.Column, name, name)
		}
		if strings.Contains(name, ".") {
			if hasPython(name) || hasHost(name) {
				return nil
			}
			if suggestion := spellingSuggestion(name, operationCandidates(functionNames)); suggestion != "" {
				return unknown(fmt.Sprintf("unknown operation %q; did you mean %q?", name, suggestion))
			}
			return fmt.Errorf("%d:%d: Python operation %q is not declared; add import python %q or import host %q", pos.Line, pos.Column, name, strings.SplitN(name, ".", 2)[0], name)
		}
		if hasHost(name) {
			return nil
		}
		if suggestion := spellingSuggestion(name, operationCandidates(functionNames)); suggestion != "" {
			return fmt.Errorf("%d:%d: %w", pos.Line, pos.Column, repairError("LIP_UNKNOWN_OPERATION", fmt.Sprintf("unknown operation %q; did you mean %q?", name, suggestion), fmt.Sprintf("Check the spelling of %s(...). If %s is an intended Host operation, declare import host %q and register it in the Go host.", suggestion, name, name)))
		}
		return fmt.Errorf("%d:%d: Host operation %q is not declared; add import host %q", pos.Line, pos.Column, name, name)
	}
	var visitExpr func(ast.Expr) error
	visitExpr = func(expr ast.Expr) error {
		switch value := expr.(type) {
		case *ast.CallExpr:
			if err := validateCollectionCallShape(value); err != nil {
				return fmt.Errorf("%d:%d: %w", value.Pos.Line, value.Pos.Column, err)
			}
			if value.Python {
				if !hasPython(value.Name) {
					return fmt.Errorf("%d:%d: Python operation %q is not declared; add an import python declaration", value.Pos.Line, value.Pos.Column, value.Name)
				}
				for _, arg := range value.Args {
					if err := visitExpr(arg); err != nil {
						return err
					}
				}
				return nil
			}
			switch value.Name {
			case "retry":
				if len(value.Args) > 0 {
					return visitExpr(value.Args[0])
				}
			case "feedback":
				if len(value.Args) > 0 {
					if err := visitExpr(value.Args[0]); err != nil {
						return err
					}
				}
				for _, index := range []int{1, 2} {
					if index >= len(value.Args) {
						continue
					}
					operation, ok := value.Args[index].(*ast.IdentExpr)
					if ok {
						if err := checkOperation(operation.Name, operation.Pos); err != nil {
							return err
						}
					}
				}
			default:
				if err := checkOperation(value.Name, value.Pos); err != nil {
					return err
				}
				for _, arg := range value.Args {
					if err := visitExpr(arg); err != nil {
						return err
					}
				}
			}
		case *ast.UnaryExpr:
			return visitExpr(value.Operand)
		case *ast.LambdaExpr:
			return visitExpr(value.Return)
		case *ast.ObjectExpr:
			for _, field := range value.Fields {
				if err := visitExpr(field.Value); err != nil {
					return err
				}
			}
		case *ast.BinaryExpr:
			if err := visitExpr(value.Left); err != nil {
				return err
			}
			return visitExpr(value.Right)
		case *ast.IfExpr:
			for _, part := range []ast.Expr{value.Cond, value.Then, value.Else} {
				if err := visitExpr(part); err != nil {
					return err
				}
			}
		case *ast.ListExpr:
			for _, item := range value.Items {
				if err := visitExpr(item); err != nil {
					return err
				}
			}
		case *ast.ComprehensionExpr:
			if err := visitExpr(value.Source); err != nil {
				return err
			}
			return visitExpr(value.Element)
		case *ast.FieldExpr:
			return visitExpr(value.Object)
		case *ast.IndexExpr:
			if err := visitExpr(value.Object); err != nil {
				return err
			}
			return visitExpr(value.Index)
		}
		return nil
	}
	var visitStmts func([]ast.Stmt) error
	visitStmts = func(statements []ast.Stmt) error {
		for _, statement := range statements {
			switch value := statement.(type) {
			case *ast.BindStmt:
				if err := visitExpr(value.Expr); err != nil {
					return err
				}
			case *ast.ExprStmt:
				if err := visitExpr(value.Expr); err != nil {
					return err
				}
			case *ast.ReturnStmt:
				if err := visitExpr(value.Expr); err != nil {
					return err
				}
			case *ast.WhenStmt:
				if err := visitExpr(value.Cond); err != nil {
					return err
				}
				if err := visitStmts(value.Body); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, fn := range program.Functions {
		if err := visitExpr(fn.Return); err != nil {
			return fmt.Errorf("function %s: %w", fn.Name, err)
		}
	}
	return visitStmts(program.Flow.Body)
}

func dependencyRoot(spec string) string {
	spec = strings.TrimSpace(spec)
	for index, character := range spec {
		if strings.ContainsRune("<>!=~[,; \t", character) {
			return spec[:index]
		}
	}
	return spec
}

func isPythonControlOperation(name string) bool {
	switch name {
	case "python.call", "python.to_json", "python.release", "python.module_available", "python.open_blob", "python.put_blob", "python.release_blob":
		return true
	default:
		return false
	}
}

type builder struct {
	aliases       map[string]string
	graph         *Graph
	known         map[string]bool
	allNames      map[string]bool
	types         map[string]string
	fnTypes       map[string]string
	fnParams      map[string][]string
	functionNames map[string]bool
	outputID      int
	exprID        int
	gateID        int
}

func (b *builder) stmts(stmts []ast.Stmt, gates []string) error {
	terminal := false
	for _, stmt := range stmts {
		if terminal {
			return b.err(statementPos(stmt), "statements after return are not allowed")
		}
		switch s := stmt.(type) {
		case *ast.BindStmt:
			if _, exists := b.aliases[s.Name]; exists {
				return b.err(s.Pos, "binding %q conflicts with a module alias; choose a different binding name", s.Name)
			}
			if err := b.validateCalls(s.Expr, true); err != nil {
				return b.wrapError(s.Pos, err)
			}
			if reservedName(s.Name) {
				return b.err(s.Pos, "identifier %q is reserved for generated graph nodes", s.Name)
			}
			if b.allNames[s.Name] {
				return b.err(s.Pos, "duplicate binding %q", s.Name)
			}
			stateNode := false
			retryAttempts, retryErr := retryAttemptsOf(s.Expr)
			if retryErr != nil {
				return b.wrapError(s.Pos, retryErr)
			}
			feedbackStep, feedbackVerify, feedbackAttempts, feedbackErr := feedbackInfoOf(s.Expr)
			if feedbackErr != nil {
				return b.wrapError(s.Pos, feedbackErr)
			}
			if call, ok := s.Expr.(*ast.CallExpr); ok && call.Name == "state" {
				if len(call.Args) != 1 {
					return b.wrapError(s.Pos, argumentCountError("state", 1, 1, len(call.Args)))
				}
				if err := validateSimpleExpr(call.Args[0]); err != nil {
					return b.err(s.Pos, "state initial value: %v", err)
				}
				stateNode = true
			} else if err := validateExpr(s.Expr); err != nil {
				return b.wrapError(s.Pos, err)
			}
			refs := refsOf(s.Expr)
			if err := b.checkRefs(refs, s.Pos); err != nil {
				return err
			}
			typ, err := inferExprType(s.Expr, b.types, b.fnTypes, b.fnParams)
			if err != nil {
				return b.wrapError(s.Pos, err)
			}
			b.graph.Nodes = append(b.graph.Nodes, Node{Name: s.Name, Deps: unique(refs), Gates: unique(gates), Expr: s.Expr, Type: typ, State: stateNode, RetryAttempts: retryAttempts, FeedbackAttempts: feedbackAttempts, FeedbackStep: feedbackStep, FeedbackVerify: feedbackVerify, Pos: s.Pos})
			b.known[s.Name], b.allNames[s.Name] = true, true
			b.types[s.Name] = typ
		case *ast.WhenStmt:
			if err := b.validateCalls(s.Cond, false); err != nil {
				return b.wrapError(s.Pos, err)
			}
			if err := validateSimpleExpr(s.Cond); err != nil {
				return b.err(s.Pos, "when condition: %v", err)
			}
			if err := b.checkRefs(refsOf(s.Cond), s.Pos); err != nil {
				return err
			}
			if typ, err := inferExprType(s.Cond, b.types, b.fnTypes, b.fnParams); err != nil {
				return b.wrapError(s.Pos, err)
			} else if !compatibleType("bool", typ) {
				return b.err(s.Pos, "when condition must be bool, got %s", typ)
			}
			gate, _ := gateName(s.Cond)
			if gate == "" {
				if lit, ok := s.Cond.(*ast.LiteralExpr); !ok || lit.Value != true {
					gate = fmt.Sprintf("__gate_%d", b.gateID)
					b.gateID++
					b.graph.Nodes = append(b.graph.Nodes, Node{Name: gate, Deps: unique(refsOf(s.Cond)), Gates: unique(gates), Expr: s.Cond, Type: "bool", Pos: s.Pos})
				}
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
			if b.graph.ReturnType == "void" {
				if s.Expr != nil {
					if call, ok := s.Expr.(*ast.CallExpr); ok && call.Name == "print" && !call.Python {
						return b.wrapError(s.Pos, repairError("LIP_RETURN_ERROR", "print returns null; use print(...) as a statement instead of returning it", "For printing only, write flow main() { print(79 / 134) }; omit return and the output declaration."))
					}
					return b.err(s.Pos, "flow %q needs an explicit output type to return a value; replace void (or the omitted output type) with -> number, -> bool, -> string, -> list, -> object or -> any", b.graph.Flow)
				}
				b.outputID++
				terminal = true
				continue
			}
			if s.Expr == nil {
				return b.err(s.Pos, "return needs a value for output type %s; use return <value>, or omit the Flow output declaration for effects only", b.graph.ReturnType)
			}
			if err := b.validateCalls(s.Expr, true); err != nil {
				return b.wrapError(s.Pos, err)
			}
			retryAttempts, retryErr := retryAttemptsOf(s.Expr)
			if retryErr != nil {
				return b.wrapError(s.Pos, retryErr)
			}
			feedbackStep, feedbackVerify, feedbackAttempts, feedbackErr := feedbackInfoOf(s.Expr)
			if feedbackErr != nil {
				return b.wrapError(s.Pos, feedbackErr)
			}
			if err := validateExpr(s.Expr); err != nil {
				return b.wrapError(s.Pos, err)
			}
			refs := refsOf(s.Expr)
			if err := b.checkRefs(refs, s.Pos); err != nil {
				return err
			}
			typ, err := inferExprType(s.Expr, b.types, b.fnTypes, b.fnParams)
			if err != nil {
				return b.wrapError(s.Pos, err)
			}
			if err := validateDeclaredResult(s.Expr, b.graph.ReturnType, b.types, b.fnTypes, b.fnParams); err != nil {
				return b.wrapError(s.Pos, err)
			}
			name := fmt.Sprintf("__return_%d", b.outputID)
			b.outputID++
			b.graph.Nodes = append(b.graph.Nodes, Node{Name: name, Deps: unique(refs), Gates: unique(gates), Expr: s.Expr, Type: typ, Output: true, RetryAttempts: retryAttempts, FeedbackAttempts: feedbackAttempts, FeedbackStep: feedbackStep, FeedbackVerify: feedbackVerify, Pos: s.Pos})
			terminal = true
		case *ast.ExprStmt:
			if err := b.validateCalls(s.Expr, true); err != nil {
				return b.wrapError(s.Pos, err)
			}
			retryAttempts, retryErr := retryAttemptsOf(s.Expr)
			if retryErr != nil {
				return b.wrapError(s.Pos, retryErr)
			}
			feedbackStep, feedbackVerify, feedbackAttempts, feedbackErr := feedbackInfoOf(s.Expr)
			if feedbackErr != nil {
				return b.wrapError(s.Pos, feedbackErr)
			}
			if err := validateExpr(s.Expr); err != nil {
				return b.wrapError(s.Pos, err)
			}
			refs := refsOf(s.Expr)
			if err := b.checkRefs(refs, s.Pos); err != nil {
				return err
			}
			if _, err := inferExprType(s.Expr, b.types, b.fnTypes, b.fnParams); err != nil {
				return b.wrapError(s.Pos, err)
			}
			name := fmt.Sprintf("__expr_%d", b.exprID)
			b.exprID++
			b.graph.Nodes = append(b.graph.Nodes, Node{Name: name, Deps: unique(refs), Gates: unique(gates), Expr: s.Expr, RetryAttempts: retryAttempts, FeedbackAttempts: feedbackAttempts, FeedbackStep: feedbackStep, FeedbackVerify: feedbackVerify, Pos: s.Pos})
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

func retryAttemptsOf(expr ast.Expr) (int, error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call.Name != "retry" {
		return 0, nil
	}
	if len(call.Args) != 2 {
		return 0, fmt.Errorf("retry expects a call and an attempt count")
	}
	if _, ok := call.Args[0].(*ast.CallExpr); !ok {
		return 0, fmt.Errorf("retry first argument must be a Host or local function call")
	}
	inner := call.Args[0].(*ast.CallExpr)
	if inner.Name == "retry" || inner.Name == "feedback" || inner.Name == "state" {
		return 0, fmt.Errorf("retry requires an ordinary operation call")
	}
	if err := validateExpr(inner); err != nil {
		return 0, err
	}
	literal, ok := call.Args[1].(*ast.LiteralExpr)
	if !ok {
		return 0, fmt.Errorf("retry attempt count must be an integer literal")
	}
	number, ok := literal.Value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 1 || number != math.Trunc(number) || number > float64(int(^uint(0)>>1)) {
		return 0, fmt.Errorf("retry attempt count must be a positive integer")
	}
	return int(number), nil
}

func feedbackInfoOf(expr ast.Expr) (step, verify string, attempts int, err error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call.Name != "feedback" {
		return "", "", 0, nil
	}
	if len(call.Args) != 4 {
		return "", "", 0, fmt.Errorf("feedback expects an initial call, step name, verifier name and attempt count")
	}
	if _, ok := call.Args[0].(*ast.CallExpr); !ok {
		return "", "", 0, fmt.Errorf("feedback first argument must be a Host or local function call")
	}
	inner := call.Args[0].(*ast.CallExpr)
	if inner.Name == "retry" || inner.Name == "feedback" || inner.Name == "state" {
		return "", "", 0, fmt.Errorf("feedback requires an ordinary initial operation call")
	}
	stepExpr, ok := call.Args[1].(*ast.IdentExpr)
	if !ok {
		return "", "", 0, fmt.Errorf("feedback step must be an operation name")
	}
	verifyExpr, ok := call.Args[2].(*ast.IdentExpr)
	if !ok {
		return "", "", 0, fmt.Errorf("feedback verifier must be an operation name")
	}
	literal, ok := call.Args[3].(*ast.LiteralExpr)
	if !ok {
		return "", "", 0, fmt.Errorf("feedback attempt count must be an integer literal")
	}
	number, ok := literal.Value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 1 || number != math.Trunc(number) || number > float64(int(^uint(0)>>1)) {
		return "", "", 0, fmt.Errorf("feedback attempt count must be a positive integer")
	}
	return stepExpr.Name, verifyExpr.Name, int(number), nil
}

func validateExpr(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.IdentExpr, *ast.LiteralExpr:
		return nil
	case *ast.LambdaExpr:
		return validateSimpleExpr(e)
	case *ast.CallExpr:
		if e.Name == "state" {
			return fmt.Errorf("state(...) is only allowed as a Flow binding")
		}
		if e.Name == "retry" {
			if _, err := retryAttemptsOf(e); err != nil {
				return err
			}
			return nil
		}
		if e.Name == "feedback" {
			if _, _, _, err := feedbackInfoOf(e); err != nil {
				return err
			}
			initial := e.Args[0].(*ast.CallExpr)
			for _, arg := range initial.Args {
				if err := validateSimpleExpr(arg); err != nil {
					return fmt.Errorf("feedback initial call: %w", err)
				}
			}
			return nil
		}
		return validateCallArguments(e)
	case *ast.UnaryExpr:
		return validateSimpleExpr(e.Operand)
	case *ast.ObjectExpr:
		seen := map[string]bool{}
		for _, field := range e.Fields {
			if seen[field.Name] {
				return fmt.Errorf("duplicate object key %q", field.Name)
			}
			seen[field.Name] = true
			if err := validateSimpleExpr(field.Value); err != nil {
				return err
			}
		}
		return nil
	case *ast.BinaryExpr:
		if err := validateSimpleExpr(e.Left); err != nil {
			return err
		}
		return validateSimpleExpr(e.Right)
	case *ast.IfExpr:
		if err := validateSimpleExpr(e.Cond); err != nil {
			return err
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
	case *ast.ComprehensionExpr:
		return validateComprehension(e)
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

func validateComprehension(expr *ast.ComprehensionExpr) error {
	if reservedName(expr.Variable) {
		return fmt.Errorf("identifier %q is reserved for generated graph nodes", expr.Variable)
	}
	if err := validateSimpleExpr(expr.Source); err != nil {
		return fmt.Errorf("comprehension source: %w", err)
	}
	if call, ok := expr.Element.(*ast.CallExpr); ok && (call.Name == "retry" || call.Name == "feedback" || call.Name == "state") {
		return fmt.Errorf("control operations are only supported as standalone Flow nodes, not Map elements")
	}
	return validateSimpleExpr(expr.Element)
}

func validateSimpleExpr(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.IdentExpr, *ast.LiteralExpr:
		return nil
	case *ast.UnaryExpr:
		return validateSimpleExpr(e.Operand)
	case *ast.ObjectExpr:
		seen := map[string]bool{}
		for _, field := range e.Fields {
			if seen[field.Name] {
				return fmt.Errorf("duplicate object key %q", field.Name)
			}
			seen[field.Name] = true
			if err := validateSimpleExpr(field.Value); err != nil {
				return err
			}
		}
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
	case *ast.ComprehensionExpr:
		return validateComprehension(e)
	case *ast.FieldExpr:
		return validateSimpleExpr(e.Object)
	case *ast.IndexExpr:
		if err := validateSimpleExpr(e.Object); err != nil {
			return err
		}
		return validateSimpleExpr(e.Index)
	case *ast.IfExpr:
		for _, part := range []ast.Expr{e.Cond, e.Then, e.Else} {
			if err := validateSimpleExpr(part); err != nil {
				return err
			}
		}
		return nil
	case *ast.CallExpr:
		if e.Name == "state" || e.Name == "retry" || e.Name == "feedback" {
			return fmt.Errorf("%s is only supported as a Flow node", e.Name)
		}
		return validateCallArguments(e)
	case *ast.LambdaExpr:
		return repairError("LIP_CALLBACK_ERROR", "inline fn is only supported as a collection callback", "Use it in list.map, list.filter, list.sort_by, list.group_by, list.split_by, list.any, list.all, list.scan or fold. For reusable functions, declare a named pure fn.")
	default:
		if _, ok := expr.(*ast.CallExpr); ok {
			return fmt.Errorf("nested calls are not supported")
		}
		return fmt.Errorf("complex expression is not supported here")
	}
}

func refsOf(expr ast.Expr) []string {
	var refs []string
	var visit func(ast.Expr, map[string]bool)
	visit = func(e ast.Expr, bound map[string]bool) {
		switch x := e.(type) {
		case *ast.IdentExpr:
			if !bound[x.Name] {
				refs = append(refs, x.Name)
			}
		case *ast.CallExpr:
			args := x.Args
			callback := callbackIndex(x)
			if x.Name == "feedback" && len(args) > 0 {
				args = args[:1]
			}
			for index, a := range args {
				if index == callback {
					if _, named := a.(*ast.IdentExpr); named {
						continue
					}
				}
				visit(a, bound)
			}
		case *ast.LambdaExpr:
			next := cloneBoolMap(bound)
			for _, param := range x.Params {
				next[param] = true
			}
			visit(x.Return, next)
		case *ast.UnaryExpr:
			visit(x.Operand, bound)
		case *ast.ObjectExpr:
			for _, field := range x.Fields {
				visit(field.Value, bound)
			}
		case *ast.BinaryExpr:
			visit(x.Left, bound)
			visit(x.Right, bound)
		case *ast.IfExpr:
			visit(x.Cond, bound)
			visit(x.Then, bound)
			visit(x.Else, bound)
		case *ast.ListExpr:
			for _, item := range x.Items {
				visit(item, bound)
			}
		case *ast.ComprehensionExpr:
			visit(x.Source, bound)
			next := cloneBoolMap(bound)
			next[x.Variable] = true
			visit(x.Element, next)
		case *ast.FieldExpr:
			visit(x.Object, bound)
		case *ast.IndexExpr:
			visit(x.Object, bound)
			visit(x.Index, bound)
		}
	}
	visit(expr, nil)
	return refs
}

func validateFunction(fn *ast.Function) error {
	if fn.ReturnType != "" && !validTypeName(fn.ReturnType) {
		return fmt.Errorf("unknown return type %q", fn.ReturnType)
	}
	seen := make(map[string]bool)
	for _, param := range fn.Params {
		if reservedName(param) {
			return fmt.Errorf("identifier %q is reserved for generated graph nodes", param)
		}
		if seen[param] {
			return fmt.Errorf("duplicate parameter %q", param)
		}
		seen[param] = true
		if fn.ParamTypes[param] == "" {
			return fmt.Errorf("parameter %q needs an explicit type", param)
		}
		if typ := fn.ParamTypes[param]; typ != "" && !validTypeName(typ) {
			return fmt.Errorf("unknown type %q for parameter %q", typ, param)
		}
	}
	if err := validateExpr(fn.Return); err != nil {
		return err
	}
	if attempts, err := retryAttemptsOf(fn.Return); err != nil {
		return err
	} else if attempts > 0 {
		return fmt.Errorf("retry is only supported as a Flow node")
	}
	if _, _, attempts, err := feedbackInfoOf(fn.Return); err != nil {
		return err
	} else if attempts > 0 {
		return fmt.Errorf("feedback is only supported as a Flow node")
	}
	for _, ref := range refsOf(fn.Return) {
		if !seen[ref] {
			return fmt.Errorf("unknown function parameter %q", ref)
		}
	}
	return nil
}

func statementPos(stmt ast.Stmt) token.Pos {
	switch s := stmt.(type) {
	case *ast.BindStmt:
		return s.Pos
	case *ast.WhenStmt:
		return s.Pos
	case *ast.ReturnStmt:
		return s.Pos
	case *ast.ExprStmt:
		return s.Pos
	}
	return token.Pos{}
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
		case nil:
			return "null", nil
		case string:
			return "string", nil
		case bool:
			return "bool", nil
		default:
			return "number", nil
		}
	case *ast.CallExpr:
		if e.Python {
			for _, arg := range e.Args {
				if _, err := inferExprType(arg, env, fnTypes, fnParams); err != nil {
					return "any", err
				}
			}
			return "any", nil
		}
		if e.Name == "str" {
			if len(e.Args) != 1 {
				return "any", argumentCountError("str", 1, 1, len(e.Args))
			}
			if _, err := inferExprType(e.Args[0], env, fnTypes, fnParams); err != nil {
				return "any", err
			}
			return "string", nil
		}
		if e.Name == "fail" {
			if len(e.Args) != 1 {
				return "any", argumentCountError("fail", 1, 1, len(e.Args))
			}
			actual, err := inferExprType(e.Args[0], env, fnTypes, fnParams)
			if err != nil {
				return "any", err
			}
			if !compatibleType("string", actual) {
				return "any", fmt.Errorf("fail expects string, got %s", actual)
			}
			// An internal bottom type preserves the successful branch's type;
			// it is not a source-level type or a dynamic-check escape hatch.
			return "never", nil
		}
		if _, ok := listops.Lookup(e.Name); ok {
			return inferListCall(e, env, fnTypes, fnParams)
		}
		if _, ok := stringops.Lookup(e.Name); ok {
			return inferStringCall(e, env, fnTypes, fnParams)
		}
		if e.Name == "len" || e.Name == "range" || e.Name == "fold" {
			return inferCollectionCall(e, env, fnTypes, fnParams)
		}
		if e.Name == "state" {
			if len(e.Args) != 1 {
				return "any", argumentCountError("state", 1, 1, len(e.Args))
			}
			return inferExprType(e.Args[0], env, fnTypes, fnParams)
		}
		if e.Name == "retry" {
			if _, err := retryAttemptsOf(e); err != nil {
				return "any", err
			}
			inner := e.Args[0].(*ast.CallExpr)
			return inferExprType(inner, env, fnTypes, fnParams)
		}
		if e.Name == "feedback" {
			step, verify, _, err := feedbackInfoOf(e)
			if err != nil {
				return "any", err
			}
			candidate, err := inferExprType(e.Args[0], env, fnTypes, fnParams)
			if err != nil {
				return "any", err
			}
			for _, operation := range []string{step, verify} {
				if expected, ok := fnParams[operation]; ok {
					if len(expected) != 1 {
						return "any", fmt.Errorf("feedback function %s expects %d arguments; feedback supplies one candidate", operation, len(expected))
					}
					if !compatibleType(expected[0], candidate) {
						return "any", fmt.Errorf("feedback function %s expects %s, candidate is %s", operation, expected[0], candidate)
					}
				}
			}
			if typ := fnTypes[verify]; typ != "" && typ != "any" && typ != "bool" {
				return "any", fmt.Errorf("feedback verifier %s must return bool, got %s", verify, typ)
			}
			if typ := fnTypes[step]; typ != "" && !compatibleType(candidate, typ) {
				return "any", fmt.Errorf("feedback step %s returns %s, candidate is %s", step, typ, candidate)
			}
			return candidate, nil
		}
		for _, arg := range e.Args {
			if _, err := inferExprType(arg, env, fnTypes, fnParams); err != nil {
				return "any", err
			}
		}
		if e.Name == "print" {
			return "null", nil
		}
		if expected, ok := fnParams[e.Name]; ok {
			if len(expected) != len(e.Args) {
				return "any", argumentCountError(e.Name, len(expected), len(expected), len(e.Args))
			}
			for i, arg := range e.Args {
				actual, err := inferExprType(arg, env, fnTypes, fnParams)
				if err != nil {
					return "any", err
				}
				if !compatibleType(expected[i], actual) {
					return "any", fmt.Errorf("function %s argument %d expects %s, got %s", e.Name, i+1, expected[i], actual)
				}
			}
		}
		if typ := fnTypes[e.Name]; typ != "" {
			return typ, nil
		}
		return "any", nil
	case *ast.UnaryExpr:
		typ, err := inferExprType(e.Operand, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if typ == "never" {
			return "never", nil
		}
		if typ != "any" && typ != "bool" {
			return "any", fmt.Errorf("operator ! expects bool, got %s", typ)
		}
		return "bool", nil
	case *ast.ObjectExpr:
		for _, field := range e.Fields {
			if _, err := inferExprType(field.Value, env, fnTypes, fnParams); err != nil {
				return "any", err
			}
		}
		return "object", nil
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
		if cond != "any" && cond != "bool" && cond != "never" {
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
		if thenType == "never" {
			return elseType, nil
		}
		if elseType == "never" {
			return thenType, nil
		}
		if thenType == "any" || elseType == "any" {
			return "any", nil
		}
		if thenType == "null" && validOutputType(elseType) {
			return strings.TrimSuffix(elseType, "?") + "?", nil
		}
		if elseType == "null" && validOutputType(thenType) {
			return strings.TrimSuffix(thenType, "?") + "?", nil
		}
		if strings.TrimSuffix(thenType, "?") == strings.TrimSuffix(elseType, "?") {
			if strings.HasSuffix(thenType, "?") || strings.HasSuffix(elseType, "?") {
				return strings.TrimSuffix(thenType, "?") + "?", nil
			}
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
	case *ast.ComprehensionExpr:
		sourceType, err := inferExprType(e.Source, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if sourceType != "never" && sourceType != "any" && sourceType != "list" {
			return "any", fmt.Errorf("comprehension source must be a list, got %s", sourceType)
		}
		next := cloneStringMap(env)
		next[e.Variable] = "any"
		if _, err := inferExprType(e.Element, next, fnTypes, fnParams); err != nil {
			return "any", err
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
	if left == "never" {
		return "never", nil
	}
	if right == "never" {
		if op != "&&" && op != "||" {
			return "never", nil
		}
		if compatibleType("bool", left) {
			return "bool", nil
		}
		return "any", fmt.Errorf("operator %s expects booleans, got %s", op, left)
	}
	known := func(t string) bool { return t != "any" }
	switch op {
	case "+":
		if known(left) && left != "string" && left != "number" || known(right) && right != "string" && right != "number" {
			return "any", fmt.Errorf("operator + cannot combine %s and %s", left, right)
		}
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
		if known(left) && left != "string" && left != "number" || known(right) && right != "string" && right != "number" {
			return "any", fmt.Errorf("operator * expects numbers or string and number, got %s and %s", left, right)
		}
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
		if known(left) && known(right) && left != right && left != "null" && right != "null" {
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

func (b *builder) wrapError(pos token.Pos, err error) error {
	return fmt.Errorf("%d:%d: %w", pos.Line, pos.Column, err)
}

func validTypeName(name string) bool {
	switch name {
	case "any", "string", "number", "bool", "list", "object":
		return true
	default:
		return false
	}
}

func reservedName(name string) bool {
	return strings.HasPrefix(name, "__return_") || strings.HasPrefix(name, "__expr_") || strings.HasPrefix(name, "__gate_") || strings.HasPrefix(name, "__lip_")
}

func validOutputType(typ string) bool {
	return typ == "void" || validTypeName(strings.TrimSuffix(typ, "?"))
}
func compatibleType(expected, actual string) bool {
	return actual == "never" || strings.TrimSuffix(expected, "?") == "any" || actual == "any" || expected == actual || strings.HasSuffix(expected, "?") && (actual == "null" || strings.TrimSuffix(expected, "?") == actual)
}
func pureBuiltinName(name string) bool {
	_, list := listops.Lookup(name)
	_, text := stringops.Lookup(name)
	return list || text || name == "str" || name == "len" || name == "range" || name == "fold" || name == "fail"
}

func builtinName(name string) bool {
	return pureBuiltinName(name) || name == "print" || name == "state" || name == "retry" || name == "feedback"
}

// Pure local functions may compose other local functions and str. External
// work belongs to explicit Flow nodes, whose effects the scheduler can see.
func validatePureFunctions(functions []*ast.Function, names map[string]bool) error {
	byName := map[string]*ast.Function{}
	for _, fn := range functions {
		byName[fn.Name] = fn
	}
	state := map[string]int{}
	var stack []string
	var visit func(string) error
	visit = func(name string) error {
		fn := byName[name]
		if state[name] == 1 {
			start := 0
			for stack[start] != name {
				start++
			}
			for _, member := range stack[start:] {
				if byName[member].ReturnType == "" {
					f := byName[member]
					return fmt.Errorf("%d:%d: recursive function %q needs an explicit return type", f.Pos.Line, f.Pos.Column, member)
				}
			}
			return nil
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		stack = append(stack, name)
		if external := firstPythonCall(fn.Return); external != nil {
			return fmt.Errorf("%d:%d: function %q must be pure; bind Python operation %q in the Flow", external.Pos.Line, external.Pos.Column, name, external.Name)
		}
		for _, called := range callNames(fn.Return) {
			if pureBuiltinName(called) {
				continue
			}
			if !names[called] {
				return fmt.Errorf("%d:%d: function %q must be pure; bind external operation %q in the Flow", fn.Pos.Line, fn.Pos.Column, name, called)
			}
			if err := visit(called); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		return nil
	}
	for _, fn := range functions {
		if err := visit(fn.Name); err != nil {
			return err
		}
	}
	return nil
}

var pythonRequirement = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*(?:\s*(?:~=|==|!=|<=|>=|<|>)\s*[0-9][A-Za-z0-9.*+_-]*(?:\s*,\s*(?:~=|==|!=|<=|>=|<|>)\s*[0-9][A-Za-z0-9.*+_-]*)*)?$`)
var hostRequirement = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_]*(?:\.[\p{L}_][\p{L}\p{N}_]*)*(?:\.\*)?$`)

func validateDependency(d ast.Dependency) error {
	valid := strings.TrimSpace(d.Spec) == d.Spec && d.Spec != ""
	switch d.Kind {
	case "python":
		valid = valid && pythonRequirement.MatchString(d.Spec)
	case "host":
		valid = valid && hostRequirement.MatchString(d.Spec)
	case "go":
		valid = valid && !strings.ContainsAny(d.Spec, " \t\n<>!=?*#:") && !strings.HasPrefix(d.Spec, "/") && !strings.HasSuffix(d.Spec, "/") && !strings.Contains(d.Spec, "//")
	}
	if !valid {
		guidance := map[string]string{
			"python": `use the actual Python module name, optionally with a version constraint, e.g. import python "numpy>=1.26" as np`,
			"host":   `use an operation name, e.g. import host "fetch" or import host "service.*"`,
			"go":     `use a Go module path, e.g. import go "example.com/adapter"`,
		}
		if d.Kind == "python" && d.Spec == "scikit-learn" {
			guidance["python"] = `use the actual Python module name: import python "sklearn" as ml; scikit-learn is its installation name`
		}
		return fmt.Errorf("%d:%d: invalid %s dependency %q; %s", d.Pos.Line, d.Pos.Column, d.Kind, d.Spec, guidance[d.Kind])
	}
	return nil
}

func (b *builder) validateCalls(expr ast.Expr, root bool) error {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if e.Name == "retry" || e.Name == "feedback" {
			if !root {
				return fmt.Errorf("%s is only supported as a Flow node", e.Name)
			}
			if len(e.Args) > 0 {
				return b.validateCalls(e.Args[0], true)
			}
			return nil
		}
		if e.Name == "state" {
			if !root {
				return fmt.Errorf("state is only supported as a Flow binding")
			}
		} else if !root && !b.functionNames[e.Name] && (e.Python || !pureBuiltinName(e.Name)) {
			return fmt.Errorf("nested external call %q must be assigned to a Flow binding first", e.Name)
		}
		args := e.Args
		if index := callbackIndex(e); index >= 0 {
			switch fn := args[index].(type) {
			case *ast.IdentExpr:
				if !b.functionNames[fn.Name] {
					return callbackError(e.Name, callbackArity(e), fmt.Sprintf("%s callback/reducer must name a local pure function or use inline fn", e.Name))
				}
			case *ast.LambdaExpr:
				if err := b.validateCalls(fn.Return, false); err != nil {
					return fmt.Errorf("%s callback must be pure: %w", e.Name, err)
				}
			default:
				return callbackError(e.Name, callbackArity(e), fmt.Sprintf("%s callback/reducer must name a local pure function or use inline fn", e.Name))
			}
			args = args[:index]
		}
		for _, arg := range args {
			if err := b.validateCalls(arg, false); err != nil {
				return err
			}
		}
	case *ast.UnaryExpr:
		return b.validateCalls(e.Operand, false)
	case *ast.ObjectExpr:
		for _, field := range e.Fields {
			if err := b.validateCalls(field.Value, false); err != nil {
				return err
			}
		}
	case *ast.BinaryExpr:
		if err := b.validateCalls(e.Left, false); err != nil {
			return err
		}
		return b.validateCalls(e.Right, false)
	case *ast.IfExpr:
		for _, part := range []ast.Expr{e.Cond, e.Then, e.Else} {
			if err := b.validateCalls(part, false); err != nil {
				return err
			}
		}
	case *ast.ListExpr:
		for _, item := range e.Items {
			if err := b.validateCalls(item, false); err != nil {
				return err
			}
		}
	case *ast.ComprehensionExpr:
		if err := b.validateCalls(e.Source, false); err != nil {
			return fmt.Errorf("comprehension source: %w", err)
		}
		if err := b.validateCalls(e.Element, root); err != nil {
			return fmt.Errorf("comprehension element: %w", err)
		}
		return nil
	case *ast.FieldExpr:
		return b.validateCalls(e.Object, false)
	case *ast.IndexExpr:
		if err := b.validateCalls(e.Object, false); err != nil {
			return err
		}
		return b.validateCalls(e.Index, false)
	}
	return nil
}

func validateDeclaredResult(expr ast.Expr, expected string, env, fnTypes map[string]string, fnParams map[string][]string) error {
	if expected == "" || expected == "any" {
		return nil
	}
	if conditional, ok := expr.(*ast.IfExpr); ok {
		if err := validateDeclaredResult(conditional.Then, expected, env, fnTypes, fnParams); err != nil {
			return err
		}
		return validateDeclaredResult(conditional.Else, expected, env, fnTypes, fnParams)
	}
	actual, err := inferExprType(expr, env, fnTypes, fnParams)
	if err != nil {
		return err
	}
	if !compatibleType(expected, actual) {
		if call, ok := expr.(*ast.CallExpr); ok && call.Name == "print" && !call.Python {
			return repairError("LIP_TYPE_ERROR", fmt.Sprintf("return has type null, declared output is %s; print writes a value and returns null", expected), "For printing only, use print(...) as a statement and omit the Flow output declaration and return.")
		}
		return fmt.Errorf("return has type %s, declared output is %s", actual, expected)
	}
	return nil
}
