package parser

import (
	"strconv"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/token"
)

// The match keyword has already been consumed. Both forms share patterns and
// guards; Flow statement arms contain graph statements, expression arms values.
func (p *Parser) parseMatch(expression bool) (ast.Expr, []ast.MatchArm, error) {
	value, err := p.parseExpr(0)
	if err != nil {
		return nil, nil, err
	}
	if _, err := p.expect(token.LBrace); err != nil {
		return nil, nil, err
	}
	var arms []ast.MatchArm
	for !p.match(token.RBrace) {
		arm := ast.MatchArm{Pos: p.peek().Pos}
		var patternType string
		arm.Pattern, patternType, err = p.parseMatchPattern()
		if err != nil {
			return nil, nil, err
		}
		if arm.Pattern != nil || patternType != "" {
			if patternType != "" {
				arm.TypePatterns = []string{patternType}
			}
			arm.Patterns = []*ast.LiteralExpr{arm.Pattern}
			if arm.Pattern == nil {
				arm.Patterns = nil
			}
			for p.match(token.Comma) {
				pattern, typeName, patternErr := p.parseMatchPattern()
				if patternErr != nil {
					return nil, nil, patternErr
				}
				if pattern == nil && typeName == "" {
					return nil, nil, p.errorf(p.tokens[p.i-1], "wildcard _ must be a separate match arm")
				}
				if pattern != nil {
					arm.Patterns = append(arm.Patterns, pattern)
				}
				if typeName != "" {
					arm.TypePatterns = append(arm.TypePatterns, typeName)
				}
			}
		} else if p.peek().Kind == token.Comma {
			comma := p.next()
			return nil, nil, p.errorf(comma, "wildcard _ must be a separate match arm")
		}
		if p.match(token.If) {
			arm.Guard, err = p.parseExpr(0)
			if err != nil {
				return nil, nil, err
			}
		}
		if _, err := p.expect(token.FatArrow); err != nil {
			return nil, nil, err
		}
		block := p.peek().Kind == token.LBrace
		if expression {
			if block {
				arm.Expr, err = p.parseFunctionBody()
			} else {
				arm.Expr, err = p.parseExpr(0)
			}
		} else if p.match(token.LBrace) {
			arm.Body, err = p.parseStatements()
		} else {
			var stmt ast.Stmt
			stmt, err = p.parseStatement()
			arm.Body = []ast.Stmt{stmt}
		}
		if err != nil {
			return nil, nil, err
		}
		arms = append(arms, arm)
		if !p.match(token.Comma) && !block && p.peek().Kind != token.RBrace {
			return nil, nil, p.errorf(p.peek(), "expected ',' between match arms")
		}
	}
	if len(arms) == 0 {
		return nil, nil, p.errorf(p.tokens[p.i-1], "match needs at least one arm")
	}
	return value, arms, nil
}

func (p *Parser) parseMatchPattern() (*ast.LiteralExpr, string, error) {
	t := p.next()
	pattern := &ast.LiteralExpr{Raw: t.Text, Pos: t.Pos}
	switch t.Kind {
	case token.Ident:
		if t.Text == "_" {
			return nil, "", nil
		}
		switch t.Text {
		case "number", "string", "bool", "list", "object":
			return nil, t.Text, nil
		}
	case token.True:
		pattern.Value = true
		return pattern, "", nil
	case token.False:
		pattern.Value = false
		return pattern, "", nil
	case token.Null:
		return pattern, "", nil
	case token.String:
		pattern.Value = t.Text
		return pattern, "", nil
	case token.Number, token.Minus, token.Plus:
		text := t.Text
		if t.Kind != token.Number {
			number, err := p.expect(token.Number)
			if err != nil {
				return nil, "", err
			}
			text += number.Text
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, "", p.errorf(t, "invalid number pattern")
		}
		pattern.Value, pattern.Raw = number, text
		return pattern, "", nil
	}
	return nil, "", p.errorf(t, "match pattern must be a number, string, bool, null, type (number/string/bool/list/object) or _; use an if guard for other conditions")
}
