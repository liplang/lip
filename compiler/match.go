package compiler

import (
	"fmt"
	"strings"

	"lipalpha/compiler/ast"
)

func matchParts(expr *ast.MatchExpr) []ast.Expr {
	parts := []ast.Expr{expr.Value}
	for _, arm := range expr.Arms {
		if arm.Guard != nil {
			parts = append(parts, arm.Guard)
		}
		parts = append(parts, arm.Expr)
	}
	return parts
}

// matchPatterns keeps ASTs built by older callers compatible with the newer
// comma-separated pattern representation.
func matchPatterns(arm ast.MatchArm) []*ast.LiteralExpr {
	if len(arm.Patterns) != 0 {
		return arm.Patterns
	}
	if arm.Pattern != nil {
		return []*ast.LiteralExpr{arm.Pattern}
	}
	return nil
}

func matchTypePatterns(arm ast.MatchArm) []string {
	return arm.TypePatterns
}

func matchHasAlternatives(arm ast.MatchArm) bool {
	return len(matchPatterns(arm)) != 0 || len(matchTypePatterns(arm)) != 0
}

func compatibleMatchType(valueType, patternType string) bool {
	if valueType == "any" || valueType == "never" || patternType == "any" {
		return true
	}
	return strings.TrimSuffix(valueType, "?") == patternType
}

func validateMatch(value ast.Expr, arms []ast.MatchArm, env, fnTypes map[string]string, fnParams map[string][]string) error {
	typ, err := inferExprType(value, env, fnTypes, fnParams)
	if err != nil {
		return err
	}
	seen := map[any]bool{}
	seenTypes := map[string]bool{}
	wildcard := false
	scopes := matchArmTypes(value, arms, env)
	for index, arm := range arms {
		patterns := matchPatterns(arm)
		typePatterns := matchTypePatterns(arm)
		if wildcard {
			return fmt.Errorf("%d:%d: unreachable match arm", arm.Pos.Line, arm.Pos.Column)
		}
		for _, pattern := range patterns {
			if seen[pattern.Value] || matchTypeCoversLiteral(seenTypes, pattern, env) {
				return fmt.Errorf("%d:%d: unreachable match arm", arm.Pos.Line, arm.Pos.Column)
			}
			patternType, _ := inferExprType(pattern, env, fnTypes, fnParams)
			if typ != "any" && typ != "never" && patternType != strings.TrimSuffix(typ, "?") && !(patternType == "null" && strings.HasSuffix(typ, "?")) {
				return fmt.Errorf("%d:%d: match pattern has type %s, value has type %s", arm.Pos.Line, arm.Pos.Column, patternType, typ)
			}
		}
		for _, patternType := range typePatterns {
			if seenTypes[patternType] || seenTypes["any"] {
				return fmt.Errorf("%d:%d: unreachable match arm", arm.Pos.Line, arm.Pos.Column)
			}
			if !compatibleMatchType(typ, patternType) {
				return fmt.Errorf("%d:%d: match type pattern %s does not match value type %s", arm.Pos.Line, arm.Pos.Column, patternType, typ)
			}
		}
		if arm.Guard != nil {
			guardType, err := inferExprType(arm.Guard, scopes[index], fnTypes, fnParams)
			if err != nil {
				return err
			}
			if !compatibleType("bool", guardType) {
				return fmt.Errorf("%d:%d: match guard must be bool, got %s", arm.Pos.Line, arm.Pos.Column, guardType)
			}
		} else if !matchHasAlternatives(arm) {
			wildcard = true
		} else {
			localSeen := map[any]bool{}
			localTypes := map[string]bool{}
			for _, patternType := range typePatterns {
				if localTypes[patternType] || localTypes["any"] {
					return fmt.Errorf("%d:%d: unreachable match arm", arm.Pos.Line, arm.Pos.Column)
				}
				localTypes[patternType] = true
				seenTypes[patternType] = true
			}
			for _, pattern := range patterns {
				if localSeen[pattern.Value] || matchTypeCoversLiteral(localTypes, pattern, env) {
					return fmt.Errorf("%d:%d: unreachable match arm", arm.Pos.Line, arm.Pos.Column)
				}
				localSeen[pattern.Value] = true
				seen[pattern.Value] = true
			}
		}
	}
	if wildcard || matchTypesExhaustive(typ, seenTypes, seen) || typ == "bool" && seen[true] && seen[false] || typ == "bool?" && seen[true] && seen[false] && seen[nil] || typ == "null" && seen[nil] || typ == "never" {
		return nil
	}
	return fmt.Errorf("non-exhaustive match; cover all values or add a final _ => arm (guards do not guarantee coverage)")
}

func matchTypeCoversLiteral(seenTypes map[string]bool, pattern *ast.LiteralExpr, env map[string]string) bool {
	patternType, _ := inferExprType(pattern, env, nil, nil)
	return seenTypes["any"] || seenTypes[patternType]
}

func matchTypesExhaustive(valueType string, seenTypes map[string]bool, seen map[any]bool) bool {
	if seenTypes["any"] {
		return true
	}
	if valueType == "any" || valueType == "never" {
		return false
	}
	base := strings.TrimSuffix(valueType, "?")
	if !seenTypes[base] {
		return false
	}
	return !strings.HasSuffix(valueType, "?") || seen[nil]
}

// A literal arm establishes the scrutinee's type. After an unguarded null
// arm, a wildcard arm of an optional identifier receives its non-null type.
func matchArmTypes(value ast.Expr, arms []ast.MatchArm, env map[string]string) []map[string]string {
	scopes := make([]map[string]string, len(arms))
	ident, isIdent := value.(*ast.IdentExpr)
	nullCovered := false
	for index, arm := range arms {
		scopes[index] = env
		if isIdent && (env[ident.Name] == "any" || strings.HasSuffix(env[ident.Name], "?")) {
			typ := env[ident.Name]
			patterns := matchPatterns(arm)
			typePatterns := matchTypePatterns(arm)
			if len(patterns) > 0 || len(typePatterns) > 0 {
				typ = matchPatternScopeType(patterns, typePatterns, env, typ)
			} else if nullCovered {
				typ = strings.TrimSuffix(typ, "?")
			}
			scopes[index] = cloneStringMap(env)
			scopes[index][ident.Name] = typ
		}
		if arm.Guard == nil {
			for _, pattern := range matchPatterns(arm) {
				if pattern.Value == nil {
					nullCovered = true
					break
				}
			}
		}
	}
	return scopes
}

// A multi-pattern arm narrows an optional/any value to the common literal
// type. If null is mixed with another type, the arm still admits both values.
func matchPatternScopeType(patterns []*ast.LiteralExpr, typePatterns []string, env map[string]string, original string) string {
	typeName := ""
	for _, pattern := range patterns {
		patternType, _ := inferExprType(pattern, env, nil, nil)
		if typeName == "" {
			typeName = patternType
			continue
		}
		if typeName != patternType {
			return original
		}
	}
	for _, patternType := range typePatterns {
		if typeName == "" {
			typeName = patternType
			continue
		}
		if typeName != patternType {
			return original
		}
	}
	return typeName
}

// Match itself accepts heterogeneous results. An enclosing fn or Flow validates
// each result against its declared boundary; an unconstrained match infers any.
func inferMatchType(expr *ast.MatchExpr, env, fnTypes map[string]string, fnParams map[string][]string) (string, error) {
	if err := validateMatch(expr.Value, expr.Arms, env, fnTypes, fnParams); err != nil {
		return "any", err
	}
	result := "never"
	scopes := matchArmTypes(expr.Value, expr.Arms, env)
	for index, arm := range expr.Arms {
		typ, err := inferExprType(arm.Expr, scopes[index], fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		result = matchResultType(result, typ)
	}
	return result, nil
}

func matchResultType(left, right string) string {
	if left == "never" {
		return right
	}
	if right == "never" || left == right {
		return left
	}
	if left == "any" || right == "any" {
		return "any"
	}
	if left == "null" {
		return strings.TrimSuffix(right, "?") + "?"
	}
	if right == "null" {
		return strings.TrimSuffix(left, "?") + "?"
	}
	if strings.TrimSuffix(left, "?") == strings.TrimSuffix(right, "?") {
		return strings.TrimSuffix(left, "?") + "?"
	}
	return "any"
}

func (b *builder) matchStmt(stmt *ast.MatchStmt, gates []string) error {
	if err := validateMatch(stmt.Value, stmt.Arms, b.types, b.fnTypes, b.fnParams); err != nil {
		return b.wrapError(stmt.Pos, err)
	}
	selector := &ast.MatchExpr{Value: stmt.Value, Pos: stmt.Pos}
	for index, arm := range stmt.Arms {
		selector.Arms = append(selector.Arms, ast.MatchArm{Pattern: arm.Pattern, Patterns: arm.Patterns, TypePatterns: arm.TypePatterns, Guard: arm.Guard, Expr: &ast.LiteralExpr{Value: float64(index), Pos: arm.Pos}, Pos: arm.Pos})
	}
	if err := b.validateCalls(selector, false); err != nil {
		return b.wrapError(stmt.Pos, err)
	}
	if err := validateSimpleExpr(selector); err != nil {
		return b.wrapError(stmt.Pos, err)
	}
	if err := b.checkRefs(refsOf(selector), stmt.Pos); err != nil {
		return err
	}
	name := fmt.Sprintf("__match_%d", b.matchID)
	b.matchID++
	b.graph.Nodes = append(b.graph.Nodes, Node{Name: name, Deps: b.graphRefs(refsOf(selector)), Gates: unique(gates), Expr: b.graphExpr(selector), Type: "number", Pos: stmt.Pos})
	outerKnown, outerTypes, outerNames, outerGraphNames := b.known, b.types, b.allNames, b.graphNames
	armNames := map[string]bool{}
	scopes := matchArmTypes(stmt.Value, stmt.Arms, outerTypes)
	for index, arm := range stmt.Arms {
		gate := fmt.Sprintf("__gate_%d", b.gateID)
		b.gateID++
		condition := &ast.BinaryExpr{Op: "==", Left: &ast.IdentExpr{Name: name, Pos: stmt.Pos}, Right: &ast.LiteralExpr{Value: float64(index), Pos: arm.Pos}, Pos: arm.Pos}
		b.graph.Nodes = append(b.graph.Nodes, Node{Name: gate, Deps: []string{name}, Gates: unique(gates), Expr: condition, Type: "bool", Pos: arm.Pos})
		b.known, b.types, b.allNames, b.graphNames = cloneBoolMap(outerKnown), cloneStringMap(scopes[index]), cloneBoolMap(outerNames), cloneStringMap(outerGraphNames)
		if err := b.stmts(arm.Body, append(append([]string{}, gates...), gate)); err != nil {
			return err
		}
		for local := range b.allNames {
			armNames[local] = true
		}
	}
	b.known, b.types, b.allNames, b.graphNames = outerKnown, outerTypes, outerNames, outerGraphNames
	for local := range armNames {
		b.allNames[local] = true
	}
	return nil
}

// Returns from mutually exclusive arms form one logical Flow result. Partial
// results need an optional output, independently of the number of graph nodes.
func matchReturns(body []ast.Stmt) (count int, complete bool) {
	for _, stmt := range body {
		switch value := stmt.(type) {
		case *ast.ReturnStmt:
			count++
			complete = true
		case *ast.MatchStmt:
			hasReturn, allReturn := false, true
			for _, arm := range value.Arms {
				armCount, armComplete := matchReturns(arm.Body)
				if armCount > 1 {
					return count + armCount, false
				}
				hasReturn = hasReturn || armCount > 0
				allReturn = allReturn && armComplete
			}
			if hasReturn {
				count++
				complete = allReturn
			}
		}
	}
	return count, complete
}

func (b *builder) bindingName(name string) string {
	for _, node := range b.graph.Nodes {
		if node.Name == name {
			return fmt.Sprintf("__lip_match_%d_%s", len(b.graph.Nodes), name)
		}
	}
	return name
}

func (b *builder) graphRefs(refs []string) []string {
	result := make([]string, len(refs))
	for index, name := range refs {
		result[index] = name
		if resolved := b.graphNames[name]; resolved != "" {
			result[index] = resolved
		}
	}
	return unique(result)
}

func (b *builder) graphExpr(expr ast.Expr) ast.Expr { return renameExpr(expr, b.graphNames) }

// Copy expressions while resolving arm-local bindings to unique graph names.
// Callback parameters and comprehension variables shadow graph references.
func renameExpr(expr ast.Expr, names map[string]string) ast.Expr {
	recur := func(expr ast.Expr) ast.Expr { return renameExpr(expr, names) }
	switch value := expr.(type) {
	case *ast.IdentExpr:
		copy := *value
		if name := names[copy.Name]; name != "" {
			copy.Name = name
		}
		return &copy
	case *ast.CallExpr:
		copy := *value
		copy.Args = append([]ast.Expr{}, value.Args...)
		for i, arg := range copy.Args {
			if i == callbackIndex(value) {
				if _, named := arg.(*ast.IdentExpr); named {
					continue
				}
			}
			if value.Name == "feedback" && !externalCall(value) && (i == 1 || i == 2) {
				continue
			}
			copy.Args[i] = recur(arg)
		}
		return &copy
	case *ast.BinaryExpr:
		copy := *value
		copy.Left, copy.Right = recur(value.Left), recur(value.Right)
		return &copy
	case *ast.UnaryExpr:
		copy := *value
		copy.Operand = recur(value.Operand)
		return &copy
	case *ast.IfExpr:
		copy := *value
		copy.Cond, copy.Then, copy.Else = recur(value.Cond), recur(value.Then), recur(value.Else)
		return &copy
	case *ast.MatchExpr:
		copy := *value
		copy.Value = recur(value.Value)
		copy.Arms = append([]ast.MatchArm{}, value.Arms...)
		for i := range copy.Arms {
			copy.Arms[i].Guard, copy.Arms[i].Expr = recur(copy.Arms[i].Guard), recur(copy.Arms[i].Expr)
		}
		return &copy
	case *ast.ListExpr:
		copy := *value
		copy.Items = append([]ast.Expr{}, value.Items...)
		for i := range copy.Items {
			copy.Items[i] = recur(copy.Items[i])
		}
		return &copy
	case *ast.ObjectExpr:
		copy := *value
		copy.Fields = append([]ast.ObjectField{}, value.Fields...)
		for i := range copy.Fields {
			copy.Fields[i].Value = recur(copy.Fields[i].Value)
		}
		return &copy
	case *ast.FieldExpr:
		copy := *value
		copy.Object = recur(value.Object)
		return &copy
	case *ast.IndexExpr:
		copy := *value
		copy.Object, copy.Index = recur(value.Object), recur(value.Index)
		return &copy
	case *ast.LambdaExpr:
		copy := *value
		locals := cloneStringMap(names)
		for _, param := range value.Params {
			delete(locals, param)
		}
		copy.Return = renameExpr(value.Return, locals)
		return &copy
	case *ast.ComprehensionExpr:
		copy := *value
		copy.Source = recur(value.Source)
		locals := cloneStringMap(names)
		delete(locals, value.Variable)
		copy.Element = renameExpr(value.Element, locals)
		return &copy
	}
	return expr
}
