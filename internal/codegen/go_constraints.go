package codegen

import (
	"bytes"
	"fmt"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/pkg/genkit"
)

// GoConstraintsGenerator generates Go validation functions for table constraints.
type GoConstraintsGenerator struct{}

// Generate produces a Go file with Validate<TableName> functions for each table
// that has extractable constraints (NOT NULL, enum, CHECK, JSON schema).
func (g *GoConstraintsGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	var buf bytes.Buffer
	buf.WriteString(genkit.Header(genkit.CommentSlash))
	buf.WriteString("// Regenerate with: pgdesign codegen --lang go --mode constraints <schema-files>\n")
	if schema.Name != "" {
		fmt.Fprintf(&buf, "// Schema: %s\n", schema.Name)
	}
	// Validators live in the same package as the branded row structs and enum
	// types they validate: Go has no configurable cross-package import path for
	// the schema package, so same-package reference is the only self-contained
	// compilable form. Row structs (Accounts) and branded enums (Role) are
	// referenced directly; enum fields validate via .String()/.IsValid().
	buf.WriteString("\npackage schema\n")

	needsRegexp := false

	// Collect tables with constraints.
	type tableWork struct {
		table model.Table
		cs    ConstraintSet
	}
	var work []tableWork
	for _, tbl := range schema.Tables {
		cs := ExtractConstraints(tbl, *schema)
		if !cs.HasConstraints() {
			continue
		}
		work = append(work, tableWork{table: tbl, cs: cs})
		for _, expr := range cs.CheckExprs {
			if pat := classifyCheck(expr); pat != nil {
				if _, ok := pat.(*likePattern); ok {
					needsRegexp = true
				}
			}
		}
	}

	if len(work) == 0 {
		buf.WriteString("\n// No tables with extractable constraints.\n")
		return buf.Bytes(), nil
	}

	// Write imports.
	var imports []string
	imports = append(imports, "fmt")
	if needsRegexp {
		imports = append(imports, "regexp")
	}
	buf.WriteString("\nimport (\n")
	for _, imp := range imports {
		fmt.Fprintf(&buf, "\t%q\n", imp)
	}
	buf.WriteString(")\n")

	// Write ValidationError type.
	buf.WriteString(`
// ValidationError describes a single constraint violation.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
`)

	// Generate per-table validators.
	for _, tw := range work {
		writeGoValidator(&buf, tw.table, tw.cs)
	}

	return buf.Bytes(), nil
}

func writeGoValidator(buf *bytes.Buffer, table model.Table, cs ConstraintSet) {
	funcName := "Validate" + toPascalCase(table.Name)
	typeName := toPascalCase(table.Name)

	fmt.Fprintf(buf, "\n// %s validates constraint rules for the %s table.\n", funcName, table.Name)
	fmt.Fprintf(buf, "func %s(row %s) []ValidationError {\n", funcName, typeName)
	buf.WriteString("\tvar errs []ValidationError\n")

	// Enum columns are branded structs whose zero value is detectably invalid;
	// track them so the NOT NULL check uses !IsValid() rather than a == "" check
	// that no longer type-checks against the struct type.
	enumCols := make(map[string]bool, len(cs.EnumFields))
	for col := range cs.EnumFields {
		enumCols[col] = true
	}

	// NOT NULL checks. String columns use the empty-string zero value; branded
	// enum columns use the branded zero value, which .IsValid() rejects.
	for _, col := range cs.NotNullFields {
		goField := toPascalCase(col)
		if enumCols[col] {
			fmt.Fprintf(buf, "\tif !row.%s.IsValid() {\n", goField)
			fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: \"must be a defined value\"})\n", col)
			buf.WriteString("\t}\n")
			continue
		}
		pgType := columnPGType(table, col)
		if isStringPGType(pgType) {
			fmt.Fprintf(buf, "\tif row.%s == \"\" {\n", goField)
			fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: \"must not be empty\"})\n", col)
			buf.WriteString("\t}\n")
		}
	}

	// Enum value checks: switch on the branded value's .String() so the cases
	// stay plain string literals against the underlying value.
	for _, ef := range cs.SortedEnumFields() {
		goField := toPascalCase(ef.Column)
		fmt.Fprintf(buf, "\tswitch row.%s.String() {\n", goField)
		fmt.Fprintf(buf, "\tcase ")
		for i, v := range ef.Values {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(buf, "%q", v)
		}
		buf.WriteString(":\n")
		buf.WriteString("\t\t// valid\n")
		buf.WriteString("\tdefault:\n")
		fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: fmt.Sprintf(\"invalid value %%q\", row.%s.String())})\n", ef.Column, goField)
		buf.WriteString("\t}\n")
	}

	// Nullable columns map to Go pointer fields; a CHECK only applies to a
	// present value, so wrap those checks in a nil guard and dereference.
	nullable := make(map[string]bool, len(table.Columns))
	for _, c := range table.Columns {
		if !c.NotNull && !c.Array {
			nullable[c.Name] = true
		}
	}

	// CHECK constraint checks.
	for _, ce := range cs.SortedCheckExprs() {
		goField := toPascalCase(ce.Column)
		pat := classifyCheck(ce.Expr)
		if pat == nil {
			fmt.Fprintf(buf, "\t// CHECK on %s: %s (unrecognized pattern, skipped)\n", ce.Column, ce.Expr)
			continue
		}
		fieldExpr := "row." + goField
		if nullable[ce.Column] {
			fmt.Fprintf(buf, "\tif row.%s != nil {\n", goField)
			fieldExpr = "(*row." + goField + ")"
		}
		writeGoCheckPattern(buf, ce.Column, fieldExpr, pat)
		if nullable[ce.Column] {
			buf.WriteString("\t}\n")
		}
	}

	buf.WriteString("\treturn errs\n")
	buf.WriteString("}\n")
}

// writeGoCheckPattern emits a validation branch. fieldExpr is the Go value
// expression for the column (e.g. "row.Slug" or "(*row.Note)" for a nullable
// column already nil-guarded by the caller).
func writeGoCheckPattern(buf *bytes.Buffer, col, fieldExpr string, pat checkPattern) {
	switch p := pat.(type) {
	case *rangePattern:
		// The CHECK passes when value >= low (if LowIncl) or value > low (if !LowIncl).
		// The validation fails on the inverse condition.
		var lowOp, highOp string
		if p.LowIncl {
			lowOp = "<" // fails when value < low
		} else {
			lowOp = "<=" // fails when value <= low
		}
		if p.HighIncl {
			highOp = ">" // fails when value > high
		} else {
			highOp = ">=" // fails when value >= high
		}
		fmt.Fprintf(buf, "\tif %s %s %s || %s %s %s {\n", fieldExpr, lowOp, p.Low, fieldExpr, highOp, p.High)
		fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: \"value out of range\"})\n", col)
		buf.WriteString("\t}\n")

	case *comparisonPattern:
		failOp := invertComparisonOp(p.Op)
		fmt.Fprintf(buf, "\tif %s %s %s {\n", fieldExpr, failOp, p.Value)
		fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: \"failed check: %s\"})\n", col, escapeGoString(p.Op+" "+p.Value))
		buf.WriteString("\t}\n")

	case *lengthPattern:
		failOp := invertComparisonOp(p.Op)
		fmt.Fprintf(buf, "\tif len(%s) %s %d {\n", fieldExpr, failOp, p.Value)
		fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: fmt.Sprintf(\"length must be %s %d, got %%d\", len(%s))})\n", col, p.Op, p.Value, fieldExpr)
		buf.WriteString("\t}\n")

	case *likePattern:
		regex := likeToRegex(p.Pattern)
		if p.IsCaseInsensitive() {
			regex = "(?i)" + regex
		}
		varName := col + "Re"
		fmt.Fprintf(buf, "\t%s := regexp.MustCompile(%q)\n", varName, regex)
		if p.IsNegated() {
			fmt.Fprintf(buf, "\tif %s.MatchString(%s) {\n", varName, fieldExpr)
		} else {
			fmt.Fprintf(buf, "\tif !%s.MatchString(%s) {\n", varName, fieldExpr)
		}
		fmt.Fprintf(buf, "\t\terrs = append(errs, ValidationError{Field: %q, Message: \"does not match required pattern\"})\n", col)
		buf.WriteString("\t}\n")
	}
}

// columnPGType returns the PostgreSQL type for a column in the table.
func columnPGType(table model.Table, colName string) string {
	for _, c := range table.Columns {
		if c.Name == colName {
			return c.PGType.Base
		}
	}
	return ""
}

// isStringPGType returns true if the PostgreSQL type maps to a Go string.
func isStringPGType(pgType string) bool {
	switch pgType {
	case "text", "varchar", "char", "name", "citext":
		return true
	}
	return false
}
