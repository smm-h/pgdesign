package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// PythonGenerator generates Python async validator functions for RLS policies.
type PythonGenerator struct{}

// privacyCheck describes a parsed reference to player_privacy_settings.
type privacyCheck struct {
	// lookupColumn is the column in the policy table whose value is used to
	// look up the privacy row (e.g. "sender_id", "followed_id", "player_id").
	lookupColumn string
	// flagColumn is the boolean column checked in player_privacy_settings
	// (e.g. "chat_enabled", "friends_enabled").
	flagColumn string
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

// existsLookup describes a parsed EXISTS subquery that checks a flag in a lookup table.
type existsLookup struct {
	tableParts   []string // fully qualified table reference parts (e.g., ["game", "player_privacy_settings"])
	joinColumn   string   // column in the lookup table used for the join (e.g., "player_id")
	lookupColumn string   // column from the outer table (e.g., "sender_id")
	flagColumn   string   // boolean flag column (e.g., "chat_enabled")
}

// detectAllExistsLookups walks the AST and collects all ExistsExpr nodes,
// extracting the lookup table, join condition, and flag condition from each.
// Returns nil if no EXISTS subqueries with the expected pattern are found.
func detectAllExistsLookups(node sqlexpr.Node) []*existsLookup {
	var results []*existsLookup
	sqlexpr.Walk(node, func(n sqlexpr.Node) bool {
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

	var joinCol, lookupCol, flagCol string
	for _, eq := range eqs {
		left := unwrapCast(unwrapParen(eq.Left))
		right := unwrapCast(unwrapParen(eq.Right))

		// Check for flag = true pattern
		if leftCol, ok := left.(*sqlexpr.ColumnRef); ok {
			if boolLit, ok := right.(*sqlexpr.BoolLiteral); ok && boolLit.Value {
				flagCol = leftCol.Parts[len(leftCol.Parts)-1]
				continue
			}
		}
		if rightCol, ok := right.(*sqlexpr.ColumnRef); ok {
			if boolLit, ok := left.(*sqlexpr.BoolLiteral); ok && boolLit.Value {
				flagCol = rightCol.Parts[len(rightCol.Parts)-1]
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

	if joinCol == "" || lookupCol == "" || flagCol == "" {
		return nil
	}

	return &existsLookup{
		tableParts:   sel.From.Parts,
		joinColumn:   joinCol,
		lookupColumn: lookupCol,
		flagColumn:   flagCol,
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

// Generate produces a Python file with async validator functions for all
// eligible policies in the schema.
func (g *PythonGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	all := ExtractPolicies(schema)
	generatable, filterDiags := FilterGeneratable(all)

	var diags []diagnostic.Diagnostic
	diags = append(diags, filterDiags...)

	if len(generatable) == 0 {
		return []byte(pythonHeader(schema.Name) + "\n# No generatable policies found.\n"), diags
	}

	var buf bytes.Buffer
	buf.WriteString(pythonHeader(schema.Name))

	for i, pol := range generatable {
		expr := pol.WithCheck
		if expr == "" {
			expr = pol.Using
		}

		if i > 0 {
			buf.WriteString("\n")
		}

		ast, err := sqlexpr.Parse(expr)
		if err != nil {
			// Should not happen since FilterGeneratable already parsed successfully,
			// but handle defensively.
			buf.WriteString(fmt.Sprintf(
				"\n# Skipped %s: could not parse expression\n",
				pol.PolicyName,
			))
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C001",
				Table:    pol.TableName,
				Message:  fmt.Sprintf("policy %q: could not parse expression: %v", pol.PolicyName, err),
			})
			continue
		}

		existsLookups := detectAllExistsLookups(ast)

		if len(existsLookups) >= 2 {
			// Dual/multi exists-lookup pattern
			dual := &dualPrivacyCheck{
				first: privacyCheck{
					lookupColumn: existsLookups[0].lookupColumn,
					flagColumn:   existsLookups[0].flagColumn,
				},
				second: privacyCheck{
					lookupColumn: existsLookups[1].lookupColumn,
					flagColumn:   existsLookups[1].flagColumn,
				},
			}
			// Use table from the first lookup for the FQN
			generateDualPrivacyValidator(&buf, pol, dual, existsLookups[0].tableParts)
		} else if len(existsLookups) == 1 {
			// Single exists-lookup pattern
			check := &privacyCheck{
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumn:   existsLookups[0].flagColumn,
			}
			generatePrivacyValidator(&buf, pol, check, existsLookups[0].tableParts)
		} else if own := detectOwnership(ast); own != nil {
			generateOwnershipValidator(&buf, pol, own)
		} else {
			buf.WriteString(fmt.Sprintf(
				"\n# Skipped %s: could not parse expression into a known pattern\n",
				pol.PolicyName,
			))
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C001",
				Table:    pol.TableName,
				Message:  fmt.Sprintf("policy %q: could not parse expression into a known pattern", pol.PolicyName),
			})
		}
	}

	return buf.Bytes(), diags
}

// generatePrivacyValidator writes a single-player privacy check validator.
func generatePrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck, tableParts []string) {
	paramName := check.lookupColumn

	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str) -> PolicyResult:\n",
		pol.PolicyName, paramName,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))

	tableFQN := strings.Join(tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE player_id = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		check.flagColumn, tableFQN, paramName,
	))
	buf.WriteString(fmt.Sprintf(
		"    if not row or not row[\"%s\"]:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
			"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
		check.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))
}

// generateOwnershipValidator writes a pure ID-comparison validator.
func generateOwnershipValidator(buf *bytes.Buffer, pol PolicyContext, own *ownershipCheck) {
	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str, target_%s: str) -> PolicyResult:\n",
		pol.PolicyName, own.column, own.column,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))
	buf.WriteString(fmt.Sprintf(
		"    if %s != target_%s:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
			"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
		own.column, own.column, pol.ErrorCode, pol.ErrorMessage,
	))
}

// generateDualPrivacyValidator writes a validator that checks two players' settings.
func generateDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck, tableParts []string) {
	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str, %s: str) -> PolicyResult:\n",
		pol.PolicyName, dual.first.lookupColumn, dual.second.lookupColumn,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))

	tableFQN := strings.Join(tableParts, ".")

	// First player check.
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE player_id = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		dual.first.flagColumn, tableFQN, dual.first.lookupColumn,
	))
	buf.WriteString(fmt.Sprintf(
		"    if not row or not row[\"%s\"]:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n",
		dual.first.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second player check.
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE player_id = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		dual.second.flagColumn, tableFQN, dual.second.lookupColumn,
	))
	buf.WriteString(fmt.Sprintf(
		"    if not row or not row[\"%s\"]:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
			"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
		dual.second.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))
}

// pythonHeader returns the standard header for generated Python files.
func pythonHeader(schemaName string) string {
	var sb strings.Builder
	sb.WriteString("# Generated by pgdesign -- do not edit manually.\n")
	sb.WriteString("# Regenerate with: pgdesign codegen --lang python <schema-files>\n")
	if schemaName != "" {
		sb.WriteString(fmt.Sprintf("# Schema: %s\n", schemaName))
	}
	sb.WriteString(`
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True, slots=True)
class PolicyResult:
    """Result of a policy pre-check."""

    ok: bool
    code: str = ""
    message: str = ""
`)
	return sb.String()
}
