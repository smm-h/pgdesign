package sqlexpr

// Node is the interface all AST nodes implement.
type Node interface {
	nodeType() string
}

// ColumnRef is a column reference, optionally qualified.
// Parts: ["column"], ["table", "column"], or ["schema", "table", "column"].
type ColumnRef struct {
	Parts []string
}

func (n *ColumnRef) nodeType() string { return "ColumnRef" }

// BoolLiteral is a boolean literal (true/false).
type BoolLiteral struct {
	Value bool
}

func (n *BoolLiteral) nodeType() string { return "BoolLiteral" }

// StringLiteral is a single-quoted string literal.
type StringLiteral struct {
	Value string
}

func (n *StringLiteral) nodeType() string { return "StringLiteral" }

// IntLiteral is an integer literal.
type IntLiteral struct {
	Value int
}

func (n *IntLiteral) nodeType() string { return "IntLiteral" }

// FloatLiteral is a floating-point literal.
type FloatLiteral struct {
	Value float64
}

func (n *FloatLiteral) nodeType() string { return "FloatLiteral" }

// NullLiteral is a NULL literal.
type NullLiteral struct{}

func (n *NullLiteral) nodeType() string { return "NullLiteral" }

// FuncCall is a function call with arguments.
type FuncCall struct {
	Name string
	Args []Node
}

func (n *FuncCall) nodeType() string { return "FuncCall" }

// Cast is a type cast expression (expr::type).
type Cast struct {
	Expr     Node
	TypeName string
}

func (n *Cast) nodeType() string { return "Cast" }

// BinaryOp is a binary operation (left op right).
type BinaryOp struct {
	Op    string
	Left  Node
	Right Node
}

func (n *BinaryOp) nodeType() string { return "BinaryOp" }

// UnaryOp is a unary operation (e.g., NOT expr).
type UnaryOp struct {
	Op      string
	Operand Node
}

func (n *UnaryOp) nodeType() string { return "UnaryOp" }

// ExistsExpr is an EXISTS (subquery) expression.
type ExistsExpr struct {
	Subquery *SelectExpr
}

func (n *ExistsExpr) nodeType() string { return "ExistsExpr" }

// SelectExpr is a SELECT statement inside EXISTS.
type SelectExpr struct {
	Columns []Node
	From    *ColumnRef
	Where   Node
}

func (n *SelectExpr) nodeType() string { return "SelectExpr" }

// ParenExpr is a parenthesized expression.
type ParenExpr struct {
	Inner Node
}

func (n *ParenExpr) nodeType() string { return "ParenExpr" }

// CaseExpr is a CASE WHEN ... THEN ... [ELSE ...] END expression.
type CaseExpr struct {
	Whens []WhenClause
	Else  Node // nil if no ELSE
}

// WhenClause is a WHEN condition THEN result pair.
type WhenClause struct {
	Condition Node
	Result    Node
}

func (n *CaseExpr) nodeType() string { return "CaseExpr" }
