package generate

import (
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

// pgTypeToGraphQL maps a column's PostgreSQL type to a GraphQL type. Enum and
// state machine columns are detected via Column.TypeKind (set by both
// Schema.Build and introspection), not by name lookup against schema.Enums.
func pgTypeToGraphQL(col *model.Column, isPK bool) string {
	switch col.PGType.Base {
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
	if col.TypeKind == "enum" || col.TypeKind == "state_machine" {
		return toPascalCase(col.PGType.Base)
	}
	return "String"
}

// generateGraphQL produces a GraphQL schema from the resolved schema.
func generateGraphQL(schema *model.Schema) string {
	var b strings.Builder

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

		// Build this table's own column nullability lookup. Keyed on the
		// table's own Columns (never a shared bare-name map) so same-named
		// tables in different schemas cannot collide on FK nullability.
		colNotNull := make(map[string]bool, len(t.Columns))
		for _, col := range t.Columns {
			colNotNull[col.Name] = col.NotNull
		}

		// Columns.
		for i := range t.Columns {
			col := &t.Columns[i]
			isPK := pkCols[col.Name]
			var gqlType string
			if isPK {
				gqlType = "ID"
			} else {
				gqlType = pgTypeToGraphQL(col, false)
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

		// FK relation fields, emitted in the table's canonical FK order.
		type fkField struct {
			fieldName string
			typeName  string
			notNull   bool
		}
		var fkFields []fkField
		for _, fk := range t.FKs {
			allNotNull := true
			for _, fkCol := range fk.Columns {
				if !colNotNull[fkCol] {
					allNotNull = false
					break
				}
			}
			// Imported FK targets (roadmap 7.3, union site 5): qualify the relation
			// type (and field) with the target schema so the field points at the
			// imported reference type — never at a bare, possibly-colliding local
			// type name, and never at an UNDEFINED type (the SDL would not compile).
			fieldName := toCamelCase(fk.RefTable)
			typeName := toPascalCase(fk.RefTable)
			if fk.RefAlias != "" {
				fieldName = toCamelCase(fk.RefSchema + "_" + fk.RefTable)
				typeName = toPascalCase(fk.RefSchema + "_" + fk.RefTable)
			}
			fkFields = append(fkFields, fkField{
				fieldName: fieldName,
				typeName:  typeName,
				notNull:   allNotNull,
			})
		}
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

		// Reverse relation fields, emitted in canonical FKGraph edge order.
		type revField struct {
			fieldName string
			typeName  string
		}
		var revFields []revField
		seen := make(map[string]bool)
		for _, edge := range schema.FKGraph.Reverse[model.TableKey(t.Schema, t.Name)] {
			if seen[edge.FKName] {
				continue
			}
			seen[edge.FKName] = true
			revFields = append(revFields, revField{
				fieldName: toCamelCase(edge.FromTable),
				typeName:  toPascalCase(edge.FromTable),
			})
		}
		for _, f := range revFields {
			b.WriteString("  ")
			b.WriteString(f.fieldName)
			b.WriteString(": [")
			b.WriteString(f.typeName)
			b.WriteString("!]!\n")
		}

		b.WriteString("}\n")
	}

	// Minimal REFERENCE types for imported tables (roadmap 7.3/7.4). Without a type
	// definition the imported relation fields above would reference undefined
	// types and the SDL would not compile. Each reference type is schema-qualified
	// (matching the relation field's typeName) and carries only its columns — it is
	// a reference shape, not this project's generated type. Emitted in the
	// imported-tables' canonical (name-sorted) order.
	for i := range schema.ImportedTables {
		it := &schema.ImportedTables[i]
		b.WriteString("\n")
		b.WriteString("\"\"\"Imported reference: ")
		b.WriteString(it.Schema)
		b.WriteString(".")
		b.WriteString(it.Name)
		b.WriteString(" (owned by another project)\"\"\"\n")
		b.WriteString("type ")
		b.WriteString(toPascalCase(it.Schema + "_" + it.Name))
		b.WriteString(" {\n")
		pk := make(map[string]bool, len(it.PK))
		for _, p := range it.PK {
			pk[p] = true
		}
		for j := range it.Columns {
			col := &it.Columns[j]
			var gqlType string
			if pk[col.Name] {
				gqlType = "ID"
			} else {
				gqlType = pgTypeToGraphQL(col, false)
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
		b.WriteString("}\n")
	}

	b.WriteString("\n")
	return b.String()
}
