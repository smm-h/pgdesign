package generate

import (
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
)

// toPascalCase converts a snake_case string to PascalCase.
func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// toCamelCase converts a snake_case string to camelCase.
func toCamelCase(s string) string {
	pascal := toPascalCase(s)
	if len(pascal) == 0 {
		return pascal
	}
	return strings.ToLower(pascal[:1]) + pascal[1:]
}

// pgTypeToGraphQL maps a PostgreSQL type to a GraphQL type.
func pgTypeToGraphQL(pgType string, isPK bool, enumNames map[string]bool) string {
	switch pgType {
	case "int4", "int2":
		return "Int"
	case "int8":
		return "Int"
	case "text", "varchar", "char", "name":
		return "String"
	case "bool":
		return "Boolean"
	case "uuid":
		if isPK {
			return "ID"
		}
		return "String"
	case "float4":
		return "Float"
	case "float8":
		return "Float"
	case "numeric":
		return "Float"
	case "timestamptz", "timestamp", "date", "time", "timetz", "interval":
		return "DateTime"
	case "jsonb", "json":
		return "JSON"
	case "bytea":
		return "String"
	}
	if enumNames[pgType] {
		return toPascalCase(pgType)
	}
	return "String"
}

// generateGraphQL produces a GraphQL schema from the resolved schema.
func generateGraphQL(schema *model.Schema) string {
	var b strings.Builder

	// Build set of enum names for type lookup.
	enumNames := make(map[string]bool, len(schema.Enums))
	for _, e := range schema.Enums {
		enumNames[e.Name] = true
	}

	if schema.FKGraph == nil {
		schema.BuildFKGraph()
	}

	tables := schema.TableOrder()

	// Scalars.
	b.WriteString("scalar DateTime\nscalar JSON\n")

	// Enums.
	for _, e := range schema.Enums {
		b.WriteString("\n")
		b.WriteString("enum ")
		b.WriteString(toPascalCase(e.Name))
		b.WriteString(" {\n")
		for _, v := range e.Values {
			b.WriteString("  ")
			b.WriteString(strings.ToUpper(v))
			b.WriteString("\n")
		}
		b.WriteString("}\n")
	}

	// Build column lookup per table for FK nullability checks.
	colNotNull := make(map[string]map[string]bool) // table name -> column name -> NotNull
	for _, t := range tables {
		m := make(map[string]bool, len(t.Columns))
		for _, col := range t.Columns {
			m[col.Name] = col.NotNull
		}
		colNotNull[t.Name] = m
	}

	// Types.
	for _, t := range tables {
		b.WriteString("\n")
		b.WriteString("type ")
		b.WriteString(toPascalCase(t.Name))
		b.WriteString(" {\n")

		// Build PK column set.
		pkCols := make(map[string]bool, len(t.PK))
		for _, pk := range t.PK {
			pkCols[pk] = true
		}

		// Columns.
		for _, col := range t.Columns {
			isPK := pkCols[col.Name]
			var gqlType string
			if isPK {
				gqlType = "ID"
			} else {
				gqlType = pgTypeToGraphQL(col.PGType.Base, false, enumNames)
			}

			b.WriteString("  ")
			b.WriteString(toCamelCase(col.Name))
			b.WriteString(": ")

			if col.Array {
				b.WriteString("[")
				b.WriteString(gqlType)
				b.WriteString("!]")
				if col.NotNull {
					b.WriteString("!")
				}
			} else {
				b.WriteString(gqlType)
				if col.NotNull {
					b.WriteString("!")
				}
			}
			b.WriteString("\n")
		}

		// FK relation fields, sorted by field name.
		type fkField struct {
			fieldName string
			typeName  string
			notNull   bool
		}
		var fkFields []fkField
		for _, fk := range t.FKs {
			allNotNull := true
			for _, fkCol := range fk.Columns {
				if !colNotNull[t.Name][fkCol] {
					allNotNull = false
					break
				}
			}
			fkFields = append(fkFields, fkField{
				fieldName: toCamelCase(fk.RefTable),
				typeName:  toPascalCase(fk.RefTable),
				notNull:   allNotNull,
			})
		}
		sort.Slice(fkFields, func(i, j int) bool {
			return fkFields[i].fieldName < fkFields[j].fieldName
		})
		for _, f := range fkFields {
			b.WriteString("  ")
			b.WriteString(f.fieldName)
			b.WriteString(": ")
			b.WriteString(f.typeName)
			if f.notNull {
				b.WriteString("!")
			}
			b.WriteString("\n")
		}

		// Reverse relation fields, sorted by field name.
		type revField struct {
			fieldName string
			typeName  string
		}
		var revFields []revField
		seen := make(map[string]bool)
		for _, edge := range schema.FKGraph.Reverse[t.Name] {
			if seen[edge.FKName] {
				continue
			}
			seen[edge.FKName] = true
			revFields = append(revFields, revField{
				fieldName: toCamelCase(edge.FromTable),
				typeName:  toPascalCase(edge.FromTable),
			})
		}
		sort.Slice(revFields, func(i, j int) bool {
			return revFields[i].fieldName < revFields[j].fieldName
		})
		for _, f := range revFields {
			b.WriteString("  ")
			b.WriteString(f.fieldName)
			b.WriteString(": [")
			b.WriteString(f.typeName)
			b.WriteString("!]!\n")
		}

		b.WriteString("}\n")
	}

	b.WriteString("\n")
	return b.String()
}
