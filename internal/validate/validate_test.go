package validate

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

func TestE201_FKMissingOnDelete(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
			},
			FKs: []model.FK{{
				Name:      "fk_user",
				Columns:   []string{"user_id"},
				RefSchema: "public",
				RefTable:  "users",
				OnDelete:  "", // missing
			}},
		}, {
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
			},
		}},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "E201")
	if len(found) == 0 {
		t.Fatal("expected E201 for FK missing on_delete")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestE202_TableMissingComment(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "users",
			Schema: "public",
			PK:     []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
			},
			// Comment is empty
		}},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "E202")
	if len(found) == 0 {
		t.Fatal("expected E202 for table missing comment")
	}
}

func TestE203_TableMissingPK(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "events",
			Schema:  "public",
			Comment: "Events table",
			PK:      nil, // missing PK
			Columns: []model.Column{
				{Name: "data", PGType: "jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
			},
		}},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "E203")
	if len(found) == 0 {
		t.Fatal("expected E203 for table missing PK")
	}
}

func TestE207_VarcharUsage(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "email", PGType: "varchar(255)"},
				{Name: "created_at", PGType: "timestamptz"},
			},
		}},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "E207")
	if len(found) == 0 {
		t.Fatal("expected E207 for varchar usage")
	}
	if found[0].Column != "email" {
		t.Errorf("expected column 'email', got %q", found[0].Column)
	}
}

func TestE211_NamingViolation(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "UserAccounts",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
			},
		}},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "E211")
	if len(found) == 0 {
		t.Fatal("expected E211 for CamelCase table name")
	}
}

func TestW001_GodTable(t *testing.T) {
	cols := make([]model.Column, 35)
	for i := range cols {
		cols[i] = model.Column{Name: "col_" + string(rune('a'+i/26)) + string(rune('a'+i%26)), PGType: "text"}
	}
	cols = append(cols, model.Column{Name: "created_at", PGType: "timestamptz"})

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "big_table",
			Schema:  "public",
			Comment: "A very wide table",
			PK:      []string{"col_aa"},
			Columns: cols,
		}},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "W001")
	if len(found) == 0 {
		t.Fatal("expected W001 for god table (>30 columns)")
	}
}

func TestW008_CircularFK(t *testing.T) {
	schema := &model.Schema{
		CycleGroups: [][]string{{"a", "b", "c"}},
		Tables: []model.Table{
			{
				Name: "a", Schema: "public", Comment: "A",
				PK:      []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: "uuid"}, {Name: "b_id", PGType: "uuid"}, {Name: "created_at", PGType: "timestamptz"}},
				FKs:     []model.FK{{Name: "fk_b", Columns: []string{"b_id"}, RefSchema: "public", RefTable: "b", OnDelete: "cascade"}},
			},
			{
				Name: "b", Schema: "public", Comment: "B",
				PK:      []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: "uuid"}, {Name: "c_id", PGType: "uuid"}, {Name: "created_at", PGType: "timestamptz"}},
				FKs:     []model.FK{{Name: "fk_c", Columns: []string{"c_id"}, RefSchema: "public", RefTable: "c", OnDelete: "cascade"}},
			},
			{
				Name: "c", Schema: "public", Comment: "C",
				PK:      []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: "uuid"}, {Name: "a_id", PGType: "uuid"}, {Name: "created_at", PGType: "timestamptz"}},
				FKs:     []model.FK{{Name: "fk_a", Columns: []string{"a_id"}, RefSchema: "public", RefTable: "a", OnDelete: "cascade"}},
			},
		},
	}

	diags := Validate(schema, nil)
	found := findByCode(diags, "W008")
	if len(found) == 0 {
		t.Fatal("expected W008 for circular FK")
	}
}

func TestCleanSchema(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "public",
				Comment: "User accounts",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "email", PGType: "text"},
					{Name: "created_at", PGType: "timestamptz"},
				},
			},
			{
				Name:    "posts",
				Schema:  "public",
				Comment: "Blog posts",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "title", PGType: "text"},
					{Name: "created_at", PGType: "timestamptz"},
				},
				FKs: []model.FK{{
					Name:      "fk_user",
					Columns:   []string{"user_id"},
					RefSchema: "public",
					RefTable:  "users",
					OnDelete:  "cascade",
				}},
			},
		},
	}

	diags := Validate(schema, nil)
	errors := filterSeverity(diags, diagnostic.Error)
	if len(errors) > 0 {
		t.Fatalf("expected no errors for clean schema, got %d: %v", len(errors), errors)
	}
}

func TestDisabledRules(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "users",
			Schema: "public",
			PK:     []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
			},
			// Comment is empty -- would trigger E202
		}},
	}

	config := &Config{
		Disabled:      []string{"E202"},
		NamingPattern: "snake_case",
		MaxColumns:    30,
	}

	diags := Validate(schema, config)
	found := findByCode(diags, "E202")
	if len(found) > 0 {
		t.Fatal("expected E202 to be suppressed when disabled")
	}
}

// --- Helpers ---

func findByCode(diags []diagnostic.Diagnostic, code string) []diagnostic.Diagnostic {
	var result []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			result = append(result, d)
		}
	}
	return result
}

func filterSeverity(diags []diagnostic.Diagnostic, sev diagnostic.Severity) []diagnostic.Diagnostic {
	var result []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Severity == sev {
			result = append(result, d)
		}
	}
	return result
}
