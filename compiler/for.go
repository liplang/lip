package compiler

import (
	"fmt"
	"sort"

	"lipalpha/compiler/ast"
)

// Each iteration runs a fresh, sequential body graph. Captures map its input
// names to the enclosing graph's names, which can include match-local names.
type ForNode struct {
	Variable string
	Body     *Graph
	Captures map[string]string
}

func (b *builder) forStmt(stmt *ast.ForStmt, gates []string) error {
	if stmt.Variable != "" && reservedName(stmt.Variable) {
		return b.err(stmt.Pos, "identifier %q is reserved for generated graph nodes", stmt.Variable)
	}
	var refs []string
	if stmt.Source != nil {
		if err := b.validateCalls(stmt.Source, true); err != nil {
			return b.wrapError(stmt.Pos, err)
		}
		if err := validateSimpleExpr(stmt.Source); err != nil {
			return b.wrapError(stmt.Pos, err)
		}
		refs = refsOf(stmt.Source)
		if err := b.checkRefs(refs, stmt.Pos); err != nil {
			return err
		}
		typ, err := inferExprType(stmt.Source, b.types, b.fnTypes, b.fnParams)
		if err != nil {
			return b.wrapError(stmt.Pos, err)
		}
		if !compatibleType("list", typ) {
			return b.err(stmt.Pos, "for source must be a list, got %s", typ)
		}
	}
	body := &Graph{Flow: b.graph.Flow, ReturnType: "void", ParamTypes: map[string]string{}, Dependencies: b.graph.Dependencies, Functions: b.graph.Functions}
	inner := builder{
		graph: body, inLoop: true,
		known: cloneBoolMap(b.known), allNames: cloneBoolMap(b.allNames), types: cloneStringMap(b.types), graphNames: map[string]string{},
		fnTypes: b.fnTypes, fnParams: b.fnParams, functionNames: b.functionNames, aliases: b.aliases,
		allowNestedExternalCalls: b.allowNestedExternalCalls,
	}
	if stmt.Variable != "" {
		inner.known[stmt.Variable], inner.allNames[stmt.Variable] = true, true
		inner.types[stmt.Variable] = "any"
		if call, ok := stmt.Source.(*ast.CallExpr); ok && call.Name == "range" && !externalCall(call) {
			inner.types[stmt.Variable] = "number"
		}
	}
	if err := inner.stmts(stmt.Body, nil); err != nil {
		return err
	}
	// Capture only inputs actually used by the body. Unrelated gated bindings
	// must not skip a loop, and the iteration variable shadows an outer name.
	captures := map[string]string{}
	for _, node := range body.Nodes {
		for _, name := range node.Deps {
			if name != stmt.Variable && b.known[name] {
				resolved := b.graphNames[name]
				if resolved == "" {
					resolved = name
				}
				captures[name] = resolved
			}
		}
	}
	names := make([]string, 0, len(captures))
	for name := range captures {
		names = append(names, name)
	}
	sort.Strings(names)
	deps := b.graphRefs(refs)
	for _, name := range names {
		body.Params = append(body.Params, name)
		body.ParamTypes[name] = b.types[name]
		deps = append(deps, captures[name])
	}
	if stmt.Variable != "" {
		body.Params = append(body.Params, stmt.Variable)
		body.ParamTypes[stmt.Variable] = inner.types[stmt.Variable]
	}
	name := fmt.Sprintf("__for_%d", b.forID)
	b.forID++
	var source ast.Expr
	if stmt.Source != nil {
		source = b.graphExpr(stmt.Source)
	}
	b.graph.Nodes = append(b.graph.Nodes, Node{Name: name, Expr: source, Deps: unique(deps), Gates: unique(gates), Type: "void", Pos: stmt.Pos, For: &ForNode{Variable: stmt.Variable, Body: body, Captures: captures}})
	for local := range inner.allNames {
		b.allNames[local] = true
	}
	return nil
}

func nodeCalls(node Node) []string {
	calls := callNames(node.Expr)
	if node.FeedbackAttempts > 0 {
		calls = append(calls, node.FeedbackStep, node.FeedbackVerify)
	}
	if node.For != nil {
		for _, inner := range node.For.Body.Nodes {
			calls = append(calls, nodeCalls(inner)...)
		}
	}
	return unique(calls)
}

// A loop can always fall through (including an empty source). A match stops
// its enclosing block only if every possible arm stops it.
func blockTerminates(body []ast.Stmt) bool {
	for _, stmt := range body {
		switch value := stmt.(type) {
		case *ast.ReturnStmt, *ast.LoopControlStmt:
			return true
		case *ast.MatchStmt:
			all := len(value.Arms) > 0
			for _, arm := range value.Arms {
				all = all && blockTerminates(arm.Body)
			}
			if all {
				return true
			}
		}
	}
	return false
}
