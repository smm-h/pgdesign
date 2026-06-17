package codegen

import (
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// checkPattern describes a recognized CHECK constraint pattern that can be
// translated to language-native validation code.
type checkPattern interface {
	patternType() string
}

// rangePattern matches: column >= low AND column <= high (or >, <)
type rangePattern struct {
	Column   string
	Low      string // numeric literal as string
	High     string // numeric literal as string
	LowIncl  bool   // true for >=, false for >
	HighIncl bool   // true for <=, false for <
}

func (p *rangePattern) patternType() string { return "range" }

// comparisonPattern matches: column >= N, column < N, etc. (single comparison)
type comparisonPattern struct {
	Column string
	Op     string // >=, <=, >, <, =, !=
	Value  string // numeric literal as string
}

func (p *comparisonPattern) patternType() string { return "comparison" }

// lengthPattern matches: LENGTH(column) <= N, LENGTH(column) >= N, etc.
type lengthPattern struct {
	Column string
	Op     string // >=, <=, >, <, =
	Value  int
}

func (p *lengthPattern) patternType() string { return "length" }

// likePattern matches: column LIKE 'pattern'
type likePattern struct {
	Column  string
	Pattern string
	Op      string // LIKE, ILIKE, NOT LIKE, NOT ILIKE
}

func (p *likePattern) patternType() string { return "like" }

// classifyCheck parses a CHECK expression and returns a checkPattern if it
// matches a recognized pattern. Returns nil for unrecognized expressions.
func classifyCheck(expr string) checkPattern {
	ast, err := sqlexpr.Parse(expr)
	if err != nil {
		return nil
	}
	return classifyNode(ast)
}

// classifyNode examines an AST node and attempts to match it against known
// CHECK constraint patterns.
func classifyNode(node sqlexpr.Node) checkPattern {
	switch n := node.(type) {
	case *sqlexpr.ParenExpr:
		return classifyNode(n.Inner)
	case *sqlexpr.BinaryOp:
		// Range: col >= low AND col <= high
		if strings.EqualFold(n.Op, "AND") {
			left := classifyNode(n.Left)
			right := classifyNode(n.Right)
			lc, lok := left.(*comparisonPattern)
			rc, rok := right.(*comparisonPattern)
			if lok && rok && strings.EqualFold(lc.Column, rc.Column) {
				if isLowerBound(lc.Op) && isUpperBound(rc.Op) {
					return &rangePattern{
						Column:   lc.Column,
						Low:      lc.Value,
						High:     rc.Value,
						LowIncl:  lc.Op == ">=",
						HighIncl: rc.Op == "<=",
					}
				}
				if isUpperBound(lc.Op) && isLowerBound(rc.Op) {
					return &rangePattern{
						Column:   lc.Column,
						Low:      rc.Value,
						High:     lc.Value,
						LowIncl:  rc.Op == ">=",
						HighIncl: lc.Op == "<=",
					}
				}
			}
		}

		// LIKE/ILIKE patterns
		if strings.EqualFold(n.Op, "LIKE") || strings.EqualFold(n.Op, "ILIKE") ||
			strings.EqualFold(n.Op, "NOT LIKE") || strings.EqualFold(n.Op, "NOT ILIKE") {
			col := extractColumnName(n.Left)
			if col != "" {
				if sl, ok := n.Right.(*sqlexpr.StringLiteral); ok {
					return &likePattern{Column: col, Pattern: sl.Value, Op: n.Op}
				}
			}
		}

		// Length check: LENGTH(col) op N
		if isComparisonOp(n.Op) {
			if lp := matchLengthCheck(n); lp != nil {
				return lp
			}
		}

		// Simple comparison: col op N
		if isComparisonOp(n.Op) {
			col := extractColumnName(n.Left)
			val := extractNumericLiteral(n.Right)
			if col != "" && val != "" {
				return &comparisonPattern{Column: col, Op: n.Op, Value: val}
			}
			// Reversed: N op col -> invert operator
			col = extractColumnName(n.Right)
			val = extractNumericLiteral(n.Left)
			if col != "" && val != "" {
				return &comparisonPattern{Column: col, Op: invertOp(n.Op), Value: val}
			}
		}
	}
	return nil
}

func isLowerBound(op string) bool { return op == ">=" || op == ">" }
func isUpperBound(op string) bool { return op == "<=" || op == "<" }

func isComparisonOp(op string) bool {
	switch op {
	case ">=", "<=", ">", "<", "=", "!=", "<>":
		return true
	}
	return false
}

func invertOp(op string) string {
	switch op {
	case ">=":
		return "<="
	case "<=":
		return ">="
	case ">":
		return "<"
	case "<":
		return ">"
	default:
		return op // =, !=, <> are symmetric
	}
}

func extractColumnName(node sqlexpr.Node) string {
	switch n := node.(type) {
	case *sqlexpr.ColumnRef:
		return n.Parts[len(n.Parts)-1]
	case *sqlexpr.ParenExpr:
		return extractColumnName(n.Inner)
	}
	return ""
}

func extractNumericLiteral(node sqlexpr.Node) string {
	switch n := node.(type) {
	case *sqlexpr.IntLiteral:
		return fmt.Sprintf("%d", n.Value)
	case *sqlexpr.FloatLiteral:
		return fmt.Sprintf("%g", n.Value)
	case *sqlexpr.UnaryOp:
		if n.Op == "-" {
			inner := extractNumericLiteral(n.Operand)
			if inner != "" {
				return "-" + inner
			}
		}
	case *sqlexpr.ParenExpr:
		return extractNumericLiteral(n.Inner)
	}
	return ""
}

func matchLengthCheck(binop *sqlexpr.BinaryOp) *lengthPattern {
	// Try: LENGTH(col) op N
	if fc, ok := binop.Left.(*sqlexpr.FuncCall); ok {
		if strings.EqualFold(fc.Name, "LENGTH") || strings.EqualFold(fc.Name, "CHAR_LENGTH") ||
			strings.EqualFold(fc.Name, "CHARACTER_LENGTH") {
			if len(fc.Args) == 1 {
				col := extractColumnName(fc.Args[0])
				if il, ok := binop.Right.(*sqlexpr.IntLiteral); ok && col != "" {
					return &lengthPattern{Column: col, Op: binop.Op, Value: il.Value}
				}
			}
		}
	}
	// Try reversed: N op LENGTH(col) -> invert operator
	if fc, ok := binop.Right.(*sqlexpr.FuncCall); ok {
		if strings.EqualFold(fc.Name, "LENGTH") || strings.EqualFold(fc.Name, "CHAR_LENGTH") ||
			strings.EqualFold(fc.Name, "CHARACTER_LENGTH") {
			if len(fc.Args) == 1 {
				col := extractColumnName(fc.Args[0])
				if il, ok := binop.Left.(*sqlexpr.IntLiteral); ok && col != "" {
					return &lengthPattern{Column: col, Op: invertOp(binop.Op), Value: il.Value}
				}
			}
		}
	}
	return nil
}

// invertComparisonOp returns the logical inverse of a comparison operator.
func invertComparisonOp(op string) string {
	switch op {
	case ">=":
		return "<"
	case "<=":
		return ">"
	case ">":
		return "<="
	case "<":
		return ">="
	case "=":
		return "!="
	case "!=", "<>":
		return "=="
	default:
		return op
	}
}

// likeToRegex converts a SQL LIKE pattern to a regex pattern.
// _ -> . (single char), % -> .* (any chars), literal chars are escaped.
func likeToRegex(pattern string) string {
	var buf strings.Builder
	buf.WriteString("^")
	for _, ch := range pattern {
		switch ch {
		case '%':
			buf.WriteString(".*")
		case '_':
			buf.WriteString(".")
		case '.', '^', '$', '*', '+', '?', '{', '}', '[', ']', '(', ')', '|', '\\':
			buf.WriteRune('\\')
			buf.WriteRune(ch)
		default:
			buf.WriteRune(ch)
		}
	}
	buf.WriteString("$")
	return buf.String()
}

// escapeGoString escapes a string for use in a Go string literal within generated code.
func escapeGoString(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// escapeJSRegex escapes characters that would break a JS regex literal /pattern/.
// The pattern is already a valid regex from likeToRegex, but forward slashes need escaping.
func escapeJSRegex(pattern string) string {
	return strings.ReplaceAll(pattern, "/", "\\/")
}
