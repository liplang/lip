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
	p.skipSemicolons()
	if err := p.checkDirective(); err != nil {
		return nil, err
	}
	var dependencies []ast.Dependency
	for p.peek().Kind == token.Import {
		dependency, err := p.parseDependency()
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, dependency)
		p.skipSemicolons()
		if err := p.checkDirective(); err != nil {
			return nil, err
		}
	}
	var functions []*ast.Function
	for p.peek().Kind == token.Fn {
		fn, err := p.parseFunction()
		if err != nil {
			return nil, err
		}
		functions = append(functions, fn)
		p.skipSemicolons()
		if err := p.checkDirective(); err != nil {
			return nil, err
		}
	}
	var flow *ast.Flow
	var err error
	if p.peek().Kind == token.Flow {
		flow, err = p.parseFlow()
	} else {
		pos := p.peek().Pos
		var body []ast.Stmt
		body, err = p.parseStatementList(token.EOF)
		flow = &ast.Flow{Name: "main", ParamTypes: map[string]string{}, ReturnType: "void", Body: body, Pos: pos}
	}
	if err != nil {
		return nil, err
	}
	p.skipSemicolons()
	if err := p.checkDirective(); err != nil {
		return nil, err
	}
	if p.peek().Kind != token.EOF {
		return nil, p.errorf(p.peek(), "expected end of file; an explicit flow cannot be mixed with top-level statements or another flow")
	}
	return &ast.Program{Functions: functions, Flow: flow, Dependencies: dependencies}, nil
}

func (p *Parser) checkDirective() error {
	if p.peek().Kind == token.Ident && (p.peek().Text == "requires" || p.peek().Text == "require") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == token.Ident {
		kind := p.tokens[p.i+1].Text
		if kind != "python" && kind != "host" && kind != "go" {
			return nil
		}
		return p.errorf(p.peek(), "dependency directive %q has been replaced by %q; use import python, import host or import go", p.peek().Text, "import")
	}
	return nil
}

func (p *Parser) parseType(allowVoid bool) (string, error) {
	typ, err := p.expect(token.Ident)
	if err != nil {
		return "", err
	}
	if !validTypeName(typ.Text) && !(allowVoid && typ.Text == "void") {
		return "", p.errorf(typ, "unknown type %q; use any, number, bool, string, list or object (void is only for Flow output)", typ.Text)
	}
	name := typ.Text
	if p.match(token.Question) {
		if name == "void" {
			return "", p.errorf(typ, "void cannot be optional; omit the output declaration for a Flow without a result")
		}
		name += "?"
	}
	return name, nil
}

func (p *Parser) parseDependency() (ast.Dependency, error) {
	kw, err := p.expect(token.Import)
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
		return ast.Dependency{}, p.errorf(p.peek(), "expected string after import %s; put the dependency name in double quotes", kind.Text)
	}
	if strings.TrimSpace(spec.Text) == "" {
		return ast.Dependency{}, p.errorf(spec, "dependency spec cannot be empty")
	}
	dependency := ast.Dependency{Kind: kind.Text, Spec: spec.Text, Pos: kw.Pos}
	if p.match(token.As) {
		alias, err := p.expect(token.Ident)
		if err != nil {
			return ast.Dependency{}, p.errorf(p.peek(), "expected a module alias after as; choose one identifier, e.g. np")
		}
		dependency.Alias = alias.Text
	}
	return dependency, nil
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
	params, paramTypes, err := p.parseParameters()
	if err != nil {
		return nil, err
	}
	returnType := ""
	if p.match(token.Arrow) {
		returnType, err = p.parseType(false)
		if err != nil {
			return nil, err
		}
	}
	result, err := p.parseFunctionBody()
	if err != nil {
		return nil, err
	}
	return &ast.Function{Name: name.Text, Params: params, ParamTypes: paramTypes, ReturnType: returnType, Return: result, Pos: kw.Pos}, nil
}

func (p *Parser) parseFunctionBody() (ast.Expr, error) {
	if _, err := p.expect(token.LBrace); err != nil {
		return nil, err
	}
	p.skipSemicolons()
	p.match(token.Return)
	if p.peek().Kind == token.For {
		return nil, p.errorf(p.peek(), "statement for is only allowed in Flow, top-level statements or REPL cells; use a comprehension or fold in a pure fn")
	}
	if p.peek().Kind == token.Break || p.peek().Kind == token.Continue {
		return nil, p.errorf(p.peek(), "%s is only allowed inside a for body", p.peek().Text)
	}
	result, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	p.skipSemicolons()
	if _, err := p.expect(token.RBrace); err != nil {
		return nil, p.errorf(p.peek(), "fn body needs one expression, optionally preceded by return; compose pure functions for more complex calculations")
	}
	return result, nil
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
	if p.peek().Kind != token.LParen {
		return nil, p.errorf(p.peek(), "flow needs an explicit parameter list; use () when it has no inputs")
	}
	params, paramTypes, err := p.parseParameters()
	if err != nil {
		return nil, err
	}
	returnType := "void"
	if p.match(token.Arrow) {
		returnType, err = p.parseType(true)
		if err != nil {
			return nil, err
		}
	}
	if p.peek().Kind == token.Ident && (validTypeName(p.peek().Text) || p.peek().Text == "void") {
		return nil, p.errorf(p.peek(), "output type needs '->' before it; write -> %s, or omit the output declaration for no result", p.peek().Text)
	}
	if _, err := p.expect(token.LBrace); err != nil {
		return nil, err
	}
	body, err := p.parseStatements()
	if err != nil {
		return nil, err
	}
	return &ast.Flow{Name: name.Text, Params: params, ParamTypes: paramTypes, ReturnType: returnType, Body: body, Pos: kw.Pos}, nil
}

func (p *Parser) parseParameters() ([]string, map[string]string, error) {
	return p.parseParameterList(false)
}

func (p *Parser) parseParameterList(inline bool) ([]string, map[string]string, error) {
	if _, err := p.expect(token.LParen); err != nil {
		return nil, nil, err
	}
	params := []string{}
	paramTypes := map[string]string{}
	if p.match(token.RParen) {
		return params, paramTypes, nil
	}
	for {
		param, err := p.expect(token.Ident)
		if err != nil {
			return nil, nil, err
		}
		typ := "any"
		if p.match(token.Colon) {
			typ, err = p.parseType(false)
			if err != nil {
				return nil, nil, err
			}
		} else if !inline {
			return nil, nil, p.errorf(param, "parameter %q needs an explicit type; use : any for dynamic values", param.Text)
		}
		params = append(params, param.Text)
		paramTypes[param.Text] = typ
		if p.match(token.RParen) {
			return params, paramTypes, nil
		}
		if _, err := p.expect(token.Comma); err != nil {
			return nil, nil, err
		}
		if p.match(token.RParen) {
			return params, paramTypes, nil
		}
	}
}

func validTypeName(name string) bool {
	switch name {
	case "any", "string", "number", "bool", "list", "object":
		return true
	default:
		return false
	}
}

func (p *Parser) parseStatements() ([]ast.Stmt, error) {
	return p.parseStatementList(token.RBrace)
}

func (p *Parser) parseStatementList(end token.Kind) ([]ast.Stmt, error) {
	var out []ast.Stmt
	for {
		p.skipSemicolons()
		if p.peek().Kind == end || p.peek().Kind == token.EOF {
			break
		}
		if end == token.EOF {
			if err := p.checkDirective(); err != nil {
				return nil, err
			}
			if p.peek().Kind == token.Flow {
				return nil, p.errorf(p.peek(), "an explicit flow cannot be mixed with top-level statements; use one entry style per file")
			}
			if p.peek().Kind == token.Fn || p.peek().Kind == token.Import {
				return nil, p.errorf(p.peek(), "import and fn declarations must precede top-level statements")
			}
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
		if err := p.statementEnd(end); err != nil {
			return nil, err
		}
	}
	if end != token.EOF {
		if _, err := p.expect(end); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// A statement may end at a newline, an explicit semicolon, or the end of its
// block/file. Whitespace alone cannot separate statements on the same line.
func (p *Parser) statementEnd(end token.Kind) error {
	if p.match(token.Semicolon) {
		p.skipSemicolons()
		return nil
	}
	if p.peek().Kind == end || p.peek().Kind == token.EOF {
		return nil
	}
	if p.peek().Pos.Line > p.tokens[p.i-1].Pos.Line {
		return nil
	}
	return p.errorf(p.peek(), "statements on the same line must be separated by ';'; add ';' or start a new line")
}

func (p *Parser) parseStatement() (ast.Stmt, error) {
	switch p.peek().Kind {
	case token.Break, token.Continue:
		kw := p.next()
		return &ast.LoopControlStmt{Kind: kw.Text, Pos: kw.Pos}, nil
	case token.For:
		pos := p.next().Pos
		variable, err := p.expect(token.Ident)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.In); err != nil {
			return nil, err
		}
		source, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(token.LBrace); err != nil {
			return nil, err
		}
		body, err := p.parseStatements()
		if err != nil {
			return nil, err
		}
		return &ast.ForStmt{Variable: variable.Text, Source: source, Body: body, Pos: pos}, nil
	case token.Match:
		pos := p.next().Pos
		value, arms, err := p.parseMatch(false)
		if err != nil {
			return nil, err
		}
		return &ast.MatchStmt{Value: value, Arms: arms, Pos: pos}, nil
	case token.Return:
		pos := p.next().Pos
		if p.peek().Kind == token.RBrace || p.peek().Kind == token.EOF || p.peek().Kind == token.Semicolon {
			return &ast.ReturnStmt{Pos: pos}, nil
		}
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
		fallthrough
	default:
		start := p.peek()
		expr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, ok := expr.(*ast.CallExpr); !ok {
			return nil, p.errorf(start, "an expression alone does not display a value in a file; use print(expression) or name = expression, or enter it directly in lipc repl")
		}
		return &ast.ExprStmt{Expr: expr, Pos: tPos(expr)}, nil
	}
}

var precedence = map[token.Kind]int{
	token.Or: 1, token.And: 2, token.Equal: 3, token.NotEqual: 3,
	token.Greater: 4, token.GreaterEqual: 4, token.Less: 4, token.LessEqual: 4,
	token.Plus: 5, token.Minus: 5, token.Star: 6, token.Slash: 6, token.FloorDiv: 6, token.Modulo: 6, token.Log: 6,
	token.Power: 8,
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
		rightPrec := prec + 1
		if op.Kind == token.Power {
			rightPrec = prec
		}
		right, err := p.parseExpr(rightPrec)
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
	case token.Minus, token.Plus:
		operand, err := p.parseExpr(7)
		if err != nil {
			return nil, err
		}
		expr = &ast.BinaryExpr{Op: t.Text, Left: &ast.LiteralExpr{Value: float64(0), Raw: "0", Pos: t.Pos}, Right: operand, Pos: t.Pos}
	case token.Not:
		operand, err := p.parseExpr(7)
		if err != nil {
			return nil, err
		}
		expr = &ast.UnaryExpr{Op: t.Text, Operand: operand, Pos: t.Pos}
	case token.String:
		expr = &ast.LiteralExpr{Value: t.Text, Raw: t.Text, Pos: t.Pos}
	case token.True:
		expr = &ast.LiteralExpr{Value: true, Raw: t.Text, Pos: t.Pos}
	case token.False:
		expr = &ast.LiteralExpr{Value: false, Raw: t.Text, Pos: t.Pos}
	case token.Null:
		expr = &ast.LiteralExpr{Value: nil, Raw: t.Text, Pos: t.Pos}
	case token.Fn:
		params, types, err := p.parseParameterList(true)
		if err != nil {
			return nil, err
		}
		resultType := ""
		if p.match(token.Arrow) {
			resultType, err = p.parseType(false)
			if err != nil {
				return nil, err
			}
		}
		body, err := p.parseFunctionBody()
		if err != nil {
			return nil, err
		}
		expr = &ast.LambdaExpr{Params: params, ParamTypes: types, ReturnType: resultType, Return: body, Pos: t.Pos}
	case token.LBrace:
		fields := []ast.ObjectField{}
		seen := map[string]bool{}
		if !p.match(token.RBrace) {
			for {
				key := p.next()
				if key.Kind != token.Ident && key.Kind != token.String {
					return nil, p.errorf(key, "object key must be an identifier or string")
				}
				if seen[key.Text] {
					return nil, p.errorf(key, "duplicate object key %q", key.Text)
				}
				seen[key.Text] = true
				if _, err := p.expect(token.Colon); err != nil {
					return nil, err
				}
				value, err := p.parseExpr(0)
				if err != nil {
					return nil, err
				}
				fields = append(fields, ast.ObjectField{Name: key.Text, Value: value, Pos: key.Pos})
				if p.match(token.RBrace) {
					break
				}
				if _, err := p.expect(token.Comma); err != nil {
					return nil, err
				}
				if p.match(token.RBrace) {
					break
				}
			}
		}
		expr = &ast.ObjectExpr{Fields: fields, Pos: t.Pos}
	case token.If:
		cond, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.LBrace); err != nil {
			return nil, err
		}
		thenExpr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.RBrace); err != nil {
			return nil, err
		}
		if _, err = p.expect(token.Else); err != nil {
			return nil, err
		}
		if _, err = p.expect(token.LBrace); err != nil {
			return nil, err
		}
		elseExpr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err = p.expect(token.RBrace); err != nil {
			return nil, err
		}
		expr = &ast.IfExpr{Cond: cond, Then: thenExpr, Else: elseExpr, Pos: t.Pos}
	case token.Match:
		value, arms, err := p.parseMatch(true)
		if err != nil {
			return nil, err
		}
		expr = &ast.MatchExpr{Value: value, Arms: arms, Pos: t.Pos}
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
			if p.match(token.RBracket) {
				break
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
		if t.Kind == token.EOF {
			if p.i > 1 {
				return nil, p.errorf(t, "expected expression after %q, got end of input", p.tokens[p.i-2].Text)
			}
			return nil, p.errorf(t, "expected expression, got end of input")
		}
		return nil, p.errorf(t, "expected expression, got %s %q", t.Kind, t.Text)
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
					if p.match(token.RParen) {
						break
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
	case *ast.LambdaExpr:
		return e.Pos
	case *ast.BinaryExpr:
		return e.Pos
	case *ast.IfExpr:
		return e.Pos
	case *ast.MatchExpr:
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
func (p *Parser) skipSemicolons() {
	for p.match(token.Semicolon) {
	}
}
func (p *Parser) match(kind token.Kind) bool {
	if p.peek().Kind != kind {
		return false
	}
	p.i++
	return true
}
func (p *Parser) expect(kind token.Kind) (token.Token, error) {
	if p.peek().Kind != kind {
		if p.peek().Kind == token.EOF {
			return token.Token{}, p.errorf(p.peek(), "expected %s, got end of input", kind)
		}
		return token.Token{}, p.errorf(p.peek(), "expected %s, got %s %q", kind, p.peek().Kind, p.peek().Text)
	}
	return p.next(), nil
}
func (p *Parser) errorf(t token.Token, format string, args ...any) error {
	return &Error{Pos: t.Pos, Message: fmt.Sprintf(format, args...), Incomplete: t.Kind == token.EOF}
}
