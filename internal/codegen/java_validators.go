package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// JavaValidatorGenerator generates Java validator functions for RLS policies.
type JavaValidatorGenerator struct{}

// Generate produces a Java file with validator functions for all eligible
// policies in the schema.
func (g *JavaValidatorGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	all := ExtractPolicies(schema)
	generatable, filterDiags := FilterGeneratable(all)

	var diags []diagnostic.Diagnostic
	diags = append(diags, filterDiags...)

	if len(generatable) == 0 {
		return []byte(javaHeader(schema.Name) + "\n// No generatable policies found.\n}\n"), diags
	}

	var buf bytes.Buffer
	buf.WriteString(javaHeader(schema.Name))

	for i, pol := range generatable {
		if i > 0 {
			buf.WriteString("\n")
		}

		ast := pol.AST

		// Check for OR-compound first (before individual pattern detection),
		// since individual detectors would partially match an OR compound.
		if orComp := detectOrCompound(ast); orComp != nil {
			javaOrOwnershipExistsValidator(&buf, pol, orComp)
			continue
		}

		existsLookups := detectAllExistsLookups(ast)

		if len(existsLookups) >= 2 {
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
			javaDualPrivacyValidator(&buf, pol, dual)
		} else if len(existsLookups) == 1 {
			check := &privacyCheck{
				joinColumn:   existsLookups[0].joinColumn,
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumns:  existsLookups[0].flagColumns,
			}
			javaPrivacyValidator(&buf, pol, check, existsLookups[0].tableParts, existsLookups[0].negated)
		} else if own := detectOwnership(ast); own != nil {
			javaOwnershipValidator(&buf, pol, own)
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

	buf.WriteString("}\n")

	return buf.Bytes(), diags
}

// javaOwnershipValidator writes a pure ID-comparison validator in Java.
func javaOwnershipValidator(buf *bytes.Buffer, pol PolicyContext, own *ownershipCheck) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"public static PolicyResult %s(String %s, String target_%s) {\n",
		pol.ErrorMessage, funcName, own.column, own.column,
	))
	buf.WriteString(fmt.Sprintf(
		"\tif (!%s.equals(target_%s)) {\n"+
			"\t\treturn new PolicyResult(false, %q, %q);\n"+
			"\t}\n"+
			"\treturn new PolicyResult(true, \"\", \"\");\n"+
			"}\n",
		own.column, own.column, pol.ErrorCode, pol.ErrorMessage,
	))
}

// javaPrivacyValidator writes a single-player privacy check validator in Java.
// When negated is true (NOT EXISTS), the logic is inverted: the policy fails
// when the flag IS set, rather than when it is not set.
func javaPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck, tableParts []string, negated bool) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	paramName := check.lookupColumn
	tableFQN := strings.Join(tableParts, ".")
	selectCols := strings.Join(check.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"public static PolicyResult %s(Connection conn, String %s) throws SQLException {\n",
		pol.ErrorMessage, funcName, paramName,
	))
	buf.WriteString(fmt.Sprintf(
		"\tPreparedStatement stmt = conn.prepareStatement(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = ?\"\n"+
			"\t);\n"+
			"\tstmt.setString(1, %s);\n"+
			"\tResultSet rs = stmt.executeQuery();\n",
		selectCols, tableFQN, check.joinColumn, paramName,
	))

	if negated {
		// NOT EXISTS: fail when ALL flags are set.
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, fmt.Sprintf("rs.getBoolean(%q)", flag))
		}
		cond := strings.Join(parts, " && ")
		buf.WriteString(fmt.Sprintf(
			"\tif (rs.next() && %s) {\n"+
				"\t\treturn new PolicyResult(false, %q, %q);\n"+
				"\t}\n"+
				"\treturn new PolicyResult(true, \"\", \"\");\n"+
				"}\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	} else {
		// EXISTS: fail when row missing or ANY flag is missing.
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, fmt.Sprintf("!rs.getBoolean(%q)", flag))
		}
		cond := strings.Join(parts, " || ")
		buf.WriteString(fmt.Sprintf(
			"\tif (!rs.next() || %s) {\n"+
				"\t\treturn new PolicyResult(false, %q, %q);\n"+
				"\t}\n"+
				"\treturn new PolicyResult(true, \"\", \"\");\n"+
				"}\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	}
}

// javaOrOwnershipExistsValidator writes a validator for ownership OR exists-lookup in Java.
// Returns OK if the ownership check passes (short-circuit) or if the
// exists-lookup check passes. Returns failure only when neither branch passes.
func javaOrOwnershipExistsValidator(buf *bytes.Buffer, pol PolicyContext, orComp *orCompound) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	col := orComp.ownership.column
	tableFQN := strings.Join(orComp.existsLookup.tableParts, ".")
	selectCols := strings.Join(orComp.existsLookup.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"public static PolicyResult %s(Connection conn, String %s, String target_%s) throws SQLException {\n",
		pol.ErrorMessage, funcName, col, col,
	))

	// Ownership check (cheap, no DB query).
	buf.WriteString(fmt.Sprintf(
		"\t// Ownership check (cheap, no DB query).\n"+
			"\tif (%s.equals(target_%s)) {\n"+
			"\t\treturn new PolicyResult(true, \"\", \"\");\n"+
			"\t}\n",
		col, col,
	))

	// Exists-lookup fallback.
	buf.WriteString(fmt.Sprintf(
		"\t// Exists-lookup fallback.\n"+
			"\tPreparedStatement stmt = conn.prepareStatement(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = ?\"\n"+
			"\t);\n"+
			"\tstmt.setString(1, target_%s);\n"+
			"\tResultSet rs = stmt.executeQuery();\n",
		selectCols, tableFQN, orComp.existsLookup.joinColumn, col,
	))

	var flagParts []string
	for _, flag := range orComp.existsLookup.flagColumns {
		flagParts = append(flagParts, fmt.Sprintf("rs.getBoolean(%q)", flag))
	}
	flagCond := strings.Join(flagParts, " && ")
	buf.WriteString(fmt.Sprintf(
		"\tif (rs.next() && %s) {\n"+
			"\t\treturn new PolicyResult(true, \"\", \"\");\n"+
			"\t}\n"+
			"\treturn new PolicyResult(false, %q, %q);\n"+
			"}\n",
		flagCond, pol.ErrorCode, pol.ErrorMessage,
	))
}

// javaDualPrivacyValidator writes a validator that checks two players' settings in Java.
func javaDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	firstTableFQN := strings.Join(dual.first.tableParts, ".")
	secondTableFQN := strings.Join(dual.second.tableParts, ".")
	firstSelectCols := strings.Join(dual.first.flagColumns, ", ")
	secondSelectCols := strings.Join(dual.second.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"public static PolicyResult %s(Connection conn, String %s, String %s) throws SQLException {\n",
		pol.ErrorMessage, funcName, dual.first.lookupColumn, dual.second.lookupColumn,
	))

	// First check.
	buf.WriteString(fmt.Sprintf(
		"\t// First check.\n"+
			"\tPreparedStatement stmt1 = conn.prepareStatement(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = ?\"\n"+
			"\t);\n"+
			"\tstmt1.setString(1, %s);\n"+
			"\tResultSet rs1 = stmt1.executeQuery();\n",
		firstSelectCols, firstTableFQN, dual.first.joinColumn, dual.first.lookupColumn,
	))

	var firstParts []string
	for _, flag := range dual.first.flagColumns {
		firstParts = append(firstParts, fmt.Sprintf("!rs1.getBoolean(%q)", flag))
	}
	firstCond := strings.Join(firstParts, " || ")
	buf.WriteString(fmt.Sprintf(
		"\tif (!rs1.next() || %s) {\n"+
			"\t\treturn new PolicyResult(false, %q, %q);\n"+
			"\t}\n",
		firstCond, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second check.
	buf.WriteString(fmt.Sprintf(
		"\t// Second check.\n"+
			"\tPreparedStatement stmt2 = conn.prepareStatement(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = ?\"\n"+
			"\t);\n"+
			"\tstmt2.setString(1, %s);\n"+
			"\tResultSet rs2 = stmt2.executeQuery();\n",
		secondSelectCols, secondTableFQN, dual.second.joinColumn, dual.second.lookupColumn,
	))

	var secondParts []string
	for _, flag := range dual.second.flagColumns {
		secondParts = append(secondParts, fmt.Sprintf("!rs2.getBoolean(%q)", flag))
	}
	secondCond := strings.Join(secondParts, " || ")
	buf.WriteString(fmt.Sprintf(
		"\tif (!rs2.next() || %s) {\n"+
			"\t\treturn new PolicyResult(false, %q, %q);\n"+
			"\t}\n"+
			"\treturn new PolicyResult(true, \"\", \"\");\n"+
			"}\n",
		secondCond, pol.ErrorCode, pol.ErrorMessage,
	))
}

// javaHeader returns the standard header for generated Java validator files.
func javaHeader(schemaName string) string {
	var sb strings.Builder
	sb.WriteString("// Generated by pgdesign -- do not edit manually.\n")
	sb.WriteString("// Regenerate with: pgdesign codegen --lang java <schema-files>\n")
	if schemaName != "" {
		sb.WriteString(fmt.Sprintf("// Schema: %s\n", schemaName))
	}
	sb.WriteString(`
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;

public class Validators {

public static class PolicyResult {
	public final boolean ok;
	public final String code;
	public final String message;

	public PolicyResult(boolean ok, String code, String message) {
		this.ok = ok;
		this.code = code;
		this.message = message;
	}
}
`)
	return sb.String()
}
