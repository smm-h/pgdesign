package codegen

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"github.com/smm-h/pgdesign/pkg/genkit"
)

// JavaJPAGenerator generates JPA entity classes corresponding to database tables,
// including @ManyToOne and @OneToMany relationship annotations derived from
// foreign key metadata.
type JavaJPAGenerator struct{}

// jpaField is one field of a generated JPA entity, with the imports its type
// requires (so per-file import blocks can be assembled in GenerateFiles).
type jpaField struct {
	Annotations []string
	JavaType    string
	Name        string
	Group       int // 0=columns, 1=ManyToOne, 2=OneToMany
	Imports     []string
}

// jpaEntity is one generated @Entity class.
type jpaEntity struct {
	TableName string
	ClassName string
	Fields    []jpaField
}

// collectJPAEntities builds the entity model from the schema. It ensures the FK
// graph is present. javax.persistence.* is required by every entity and is not
// tracked per field.
func collectJPAEntities(schema *model.Schema) []jpaEntity {
	if schema.FKGraph == nil {
		schema.BuildFKGraph()
	}

	var entities []jpaEntity
	for _, tbl := range schema.Tables {
		ei := jpaEntity{
			TableName: tbl.Name,
			ClassName: toPascalCase(tbl.Name),
		}

		pkSet := make(map[string]bool, len(tbl.PK))
		for _, pk := range tbl.PK {
			pkSet[pk] = true
		}

		fkColSet := make(map[string]model.FK)
		for _, fk := range tbl.FKs {
			if len(fk.Columns) == 1 {
				fkColSet[fk.Columns[0]] = fk
			}
		}

		// Regular columns (skipping single-column FK columns).
		for _, col := range tbl.Columns {
			if _, isFKCol := fkColSet[col.Name]; isFKCol {
				continue
			}

			javaType, typeImports := pgBaseTypeToJava(col)
			if col.Array {
				wrapperType, wrapperImports := toJavaWrapper(javaType, typeImports)
				javaType = "List<" + wrapperType + ">"
				typeImports = append([]string{"java.util.List"}, wrapperImports...)
			} else if !col.NotNull {
				javaType, typeImports = toJavaWrapper(javaType, typeImports)
			}

			var annotations []string
			if pkSet[col.Name] {
				annotations = append(annotations, "@Id")
			}
			// Enum columns persist via a generated AttributeConverter (backed by
			// getValue()/fromValue()), never @Enumerated(STRING) which would store
			// the constant NAME rather than the DB value.
			if (col.TypeKind == "enum" || col.TypeKind == "state_machine") && !col.Array {
				annotations = append(annotations, fmt.Sprintf("@Convert(converter = %sConverter.class)", EnumTypeName(col.PGType.Base)))
			}
			nullable := "false"
			if !col.NotNull {
				nullable = "true"
			}
			if col.Default == nil && col.DefaultExpr != "" {
				pgTypeStr := typeinfo.Reconstruct(col.PGType)
				if col.Array {
					pgTypeStr += "[]"
				}
				annotations = append(annotations, fmt.Sprintf("@Column(name = %q, nullable = %s, columnDefinition = %q)", col.Name, nullable, pgTypeStr+" DEFAULT "+col.DefaultExpr))
			} else {
				annotations = append(annotations, fmt.Sprintf("@Column(name = %q, nullable = %s)", col.Name, nullable))
			}

			ei.Fields = append(ei.Fields, jpaField{
				Annotations: annotations,
				JavaType:    javaType,
				Name:        toCamelCase(col.Name),
				Group:       0,
				Imports:     typeImports,
			})
		}

		// @ManyToOne fields for single-column FKs.
		for _, fk := range tbl.FKs {
			if len(fk.Columns) != 1 {
				continue
			}
			col := findColumn(tbl, fk.Columns[0])
			refType := toPascalCase(fk.RefTable)
			fieldName := jpaRelFieldName(fk.Columns[0], fk.RefTable)

			nullable := "false"
			if col != nil && !col.NotNull {
				nullable = "true"
			}

			annotations := []string{
				"@ManyToOne(fetch = FetchType.LAZY)",
				fmt.Sprintf("@JoinColumn(name = %q, nullable = %s)", fk.Columns[0], nullable),
			}

			ei.Fields = append(ei.Fields, jpaField{
				Annotations: annotations,
				JavaType:    refType,
				Name:        fieldName,
				Group:       1,
			})
		}

		// @OneToMany fields from reverse FK map.
		if edges := schema.FKGraph.Reverse[model.TableKey(tbl.Schema, tbl.Name)]; len(edges) > 0 {
			fkColCount := make(map[string]int)
			for _, e := range edges {
				fkColCount[e.FKName]++
			}
			seen := make(map[string]bool)
			for _, e := range edges {
				if fkColCount[e.FKName] != 1 || seen[e.FKName] {
					continue
				}
				seen[e.FKName] = true
				refType := toPascalCase(e.FromTable)
				fieldName := jpaRelFieldName(e.FromColumn, tbl.Name)
				annotations := []string{
					fmt.Sprintf("@OneToMany(mappedBy = %q)", fieldName),
				}
				ei.Fields = append(ei.Fields, jpaField{
					Annotations: annotations,
					JavaType:    "List<" + refType + ">",
					Name:        toCamelCase(e.FromTable),
					Group:       2,
					Imports:     []string{"java.util.List"},
				})
			}
		}

		entities = append(entities, ei)
	}
	return entities
}

// writeJPAEntityBody writes the @Entity class declaration and its fields.
func writeJPAEntityBody(buf *bytes.Buffer, ei jpaEntity) {
	fmt.Fprintf(buf, "@Entity\n")
	fmt.Fprintf(buf, "@Table(name = %q)\n", ei.TableName)
	fmt.Fprintf(buf, "public class %s {\n", ei.ClassName)

	lastGroup := -1
	for _, f := range ei.Fields {
		if lastGroup >= 0 && f.Group != lastGroup {
			buf.WriteString("\n")
		}
		for _, ann := range f.Annotations {
			fmt.Fprintf(buf, "    %s\n", ann)
		}
		fmt.Fprintf(buf, "    private %s %s;\n", f.JavaType, f.Name)
		lastGroup = f.Group
	}
	buf.WriteString("}\n")
}

// Generate produces a single Java source file with one @Entity class per table.
// This combined form has multiple public classes and is not legal Java for
// multi-table schemas; GenerateFiles is the compilable, one-class-per-file form.
func (g *JavaJPAGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	var buf bytes.Buffer
	header := genkit.Header(genkit.CommentSlash)

	if len(schema.Tables) == 0 {
		buf.WriteString(header)
		return buf.Bytes(), nil
	}

	entities := collectJPAEntities(schema)

	imports := make(map[string]bool)
	imports["javax.persistence.*"] = true
	for _, ei := range entities {
		for _, f := range ei.Fields {
			for _, imp := range f.Imports {
				imports[imp] = true
			}
		}
	}

	// Separate imports into groups: java.*, javax.*, third-party.
	var javaImports, javaxImports, thirdPartyImports []string
	for imp := range imports {
		switch {
		case strings.HasPrefix(imp, "javax."):
			javaxImports = append(javaxImports, imp)
		case strings.HasPrefix(imp, "java."):
			javaImports = append(javaImports, imp)
		default:
			thirdPartyImports = append(thirdPartyImports, imp)
		}
	}
	sort.Strings(javaImports)
	sort.Strings(javaxImports)
	sort.Strings(thirdPartyImports)

	buf.WriteString(header)
	buf.WriteString("\n")
	for _, imp := range javaImports {
		fmt.Fprintf(&buf, "import %s;\n", imp)
	}
	if len(javaImports) > 0 && (len(javaxImports) > 0 || len(thirdPartyImports) > 0) {
		buf.WriteString("\n")
	}
	for _, imp := range javaxImports {
		fmt.Fprintf(&buf, "import %s;\n", imp)
	}
	if len(thirdPartyImports) > 0 {
		buf.WriteString("\n")
		for _, imp := range thirdPartyImports {
			fmt.Fprintf(&buf, "import %s;\n", imp)
		}
	}

	for _, ei := range entities {
		buf.WriteString("\n")
		writeJPAEntityBody(&buf, ei)
	}

	return buf.Bytes(), nil
}

// GenerateFiles implements MultiFileGenerator, emitting one <Entity>.java per
// table. Each file carries only the imports its own fields require (plus the
// shared javax.persistence.*), so every file is a legal single-public-class
// compilation unit.
func (g *JavaJPAGenerator) GenerateFiles(schema *model.Schema) (map[string][]byte, []diagnostic.Diagnostic) {
	header := genkit.Header(genkit.CommentSlash)
	files := make(map[string][]byte)

	// Enum class files and their AttributeConverters (one public type per file).
	for _, e := range usedJPAEnums(schema) {
		typeName := toPascalCase(e.Name)
		var enumBuf bytes.Buffer
		enumBuf.WriteString(header)
		enumBuf.WriteString("\n")
		enumBuf.WriteString(generateJavaEnum(e))
		files[typeName+".java"] = enumBuf.Bytes()

		files[typeName+"Converter.java"] = generateJPAConverter(header, typeName)
	}

	for _, ei := range collectJPAEntities(schema) {
		fileImports := make(map[string]bool)
		for _, f := range ei.Fields {
			for _, imp := range f.Imports {
				fileImports[imp] = true
			}
		}

		var buf bytes.Buffer
		buf.WriteString(header)
		writeJavaFileImports(&buf, fileImports)
		buf.WriteString("\nimport javax.persistence.*;\n\n")
		writeJPAEntityBody(&buf, ei)
		files[ei.ClassName+".java"] = buf.Bytes()
	}

	return files, nil
}

// usedJPAEnums returns the enums referenced by non-array enum/state-machine
// columns across the schema, in schema.Enums order (canonical). Each needs a
// class file and an AttributeConverter file.
func usedJPAEnums(schema *model.Schema) []model.Enum {
	used := make(map[string]bool)
	for _, tbl := range schema.Tables {
		for _, col := range tbl.Columns {
			if (col.TypeKind == "enum" || col.TypeKind == "state_machine") && !col.Array {
				used[col.PGType.Base] = true
			}
		}
	}
	var out []model.Enum
	for _, e := range schema.Enums {
		if used[e.Name] {
			out = append(out, e)
		}
	}
	return out
}

// generateJPAConverter emits a JPA AttributeConverter that persists an enum by
// its DB value (getValue) and reconstructs it (fromValue) -- never by NAME.
func generateJPAConverter(header, typeName string) []byte {
	var buf bytes.Buffer
	buf.WriteString(header)
	buf.WriteString("\nimport javax.persistence.AttributeConverter;\n")
	buf.WriteString("import javax.persistence.Converter;\n\n")
	buf.WriteString("@Converter(autoApply = false)\n")
	fmt.Fprintf(&buf, "public class %sConverter implements AttributeConverter<%s, String> {\n", typeName, typeName)
	buf.WriteString("    @Override\n")
	fmt.Fprintf(&buf, "    public String convertToDatabaseColumn(%s attribute) {\n", typeName)
	buf.WriteString("        return attribute == null ? null : attribute.getValue();\n")
	buf.WriteString("    }\n\n")
	buf.WriteString("    @Override\n")
	fmt.Fprintf(&buf, "    public %s convertToEntityAttribute(String dbData) {\n", typeName)
	fmt.Fprintf(&buf, "        return dbData == null ? null : %s.fromValue(dbData);\n", typeName)
	buf.WriteString("    }\n")
	buf.WriteString("}\n")
	return buf.Bytes()
}

// jpaRelFieldName derives the Java field name for a @ManyToOne relationship.
// If the FK column ends with "_id", the suffix is stripped and the result is
// camelCased. Otherwise, the referenced table name is camelCased.
func jpaRelFieldName(fkColumn, refTable string) string {
	if strings.HasSuffix(fkColumn, "_id") {
		return toCamelCase(strings.TrimSuffix(fkColumn, "_id"))
	}
	return toCamelCase(refTable)
}

// findColumn looks up a column by name in a table. Returns nil if not found.
func findColumn(tbl model.Table, name string) *model.Column {
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == name {
			return &tbl.Columns[i]
		}
	}
	return nil
}
