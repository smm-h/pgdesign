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
