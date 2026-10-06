package parser

import (
	"fmt"
	"strconv"
	"strings"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/token"
)

type Parser struct {
	tokens []token.Token
	i      int
}

func New(tokens []token.Token) *Parser { return &Parser{tokens: tokens} }

func (p *Parser) Parse() (*ast.Program, error) {
	if p.peek().Kind == token.Ident && p.peek().Text == "requires" {
		return nil, p.errorf(p.peek(), "unknown dependency directive %q; use singular %q", p.peek().Text, "require")
	}
	var dependencies []ast.Dependency
	for p.peek().Kind == token.Require {
		dependency, err := p.parseDependency()
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, dependency)
	}
	var functions []*ast.Function
	for p.peek().Kind == token.Fn {
		fn, err := p.parseFunction()
		if err != nil {
			return nil, err
		}
		functions = append(functions, fn)
	}
	flow, err := p.parseFlow()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != token.EOF {
		return nil, p.errorf(p.peek(), "expected end of file")
	}
	return &ast.Program{Functions: functions, Flow: flow, Dependencies: dependencies}, nil
}

func (p *Parser) parseDependency() (ast.Dependency, error) {
	kw, err := p.expect(token.Require)
	if err != nil {
		return ast.Dependency{}, err
	}
	kind, err := p.expect(token.Ident)
	if err != nil {
		return ast.Dependency{}, err
	}
	switch kind.Text {
	case "python", "go", "host":
	default:
		return ast.Dependency{}, p.errorf(kind, "unknown dependency kind %q (want python, go or host)", kind.Text)
	}
	spec, err := p.expect(token.String)
	if err != nil {
		return ast.Dependency{}, err
	}
	if strings.TrimSpace(spec.Text) == "" {
		return ast.Dependency{}, p.errorf(spec, "dependency spec cannot be empty")
	}
	return ast.Dependency{Kind: kind.Text, Spec: spec.Text, Pos: kw.Pos}, nil
}

func (p *Parser) parseFunction() (*ast.Function, error) {
	kw, err := p.expect(token.Fn)
	if err != nil {
		return nil, err
	}
	name, err := p.expect(token.Ident)
	if err != nil {
		return nil, err
	}
	if _, err = p.expect(token.LParen); err != nil {
		return nil, err
	}
	params := []string{}
	paramTypes := map[string]string{}
	if !p.match(token.RParen) {
		for {
			param, e := p.expect(token.Ident)
			if e != nil {
				return nil, e
			}
			params = append(params, param.Text)
			if p.match(token.Colon) {
				typ, e := p.expect(token.Ident)
				if e != nil {
					return nil, e
				}
				if !validTypeName(typ.Text) {
					return nil, p.errorf(typ, "unknown type %q", typ.Text)
				}
				paramTypes[param.Text] = typ.Text
			}
			if p.match(token.RParen) {
				break
			}
			if _, e := p.expect(token.Comma); e != nil {
				return nil, e
			}
		}
	}
	if _, err = p.expect(token.LBrace); err != nil {
		return nil, err
	}
	if _, err = p.expect(token.Return); err != nil {
		return nil, err
	}
	result, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if _, err = p.expect(token.RBrace); err != nil {
		return nil, err
	}
	return &ast.Function{Name: name.Text, Params: params, ParamTypes: paramTypes, Return: result, Pos: kw.Pos}, nil
}

func (p *Parser) parseFlow() (*ast.Flow, error) {
	kw, err := p.expect(token.Flow)
	if err != nil {
		return nil, err
	}
	name, err := p.expect(token.Ident)
	if err != nil {
		return nil, err
	}
	params := []string{}
	paramTypes := map[string]string{}
	if p.match(token.LParen) {
		if !p.match(token.RParen) {
			for {
				param, e := p.expect(token.Ident)
				if e != nil {
					return nil, e
				}
				params = append(params, param.Text)
				if p.match(token.Colon) {
					typ, e := p.expect(token.Ident)
					if e != nil {
						return nil, e
					}
					if !validTypeName(typ.Text) {
						return nil, p.errorf(typ, "unknown type %q", typ.Text)
					}
					paramTypes[param.Text] = typ.Text
				}
				if p.match(token.RParen) {
					break
				}
				if _, e := p.expect(token.Comma); e != nil {
					return nil, e
				}
			}
		}
	}
	if _, err := p.expect(token.LBrace); err != nil {
		return nil, err
	}
	body, err := p.parseStatements()
	if err != nil {
		return nil, err
	}
	return &ast.Flow{Name: name.Text, Params: params, ParamTypes: paramTypes, Body: body, Pos: kw.Pos}, nil
}

func validTypeName(name string) bool {
	switch name {
	case "any", "string", "number", "bool":
		return true
	default:
		return false
	}
}

func (p *Parser) parseStatements() ([]ast.Stmt, error) {
	var out []ast.Stmt
	for p.peek().Kind != token.RBrace && p.peek().Kind != token.EOF {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
	}
	if _, err := p.expect(token.RBrace); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Parser) parseStatement() (ast.Stmt, error) {
	switch p.peek().Kind {
	case token.When:
		pos := p.next().Pos
		cond, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.LBrace); err != nil {
			return nil, err
		}
		body, err := p.parseStatements()
		if err != nil {
			return nil, err
		}
		return &ast.WhenStmt{Cond: cond, Body: body, Pos: pos}, nil
	case token.Return:
		pos := p.next().Pos
		expr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		return &ast.ReturnStmt{Expr: expr, Pos: pos}, nil
	case token.Ident:
		if p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == token.Assign {
			name := p.next()
			if _, err := p.expect(token.Assign); err != nil {
				return nil, err
			}
			expr, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			return &ast.BindStmt{Name: name.Text, Expr: expr, Pos: name.Pos}, nil
		}
		expr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, ok := expr.(*ast.CallExpr); !ok {
			return nil, p.errorf(p.peek(), "only a call can be used as a standalone statement")
		}
		return &ast.ExprStmt{Expr: expr, Pos: tPos(expr)}, nil
	default:
		return nil, p.errorf(p.peek(), "expected binding, when or return")
	}
}

var precedence = map[token.Kind]int{
	token.Or: 1, token.And: 2, token.Equal: 3, token.NotEqual: 3,
	token.Greater: 4, token.GreaterEqual: 4, token.Less: 4, token.LessEqual: 4,
	token.Plus: 5, token.Minus: 5, token.Star: 6, token.Slash: 6,
}

func (p *Parser) parseExpr(minPrec int) (ast.Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		prec, ok := precedence[p.peek().Kind]
		if !ok || prec < minPrec {
			break
		}
		op := p.next()
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Op: op.Text, Left: left, Right: right, Pos: op.Pos}
	}
	return left, nil
}

func (p *Parser) parsePrimary() (ast.Expr, error) {
	t := p.next()
	var expr ast.Expr
	switch t.Kind {
	case token.Ident:
		expr = &ast.IdentExpr{Name: t.Text, Pos: t.Pos}
	case token.Number:
		v, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			return nil, p.errorf(t, "invalid number")
		}
		expr = &ast.LiteralExpr{Value: v, Raw: t.Text, Pos: t.Pos}
	case token.String:
		expr = &ast.LiteralExpr{Value: t.Text, Raw: t.Text, Pos: t.Pos}
	case token.True:
		expr = &ast.LiteralExpr{Value: true, Raw: t.Text, Pos: t.Pos}
	case token.False:
		expr = &ast.LiteralExpr{Value: false, Raw: t.Text, Pos: t.Pos}
	case token.If:
		cond, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.Then); err != nil {
			return nil, err
		}
		thenExpr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.Else); err != nil {
			return nil, err
		}
		elseExpr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		expr = &ast.IfExpr{Cond: cond, Then: thenExpr, Else: elseExpr, Pos: t.Pos}
	case token.LBracket:
		if p.match(token.RBracket) {
			expr = &ast.ListExpr{Items: []ast.Expr{}, Pos: t.Pos}
			break
		}
		item, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if p.match(token.For) {
			variable, err := p.expect(token.Ident)
			if err != nil {
				return nil, err
			}
			if _, err = p.expect(token.In); err != nil {
				return nil, err
			}
			source, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if _, err = p.expect(token.RBracket); err != nil {
				return nil, err
			}
			expr = &ast.ComprehensionExpr{Element: item, Variable: variable.Text, Source: source, Pos: t.Pos}
			break
		}
		items := []ast.Expr{item}
		for {
			if p.match(token.RBracket) {
				break
			}
			if _, err := p.expect(token.Comma); err != nil {
				return nil, err
			}
			item, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		expr = &ast.ListExpr{Items: items, Pos: t.Pos}
	case token.LParen:
		expr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.RParen); err != nil {
			return nil, err
		}
		return p.parsePostfix(expr)
	default:
		return nil, p.errorf(t, "expected expression")
	}
	return p.parsePostfix(expr)
}

func (p *Parser) parsePostfix(expr ast.Expr) (ast.Expr, error) {
	for {
		switch p.peek().Kind {
		case token.LParen:
			name, ok := callableName(expr)
			if !ok {
				return nil, p.errorf(p.peek(), "only a function name can be called")
			}
			p.next()
			args := []ast.Expr{}
			if !p.match(token.RParen) {
				for {
					arg, err := p.parseExpr(0)
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.match(token.RParen) {
						break
					}
					if _, err := p.expect(token.Comma); err != nil {
						return nil, err
					}
				}
			}
			expr = &ast.CallExpr{Name: name, Args: args, Pos: tPos(expr)}
		case token.Dot:
			p.next()
			field, err := p.expect(token.Ident)
			if err != nil {
				return nil, err
			}
			expr = &ast.FieldExpr{Object: expr, Name: field.Text, Pos: field.Pos}
		case token.LBracket:
			p.next()
			index, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			if _, err = p.expect(token.RBracket); err != nil {
				return nil, err
			}
			expr = &ast.IndexExpr{Object: expr, Index: index, Pos: tPos(expr)}
		default:
			return expr, nil
		}
	}
}

// callableName accepts both ordinary Host names and dotted Python operation
// names. Field expressions remain value expressions everywhere else; a field
// chain is treated as a callable only when it is immediately followed by '('.
func callableName(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.IdentExpr:
		return value.Name, true
	case *ast.FieldExpr:
		prefix, ok := callableName(value.Object)
		if !ok {
			return "", false
		}
		return prefix + "." + value.Name, true
	default:
		return "", false
	}
}

func tPos(expr ast.Expr) token.Pos {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		return e.Pos
	case *ast.LiteralExpr:
		return e.Pos
	case *ast.CallExpr:
		return e.Pos
	case *ast.BinaryExpr:
		return e.Pos
	case *ast.IfExpr:
		return e.Pos
	case *ast.ListExpr:
		return e.Pos
	case *ast.ComprehensionExpr:
		return e.Pos
	case *ast.FieldExpr:
		return e.Pos
	case *ast.IndexExpr:
		return e.Pos
	default:
		return token.Pos{}
	}
}

func (p *Parser) peek() token.Token { return p.tokens[p.i] }
func (p *Parser) next() token.Token { t := p.tokens[p.i]; p.i++; return t }
func (p *Parser) match(kind token.Kind) bool {
	if p.peek().Kind != kind {
		return false
	}
	p.i++
	return true
}
func (p *Parser) expect(kind token.Kind) (token.Token, error) {
	if p.peek().Kind != kind {
		return token.Token{}, p.errorf(p.peek(), "expected %s, got %s", kind, p.peek().Kind)
	}
	return p.next(), nil
}
func (p *Parser) errorf(t token.Token, format string, args ...any) error {
	return fmt.Errorf("%d:%d: %s", t.Pos.Line, t.Pos.Column, fmt.Sprintf(format, args...))
}
