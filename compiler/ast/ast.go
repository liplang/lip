package ast

import "lipalpha/compiler/token"

type Program struct {
	Functions []*Function
	Flow      *Flow
}

type Function struct {
	Name   string
	Params []string
	// ParamTypes contains optional Alpha type annotations. An omitted entry is any.
	ParamTypes map[string]string
	Return     Expr
	Pos        token.Pos
}

type Flow struct {
	Name   string
	Params []string
	// ParamTypes contains optional Alpha type annotations. An omitted entry is any.
	ParamTypes map[string]string
	Body       []Stmt
	Pos        token.Pos
}

type Stmt interface{ stmtNode() }

type BindStmt struct {
	Name string
	Expr Expr
	Pos  token.Pos
}

func (*BindStmt) stmtNode() {}

type WhenStmt struct {
	Cond Expr
	Body []Stmt
	Pos  token.Pos
}

func (*WhenStmt) stmtNode() {}

type ReturnStmt struct {
	Expr Expr
	Pos  token.Pos
}

func (*ReturnStmt) stmtNode() {}

// ExprStmt is an effect-only call such as print(value). Alpha 0.1 does not
// allow arbitrary unused expressions; the compiler accepts only call forms.
type ExprStmt struct {
	Expr Expr
	Pos  token.Pos
}

func (*ExprStmt) stmtNode() {}

type Expr interface{ exprNode() }

type IdentExpr struct {
	Name string
	Pos  token.Pos
}

func (*IdentExpr) exprNode() {}

type LiteralExpr struct {
	Value any
	Raw   string
	Pos   token.Pos
}

func (*LiteralExpr) exprNode() {}

type CallExpr struct {
	Name string
	Args []Expr
	Pos  token.Pos
}

func (*CallExpr) exprNode() {}

type BinaryExpr struct {
	Op          string
	Left, Right Expr
	Pos         token.Pos
}

func (*BinaryExpr) exprNode() {}

type IfExpr struct {
	Cond, Then, Else Expr
	Pos              token.Pos
}

func (*IfExpr) exprNode() {}

type ListExpr struct {
	Items []Expr
	Pos   token.Pos
}

func (*ListExpr) exprNode() {}

type FieldExpr struct {
	Object Expr
	Name   string
	Pos    token.Pos
}

func (*FieldExpr) exprNode() {}

type IndexExpr struct {
	Object Expr
	Index  Expr
	Pos    token.Pos
}

func (*IndexExpr) exprNode() {}
