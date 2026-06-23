package codegen

import (
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// ConstraintSet holds extracted constraint information for a single table.
type ConstraintSet struct {
	NotNullFields  []string            // column names that are NOT NULL (excludes PK/identity/generated)
	EnumFields     map[string][]string // column name -> valid enum values
	CheckExprs     map[string]string   // column name -> CHECK expression (single-column checks only)
	JSONSchemas    map[string]string   // column name -> JSON schema file path
	DomainBaseTypes map[string]string  // domain-backed column name -> base PG type (for numeric comparison type-mapping)
}

// HasConstraints reports whether any constraints were extracted.
func (cs ConstraintSet) HasConstraints() bool {
	return len(cs.NotNullFields) > 0 || len(cs.EnumFields) > 0 || len(cs.CheckExprs) > 0 || len(cs.JSONSchemas) > 0 || len(cs.DomainBaseTypes) > 0
}

// ExtractConstraints collects constraint metadata from a table for code generation.
// It identifies NOT NULL columns (excluding PK, identity, and generated columns),
// enum-typed columns, single-column CHECK constraints, and JSON schema annotations.
func ExtractConstraints(table model.Table, schema model.Schema) ConstraintSet {
	cs := ConstraintSet{
		EnumFields:      make(map[string][]string),
		CheckExprs:      make(map[string]string),
		JSONSchemas:     make(map[string]string),
		DomainBaseTypes: make(map[string]string),
	}

	// Build set of PK column names for exclusion.
	pkSet := make(map[string]bool, len(table.PK))
	for _, col := range table.PK {
		pkSet[col] = true
	}

	// Build map of enum names (lowercased) to their values.
	enumMap := make(map[string][]string, len(schema.Enums))
	for _, e := range schema.Enums {
		enumMap[strings.ToLower(e.Name)] = e.Values
	}

	// Build map of domain names (lowercased) to their definitions.
	domainMap := make(map[string]model.Domain, len(schema.Domains))
	for _, d := range schema.Domains {
		domainMap[strings.ToLower(d.Name)] = d
	}

	for _, col := range table.Columns {
		// NOT NULL: exclude PK columns, identity columns, and generated columns.
		if col.NotNull && !pkSet[col.Name] && col.Identity == "" && col.Generated == "" {
			cs.NotNullFields = append(cs.NotNullFields, col.Name)
		}

		// Enum: match column type against declared enums (case-insensitive).
		if vals, ok := enumMap[strings.ToLower(col.PGType)]; ok {
			cs.EnumFields[col.Name] = vals
		}

		// Domain: match column type against declared domains (case-insensitive).
		if domain, ok := domainMap[strings.ToLower(col.PGType)]; ok {
			if domain.Check != "" {
				cs.CheckExprs[col.Name] = domain.Check
			}
			cs.DomainBaseTypes[col.Name] = domain.BaseType
		}

		// JSON schema annotation.
		if col.JSONSchema != "" {
			cs.JSONSchemas[col.Name] = col.JSONSchema
		}
	}

	// Single-column CHECK constraints.
	for _, chk := range table.Checks {
		if colName := isSingleColumnCheck(chk.Expr); colName != "" {
			cs.CheckExprs[colName] = chk.Expr
		}
	}

	return cs
}

// isSingleColumnCheck returns the column name if the CHECK expression
// references exactly one column (possibly multiple times). Returns ""
// if the expression references zero or multiple distinct columns, or
// if parsing fails.
func isSingleColumnCheck(expr string) string {
	ast, err := sqlexpr.Parse(expr)
	if err != nil {
		return ""
	}
	refs := sqlexpr.CollectColumnRefs(ast)
	if len(refs) == 0 {
		return ""
	}
	name := refs[0].Parts[len(refs[0].Parts)-1]
	for _, ref := range refs[1:] {
		refName := ref.Parts[len(ref.Parts)-1]
		if !strings.EqualFold(refName, name) {
			return ""
		}
	}
	return name
}
