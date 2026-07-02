package codegen

import (
	"sort"
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

// EnumField pairs a column name with its valid enum values.
type EnumField struct {
	Column string
	Values []string
}

// CheckExpr pairs a column name with its CHECK expression.
type CheckExpr struct {
	Column string
	Expr   string
}

// SortedEnumFields returns the enum-typed columns ordered by column name.
// Generators must iterate this instead of ranging EnumFields directly so
// that output is deterministic (Go map iteration order is randomized).
func (cs ConstraintSet) SortedEnumFields() []EnumField {
	cols := sortedKeys(cs.EnumFields)
	fields := make([]EnumField, 0, len(cols))
	for _, col := range cols {
		fields = append(fields, EnumField{Column: col, Values: cs.EnumFields[col]})
	}
	return fields
}

// SortedCheckExprs returns the single-column CHECK expressions ordered by
// column name. Generators must iterate this instead of ranging CheckExprs
// directly so that output is deterministic.
func (cs ConstraintSet) SortedCheckExprs() []CheckExpr {
	cols := sortedKeys(cs.CheckExprs)
	exprs := make([]CheckExpr, 0, len(cols))
	for _, col := range cols {
		exprs = append(exprs, CheckExpr{Column: col, Expr: cs.CheckExprs[col]})
	}
	return exprs
}

// sortedKeys returns the sorted keys of a map[string]T.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

	// Build map of enum/state_machine type names (lowercased) to their valid values.
	enumValues := make(map[string][]string, len(schema.Enums)+len(schema.StateMachineTransitions))
	for _, e := range schema.Enums {
		enumValues[strings.ToLower(e.Name)] = e.Values
	}
	for _, smt := range schema.StateMachineTransitions {
		enumValues[strings.ToLower(smt.TypeName)] = smt.States
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

		// Enum/state_machine: use TypeKind for detection, look up values by type name.
		if col.TypeKind == "enum" || col.TypeKind == "state_machine" {
			if vals, ok := enumValues[strings.ToLower(col.PGType.Base)]; ok {
				cs.EnumFields[col.Name] = vals
			}
		}

		// Domain: match column type against declared domains.
		if col.PGType.DomainName != "" {
			if domain, ok := domainMap[strings.ToLower(col.PGType.DomainName)]; ok {
				if domain.Check != "" {
					cs.CheckExprs[col.Name] = domain.Check
				}
				cs.DomainBaseTypes[col.Name] = domain.BaseType.Base
			}
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
