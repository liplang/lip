package lexer

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"lipalpha/compiler/token"
)

type Lexer struct {
	src  []rune
	i    int
	line int
	col  int
}

func New(src string) *Lexer { return &Lexer{src: []rune(src), line: 1, col: 1} }

func (l *Lexer) Lex() ([]token.Token, error) {
	var out []token.Token
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if t.Kind == token.EOF {
			return out, nil
		}
	}
}

func (l *Lexer) next() (token.Token, error) {
	for l.i < len(l.src) {
		ch := l.src[l.i]
		if unicode.IsSpace(ch) {
			l.advance()
			continue
		}
		if ch == '#' {
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance()
			}
			continue
		}
		break
	}
	start := token.Pos{Offset: l.i, Line: l.line, Column: l.col}
	if l.i >= len(l.src) {
		return token.Token{Kind: token.EOF, Pos: start}, nil
	}
	ch := l.src[l.i]
	if unicode.IsLetter(ch) || ch == '_' {
		startI := l.i
		for l.i < len(l.src) && (unicode.IsLetter(l.src[l.i]) || unicode.IsDigit(l.src[l.i]) || l.src[l.i] == '_') {
			l.advance()
		}
		text := string(l.src[startI:l.i])
		kind := token.Ident
		switch text {
		case "flow":
			kind = token.Flow
		case "fn":
			kind = token.Fn
		case "return":
			kind = token.Return
		case "match":
			kind = token.Match
		case "for":
			kind = token.For
		case "break":
			kind = token.Break
		case "continue":
			kind = token.Continue
		case "in":
			kind = token.In
		case "if":
			kind = token.If
		case "else":
			kind = token.Else
		case "import":
			kind = token.Import
		case "as":
			kind = token.As
		case "true":
			kind = token.True
		case "false":
			kind = token.False
		case "null":
			kind = token.Null
		}
		return token.Token{Kind: kind, Text: text, Pos: start}, nil
	}
	if unicode.IsDigit(ch) {
		startI := l.i
		for unicode.IsDigit(l.peek(0)) {
			l.advance()
		}
		if l.peek(0) == '.' {
			l.advance()
			for unicode.IsDigit(l.peek(0)) {
				l.advance()
			}
		}
		if l.peek(0) == 'e' || l.peek(0) == 'E' {
			l.advance()
			if l.peek(0) == '+' || l.peek(0) == '-' {
				l.advance()
			}
			for unicode.IsDigit(l.peek(0)) {
				l.advance()
			}
		}
		text := string(l.src[startI:l.i])
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return token.Token{}, l.errorAt(start, "invalid finite number %q", text)
		}
		return token.Token{Kind: token.Number, Text: text, Pos: start}, nil
	}
	if ch == '"' {
		l.advance()
		var b strings.Builder
		for l.i < len(l.src) && l.src[l.i] != '"' {
			if l.src[l.i] == '\n' || l.src[l.i] == '\r' {
				return token.Token{}, l.errorAt(start, `newline in string; write \n inside double quotes`)
			}
			if l.src[l.i] == '\\' {
				if l.i+1 >= len(l.src) {
					return token.Token{}, l.errorAt(start, "unterminated string")
				}
				b.WriteRune(l.src[l.i])
				l.advance()
				b.WriteRune(l.src[l.i])
				l.advance()
				continue
			}
			b.WriteRune(l.src[l.i])
			l.advance()
		}
		if l.i >= len(l.src) {
			return token.Token{}, l.errorAt(start, "unterminated string")
		}
		l.advance()
		decoded, err := strconv.Unquote(`"` + b.String() + `"`)
		if err != nil {
			return token.Token{}, l.errorAt(start, `invalid string escape; use \n, \t, \", \\ or \uNNNN`)
		}
		return token.Token{Kind: token.String, Text: decoded, Pos: start}, nil
	}
	for _, op := range []struct {
		text string
		kind token.Kind
	}{{"->", token.Arrow}, {"=>", token.FatArrow}, {"//", token.FloorDiv}, {"**", token.Power}, {"*/", token.Log}, {"==", token.Equal}, {"!=", token.NotEqual}, {">=", token.GreaterEqual}, {"<=", token.LessEqual}, {"&&", token.And}, {"||", token.Or}} {
		if ch == rune(op.text[0]) && l.peekString(op.text) {
			for range op.text {
				l.advance()
			}
			return token.Token{Kind: op.kind, Text: op.text, Pos: start}, nil
		}
	}
	one := map[rune]token.Kind{'=': token.Assign, '(': token.LParen, ')': token.RParen, '{': token.LBrace, '}': token.RBrace, '[': token.LBracket, ']': token.RBracket, ',': token.Comma, ':': token.Colon, '.': token.Dot, '+': token.Plus, '-': token.Minus, '*': token.Star, '/': token.Slash, '>': token.Greater, '<': token.Less}
	one['?'] = token.Question
	one['!'] = token.Not
	one[';'] = token.Semicolon
	one['%'] = token.Modulo
	if kind, ok := one[ch]; ok {
		l.advance()
		return token.Token{Kind: kind, Text: string(ch), Pos: start}, nil
	}
	return token.Token{}, l.errorAt(start, "unexpected character %q", ch)
}

func (l *Lexer) advance() {
	if l.i >= len(l.src) {
		return
	}
	if l.src[l.i] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.i++
}

func (l *Lexer) peek(n int) rune {
	if l.i+n >= len(l.src) {
		return 0
	}
	return l.src[l.i+n]
}

func (l *Lexer) peekString(s string) bool {
	r := []rune(s)
	if l.i+len(r) > len(l.src) {
		return false
	}
	return string(l.src[l.i:l.i+len(r)]) == s
}

func (l *Lexer) errorAt(pos token.Pos, format string, args ...any) error {
	return fmt.Errorf("%d:%d: %s", pos.Line, pos.Column, fmt.Sprintf(format, args...))
}
