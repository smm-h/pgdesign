package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// GoValidatorGenerator generates Go validator functions for RLS policies.
type GoValidatorGenerator struct{}

// Generate produces a Go file with validator functions for all eligible
// policies in the schema.
func (g *GoValidatorGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	all := ExtractPolicies(schema)
	generatable, filterDiags := FilterGeneratable(all)

	var diags []diagnostic.Diagnostic
	diags = append(diags, filterDiags...)

	if len(generatable) == 0 {
		return []byte(goHeader(schema.Name) + "\n// No generatable policies found.\n"), diags
	}

	var buf bytes.Buffer
	buf.WriteString(goHeader(schema.Name))

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

		// Check for OR-compound first (before individual pattern detection),
		// since individual detectors would partially match an OR compound.
		if orComp := detectOrCompound(ast); orComp != nil {
			goOrOwnershipExistsValidator(&buf, pol, orComp)
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
			goDualPrivacyValidator(&buf, pol, dual)
		} else if len(existsLookups) == 1 {
			check := &privacyCheck{
				joinColumn:   existsLookups[0].joinColumn,
				lookupColumn: existsLookups[0].lookupColumn,
				flagColumns:  existsLookups[0].flagColumns,
			}
			goPrivacyValidator(&buf, pol, check, existsLookups[0].tableParts, existsLookups[0].negated)
		} else if own := detectOwnership(ast); own != nil {
			goOwnershipValidator(&buf, pol, own)
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

// goOwnershipValidator writes a pure ID-comparison validator in Go.
func goOwnershipValidator(buf *bytes.Buffer, pol PolicyContext, own *ownershipCheck) {
	funcName := "Check" + toPascalCase(pol.PolicyName)
	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"func %s(%s string, target_%s string) PolicyResult {\n",
		pol.ErrorMessage, funcName, own.column, own.column,
	))
	buf.WriteString(fmt.Sprintf(
		"\tif %s != target_%s {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}\n"+
			"\t}\n"+
			"\treturn PolicyResult{OK: true}\n"+
			"}\n",
		own.column, own.column, pol.ErrorCode, pol.ErrorMessage,
	))
}

// goPrivacyValidator writes a single-player privacy check validator in Go.
// When negated is true (NOT EXISTS), the logic is inverted: the policy fails
// when the flag IS set, rather than when it is not set.
func goPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, check *privacyCheck, tableParts []string, negated bool) {
	funcName := "Check" + toPascalCase(pol.PolicyName)
	paramName := check.lookupColumn
	tableFQN := strings.Join(tableParts, ".")
	selectCols := strings.Join(check.flagColumns, ", ")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"func %s(ctx context.Context, conn *pgx.Conn, %s string) (PolicyResult, error) {\n",
		pol.ErrorMessage, funcName, paramName,
	))

	// Declare flag variables.
	for _, flag := range check.flagColumns {
		buf.WriteString(fmt.Sprintf("\tvar %s bool\n", flag))
	}

	// Build scan args.
	var scanArgs []string
	for _, flag := range check.flagColumns {
		scanArgs = append(scanArgs, "&"+flag)
	}

	buf.WriteString(fmt.Sprintf(
		"\terr := conn.QueryRow(ctx,\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t%s,\n"+
			"\t).Scan(%s)\n",
		selectCols, tableFQN, check.joinColumn, paramName, strings.Join(scanArgs, ", "),
	))
	buf.WriteString(fmt.Sprintf(
		"\tif err != nil {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, fmt.Errorf(\"query failed: %%w\", err)\n"+
			"\t}\n",
		pol.ErrorCode, pol.ErrorMessage,
	))

	if negated {
		// NOT EXISTS: fail when ALL flags are set.
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, flag)
		}
		cond := strings.Join(parts, " && ")
		buf.WriteString(fmt.Sprintf(
			"\tif %s {\n"+
				"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, nil\n"+
				"\t}\n"+
				"\treturn PolicyResult{OK: true}, nil\n"+
				"}\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	} else {
		// EXISTS: fail when ANY flag is missing.
		var parts []string
		for _, flag := range check.flagColumns {
			parts = append(parts, "!"+flag)
		}
		cond := strings.Join(parts, " || ")
		buf.WriteString(fmt.Sprintf(
			"\tif %s {\n"+
				"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, nil\n"+
				"\t}\n"+
				"\treturn PolicyResult{OK: true}, nil\n"+
				"}\n",
			cond, pol.ErrorCode, pol.ErrorMessage,
		))
	}
}

// goOrOwnershipExistsValidator writes a validator for ownership OR exists-lookup in Go.
// Returns OK=true if the ownership check passes (short-circuit) or if the
// exists-lookup check passes. Returns failure only when neither branch passes.
func goOrOwnershipExistsValidator(buf *bytes.Buffer, pol PolicyContext, orComp *orCompound) {
	funcName := "Check" + toPascalCase(pol.PolicyName)
	col := orComp.ownership.column
	tableFQN := strings.Join(orComp.existsLookup.tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"func %s(ctx context.Context, conn *pgx.Conn, %s string, target_%s string) (PolicyResult, error) {\n",
		pol.ErrorMessage, funcName, col, col,
	))

	// Ownership check (cheap, no DB query).
	buf.WriteString(fmt.Sprintf(
		"\t// Ownership check (cheap, no DB query).\n"+
			"\tif %s == target_%s {\n"+
			"\t\treturn PolicyResult{OK: true}, nil\n"+
			"\t}\n",
		col, col,
	))

	// Exists-lookup fallback.
	buf.WriteString("\t// Exists-lookup fallback.\n")
	for _, flag := range orComp.existsLookup.flagColumns {
		buf.WriteString(fmt.Sprintf("\tvar %s bool\n", flag))
	}

	var scanArgs []string
	for _, flag := range orComp.existsLookup.flagColumns {
		scanArgs = append(scanArgs, "&"+flag)
	}

	selectCols := strings.Join(orComp.existsLookup.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"\terr := conn.QueryRow(ctx,\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\ttarget_%s,\n"+
			"\t).Scan(%s)\n",
		selectCols, tableFQN, orComp.existsLookup.joinColumn, col, strings.Join(scanArgs, ", "),
	))
	buf.WriteString(fmt.Sprintf(
		"\tif err != nil {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, fmt.Errorf(\"query failed: %%w\", err)\n"+
			"\t}\n",
		pol.ErrorCode, pol.ErrorMessage,
	))

	var flagParts []string
	for _, flag := range orComp.existsLookup.flagColumns {
		flagParts = append(flagParts, flag)
	}
	flagCond := strings.Join(flagParts, " && ")
	buf.WriteString(fmt.Sprintf(
		"\tif %s {\n"+
			"\t\treturn PolicyResult{OK: true}, nil\n"+
			"\t}\n"+
			"\treturn PolicyResult{OK: false, Code: %q, Message: %q}, nil\n"+
			"}\n",
		flagCond, pol.ErrorCode, pol.ErrorMessage,
	))
}

// goDualPrivacyValidator writes a validator that checks two players' settings in Go.
func goDualPrivacyValidator(buf *bytes.Buffer, pol PolicyContext, dual *dualPrivacyCheck) {
	funcName := "Check" + toPascalCase(pol.PolicyName)
	firstTableFQN := strings.Join(dual.first.tableParts, ".")
	secondTableFQN := strings.Join(dual.second.tableParts, ".")

	buf.WriteString(fmt.Sprintf(
		"\n// %s\n"+
			"func %s(ctx context.Context, conn *pgx.Conn, %s string, %s string) (PolicyResult, error) {\n",
		pol.ErrorMessage, funcName, dual.first.lookupColumn, dual.second.lookupColumn,
	))

	// First check.
	buf.WriteString("\t// First check.\n")
	for _, flag := range dual.first.flagColumns {
		buf.WriteString(fmt.Sprintf("\tvar %s bool\n", flag))
	}

	var firstScanArgs []string
	for _, flag := range dual.first.flagColumns {
		firstScanArgs = append(firstScanArgs, "&"+flag)
	}

	firstSelectCols := strings.Join(dual.first.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"\terr := conn.QueryRow(ctx,\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t%s,\n"+
			"\t).Scan(%s)\n",
		firstSelectCols, firstTableFQN, dual.first.joinColumn, dual.first.lookupColumn, strings.Join(firstScanArgs, ", "),
	))
	buf.WriteString(fmt.Sprintf(
		"\tif err != nil {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, fmt.Errorf(\"query failed: %%w\", err)\n"+
			"\t}\n",
		pol.ErrorCode, pol.ErrorMessage,
	))

	var firstParts []string
	for _, flag := range dual.first.flagColumns {
		firstParts = append(firstParts, "!"+flag)
	}
	firstCond := strings.Join(firstParts, " || ")
	buf.WriteString(fmt.Sprintf(
		"\tif %s {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, nil\n"+
			"\t}\n",
		firstCond, pol.ErrorCode, pol.ErrorMessage,
	))

	// Second check.
	buf.WriteString("\t// Second check.\n")
	for _, flag := range dual.second.flagColumns {
		buf.WriteString(fmt.Sprintf("\tvar %s bool\n", flag))
	}

	var secondScanArgs []string
	for _, flag := range dual.second.flagColumns {
		secondScanArgs = append(secondScanArgs, "&"+flag)
	}

	secondSelectCols := strings.Join(dual.second.flagColumns, ", ")
	buf.WriteString(fmt.Sprintf(
		"\terr = conn.QueryRow(ctx,\n"+
			"\t\t\"SELECT %s FROM %s WHERE %s = $1\",\n"+
			"\t\t%s,\n"+
			"\t).Scan(%s)\n",
		secondSelectCols, secondTableFQN, dual.second.joinColumn, dual.second.lookupColumn, strings.Join(secondScanArgs, ", "),
	))
	buf.WriteString(fmt.Sprintf(
		"\tif err != nil {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, fmt.Errorf(\"query failed: %%w\", err)\n"+
			"\t}\n",
		pol.ErrorCode, pol.ErrorMessage,
	))

	var secondParts []string
	for _, flag := range dual.second.flagColumns {
		secondParts = append(secondParts, "!"+flag)
	}
	secondCond := strings.Join(secondParts, " || ")
	buf.WriteString(fmt.Sprintf(
		"\tif %s {\n"+
			"\t\treturn PolicyResult{OK: false, Code: %q, Message: %q}, nil\n"+
			"\t}\n"+
			"\treturn PolicyResult{OK: true}, nil\n"+
			"}\n",
		secondCond, pol.ErrorCode, pol.ErrorMessage,
	))
}

// goHeader returns the standard header for generated Go validator files.
func goHeader(schemaName string) string {
	var sb strings.Builder
	sb.WriteString("// Generated by pgdesign -- do not edit manually.\n")
	sb.WriteString("// Regenerate with: pgdesign codegen --lang go <schema-files>\n")
	if schemaName != "" {
		sb.WriteString(fmt.Sprintf("// Schema: %s\n", schemaName))
	}
	sb.WriteString(`
package validators

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PolicyResult holds the result of a policy pre-check.
type PolicyResult struct {
	OK      bool
	Code    string
	Message string
}
`)
	return sb.String()
}
