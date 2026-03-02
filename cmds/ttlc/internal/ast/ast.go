package ast

type Position struct {
	Offset int
	Line   int
	Column int
}

type Span struct {
	Start Position
	End   Position
}

type Module struct {
	PackageName string
	Imports     []ImportDecl
	Decls       []Decl
	Span        Span
}

type ImportDecl struct {
	Path string
	Span Span
}

type Decl interface {
	declNode()
	DeclName() string
	DeclSpan() Span
}

type TypeDecl struct {
	Name   string
	Fields []StructField
	Span   Span
}

func (d *TypeDecl) declNode()        {}
func (d *TypeDecl) DeclName() string { return d.Name }
func (d *TypeDecl) DeclSpan() Span   { return d.Span }

type TaskDecl struct {
	Name       string
	Parameters []Parameter
	ReturnType *TypeExpr
	Body       []Stmt
	Span       Span
}

func (d *TaskDecl) declNode()        {}
func (d *TaskDecl) DeclName() string { return d.Name }
func (d *TaskDecl) DeclSpan() Span   { return d.Span }

type FuncDecl struct {
	Name       string
	Parameters []Parameter
	ReturnType *TypeExpr
	Body       []Stmt
	Span       Span
}

func (d *FuncDecl) declNode()        {}
func (d *FuncDecl) DeclName() string { return d.Name }
func (d *FuncDecl) DeclSpan() Span   { return d.Span }

type StructField struct {
	Name string
	Type *TypeExpr
	Span Span
}

type Parameter struct {
	Name string
	Type *TypeExpr
	Span Span
}

type TypeExpr struct {
	Package  string
	Name     string
	TypeArgs []*TypeExpr
	Span     Span
}

type Stmt interface {
	stmtNode()
	StmtSpan() Span
}

type AssignOperator string

const (
	AssignOperatorDefine AssignOperator = ":="
	AssignOperatorSet    AssignOperator = "="
)

type ReturnStmt struct {
	Value Expr
	Span  Span
}

func (s *ReturnStmt) stmtNode()      {}
func (s *ReturnStmt) StmtSpan() Span { return s.Span }

type AssignStmt struct {
	Name     string
	Operator AssignOperator
	Value    Expr
	Span     Span
}

func (s *AssignStmt) stmtNode()      {}
func (s *AssignStmt) StmtSpan() Span { return s.Span }

type ExprStmt struct {
	Value Expr
	Span  Span
}

func (s *ExprStmt) stmtNode()      {}
func (s *ExprStmt) StmtSpan() Span { return s.Span }

type Expr interface {
	exprNode()
	ExprSpan() Span
}

type IdentifierExpr struct {
	Name string
	Span Span
}

func (e *IdentifierExpr) exprNode()      {}
func (e *IdentifierExpr) ExprSpan() Span { return e.Span }

type StringLiteralExpr struct {
	Value string
	Span  Span
}

func (e *StringLiteralExpr) exprNode()      {}
func (e *StringLiteralExpr) ExprSpan() Span { return e.Span }

type NumberLiteralExpr struct {
	Value string
	Span  Span
}

func (e *NumberLiteralExpr) exprNode()      {}
func (e *NumberLiteralExpr) ExprSpan() Span { return e.Span }

type CallExpr struct {
	Callee Expr
	Args   []Expr
	Span   Span
}

func (e *CallExpr) exprNode()      {}
func (e *CallExpr) ExprSpan() Span { return e.Span }

type SelectorExpr struct {
	Target Expr
	Name   string
	Span   Span
}

func (e *SelectorExpr) exprNode()      {}
func (e *SelectorExpr) ExprSpan() Span { return e.Span }

type CompositeField struct {
	Name  string
	Value Expr
	Span  Span
}

type CompositeLiteralExpr struct {
	Type   *TypeExpr
	Fields []CompositeField
	Span   Span
}

func (e *CompositeLiteralExpr) exprNode()      {}
func (e *CompositeLiteralExpr) ExprSpan() Span { return e.Span }
