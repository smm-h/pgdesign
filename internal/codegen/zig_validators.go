package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// ZigValidatorGenerator generates Zig validator functions for RLS policies.
type ZigValidatorGenerator struct{}

// Generate produces a Zig file with validator functions for all eligible
// policies in the schema.
func (g *ZigValidatorGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	all := ExtractPolicies(schema)
	generatable, filterDiags := FilterGeneratable(all)

	var diags []diagnostic.Diagnostic
	diags = append(diags, filterDiags...)

	if len(generatable) == 0 {
		return []byte(zigHeader(schema.Name) + "\n// No generatable policies found.\n"), diags
	}

	var buf bytes.Buffer
	buf.WriteString(zigHeader(schema.Name))

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
				"\n// Skipped %s: could not parse expression\n",
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
			zigDualPrivacyValidator(&buf, pol, dual, existsLookups[0].tableParts)
		} else if len(existsLookups) == 1 {
			check := &privacyCheck{
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumn:   existsLookups[0].flagColumn,
			}
			zigPrivacyValidator(&buf, pol, check, existsLookups[0].tableParts)
		} else if own := detectOwnership(ast); own != nil {
			zigOwnershipValidator(&buf, pol, own)
		} else {
			buf.WriteString(fmt.Sprintf(
				"\n// Skipped %s: could not parse expression into a known pattern\n",
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

// zigOwnershipValidator writes a pure ID-comparison validator in Zig.
func zigOwnershipValidator(buf *bytes.Buffer, pol PolicyContext, own *ownershipCheck) {
	buf.WriteString(fmt.Sprintf(
		"\n/// %s\n"+
			"pub fn check_%s(%s: []const u8, target_%s: []const u8) PolicyResult {\n",
		pol.ErrorMessage, pol.PolicyName, own.column, own.column,
	))
	buf.WriteString(fmt.Sprintf(
		"    if (!std.mem.eql(u8, %s, target_%s)) {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    }\n"+
			"    return .{ .ok = true };\n"+
			"}\n",
		own.column, own.column, pol.ErrorCode, pol.ErrorMessage,
	))
}

// zigPrivacyValidator writes a single-player privacy check validator in Zig.
func zigPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck, tableParts []string) {
	paramName := check.lookupColumn
	tableFQN := strings.Join(tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"\n/// %s\n"+
			"pub fn check_%s(conn: *pg.Conn, %s: []const u8) !PolicyResult {\n",
		pol.ErrorMessage, pol.PolicyName, paramName,
	))
	buf.WriteString(fmt.Sprintf(
		"    const row = conn.queryRow(\n"+
			"        \"SELECT %s FROM %s WHERE player_id = $1\",\n"+
			"        .{%s},\n"+
			"    ) catch {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    };\n",
		check.flagColumn, tableFQN, paramName, pol.ErrorCode, pol.ErrorMessage,
	))
	buf.WriteString(fmt.Sprintf(
		"    if (!row.get(bool, %q)) {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    }\n"+
			"    return .{ .ok = true };\n"+
			"}\n",
		check.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))
}

// zigDualPrivacyValidator writes a validator that checks two players' settings in Zig.
func zigDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck, tableParts []string) {
	tableFQN := strings.Join(tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"\n/// %s\n"+
			"pub fn check_%s(conn: *pg.Conn, %s: []const u8, %s: []const u8) !PolicyResult {\n",
		pol.ErrorMessage, pol.PolicyName, dual.first.lookupColumn, dual.second.lookupColumn,
	))

	// First player check.
	buf.WriteString(fmt.Sprintf(
		"    const row1 = conn.queryRow(\n"+
			"        \"SELECT %s FROM %s WHERE player_id = $1\",\n"+
			"        .{%s},\n"+
			"    ) catch {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    };\n",
		dual.first.flagColumn, tableFQN, dual.first.lookupColumn, pol.ErrorCode, pol.ErrorMessage,
	))
	buf.WriteString(fmt.Sprintf(
		"    if (!row1.get(bool, %q)) {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    }\n",
		dual.first.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second player check.
	buf.WriteString(fmt.Sprintf(
		"    const row2 = conn.queryRow(\n"+
			"        \"SELECT %s FROM %s WHERE player_id = $1\",\n"+
			"        .{%s},\n"+
			"    ) catch {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    };\n",
		dual.second.flagColumn, tableFQN, dual.second.lookupColumn, pol.ErrorCode, pol.ErrorMessage,
	))
	buf.WriteString(fmt.Sprintf(
		"    if (!row2.get(bool, %q)) {\n"+
			"        return .{ .ok = false, .code = %q, .message = %q };\n"+
			"    }\n"+
			"    return .{ .ok = true };\n"+
			"}\n",
		dual.second.flagColumn, pol.ErrorCode, pol.ErrorMessage,
	))
}

// zigHeader returns the standard header for generated Zig files.
func zigHeader(schemaName string) string {
	var sb strings.Builder
	sb.WriteString("// Generated by pgdesign -- do not edit manually.\n")
	sb.WriteString("// Regenerate with: pgdesign codegen --lang zig <schema-files>\n")
	if schemaName != "" {
		sb.WriteString(fmt.Sprintf("// Schema: %s\n", schemaName))
	}
	sb.WriteString(`
const std = @import("std");
const pg = @import("pg");

pub const PolicyResult = struct {
    ok: bool,
    code: []const u8 = "",
    message: []const u8 = "",
};
`)
	return sb.String()
}
