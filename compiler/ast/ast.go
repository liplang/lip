package ast

import "lipalpha/compiler/token"

type Program struct {
	Functions []*Function
	Flow      *Flow
	// Dependencies are declarative environment requirements. They do not
	// import code into the generated Go package or install anything.
	Dependencies []Dependency
}

type Dependency struct {
	Kind string // "python", "go", or "host"
	Spec string
	Pos  token.Pos
}

type Function struct {
	Name   string
	Params []string
	// Parameter types are explicit; function result types may be inferred.
	ParamTypes map[string]string
	ReturnType string
	// InferredReturnType is analysis metadata; it never rewrites source declarations.
	InferredReturnType string
	Return             Expr
	Pos                token.Pos
}

type Flow struct {
	Name   string
	Params []string
	// Flow parameters and its output form the explicit program boundary.
	ParamTypes map[string]string
	ReturnType string
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

// ExprStmt is an effect-only call such as print(value). Alpha 0.2 does not
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

type UnaryExpr struct {
	Op      string
	Operand Expr
	Pos     token.Pos
}

func (*UnaryExpr) exprNode() {}

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

// Fields keep source order for deterministic evaluation and diagnostics.
type ObjectExpr struct {
	Fields []ObjectField
	Pos    token.Pos
}

type ObjectField struct {
	Name  string
	Value Expr
	Pos   token.Pos
}

func (*ObjectExpr) exprNode() {}

// ComprehensionExpr is the Alpha 0.2 one-shot dynamic map form:
// [Element for Variable in Source]. The compiler keeps this as one graph node
// and the runtime expands its execution instances after Source is available.
type ComprehensionExpr struct {
	Element  Expr
	Variable string
	Source   Expr
	Pos      token.Pos
}

func (*ComprehensionExpr) exprNode() {}

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
