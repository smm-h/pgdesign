package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// TSValidatorGenerator generates TypeScript validator functions for RLS policies.
type TSValidatorGenerator struct{}

// Generate produces a TypeScript file with validator functions for all eligible
// policies in the schema.
func (g *TSValidatorGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	all := ExtractPolicies(schema)
	generatable, filterDiags := FilterGeneratable(all)

	var diags []diagnostic.Diagnostic
	diags = append(diags, filterDiags...)

	if len(generatable) == 0 {
		return []byte(tsHeader(schema.Name) + "\n// No generatable policies found.\n"), diags
	}

	var buf bytes.Buffer
	buf.WriteString(tsHeader(schema.Name))

	for i, pol := range generatable {
		if i > 0 {
			buf.WriteString("\n")
		}

		ast := pol.AST

		// Check for OR-compound first (before individual pattern detection),
		// since individual detectors would partially match an OR compound.
		if orComp := detectOrCompound(ast); orComp != nil {
			tsOrOwnershipExistsValidator(&buf, pol, orComp)
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
			tsDualPrivacyValidator(&buf, pol, dual)
		} else if len(existsLookups) == 1 {
			check := &privacyCheck{
				joinColumn:   existsLookups[0].joinColumn,
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumns:  existsLookups[0].flagColumns,
			}
			tsPrivacyValidator(&buf, pol, check, existsLookups[0].tableParts, existsLookups[0].negated)
		} else if own := detectOwnership(ast); own != nil {
			tsOwnershipValidator(&buf, pol, own)
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

// tsOwnershipValidator writes a pure ID-comparison validator in TypeScript.
func tsOwnershipValidator(buf *bytes.Buffer, pol PolicyContext, own *ownershipCheck) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"export function %s(%s: string, target_%s: string): PolicyResult {\n",
		pol.ErrorMessage, funcName, own.column, own.column,
	))
	buf.WriteString(fmt.Sprintf(
		"\tif (%s !== target_%s) {\n"+
			"\t\treturn { ok: false, code: %q, message: %q }\n"+
			"\t}\n"+
			"\treturn { ok: true, code: \"\", message: \"\" }\n"+
			"}\n",
		own.column, own.column, pol.ErrorCode, pol.ErrorMessage,
	))
}

// tsPrivacyValidator writes a single-player privacy check validator in TypeScript.
// When negated is true (NOT EXISTS), the logic is inverted: the policy fails
// when the flag IS set, rather than when it is not set.
func tsPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck, tableParts []string, negated bool) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	paramName := check.lookupColumn
	tableFQN := strings.Join(tableParts, ".")
	selectCols := strings.Join(check.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"export async function %s(client: PoolClient, %s: string): Promise<PolicyResult> {\n",
		pol.ErrorMessage, funcName, paramName,
	))
	buf.WriteString(fmt.Sprintf(
		"\tconst result = await client.query(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t[%s]\n"+
			"\t)\n",
		selectCols, tableFQN, check.joinColumn, paramName,
	))

	if negated {
		// NOT EXISTS: fail when ALL flags are set.
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, "result.rows[0]."+flag)
		}
		cond := strings.Join(parts, " && ")
		buf.WriteString(fmt.Sprintf(
			"\tif (result.rows.length > 0 && %s) {\n"+
				"\t\treturn { ok: false, code: %q, message: %q }\n"+
				"\t}\n"+
				"\treturn { ok: true, code: \"\", message: \"\" }\n"+
				"}\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	} else {
		// EXISTS: fail when row missing or ANY flag is missing.
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, "!result.rows[0]."+flag)
		}
		cond := strings.Join(parts, " || ")
		buf.WriteString(fmt.Sprintf(
			"\tif (result.rows.length === 0 || %s) {\n"+
				"\t\treturn { ok: false, code: %q, message: %q }\n"+
				"\t}\n"+
				"\treturn { ok: true, code: \"\", message: \"\" }\n"+
				"}\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	}
}

// tsOrOwnershipExistsValidator writes a validator for ownership OR exists-lookup in TypeScript.
// Returns ok=true if the ownership check passes (short-circuit) or if the
// exists-lookup check passes. Returns failure only when neither branch passes.
func tsOrOwnershipExistsValidator(buf *bytes.Buffer, pol PolicyContext, orComp *orCompound) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	col := orComp.ownership.column
	tableFQN := strings.Join(orComp.existsLookup.tableParts, ".")
	selectCols := strings.Join(orComp.existsLookup.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"export async function %s(client: PoolClient, %s: string, target_%s: string): Promise<PolicyResult> {\n",
		pol.ErrorMessage, funcName, col, col,
	))

	// Ownership check (cheap, no DB query).
	buf.WriteString(fmt.Sprintf(
		"\t// Ownership check (cheap, no DB query).\n"+
			"\tif (%s === target_%s) {\n"+
			"\t\treturn { ok: true, code: \"\", message: \"\" }\n"+
			"\t}\n",
		col, col,
	))

	// Exists-lookup fallback.
	buf.WriteString(fmt.Sprintf(
		"\t// Exists-lookup fallback.\n"+
			"\tconst result = await client.query(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t[target_%s]\n"+
			"\t)\n",
		selectCols, tableFQN, orComp.existsLookup.joinColumn, col,
	))

	var flagParts []string
	for _, flag := range orComp.existsLookup.flagColumns {
		flagParts = append(flagParts, "result.rows[0]."+flag)
	}
	flagCond := strings.Join(flagParts, " && ")
	buf.WriteString(fmt.Sprintf(
		"\tif (result.rows.length > 0 && %s) {\n"+
			"\t\treturn { ok: true, code: \"\", message: \"\" }\n"+
			"\t}\n"+
			"\treturn { ok: false, code: %q, message: %q }\n"+
			"}\n",
		flagCond, pol.ErrorCode, pol.ErrorMessage,
	))
}

// tsDualPrivacyValidator writes a validator that checks two players' settings in TypeScript.
func tsDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck) {
	funcName := "check" + toPascalCase(pol.PolicyName)
	firstTableFQN := strings.Join(dual.first.tableParts, ".")
	secondTableFQN := strings.Join(dual.second.tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"export async function %s(client: PoolClient, %s: string, %s: string): Promise<PolicyResult> {\n",
		pol.ErrorMessage, funcName, dual.first.lookupColumn, dual.second.lookupColumn,
	))

	// First check.
	firstSelectCols := strings.Join(dual.first.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"\t// First check.\n"+
			"\tconst result1 = await client.query(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t[%s]\n"+
			"\t)\n",
		firstSelectCols, firstTableFQN, dual.first.joinColumn, dual.first.lookupColumn,
	))

	var firstParts []string
	for _, flag := range dual.first.flagColumns {
		firstParts = append(firstParts, "!result1.rows[0]."+flag)
	}
	firstCond := strings.Join(firstParts, " || ")
	buf.WriteString(fmt.Sprintf(
		"\tif (result1.rows.length === 0 || %s) {\n"+
			"\t\treturn { ok: false, code: %q, message: %q }\n"+
			"\t}\n",
		firstCond, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second check.
	secondSelectCols := strings.Join(dual.second.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"\t// Second check.\n"+
			"\tconst result2 = await client.query(\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t[%s]\n"+
			"\t)\n",
		secondSelectCols, secondTableFQN, dual.second.joinColumn, dual.second.lookupColumn,
	))

	var secondParts []string
	for _, flag := range dual.second.flagColumns {
		secondParts = append(secondParts, "!result2.rows[0]."+flag)
	}
	secondCond := strings.Join(secondParts, " || ")
	buf.WriteString(fmt.Sprintf(
		"\tif (result2.rows.length === 0 || %s) {\n"+
			"\t\treturn { ok: false, code: %q, message: %q }\n"+
			"\t}\n"+
			"\treturn { ok: true, code: \"\", message: \"\" }\n"+
			"}\n",
		secondCond, pol.ErrorCode, pol.ErrorMessage,
	))
}

// tsHeader returns the standard header for generated TypeScript validator files.
func tsHeader(schemaName string) string {
	var sb strings.Builder
	sb.WriteString("// Generated by pgdesign -- do not edit manually.\n")
	sb.WriteString("// Regenerate with: pgdesign codegen --lang ts <schema-files>\n")
	if schemaName != "" {
		sb.WriteString(fmt.Sprintf("// Schema: %s\n", schemaName))
	}
	sb.WriteString(`
import { PoolClient } from "pg"

export interface PolicyResult {
	ok: boolean
	code: string
	message: string
}
`)
	return sb.String()
}
