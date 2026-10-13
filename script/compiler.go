package script

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type token struct {
	kind, text         string
	site, line, column int
	value              Value
}
type lexer struct {
	src             string
	i, line, column int
	tokens          []token
}

func lex(src string) ([]token, error) {
	if !utf8.ValidString(src) || strings.HasPrefix(src, "\ufeff") {
		return nil, diagnostic("E-LEX", "source must be UTF-8 without BOM")
	}
	l := lexer{src: src, line: 1, column: 1}
	for l.i < len(src) {
		c := src[l.i]
		if c == ' ' || c == '\t' {
			l.advance()
			continue
		}
		if c == '#' {
			for l.i < len(src) && src[l.i] != '\n' && src[l.i] != '\r' {
				l.advance()
			}
			continue
		}
		t := token{site: len(l.tokens), line: l.line, column: l.column}
		if c == '\n' || c == '\r' {
			if c == '\r' {
				if l.i+1 >= len(src) || src[l.i+1] != '\n' {
					return nil, at(diagnostic("E-LEX", "bare carriage return"), t)
				}
				l.i++
			}
			l.advance()
			t.kind, t.text = "\n", "\n"
			l.tokens = append(l.tokens, t)
			continue
		}
		if c == '\\' {
			l.advance()
			if l.i < len(src) && src[l.i] == '\r' && l.i+1 < len(src) && src[l.i+1] == '\n' {
				l.i++
			}
			if l.i >= len(src) || src[l.i] != '\n' {
				return nil, at(diagnostic("E-LEX", "continuation must end the physical line"), t)
			}
			l.advance()
			continue
		}
		start := l.i
		if asciiLetter(c) || c == '_' {
			for l.i < len(src) && (asciiLetter(src[l.i]) || asciiDigit(src[l.i]) || src[l.i] == '_') {
				l.advance()
			}
			t.text, t.kind = src[start:l.i], "id"
			if strings.Contains(" import as flow fn return if else match for in parallel break continue with next effect perform true false null void ", " "+t.text+" ") {
				t.kind = t.text
			}
		} else if asciiDigit(c) {
			for l.i < len(src) && asciiDigit(src[l.i]) {
				l.advance()
			}
			if l.i-start > 1 && c == '0' {
				return nil, at(diagnostic("E-LEX", "leading zero in number"), t)
			}
			if l.i < len(src) && src[l.i] == '.' {
				l.advance()
				begin := l.i
				for l.i < len(src) && asciiDigit(src[l.i]) {
					l.advance()
				}
				if l.i == begin {
					return nil, at(diagnostic("E-LEX", "fraction requires digits"), t)
				}
			}
			if l.i < len(src) && (src[l.i] == 'e' || src[l.i] == 'E') {
				l.advance()
				if l.i < len(src) && (src[l.i] == '+' || src[l.i] == '-') {
					l.advance()
				}
				begin := l.i
				for l.i < len(src) && asciiDigit(src[l.i]) {
					l.advance()
				}
				if l.i == begin {
					return nil, at(diagnostic("E-LEX", "exponent requires digits"), t)
				}
			}
			if l.i < len(src) && (asciiLetter(src[l.i]) || src[l.i] == '_') {
				return nil, at(diagnostic("E-LEX", "invalid number suffix"), t)
			}
			t.kind, t.text = "number", src[start:l.i]
			n, e := strconv.ParseFloat(t.text, 64)
			if e != nil {
				return nil, at(diagnostic("E-LEX", "number overflow"), t)
			}
			t.value, e = NumberValue(n)
			if e != nil {
				return nil, at(diagnostic("E-LEX", "non-finite number"), t)
			}
		} else if c == '"' {
			l.advance()
			var b strings.Builder
			closed := false
			for l.i < len(src) {
				c = src[l.i]
				if c == '"' {
					l.advance()
					closed = true
					break
				}
				if c < 32 {
					return nil, at(diagnostic("E-LEX", "bare control character in string"), t)
				}
				if c != '\\' {
					r, _ := utf8.DecodeRuneInString(src[l.i:])
					b.WriteRune(r)
					l.advance()
					continue
				}
				l.advance()
				if l.i >= len(src) {
					break
				}
				c = src[l.i]
				l.advance()
				switch c {
				case '"', '\\':
					b.WriteByte(c)
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case 'u':
					if l.i >= len(src) || src[l.i] != '{' {
						return nil, at(diagnostic("E-LEX", "Unicode escape requires braces"), t)
					}
					l.advance()
					begin := l.i
					for l.i < len(src) && isHex(src[l.i]) {
						l.advance()
					}
					if l.i-begin < 1 || l.i-begin > 6 || l.i >= len(src) || src[l.i] != '}' {
						return nil, at(diagnostic("E-LEX", "invalid Unicode escape"), t)
					}
					n, _ := strconv.ParseUint(src[begin:l.i], 16, 32)
					l.advance()
					if n > utf8.MaxRune || n >= 0xd800 && n <= 0xdfff {
						return nil, at(diagnostic("E-LEX", "escape is not a Unicode scalar"), t)
					}
					b.WriteRune(rune(n))
				default:
					return nil, at(diagnostic("E-LEX", "unknown string escape"), t)
				}
			}
			if !closed {
				return nil, at(diagnostic("E-LEX", "unterminated string"), t)
			}
			t.kind, t.text, t.value = "string", src[start:l.i], textValue(b.String())
		} else {
			matched := false
			for _, op := range []string{"<+", "->", "=>", "==", "!=", "<=", ">=", "&&", "||", "//", "**", "*/"} {
				if strings.HasPrefix(src[l.i:], op) {
					t.kind, t.text = op, op
					l.advance()
					l.advance()
					matched = true
					break
				}
			}
			if !matched {
				if !strings.ContainsRune("()[]{}.,:;?=+-*/%!<>", rune(c)) {
					return nil, at(diagnostic("E-LEX", "unexpected character"), t)
				}
				t.kind, t.text = string(c), string(c)
				l.advance()
			}
		}
		l.tokens = append(l.tokens, t)
	}
	l.tokens = append(l.tokens, token{kind: "eof", site: -1, line: l.line, column: l.column})
	return l.tokens, nil
}
func (l *lexer) advance() {
	r, n := utf8.DecodeRuneInString(l.src[l.i:])
	l.i += n
	if r == '\n' {
		l.line++
		l.column = 1
	} else {
		l.column++
	}
}
func asciiDigit(c byte) bool  { return c >= '0' && c <= '9' }
func asciiLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isHex(c byte) bool       { return asciiDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

type parameter struct {
	name, typ string
	tok       token
}
type argument struct {
	name string
	expr *expression
}
type field struct {
	name string
	expr *expression
	tok  token
}
type pattern struct {
	typ      string
	value    Value
	wildcard bool
	tok      token
}
type arm struct {
	patterns      []pattern
	guard, result *expression
	body          *block
	tok           token
}
type expression struct {
	native                func(*evaluator, *environment, bool) (evalResult, error)
	tag, name, typ, state string
	tok                   token
	value                 Value
	x, y, z               *expression
	args                  []argument
	elems                 []*expression
	fields                []field
	params                []parameter
	body                  *block
	arms                  []arm
	info                  analysis
}
type statement struct {
	native          func(*evaluator, *environment) (string, error)
	tag, name       string
	tok             token
	expr            *expression
	body, otherwise *block
	other           *statement
	arms            []arm
	parallel        bool
}
type block struct {
	items []*statement
	tail  *expression
	exit  *statement
	tok   token
}
type importDecl struct {
	language, path, alias string
	tok                   token
}
type syntax struct {
	imports          []importDecl
	functions        map[string]*expression
	flowName, result string
	params           []parameter
	body             *block
	source           string
}
type analysis struct {
	typ        string
	ops        map[string]bool
	unknown    bool
	effect     EffectClass
	concurrent bool
}

type function struct {
	params   []parameter
	typ      string
	body     *block
	captures *environment
	builtin  string
}
type proposal struct {
	contract   Contract
	positional []Value
	named      map[string]Value
}

type parser struct {
	ts      []token
	i, soft int
	err     error
}

func parse(source string) (*syntax, error) {
	ts, err := lex(source)
	if err != nil {
		return nil, err
	}
	p := &parser{ts: ts}
	s := &syntax{source: source, functions: map[string]*expression{}, flowName: "main", result: "void"}
	p.padding()
	for p.peek().kind == "import" {
		s.imports = append(s.imports, p.importDeclaration())
		p.separator()
	}
	for p.peek().kind == "fn" {
		t := p.take()
		name := p.need("id").text
		e := p.function(t)
		e.name = name
		if _, exists := s.functions[name]; exists {
			p.err = at(diagnostic("E-DUPLICATE", "duplicate function "+name), t)
		}
		s.functions[name] = e
		p.padding()
	}
	if p.accept("flow") {
		s.flowName = p.need("id").text
		s.params = p.params()
		p.need("->")
		s.result = p.typeName(true)
		s.body = p.block("stmt")
		p.padding()
		p.need("eof")
	} else {
		s.body = &block{}
		for p.peek().kind != "eof" && p.err == nil {
			s.body.items = append(s.body.items, p.stmt())
			if p.peek().kind != "eof" {
				p.separator()
			}
		}
		p.need("eof")
	}
	if p.err != nil {
		return nil, p.err
	}
	return s, nil
}
func (p *parser) importDeclaration() importDecl {
	t := p.need("import")
	lang := p.need("id")
	if lang.text != "go" && lang.text != "python" && lang.text != "host" {
		p.fail(lang, "invalid import language")
	}
	path := p.need("string").value.s
	alias := ""
	if p.accept("as") {
		alias = p.need("id").text
	} else {
		index := strings.LastIndexAny(path, "/.")
		alias = path[index+1:]
		if !validID(alias) {
			p.fail(t, "import requires an explicit alias")
		}
	}
	return importDecl{lang.text, path, alias, t}
}

func validID(s string) bool {
	if s == "" || !asciiLetter(s[0]) && s[0] != '_' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !asciiLetter(s[i]) && !asciiDigit(s[i]) && s[i] != '_' {
			return false
		}
	}
	return true
}
func (p *parser) peek() token {
	if p.soft > 0 {
		for p.i < len(p.ts)-1 && p.ts[p.i].kind == "\n" {
			p.i++
		}
	}
	return p.ts[p.i]
}
func (p *parser) take() token {
	t := p.peek()
	if p.i < len(p.ts)-1 {
		p.i++
	}
	return t
}
func (p *parser) accept(k string) bool {
	if p.peek().kind == k {
		p.take()
		return true
	}
	return false
}
func (p *parser) need(k string) token {
	t := p.peek()
	if t.kind != k {
		p.fail(t, "expected "+k+", got "+t.kind)
		return t
	}
	return p.take()
}
func (p *parser) fail(t token, s string) {
	if p.err == nil {
		p.err = at(diagnostic("E-PARSE", s), t)
	}
}
func (p *parser) padding() {
	for p.peek().kind == "\n" || p.peek().kind == ";" {
		p.take()
	}
}
func (p *parser) newline() {
	for p.peek().kind == "\n" {
		p.take()
	}
}
func (p *parser) separator() {
	if p.peek().kind != "\n" && p.peek().kind != ";" {
		p.fail(p.peek(), "expected statement separator")
	}
	p.padding()
}
func (p *parser) typeName(void bool) string {
	t := p.take()
	name := t.text
	if t.kind == "null" {
		return "null"
	}
	if !strings.Contains(" any bool number string list object function effect ", " "+name+" ") && !(void && name == "void") {
		p.fail(t, "unknown type "+name)
	}
	if p.accept("?") {
		if name == "void" {
			p.fail(t, "void cannot be nullable")
		}
		name += "?"
	}
	return normalizeType(name)
}
func (p *parser) params() []parameter {
	p.need("(")
	p.soft++
	defer func() { p.soft-- }()
	var ps []parameter
	for p.peek().kind != ")" && p.err == nil {
		t := p.need("id")
		p.need(":")
		ps = append(ps, parameter{t.text, p.typeName(false), t})
		if !p.accept(",") {
			break
		}
	}
	p.need(")")
	return ps
}
func (p *parser) function(t token) *expression {
	ps := p.params()
	p.need("->")
	typ := p.typeName(false)
	return &expression{tag: "fn", tok: t, params: ps, typ: typ, body: p.block("fn")}
}

// Inspect only delimiter nesting; this makes tail classification independent of
// the statement/expression interpretation of an if or match branch.
func (p *parser) lastItem() bool {
	depth := 0
	for j := p.i; j < len(p.ts); j++ {
		k := p.ts[j].kind
		if depth == 0 && k == "}" {
			return true
		}
		if depth == 0 && (k == ";" || k == "\n") {
			n := j
			for n < len(p.ts) && (p.ts[n].kind == ";" || p.ts[n].kind == "\n") {
				n++
			}
			if n < len(p.ts) && p.ts[n].kind == "else" && k == "\n" {
				j = n - 1
				continue
			}
			return n < len(p.ts) && p.ts[n].kind == "}"
		}
		switch k {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		}
	}
	return false
}
func (p *parser) block(mode string) *block {
	t := p.need("{")
	old := p.soft
	p.soft = 0
	defer func() { p.soft = old }()
	b := &block{tok: t}
	p.padding()
	for p.peek().kind != "}" && p.peek().kind != "eof" && p.err == nil {
		if mode != "stmt" && p.lastItem() {
			if mode == "carry" {
				b.exit = p.carryExit()
			} else {
				if mode == "fn" {
					p.accept("return")
				}
				b.tail = p.expr(0)
			}
			p.padding()
			break
		}
		b.items = append(b.items, p.stmt())
		if p.peek().kind != "}" {
			p.separator()
		}
	}
	if mode != "stmt" && b.tail == nil && b.exit == nil {
		p.fail(p.peek(), "value block requires a tail")
	}
	p.need("}")
	return b
}
func (p *parser) stmt() *statement {
	t := p.peek()
	s := &statement{tok: t}
	switch t.kind {
	case "if":
		p.take()
		s.tag = "if"
		s.expr = p.expr(0)
		s.body = p.block("stmt")
		p.elseStatement(s)
	case "for":
		p.take()
		s.tag = "for"
		if p.peek().kind != "{" {
			s.name = p.need("id").text
			p.need("in")
			s.expr = p.expr(0)
			s.parallel = p.accept("parallel")
		}
		s.body = p.block("stmt")
	case "match":
		p.take()
		s.tag = "match"
		s.expr = p.expr(0)
		s.arms = p.matchArms("stmt")
	case "return", "break", "continue":
		p.take()
		s.tag = t.kind
		if t.kind == "return" && p.peek().kind != "}" && p.peek().kind != "\n" && p.peek().kind != ";" && p.peek().kind != "eof" {
			s.expr = p.expr(0)
		}
	case "id":
		if p.i+1 < len(p.ts) && (p.ts[p.i+1].kind == "=" || p.ts[p.i+1].kind == "<+") {
			p.take()
			op := p.take()
			s.name = t.text
			s.tag = "bind"
			if op.kind == "<+" {
				s.tag = "join"
			}
			if s.tag == "bind" && p.peek().kind == "id" && p.peek().text == "claim" {
				p.take()
				p.need("(")
				p.need(")")
				s.tag = "claim"
			} else {
				s.expr = p.expr(0)
			}
			return s
		}
		fallthrough
	default:
		s.tag = "eval"
		s.expr = p.expr(0)
		if s.expr != nil && s.expr.tag != "call" && s.expr.tag != "perform" {
			p.fail(t, "only calls and perform may be statements")
		}
	}
	return s
}
func (p *parser) elseStatement(s *statement) {
	index := p.i
	p.newline()
	if p.accept("else") {
		if p.peek().kind == "if" {
			s.other = p.stmt()
		} else {
			s.otherwise = p.block("stmt")
		}
	} else {
		p.i = index
	}
}
func (p *parser) carryExit() *statement {
	t := p.take()
	s := &statement{tok: t, tag: t.kind}
	switch t.kind {
	case "next", "break":
		s.expr = p.expr(0)
	case "if":
		s.expr = p.expr(0)
		s.body = p.block("carry")
		p.newline()
		p.need("else")
		if p.peek().kind == "if" {
			s.other = p.carryExit()
		} else {
			s.otherwise = p.block("carry")
		}
	case "match":
		s.expr = p.expr(0)
		s.arms = p.matchArms("carry")
	default:
		p.fail(t, "expected carry exit")
	}
	return s
}
func (p *parser) matchArms(mode string) []arm {
	p.need("{")
	old := p.soft
	p.soft = 0
	defer func() { p.soft = old }()
	p.padding()
	var arms []arm
	for p.peek().kind != "}" && p.err == nil {
		a := arm{tok: p.peek()}
		for {
			t := p.take()
			pat := pattern{tok: t}
			switch t.kind {
			case "number", "string":
				pat.value = t.value
			case "true", "false":
				pat.value = BoolValue(t.kind == "true")
			case "null":
				pat.value = Value{}
			case "+", "-":
				n := p.need("number").value.n
				if t.kind == "-" {
					n = -n
				}
				pat.value = number(n)
			case "id", "effect":
				if t.text == "_" {
					pat.wildcard = true
				} else if strings.Contains(" bool number string list object function effect ", " "+t.text+" ") {
					pat.typ = t.text
				} else {
					p.fail(t, "invalid pattern")
				}
			default:
				p.fail(t, "invalid pattern")
			}
			a.patterns = append(a.patterns, pat)
			if !p.accept(",") {
				break
			}
		}
		if p.accept("if") {
			a.guard = p.expr(0)
		}
		p.need("=>")
		if mode == "expr" {
			a.result = p.expr(0)
		} else if p.peek().kind == "{" {
			a.body = p.block(mode)
		} else if mode == "stmt" {
			a.body = &block{items: []*statement{p.stmt()}}
		} else {
			p.fail(p.peek(), "carry arm requires a block")
		}
		arms = append(arms, a)
		if p.accept(",") {
			p.padding()
		} else if p.peek().kind != "}" {
			p.separator()
		}
	}
	p.need("}")
	return arms
}
func precedence(k string) int {
	switch k {
	case "||":
		return 1
	case "&&":
		return 2
	case "==", "!=":
		return 3
	case "<", "<=", ">", ">=":
		return 4
	case "+", "-":
		return 5
	case "*", "/", "//", "%", "*/":
		return 6
	}
	return -1
}
func (p *parser) expr(min int) *expression {
	e := p.unary()
	for p.err == nil {
		t := p.peek()
		prec := precedence(t.kind)
		if prec < min {
			break
		}
		p.take()
		right := p.expr(prec + 1)
		e = &expression{tag: "binary", name: t.kind, tok: t, x: e, y: right}
		if prec == 3 || prec == 4 {
			if precedence(p.peek().kind) == prec {
				p.fail(p.peek(), "comparison operators are non-associative")
			}
		}
	}
	return e
}
func (p *parser) unary() *expression {
	t := p.peek()
	if t.kind == "!" || t.kind == "+" || t.kind == "-" {
		p.take()
		return &expression{tag: "unary", name: t.kind, tok: t, x: p.unary()}
	}
	e := p.powerBase()
	if p.accept("**") {
		t = p.ts[p.i-1]
		// The right unary admits signed exponents and recursively parses power.
		e = &expression{tag: "binary", name: "**", tok: t, x: e, y: p.unary()}
	}
	return e
}
func (p *parser) powerBase() *expression {
	t := p.peek()
	if t.kind == "effect" {
		return p.proposal()
	}
	if t.kind == "perform" {
		p.take()
		var x *expression
		if p.peek().kind == "effect" {
			x = p.proposal()
		} else {
			x = p.postfix()
		}
		return &expression{tag: "perform", tok: t, x: x}
	}
	return p.postfix()
}
func (p *parser) proposal() *expression {
	t := p.need("effect")
	name := p.need("id").text
	for p.accept(".") {
		name += "." + p.need("id").text
	}
	args := p.arguments()
	return &expression{tag: "proposal", tok: t, name: name, args: args}
}
func (p *parser) arguments() []argument {
	p.need("(")
	p.soft++
	defer func() { p.soft-- }()
	var as []argument
	named := false
	names := map[string]bool{}
	for p.peek().kind != ")" && p.err == nil {
		a := argument{}
		if p.peek().kind == "id" && p.i+1 < len(p.ts) && p.ts[p.i+1].kind == "=" {
			t := p.take()
			p.take()
			a.name = t.text
			named = true
			if names[a.name] {
				p.err = at(diagnostic("E-ARITY", "duplicate named argument"), t)
			}
			names[a.name] = true
		} else if named {
			p.fail(p.peek(), "positional argument after named argument")
		}
		a.expr = p.expr(0)
		as = append(as, a)
		if !p.accept(",") {
			break
		}
	}
	p.need(")")
	return as
}
func (p *parser) postfix() *expression {
	e := p.primary()
	for p.err == nil {
		t := p.peek()
		switch t.kind {
		case "(":
			e = &expression{tag: "call", tok: t, x: e, args: p.arguments()}
		case ".":
			p.take()
			e = &expression{tag: "field", tok: t, x: e, name: p.need("id").text}
		case "[":
			p.take()
			p.soft++
			index := p.expr(0)
			p.need("]")
			p.soft--
			e = &expression{tag: "index", tok: t, x: e, y: index}
		default:
			return e
		}
	}
	return e
}
func (p *parser) primary() *expression {
	t := p.take()
	e := &expression{tok: t}
	switch t.kind {
	case "number", "string":
		e.tag = "lit"
		e.value = t.value
	case "true", "false":
		e.tag = "lit"
		e.value = BoolValue(t.kind == "true")
	case "null":
		e.tag = "lit"
	case "id":
		e.tag = "id"
		e.name = t.text
	case "(":
		p.soft++
		e = p.expr(0)
		p.need(")")
		p.soft--
	case "fn":
		e = p.function(t)
	case "if":
		e.tag = "if"
		e.x = p.expr(0)
		e.body = p.block("value")
		p.newline()
		p.need("else")
		if p.peek().kind == "if" {
			e.y = p.primary()
		} else {
			e.y = &expression{tag: "block", tok: p.peek(), body: p.block("value")}
		}
	case "match":
		e.tag = "match"
		e.x = p.expr(0)
		e.arms = p.matchArms("expr")
	case "for":
		e.tag = "carry"
		if p.peek().kind != "with" {
			e.name = p.need("id").text
			p.need("in")
			e.x = p.expr(0)
		}
		p.need("with")
		e.state = p.need("id").text
		p.need("=")
		e.y = p.expr(0)
		e.body = p.block("carry")
	case "[":
		e.tag = "list"
		p.soft++
		if p.peek().kind != "]" {
			first := p.expr(0)
			if p.accept("for") {
				e.tag = "comprehension"
				e.tok = p.ts[p.i-1]
				e.x = first
				e.name = p.need("id").text
				p.need("in")
				e.y = p.expr(0)
				if p.accept("if") {
					e.z = p.expr(0)
				}
			} else {
				e.elems = append(e.elems, first)
				for p.accept(",") && p.peek().kind != "]" && p.err == nil {
					e.elems = append(e.elems, p.expr(0))
				}
			}
		}
		p.need("]")
		p.soft--
	case "{":
		p.i--
		if p.objectAhead() {
			p.take()
			p.soft++
			e.tag = "object"
			for p.peek().kind != "}" && p.err == nil {
				key := p.take()
				name := key.text
				if key.kind == "string" {
					name = key.value.s
				} else if key.kind != "id" {
					p.fail(key, "invalid object key")
				}
				p.need(":")
				e.fields = append(e.fields, field{name, p.expr(0), key})
				if !p.accept(",") {
					break
				}
			}
			p.need("}")
			p.soft--
		} else {
			e.tag = "block"
			e.body = p.block("value")
		}
	default:
		p.fail(t, "expected expression")
	}
	return e
}
func (p *parser) objectAhead() bool {
	j := p.i + 1
	for j < len(p.ts) && p.ts[j].kind == "\n" {
		j++
	}
	if j < len(p.ts) && p.ts[j].kind == "}" {
		return true
	}
	if j >= len(p.ts) || (p.ts[j].kind != "id" && p.ts[j].kind != "string") {
		return false
	}
	j++
	for j < len(p.ts) && p.ts[j].kind == "\n" {
		j++
	}
	return j < len(p.ts) && p.ts[j].kind == ":"
}
