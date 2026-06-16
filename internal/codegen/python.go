package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
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
		if i > 0 {
			buf.WriteString("\n")
		}

		ast := pol.AST

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
					flagColumns:  existsLookups[0].flagColumns,
				},
				second: privacyCheck{
					tableParts:   existsLookups[1].tableParts,
					joinColumn:   existsLookups[1].joinColumn,
					lookupColumn: existsLookups[1].lookupColumn,
					flagColumns:  existsLookups[1].flagColumns,
				},
			}
			generateDualPrivacyValidator(&buf, pol, dual)
		} else if len(existsLookups) == 1 {
			// Single exists-lookup pattern
			check := &privacyCheck{
				joinColumn:   existsLookups[0].joinColumn,
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumns:  existsLookups[0].flagColumns,
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
	selectCols := strings.Join(check.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		selectCols, tableFQN, check.joinColumn, paramName,
	))
	if negated {
		// NOT EXISTS: fail when ALL flags are set
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, fmt.Sprintf("row[\"%s\"]", flag))
		}
		cond := "row and " + strings.Join(parts, " and ")
		buf.WriteString(fmt.Sprintf(
			"    if %s:\n"+
				"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
				"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	} else {
		// EXISTS: fail when ANY flag is missing
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, fmt.Sprintf("not row[\"%s\"]", flag))
		}
		cond := "not row or " + strings.Join(parts, " or ")
		buf.WriteString(fmt.Sprintf(
			"    if %s:\n"+
				"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
				"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
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
	selectCols := strings.Join(orComp.existsLookup.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        target_%s,\n"+
			"    )\n",
		selectCols, tableFQN, orComp.existsLookup.joinColumn, col,
	))
	var flagParts []string
	for _, flag := range orComp.existsLookup.flagColumns {
		flagParts = append(flagParts, fmt.Sprintf("row[\"%s\"]", flag))
	}
	flagCond := "row and " + strings.Join(flagParts, " and ")
	buf.WriteString(fmt.Sprintf(
		"    if %s:\n"+
			"        return PolicyResult(ok=True, code=\"\", message=\"\")\n"+
			"    return PolicyResult(ok=False, code=%q, message=%q)\n",
		flagCond, pol.ErrorCode, pol.ErrorMessage,
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
	firstSelectCols := strings.Join(dual.first.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		firstSelectCols, firstTableFQN, dual.first.joinColumn, dual.first.lookupColumn,
	))
	var firstParts []string
	for _, flag := range dual.first.flagColumns {
		firstParts = append(firstParts, fmt.Sprintf("not row[\"%s\"]", flag))
	}
	firstCond := "not row or " + strings.Join(firstParts, " or ")
	buf.WriteString(fmt.Sprintf(
		"    if %s:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n",
		firstCond, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second player check.
	secondSelectCols := strings.Join(dual.second.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"    row = await conn.fetchrow(\n"+
			"        \"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"        %s,\n"+
			"    )\n",
		secondSelectCols, secondTableFQN, dual.second.joinColumn, dual.second.lookupColumn,
	))
	var secondParts []string
	for _, flag := range dual.second.flagColumns {
		secondParts = append(secondParts, fmt.Sprintf("not row[\"%s\"]", flag))
	}
	secondCond := "not row or " + strings.Join(secondParts, " or ")
	buf.WriteString(fmt.Sprintf(
		"    if %s:\n"+
			"        return PolicyResult(ok=False, code=%q, message=%q)\n"+
			"    return PolicyResult(ok=True, code=\"\", message=\"\")\n",
		secondCond, pol.ErrorCode, pol.ErrorMessage,
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
