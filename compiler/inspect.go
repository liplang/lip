package compiler

import (
	"encoding/json"
	"fmt"
	"lipalpha/compiler/ast"
)

// InspectJSON is a versioned view of the checked graph, independent of AST
// implementation details and without executing Host operations.
func InspectJSON(g *Graph) ([]byte, error) {
	if g == nil {
		return nil, fmt.Errorf("cannot inspect a nil graph")
	}
	type input struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	type dependency struct {
		Kind  string `json:"kind"`
		Spec  string `json:"spec"`
		Alias string `json:"alias,omitempty"`
	}
	type location struct {
		Line   int `json:"line"`
		Column int `json:"column"`
	}
	type node struct {
		Name         string   `json:"name"`
		Dependencies []string `json:"dependencies"`
		Gates        []string `json:"gates"`
		Type         string   `json:"type"`
		Kind         string   `json:"kind"`
		Output       bool     `json:"output"`
		Pure         bool     `json:"pure"`
		Calls        []string `json:"calls"`
		Source       location `json:"source"`
	}
	document := struct {
		Schema       string       `json:"schema"`
		Flow         string       `json:"flow"`
		Parameters   []input      `json:"parameters"`
		OutputType   string       `json:"output_type"`
		Requirements []dependency `json:"requirements"`
		Nodes        []node       `json:"nodes"`
	}{Schema: "lip.graph.v1", Flow: g.Flow, OutputType: g.ReturnType, Parameters: []input{}, Requirements: []dependency{}, Nodes: []node{}}
	for _, name := range g.Params {
		document.Parameters = append(document.Parameters, input{name, g.ParamTypes[name]})
	}
	for _, requirement := range g.Dependencies {
		document.Requirements = append(document.Requirements, dependency{requirement.Kind, requirement.Spec, requirement.Alias})
	}
	for _, item := range g.Nodes {
		kind := "value"
		if item.For != nil {
			kind = "for"
		} else if item.LoopControl != "" {
			kind = item.LoopControl
		} else if item.State {
			kind = "state"
		} else if item.RetryAttempts > 0 {
			kind = "retry"
		} else if item.FeedbackAttempts > 0 {
			kind = "feedback"
		} else if _, ok := item.Expr.(*ast.ComprehensionExpr); ok {
			kind = "map"
		}
		calls := nodeCalls(item)
		pure := pureExpression(item.Expr, g.Functions) && kind != "state" && kind != "retry" && kind != "feedback" && kind != "for" && item.LoopControl == ""
		document.Nodes = append(document.Nodes, node{item.Name, append([]string{}, item.Deps...), append([]string{}, item.Gates...), item.Type, kind, item.Output, pure, append([]string{}, calls...), location{item.Pos.Line, item.Pos.Column}})
	}
	return json.MarshalIndent(document, "", "  ")
}
