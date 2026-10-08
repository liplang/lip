package parser

import (
	"lipalpha/compiler/ast"
	"lipalpha/compiler/token"
)

// Cell is an interactive fragment: declarations, Flow statements and an
// optional final expression. Expressions still use the normal LIP grammar.
type Cell struct {
	Dependencies []ast.Dependency
	Functions    []*ast.Function
	Body         []ast.Stmt
	Result       ast.Expr
	ResultPos    token.Pos
}

func (p *Parser) ParseCell() (*Cell, error) {
	cell := &Cell{}
	for {
		p.skipSemicolons()
		if p.peek().Kind == token.EOF {
			break
		}
		if err := p.checkDirective(); err != nil {
			return nil, err
		}
		switch p.peek().Kind {
		case token.Import:
			dependency, err := p.parseDependency()
			if err != nil {
				return nil, err
			}
			cell.Dependencies = append(cell.Dependencies, dependency)
			p.skipSemicolons()
		case token.Fn:
			fn, err := p.parseFunction()
			if err != nil {
				return nil, err
			}
			cell.Functions = append(cell.Functions, fn)
			p.skipSemicolons()
		case token.Flow, token.Return:
			return nil, p.errorf(p.peek(), "REPL cells use bindings, fn declarations and expressions directly; no flow wrapper or return is needed")
		default:
			if p.peek().Kind == token.For || p.peek().Kind == token.Break || p.peek().Kind == token.Continue || p.peek().Kind == token.Ident && p.tokens[p.i+1].Kind == token.Assign {
				stmt, err := p.parseStatement()
				if err != nil {
					return nil, err
				}
				cell.Body = append(cell.Body, stmt)
				if err := p.statementEnd(token.EOF); err != nil {
					return nil, err
				}
				continue
			}
			pos := p.peek().Pos
			start := p.i
			expr, err := p.parseExpr(0)
			if err != nil && !IsIncomplete(err) && p.tokens[start].Kind == token.Match {
				p.i = start
				stmt, stmtErr := p.parseStatement()
				if stmtErr != nil {
					return nil, stmtErr
				}
				cell.Body = append(cell.Body, stmt)
				if err := p.statementEnd(token.EOF); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := p.statementEnd(token.EOF); err != nil {
				return nil, err
			}
			if p.peek().Kind == token.EOF {
				cell.Result, cell.ResultPos = expr, pos
			} else if _, ok := expr.(*ast.CallExpr); ok {
				cell.Body = append(cell.Body, &ast.ExprStmt{Expr: expr, Pos: pos})
			} else {
				return nil, p.errorf(p.peek(), "only the final expression in a REPL cell can be displayed; assign earlier values to names")
			}
		}
	}
	return cell, nil
}
