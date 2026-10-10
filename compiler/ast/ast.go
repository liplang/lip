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
	Kind  string // "python", "go", or "host"
	Spec  string
	Alias string // Optional local name for an imported namespace or Host operation.
	Pos   token.Pos
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

type MatchStmt struct {
	Value Expr
	Arms  []MatchArm
	Pos   token.Pos
}

func (*MatchStmt) stmtNode() {}

// ForStmt executes a list in source order without collecting results. A nil
// Source represents an explicit infinite loop (`for { ... }`) terminated by
// break or context cancellation.
type ForStmt struct {
	Variable string
	Source   Expr
	Body     []Stmt
	Pos      token.Pos
}

func (*ForStmt) stmtNode() {}

// Loop controls target the closest enclosing statement for.
type LoopControlStmt struct {
	Kind string
	Pos  token.Pos
}

func (*LoopControlStmt) stmtNode() {}

// A nil Pattern with no Patterns or TypePatterns is the wildcard _. Guards are
// evaluated only after any literal/type alternative matches. Statement arms
// use Body; expression arms use Expr.
type MatchArm struct {
	// Pattern is retained as the first pattern for callers that construct the
	// AST directly. Source code may provide several literal patterns in one arm;
	// Patterns is then authoritative and Pattern points at Patterns[0].
	Pattern  *LiteralExpr
	Patterns []*LiteralExpr
	// TypePatterns contains runtime type alternatives such as number/string.
	// They share the same guard/result as Patterns; source order between literal
	// and type alternatives does not affect their OR semantics.
	TypePatterns []string
	Guard        Expr
	Body         []Stmt
	Expr         Expr
	Pos          token.Pos
}

type ReturnStmt struct {
	Expr Expr
	Pos  token.Pos
}

func (*ReturnStmt) stmtNode() {}

// ExprStmt discards a call's result, as in print(value). Files accept only call
// forms as expression statements; the REPL also displays a final expression.
type ExprStmt struct {
	Expr Expr
	Pos  token.Pos
}

func (*ExprStmt) stmtNode() {}

type Expr interface{ exprNode() }

type IdentExpr struct {
	Name string
	Pos  token.Pos
	// OperationKind records the backend for feedback operation references.
	OperationKind string
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
	// ArgNames parallels Args; an empty entry is a positional argument.
	// Named arguments are currently used by a small set of standard-library
	// options, while keeping the AST ready for future APIs.
	ArgNames []string
	Pos      token.Pos
	// Local distinguishes pure functions from imported Host operations with
	// the same canonical name.
	Local bool
	// Explicit Python imports keep their identity after alias resolution, even
	// when the module name also names a LIP standard-library namespace.
	Python bool
	// Host preserves imported Host/Go identity even when its canonical name
	// coincides with a core builtin or standard-library namespace.
	Host bool
	// PythonAttribute reads Name instead of invoking it. Modules and callables
	// are returned as descriptive references; scalar attributes are values.
	PythonAttribute bool
}

func (*CallExpr) exprNode() {}

// LambdaExpr is an inline pure collection callback. Omitted parameter types
// are any; free variables are immutable captures and remain graph dependencies.
type LambdaExpr struct {
	Params             []string
	ParamTypes         map[string]string
	ReturnType         string
	InferredReturnType string
	Return             Expr
	Pos                token.Pos
}

func (*LambdaExpr) exprNode() {}

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

type MatchExpr struct {
	Value Expr
	Arms  []MatchArm
	Pos   token.Pos
}

func (*MatchExpr) exprNode() {}

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

// ComprehensionExpr is [Element for Variable in Source]. Standalone maps use
// one dynamic graph node; composed maps evaluate inside their expression.
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
