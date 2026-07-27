package codegen

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// twoSchemaCodegenSchema builds a schema with same-named tables in two schemas
// (public and archive), each with an entry -> account FK. Under (schema, name)
// FKGraph keying the reverse-relation lookups in every generator must stay
// scoped to their own schema.
func twoSchemaCodegenSchema() *model.Schema {
	tables := func(sch string) []model.Table {
		return []model.Table{
			{Name: "account", Schema: sch, Comment: "Account in " + sch, PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
				}},
			{Name: "entry", Schema: sch, Comment: "Entry in " + sch, PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "account_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "amount", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				FKs: []model.FK{{Name: "fk_entry_account", Columns: []string{"account_id"}, RefSchema: sch, RefTable: "account", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
		}
	}
	s := &model.Schema{}
	s.Tables = append(s.Tables, tables("public")...)
	s.Tables = append(s.Tables, tables("archive")...)
	s.Canonicalize()
	return s
}

type singleFileGenerator interface {
	Generate(*model.Schema) ([]byte, []diagnostic.Diagnostic)
}

type multiFileGenerator interface {
	GenerateFiles(*model.Schema) (map[string][]byte, []diagnostic.Diagnostic)
}

func hasError(diags []diagnostic.Diagnostic) *diagnostic.Diagnostic {
	for i := range diags {
		if diags[i].Severity == diagnostic.Error {
			return &diags[i]
		}
	}
	return nil
}

// TestTwoSchemas_EveryGenerator runs every codegen generator against the
// two-schema same-named fixture and asserts each produces output with no error
// diagnostics. This is the "full codegen must pass every generator" check.
func TestTwoSchemas_EveryGenerator(t *testing.T) {
	schema := twoSchemaCodegenSchema()

	langs := []Lang{LangGo, LangTS, LangPython, LangJava, LangKotlin, LangZig}
	single := map[string]singleFileGenerator{
		"validators/go":      &GoValidatorGenerator{},
		"validators/ts":      &TSValidatorGenerator{},
		"validators/python":  &PythonGenerator{},
		"validators/java":    &JavaValidatorGenerator{},
		"validators/kotlin":  &KotlinValidatorGenerator{},
		"validators/zig":     &ZigValidatorGenerator{},
		"constants/go":       &GoConstantsGenerator{},
		"constants/ts":       &TSConstantsGenerator{},
		"constants/python":   &PythonConstantsGenerator{},
		"constants/java":     &JavaConstantsGenerator{},
		"constants/kotlin":   &KotlinConstantsGenerator{},
		"constants/zig":      &ZigConstantsGenerator{},
		"types/go":           &GoTypesGenerator{},
		"types/ts":           &TSTypesGenerator{},
		"types/python":       &PythonTypesGenerator{},
		"types/java":         &JavaTypesGenerator{},
		"types/kotlin":       &KotlinTypesGenerator{},
		"types/zig":          &ZigTypesGenerator{},
		"constraints/go":     &GoConstraintsGenerator{},
		"constraints/ts":     &TSConstraintsGenerator{},
		"constraints/python": &PythonConstraintsGenerator{},
		"constraints/java":   &JavaConstraintsGenerator{},
		"constraints/kotlin": &KotlinConstraintsGenerator{},
		"constraints/zig":    &ZigConstraintsGenerator{},
		"gorm/go":            &GoGormGenerator{},
		"drizzle/ts":         &TSDrizzleGenerator{},
		"sqlalchemy/python":  &PythonSQLAlchemyGenerator{},
		"jpa/java":           &JavaJPAGenerator{},
	}
	for _, l := range langs {
		single["enums/"+string(l)] = &EnumsGenerator{Lang: l}
	}

	for name, gen := range single {
		out, diags := gen.Generate(schema)
		if err := hasError(diags); err != nil {
			t.Errorf("%s: error diagnostic: %s %s", name, err.Code, err.Message)
		}
		if len(out) == 0 {
			t.Errorf("%s: produced empty output", name)
		}
	}

	multi := map[string]multiFileGenerator{
		"ddl/python":         &PythonDDLGenerator{},
		"query-layer/python": &PythonQueryLayerGenerator{},
	}
	for name, gen := range multi {
		files, diags := gen.GenerateFiles(schema)
		if err := hasError(diags); err != nil {
			t.Errorf("%s: error diagnostic: %s %s", name, err.Code, err.Message)
		}
		if len(files) == 0 {
			t.Errorf("%s: produced no files", name)
		}
	}
}

// TestTwoSchemas_GormScoping asserts the FK-graph-consuming generators keep
// reverse relations scoped per schema: each account struct gets exactly one
// has-many relation (its own schema's entry), so the two account structs
// contribute two relations total — not four, which is what schema-blind bare
// keying would have produced.
func TestTwoSchemas_GormScoping(t *testing.T) {
	schema := twoSchemaCodegenSchema()
	g := schema.FKGraph
	if got := len(g.Reverse[model.TableKey("public", "account")]); got != 1 {
		t.Errorf("public.account reverse edges = %d, want 1 (no cross-schema bleed)", got)
	}
	if got := len(g.Reverse[model.TableKey("archive", "account")]); got != 1 {
		t.Errorf("archive.account reverse edges = %d, want 1", got)
	}
}
