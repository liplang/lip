package token

type Kind int

const (
	EOF Kind = iota
	Ident
	Number
	String
	True
	False
	Null
	Flow
	Fn
	Return
	When
	For
	In
	If
	Else
	Require
	Arrow
	Question
	Assign
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Colon
	Dot
	Plus
	Minus
	Star
	Slash
	Equal
	NotEqual
	Greater
	GreaterEqual
	Less
	LessEqual
	And
	Or
	Not
)

type Pos struct {
	Offset int
	Line   int
	Column int
}

type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

func (k Kind) String() string {
	names := map[Kind]string{
		EOF: "EOF", Ident: "identifier", Number: "number", String: "string",
		True: "true", False: "false", Null: "null", Flow: "flow", Fn: "fn", Return: "return", When: "when",
		If: "if", Else: "else", Require: "require", For: "for", In: "in", Assign: "=", LParen: "(", RParen: ")",
		LBrace: "{", RBrace: "}", LBracket: "[", RBracket: "]", Comma: ",", Colon: ":", Dot: ".", Plus: "+", Minus: "-", Star: "*", Slash: "/",
		Equal: "==", NotEqual: "!=", Greater: ">", GreaterEqual: ">=", Less: "<", LessEqual: "<=",
		And: "&&", Or: "||", Not: "!", Arrow: "->", Question: "?",
	}
	if s, ok := names[k]; ok {
		return s
	}
	return "unknown"
}
