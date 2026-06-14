package codegen

import (
	"strings"

	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// privacyCheck describes a parsed reference to player_privacy_settings.
type privacyCheck struct {
	// tableParts is the fully qualified table reference parts
	// (e.g., ["game", "player_privacy_settings"]).
	tableParts []string
	// joinColumn is the column in the lookup table used for the join
	// (e.g. "player_id", "user_id").
	joinColumn string
	// lookupColumn is the column in the policy table whose value is used to
	// look up the privacy row (e.g. "sender_id", "followed_id", "player_id").
	lookupColumn string
	// flagColumns are the boolean columns checked in player_privacy_settings
	// (e.g. ["chat_enabled"], ["chat_enabled", "notifications_enabled"]).
	flagColumns []string
}

// ownershipCheck describes a parsed ownership comparison.
type ownershipCheck struct {
	// column is the column being compared (e.g. "player_id").
	column string
}

// dualPrivacyCheck describes a policy that checks two players' privacy settings.
type dualPrivacyCheck struct {
	// first is the first player's lookup column and flag.
	first privacyCheck
	// second is the second player's lookup column and flag.
	second privacyCheck
}

// existsLookup describes a parsed EXISTS subquery that checks a flag in a lookup table.
type existsLookup struct {
	tableParts   []string // fully qualified table reference parts (e.g., ["game", "player_privacy_settings"])
	joinColumn   string   // column in the lookup table used for the join (e.g., "player_id")
	lookupColumn string   // column from the outer table (e.g., "sender_id")
	flagColumns  []string // boolean flag columns (e.g., ["chat_enabled"])
	negated      bool     // true for NOT EXISTS
}

// orCompound describes a top-level OR expression with ownership on one side
// and an exists-lookup on the other. Semantics: return true if EITHER passes.
type orCompound struct {
	ownership   *ownershipCheck
	existsLookup *existsLookup
}

// detectOrCompound checks whether the top-level node is a BinaryOp with OR
// where one side is an ownership check and the other is an exists-lookup.
// Returns nil if the pattern is not found.
func detectOrCompound(node sqlexpr.Node) *orCompound {
	node = unwrapParen(node)
	bin, ok := node.(*sqlexpr.BinaryOp)
	if !ok || !strings.EqualFold(bin.Op, "OR") {
		return nil
	}

	// Try left=ownership, right=exists
	own := detectOwnership(bin.Left)
	lookups := detectAllExistsLookups(bin.Right)
	if own != nil && len(lookups) == 1 {
		return &orCompound{ownership: own, existsLookup: lookups[0]}
	}

	// Try left=exists, right=ownership
	lookups = detectAllExistsLookups(bin.Left)
	own = detectOwnership(bin.Right)
	if own != nil && len(lookups) == 1 {
		return &orCompound{ownership: own, existsLookup: lookups[0]}
	}

	return nil
}

// unwrapCast returns the inner node if n is a Cast, otherwise returns n as-is.
func unwrapCast(n sqlexpr.Node) sqlexpr.Node {
	for {
		c, ok := n.(*sqlexpr.Cast)
		if !ok {
			return n
		}
		n = c.Expr
	}
}

// unwrapParen returns the inner node if n is a ParenExpr, otherwise returns n as-is.
func unwrapParen(n sqlexpr.Node) sqlexpr.Node {
	for {
		p, ok := n.(*sqlexpr.ParenExpr)
		if !ok {
			return n
		}
		n = p.Inner
	}
}

// detectOwnership walks the AST looking for a BinaryOp{Op: "="} where one side
// (after unwrapping Cast) is a ColumnRef and the other side (after unwrapping
// Cast) is a FuncCall named "current_setting".
// Returns nil if the pattern is not found.
func detectOwnership(node sqlexpr.Node) *ownershipCheck {
	var result *ownershipCheck
	sqlexpr.Walk(node, func(n sqlexpr.Node) bool {
		if result != nil {
			return false
		}
		bin, ok := n.(*sqlexpr.BinaryOp)
		if !ok || bin.Op != "=" {
			return true
		}
		left := unwrapCast(bin.Left)
		right := unwrapCast(bin.Right)
		// Try left=ColumnRef, right=FuncCall
		if col, ok := left.(*sqlexpr.ColumnRef); ok {
			if fc, ok := right.(*sqlexpr.FuncCall); ok && strings.EqualFold(fc.Name, "current_setting") {
				result = &ownershipCheck{column: col.Parts[len(col.Parts)-1]}
				return false
			}
		}
		// Try right=ColumnRef, left=FuncCall
		if col, ok := right.(*sqlexpr.ColumnRef); ok {
			if fc, ok := left.(*sqlexpr.FuncCall); ok && strings.EqualFold(fc.Name, "current_setting") {
				result = &ownershipCheck{column: col.Parts[len(col.Parts)-1]}
				return false
			}
		}
		return true
	})
	return result
}

// detectAllExistsLookups walks the AST and collects all ExistsExpr nodes,
// extracting the lookup table, join condition, and flag condition from each.
// Also detects NOT EXISTS (UnaryOp{Op:"NOT", Operand: ExistsExpr}) and marks
// those lookups as negated.
// Returns nil if no EXISTS subqueries with the expected pattern are found.
func detectAllExistsLookups(node sqlexpr.Node) []*existsLookup {
	var results []*existsLookup
	sqlexpr.Walk(node, func(n sqlexpr.Node) bool {
		// Check for NOT EXISTS: UnaryOp{Op:"NOT", Operand: ExistsExpr}
		if unary, ok := n.(*sqlexpr.UnaryOp); ok && strings.EqualFold(unary.Op, "NOT") {
			if ex, ok := unary.Operand.(*sqlexpr.ExistsExpr); ok {
				sel := ex.Subquery
				if sel != nil && sel.Where != nil {
					lookup := analyzeExistsWhere(sel)
					if lookup != nil {
						lookup.negated = true
						results = append(results, lookup)
					}
				}
				return false // don't descend further
			}
		}
		// Check for plain EXISTS
		ex, ok := n.(*sqlexpr.ExistsExpr)
		if !ok {
			return true
		}
		sel := ex.Subquery
		if sel == nil || sel.Where == nil {
			return true
		}
		lookup := analyzeExistsWhere(sel)
		if lookup != nil {
			results = append(results, lookup)
		}
		return false // don't descend into subquery children
	})
	return results
}

// analyzeExistsWhere extracts join and flag info from a SelectExpr's WHERE clause.
// Expected pattern: joinCol = outerCol AND flagCol = true
func analyzeExistsWhere(sel *sqlexpr.SelectExpr) *existsLookup {
	// Collect all equality comparisons from the WHERE clause
	var eqs []*sqlexpr.BinaryOp
	collectEquals(sel.Where, &eqs)

	var joinCol, lookupCol string
	var flagCols []string
	for _, eq := range eqs {
		left := unwrapCast(unwrapParen(eq.Left))
		right := unwrapCast(unwrapParen(eq.Right))

		// Check for flag = true pattern
		if leftCol, ok := left.(*sqlexpr.ColumnRef); ok {
			if boolLit, ok := right.(*sqlexpr.BoolLiteral); ok && boolLit.Value {
				flagCols = append(flagCols, leftCol.Parts[len(leftCol.Parts)-1])
				continue
			}
		}
		if rightCol, ok := right.(*sqlexpr.ColumnRef); ok {
			if boolLit, ok := left.(*sqlexpr.BoolLiteral); ok && boolLit.Value {
				flagCols = append(flagCols, rightCol.Parts[len(rightCol.Parts)-1])
				continue
			}
		}

		// Check for col = col pattern (join condition)
		if leftCol, ok := left.(*sqlexpr.ColumnRef); ok {
			if rightCol, ok := right.(*sqlexpr.ColumnRef); ok {
				// The join column is the unqualified one or the one from the subquery table.
				// The lookup column is the one referencing the outer table.
				// Heuristic: if one is qualified (has 2+ parts), it's the outer column.
				// If both are unqualified, the first (left) is the join column from the
				// lookup table and the second (right) is the outer reference.
				leftName := leftCol.Parts[len(leftCol.Parts)-1]
				rightName := rightCol.Parts[len(rightCol.Parts)-1]
				if len(rightCol.Parts) > 1 {
					// Right is qualified (outer ref), left is join
					joinCol = leftName
					lookupCol = rightName
				} else if len(leftCol.Parts) > 1 {
					// Left is qualified (outer ref), right is join
					lookupCol = leftName
					joinCol = rightName
				} else {
					// Both unqualified: left = join (lookup table's column), right = outer
					joinCol = leftName
					lookupCol = rightName
				}
				continue
			}
		}
	}

	if joinCol == "" || lookupCol == "" || len(flagCols) == 0 {
		return nil
	}

	return &existsLookup{
		tableParts:   sel.From.Parts,
		joinColumn:   joinCol,
		lookupColumn: lookupCol,
		flagColumns:  flagCols,
	}
}

// collectEquals walks a WHERE clause tree and collects all BinaryOp{Op: "="} nodes,
// descending through AND connectives.
func collectEquals(node sqlexpr.Node, out *[]*sqlexpr.BinaryOp) {
	node = unwrapParen(node)
	bin, ok := node.(*sqlexpr.BinaryOp)
	if !ok {
		return
	}
	if strings.EqualFold(bin.Op, "AND") {
		collectEquals(bin.Left, out)
		collectEquals(bin.Right, out)
		return
	}
	if bin.Op == "=" {
		*out = append(*out, bin)
	}
}
