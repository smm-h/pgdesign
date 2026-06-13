package codegen

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

var (
	rePrivacyCheck = regexp.MustCompile(
		`player_privacy_settings\s+WHERE\s+player_id\s*=\s*(?:[a-z_]+\.)?([a-z_]+)\s+AND\s+([a-z_]+)\s*=\s*true`,
	)
	reOwnershipCheck = regexp.MustCompile(
		`([a-z_]+)::text\s*=\s*current_setting\('app\.player_id'\)`,
	)
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

// parsePrivacyCheck extracts the privacy settings reference from a policy
// expression. It looks for the pattern:
//
//	EXISTS (SELECT 1 FROM <schema>.player_privacy_settings WHERE player_id = <col> AND <flag> = true)
//
// Returns nil if the expression doesn't match.
func parsePrivacyCheck(expr string) *privacyCheck {
	// Match: player_id = <something> AND <flag_column> = true
	// The <something> can be a bare column name, or a qualified name like game_comment.player_id.
	m := rePrivacyCheck.FindStringSubmatch(expr)
	if m == nil {
		return nil
	}
	return &privacyCheck{
		lookupColumn: m[1],
		flagColumn:   m[2],
	}
}

// parseOwnershipCheck detects the ownership pattern:
//
//	<column>::text = current_setting('app.player_id')
//
// This must be the ONLY substantive condition (no AND with privacy checks).
// Returns nil if the expression doesn't match or contains other patterns.
func parseOwnershipCheck(expr string) *ownershipCheck {
	// Skip if expression also references player_privacy_settings -- those are
	// privacy checks or dual-player checks, not pure ownership.
	if strings.Contains(expr, "player_privacy_settings") {
		return nil
	}
	m := reOwnershipCheck.FindStringSubmatch(expr)
	if m == nil {
		return nil
	}
	return &ownershipCheck{
		column: m[1],
	}
}

// parseDualPrivacyCheck detects policies with two references to
// player_privacy_settings, each checking a different player's setting.
// Returns nil if fewer than two references are found.
func parseDualPrivacyCheck(expr string) *dualPrivacyCheck {
	matches := rePrivacyCheck.FindAllStringSubmatch(expr, -1)
	if len(matches) < 2 {
		return nil
	}
	return &dualPrivacyCheck{
		first: privacyCheck{
			lookupColumn: matches[0][1],
			flagColumn:   matches[0][2],
		},
		second: privacyCheck{
			lookupColumn: matches[1][1],
			flagColumn:   matches[1][2],
		},
	}
}

// Generate produces a Python file with async validator functions for all
// eligible policies in the schema.
func (g *PythonGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	all := ExtractPolicies(schema)
	generatable := FilterGeneratable(all)

	if len(generatable) == 0 {
		return []byte(pythonHeader(schema.Name) + "\n# No generatable policies found.\n"), nil
	}

	var buf bytes.Buffer
	var diags []diagnostic.Diagnostic
	buf.WriteString(pythonHeader(schema.Name))

	for i, pol := range generatable {
		expr := pol.WithCheck
		if expr == "" {
			expr = pol.Using
		}

		if i > 0 {
			buf.WriteString("\n")
		}

		// Try patterns in order: dual-player first (most specific), then
		// single privacy check, then ownership.
		if dual := parseDualPrivacyCheck(expr); dual != nil {
			generateDualPrivacyValidator(&buf, pol, dual)
		} else if check := parsePrivacyCheck(expr); check != nil {
			generatePrivacyValidator(&buf, pol, check)
		} else if own := parseOwnershipCheck(expr); own != nil {
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
func generatePrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck) {
	paramName := check.lookupColumn

	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str) -> PolicyResult:\n",
		pol.PolicyName, paramName,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))

	tableFQN := "player_privacy_settings"
	if pol.SchemaName != "" {
		tableFQN = pol.SchemaName + ".player_privacy_settings"
	}

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
func generateDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck) {
	buf.WriteString(fmt.Sprintf(
		"\nasync def check_%s(conn, %s: str, %s: str) -> PolicyResult:\n",
		pol.PolicyName, dual.first.lookupColumn, dual.second.lookupColumn,
	))
	buf.WriteString(fmt.Sprintf(
		"    \"\"\"%s\"\"\"\n", pol.ErrorMessage,
	))

	tableFQN := "player_privacy_settings"
	if pol.SchemaName != "" {
		tableFQN = pol.SchemaName + ".player_privacy_settings"
	}

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
