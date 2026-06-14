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

		// Check for OR-compound first (before individual pattern detection),
		// since individual detectors would partially match an OR compound.
		if orComp := detectOrCompound(ast); orComp != nil {
			generateOrOwnershipExistsValidator(&buf, pol, orComp)
			continue
		}

		existsLookups := detectAllExistsLookups(ast)

		if len(existsLookups) >= 2 {
			// Dual/multi exists-lookup pattern
			dual := &dualPrivacyCheck{
				first: privacyCheck{
					tableParts:   existsLookups[0].tableParts,
					joinColumn:   existsLookups[0].joinColumn,
					lookupColumn: existsLookups[0].lookupColumn,
					flagColumn:   existsLookups[0].flagColumn,
				},
				second: privacyCheck{
					tableParts:   existsLookups[1].tableParts,
					joinColumn:   existsLookups[1].joinColumn,
					lookupColumn: existsLookups[1].lookupColumn,
					flagColumn:   existsLookups[1].flagColumn,
				},
			}
			generateDualPrivacyValidator(&buf, pol, dual)
		} else if len(existsLookups) == 1 {
			// Single exists-lookup pattern
			check := &privacyCheck{
				joinColumn:   existsLookups[0].joinColumn,
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumn:   existsLookups[0].flagColumn,
			}
			generatePrivacyValidator(&buf, pol, check, existsLookups[0].tableParts, existsLookups[0].negated)
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
// When negated is true (NOT EXISTS), the logic is inverted: the policy fails
// when the flag IS set, rather than when it is not set.
func generatePrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck, tableParts []string, negated bool) {
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
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		check.flagColumn, tableFQN, check.joinColumn, paramName,
	))
	if negated {
		buf.WriteString(fmt.Sprintf(
			"    if row and row[\"%s\"]:\n"+
				"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
				"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
			check.flagColumn, pol.ErrorCode, pol.ErrorMessage,
		))
	} else {
		buf.WriteString(fmt.Sprintf(
			"    if not row or not row[\"%s\"]:\n"+
				"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
				"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
			check.flagColumn, pol.ErrorCode, pol.ErrorMessage,
		))
	}
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

// generateOrOwnershipExistsValidator writes a validator for ownership OR exists-lookup.
// Returns ok=True if the ownership check passes (short-circuit) or if the
// exists-lookup check passes. Returns failure only when neither branch passes.
func generateOrOwnershipExistsValidator(buf *bytes.Buffer, pol PolicyContext, orComp *orCompound) {
	col := orComp.ownership.column
	tableFQN := strings.Join(orComp.existsLookup.tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str, target_%s: str) -> PolicyResult:\n",
		pol.PolicyName, col, col,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))
	// Ownership check (cheap, no DB query).
	buf.WriteString(fmt.Sprintf(
		"    if %s == target_%s:\n"+
			"        return PolicyResult(ok=True, code=\"\", message=\"\")\n",
		col, col,
	))
	// Exists-lookup fallback.
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        target_%s,\n"+
			"    )\n",
		orComp.existsLookup.flagColumn, tableFQN, orComp.existsLookup.joinColumn, col,
	))
	buf.WriteString(fmt.Sprintf(
		"    if row and row[\"%s\"]:\n"+
			"        return PolicyResult(ok=True, code=\"\", message=\"\")\n"+
			"    return PolicyResult(ok=False, code=%q, message=%q)\n",
		orComp.existsLookup.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))
}

// generateDualPrivacyValidator writes a validator that checks two players' settings.
func generateDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck) {
	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str, %s: str) -> PolicyResult:\n",
		pol.PolicyName, dual.first.lookupColumn, dual.second.lookupColumn,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))

	firstTableFQN := strings.Join(dual.first.tableParts, ".")
	secondTableFQN := strings.Join(dual.second.tableParts, ".")

	// First player check.
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		dual.first.flagColumn, firstTableFQN, dual.first.joinColumn, dual.first.lookupColumn,
	))
	buf.WriteString(fmt.Sprintf(
		"    if not row or not row[\"%s\"]:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n",
		dual.first.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second player check.
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		dual.second.flagColumn, secondTableFQN, dual.second.joinColumn, dual.second.lookupColumn,
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
