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
		arm.Pattern, err = p.parseMatchPattern()
		if err != nil {
			return nil, nil, err
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

func (p *Parser) parseMatchPattern() (*ast.LiteralExpr, error) {
	t := p.next()
	pattern := &ast.LiteralExpr{Raw: t.Text, Pos: t.Pos}
	switch t.Kind {
	case token.Ident:
		if t.Text == "_" {
			return nil, nil
		}
	case token.True:
		pattern.Value = true
		return pattern, nil
	case token.False:
		pattern.Value = false
		return pattern, nil
	case token.Null:
		return pattern, nil
	case token.String:
		pattern.Value = t.Text
		return pattern, nil
	case token.Number, token.Minus, token.Plus:
		text := t.Text
		if t.Kind != token.Number {
			number, err := p.expect(token.Number)
			if err != nil {
				return nil, err
			}
			text += number.Text
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, p.errorf(t, "invalid number pattern")
		}
		pattern.Value, pattern.Raw = number, text
		return pattern, nil
	}
	return nil, p.errorf(t, "match pattern must be a number, string, bool, null or _; use an if guard for other conditions")
}
