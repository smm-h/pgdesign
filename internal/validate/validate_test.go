package validate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func TestE201_FKMissingOnDelete(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "user_id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
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
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
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
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			// Comment is empty
		}},
	}

	diags, _ := Validate(schema, nil)
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
				{Name: "data", PGType: typeinfo.T("jsonb")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
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
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "email", PGType: typeinfo.MustParse("varchar(255)")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
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
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E211")
	if len(found) == 0 {
		t.Fatal("expected E211 for CamelCase table name")
	}
}

func TestW001_GodTable(t *testing.T) {
	cols := make([]model.Column, 35)
	for i := range cols {
		cols[i] = model.Column{Name: "col_" + string(rune('a'+i/26)) + string(rune('a'+i%26)), PGType: typeinfo.T("text")}
	}
	cols = append(cols, model.Column{Name: "created_at", PGType: typeinfo.T("timestamptz")})

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "big_table",
			Schema:  "public",
			Comment: "A very wide table",
			PK:      []string{"col_aa"},
			Columns: cols,
		}},
	}

	diags, _ := Validate(schema, nil)
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
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid")}, {Name: "b_id", PGType: typeinfo.T("uuid")}, {Name: "created_at", PGType: typeinfo.T("timestamptz")}},
				FKs:     []model.FK{{Name: "fk_b", Columns: []string{"b_id"}, RefSchema: "public", RefTable: "b", OnDelete: "cascade"}},
			},
			{
				Name: "b", Schema: "public", Comment: "B",
				PK:      []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid")}, {Name: "c_id", PGType: typeinfo.T("uuid")}, {Name: "created_at", PGType: typeinfo.T("timestamptz")}},
				FKs:     []model.FK{{Name: "fk_c", Columns: []string{"c_id"}, RefSchema: "public", RefTable: "c", OnDelete: "cascade"}},
			},
			{
				Name: "c", Schema: "public", Comment: "C",
				PK:      []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid")}, {Name: "a_id", PGType: typeinfo.T("uuid")}, {Name: "created_at", PGType: typeinfo.T("timestamptz")}},
				FKs:     []model.FK{{Name: "fk_a", Columns: []string{"a_id"}, RefSchema: "public", RefTable: "a", OnDelete: "cascade"}},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W008")
	if len(found) == 0 {
		t.Fatal("expected W008 for circular FK")
	}
}

func TestE204_CrossSchemaFK_Passes(t *testing.T) {
	// When two schemas are merged, a cross-schema FK should resolve correctly.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "auth",
				Comment: "User accounts",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
			},
			{
				Name:    "players",
				Schema:  "game",
				Comment: "Game players",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "auth_id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:       "fk_players_auth",
					Columns:    []string{"auth_id"},
					RefSchema:  "auth",
					RefTable:   "users",
					RefColumns: []string{"id"},
					OnDelete:   "SET NULL",
				}},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E204")
	if len(found) > 0 {
		t.Fatalf("expected no E204 for valid cross-schema FK, got %v", found)
	}
}

func TestE204_CrossSchemaFK_FailsWhenMissing(t *testing.T) {
	// A cross-schema FK to a table not in the schema should still error.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "players",
				Schema:  "game",
				Comment: "Game players",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "auth_id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:       "fk_players_auth",
					Columns:    []string{"auth_id"},
					RefSchema:  "auth",
					RefTable:   "users",
					RefColumns: []string{"id"},
					OnDelete:   "SET NULL",
				}},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E204")
	if len(found) == 0 {
		t.Fatal("expected E204 for FK referencing non-existent cross-schema table")
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
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "email", PGType: typeinfo.T("text")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
			},
			{
				Name:    "posts",
				Schema:  "public",
				Comment: "Blog posts",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "user_id", PGType: typeinfo.T("uuid")},
					{Name: "title", PGType: typeinfo.T("text")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:      "fk_user",
					Columns:   []string{"user_id"},
					RefSchema: "public",
					RefTable:  "users",
					OnDelete:  "cascade",
				}},
				Indexes: []model.Index{{
					Name:    "idx_posts_user_id",
					Columns: []string{"user_id"},
				}},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	errors := filterSeverity(diags, diagnostic.Error)
	if len(errors) > 0 {
		t.Fatalf("expected no errors for clean schema, got %d: %v", len(errors), errors)
	}
}

func TestE200_MissingColumnType(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "events",
			Schema:  "public",
			Comment: "Events table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "data", PGType: typeinfo.T("")}, // missing type
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E200")
	if len(found) == 0 {
		t.Fatal("expected E200 for column missing type")
	}
	if found[0].Column != "data" {
		t.Errorf("expected column 'data', got %q", found[0].Column)
	}
}

func TestE212_FKMissingIndex(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Orders table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "user_id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:      "fk_user",
					Columns:   []string{"user_id"},
					RefSchema: "public",
					RefTable:  "users",
					OnDelete:  "cascade",
				}},
				// No indexes -- should trigger E212
			},
			{
				Name:    "users",
				Schema:  "public",
				Comment: "Users table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E212")
	if len(found) == 0 {
		t.Fatal("expected E212 for FK missing covering index")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestE212_FKWithIndex_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Orders table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "user_id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:      "fk_user",
					Columns:   []string{"user_id"},
					RefSchema: "public",
					RefTable:  "users",
					OnDelete:  "cascade",
				}},
				Indexes: []model.Index{{
					Name:    "idx_orders_user_id",
					Columns: []string{"user_id"},
				}},
			},
			{
				Name:    "users",
				Schema:  "public",
				Comment: "Users table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E212")
	if len(found) > 0 {
		t.Fatal("expected no E212 when FK has covering index")
	}
}

func TestW003_BooleanStates(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "is_active", PGType: typeinfo.T("bool")},
				{Name: "is_verified", PGType: typeinfo.T("bool")},
				{Name: "is_admin", PGType: typeinfo.T("bool")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W003")
	if len(found) == 0 {
		t.Fatal("expected W003 for 3+ boolean columns")
	}
	if found[0].Table != "users" {
		t.Errorf("expected table 'users', got %q", found[0].Table)
	}
}

func TestW003_TwoBooleans_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "is_active", PGType: typeinfo.T("bool")},
				{Name: "is_verified", PGType: typeinfo.T("bool")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W003")
	if len(found) > 0 {
		t.Fatal("expected no W003 for only 2 boolean columns")
	}
}

func TestW004_JSONCouldBeTable(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "tags", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W004")
	if len(found) == 0 {
		t.Fatal("expected W004 for plural jsonb column with array default")
	}
	if found[0].Column != "tags" {
		t.Errorf("expected column 'tags', got %q", found[0].Column)
	}
}

func TestW004_NonPlural_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "metadata", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W004")
	if len(found) > 0 {
		t.Fatal("expected no W004 for non-plural jsonb column")
	}
}

func TestW007_RedundantIndex(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "user_id", PGType: typeinfo.T("uuid")},
				{Name: "status", PGType: typeinfo.T("text")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{
				{Name: "idx_user", Columns: []string{"user_id"}, Method: "btree"},
				{Name: "idx_user_status", Columns: []string{"user_id", "status"}, Method: "btree"},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W007")
	if len(found) == 0 {
		t.Fatal("expected W007 for redundant index (prefix of another with same method)")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestW007_DifferentMethod_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "user_id", PGType: typeinfo.T("uuid")},
				{Name: "status", PGType: typeinfo.T("text")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{
				{Name: "idx_user_hash", Columns: []string{"user_id"}, Method: "hash"},
				{Name: "idx_user_status", Columns: []string{"user_id", "status"}, Method: "btree"},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W007")
	if len(found) > 0 {
		t.Fatal("expected no W007 when index methods differ")
	}
}

func TestDisabledRules(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "users",
			Schema: "public",
			PK:     []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			// Comment is empty -- would trigger E202
		}},
	}

	config := &Config{
		Disabled:      []string{"E202"},
		NamingPattern: "snake_case",
		MaxColumns:    30,
	}

	diags, _ := Validate(schema, config)
	found := findByCode(diags, "E202")
	if len(found) > 0 {
		t.Fatal("expected E202 to be suppressed when disabled")
	}
}

func TestE204_RefColumnNotFound(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Orders table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "user_id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:       "fk_user",
					Columns:    []string{"user_id"},
					RefSchema:  "public",
					RefTable:   "users",
					RefColumns: []string{"nonexistent_col"},
					OnDelete:   "cascade",
				}},
				Indexes: []model.Index{{
					Name:    "idx_orders_user_id",
					Columns: []string{"user_id"},
				}},
			},
			{
				Name:    "users",
				Schema:  "public",
				Comment: "Users table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E204")
	if len(found) == 0 {
		t.Fatal("expected E204 for FK referencing nonexistent column in referenced table")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestE204_RefColumnExists_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Orders table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "user_id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
				FKs: []model.FK{{
					Name:       "fk_user",
					Columns:    []string{"user_id"},
					RefSchema:  "public",
					RefTable:   "users",
					RefColumns: []string{"id"},
					OnDelete:   "cascade",
				}},
				Indexes: []model.Index{{
					Name:    "idx_orders_user_id",
					Columns: []string{"user_id"},
				}},
			},
			{
				Name:    "users",
				Schema:  "public",
				Comment: "Users table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "created_at", PGType: typeinfo.T("timestamptz")},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E204")
	if len(found) > 0 {
		t.Fatalf("expected no E204 when FK references an existing column, got %v", found)
	}
}

func TestE211_IndexNamingViolation(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "email", PGType: typeinfo.T("text")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{{
				Name:    "IdxUsersEmail",
				Columns: []string{"email"},
			}},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E211")
	if len(found) == 0 {
		t.Fatal("expected E211 for non-snake_case index name")
	}
	// Verify it's the index-specific diagnostic.
	indexDiag := false
	for _, d := range found {
		if d.Table == "users" && d.Message == `index name "IdxUsersEmail" violates naming convention (snake_case)` {
			indexDiag = true
			break
		}
	}
	if !indexDiag {
		t.Errorf("expected E211 diagnostic about index name, got %v", found)
	}
}

func TestE213_GeneratedColRefsGenerated(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "first_name", PGType: typeinfo.T("text")},
				{Name: "last_name", PGType: typeinfo.T("text")},
				{Name: "full_name", PGType: typeinfo.T("text"), Generated: "first_name || ' ' || last_name", Stored: true},
				{Name: "display_name", PGType: typeinfo.T("text"), Generated: "lower(full_name)", Stored: true},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E213")
	if len(found) == 0 {
		t.Fatal("expected E213 for generated column referencing another generated column")
	}
	if found[0].Column != "display_name" {
		t.Errorf("expected column 'display_name', got %q", found[0].Column)
	}
}

func TestE213_GeneratedColRefsRegular_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "first_name", PGType: typeinfo.T("text")},
				{Name: "last_name", PGType: typeinfo.T("text")},
				{Name: "full_name", PGType: typeinfo.T("text"), Generated: "first_name || ' ' || last_name", Stored: true},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E213")
	if len(found) > 0 {
		t.Fatalf("expected no E213 when generated column only references regular columns, got %v", found)
	}
}

func TestE213_GeneratedColWithFunctionCalls_NoDiag(t *testing.T) {
	// Function names like "lower" should not be mistakenly treated as column references.
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "products",
			Schema:  "public",
			Comment: "Products table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "name", PGType: typeinfo.T("text")},
				{Name: "price", PGType: typeinfo.T("numeric")},
				{Name: "search_name", PGType: typeinfo.T("text"), Generated: "lower(name)", Stored: true},
				{Name: "display_price", PGType: typeinfo.T("text"), Generated: "cast(price as text) || ' USD'", Stored: true},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E213")
	// Filter to errors only -- parse warnings (e.g., CAST(x AS type) not
	// supported by sqlexpr) are expected and benign.
	var errors []diagnostic.Diagnostic
	for _, d := range found {
		if d.Severity == diagnostic.Error {
			errors = append(errors, d)
		}
	}
	if len(errors) > 0 {
		t.Fatalf("expected no E213 errors when generated columns only reference regular columns, got %v", errors)
	}
}

func TestExtractColumnRefs(t *testing.T) {
	tests := []struct {
		expr string
		want []string
	}{
		{
			expr: "first_name || ' ' || last_name",
			want: []string{"first_name", "last_name"},
		},
		{
			expr: "lower(first_name || ' ' || last_name)",
			want: []string{"first_name", "last_name"},
		},
		{
			expr: "COALESCE(nickname, first_name)",
			want: []string{"nickname", "first_name"},
		},
		{
			expr: "price * quantity",
			want: []string{"price", "quantity"},
		},
		{
			expr: "CASE WHEN status = 'active' THEN 1 ELSE 0 END",
			want: []string{"status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, _ := extractColumnRefs(tt.expr, "test")
			if len(got) != len(tt.want) {
				t.Fatalf("extractColumnRefs(%q) = %v, want %v", tt.expr, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractColumnRefs(%q)[%d] = %q, want %q", tt.expr, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestE215_InsertPolicyWithUsingOnly(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:      "messages",
			Schema:    "public",
			Comment:   "Messages table",
			PK:        []string{"id"},
			EnableRLS: true,
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "channel_id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Policies: []model.Policy{{
				Name:      "insert_bad",
				Operation: "INSERT",
				Role:      "app",
				Using:     "channel_id IS NOT NULL",
				// No WithCheck -- INSERT should use with_check
			}},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E215")
	if len(found) == 0 {
		t.Fatal("expected E215 for INSERT policy with using but no with_check")
	}
	if found[0].Table != "messages" {
		t.Errorf("expected table 'messages', got %q", found[0].Table)
	}
}

func TestE215_SelectPolicyWithWithCheck(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:      "messages",
			Schema:    "public",
			Comment:   "Messages table",
			PK:        []string{"id"},
			EnableRLS: true,
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "channel_id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Policies: []model.Policy{{
				Name:      "select_bad",
				Operation: "SELECT",
				Role:      "app",
				WithCheck: "channel_id IS NOT NULL",
			}},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E215")
	if len(found) == 0 {
		t.Fatal("expected E215 for SELECT policy with with_check")
	}
}

func TestE215_UpdatePolicyBoth_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:      "messages",
			Schema:    "public",
			Comment:   "Messages table",
			PK:        []string{"id"},
			EnableRLS: true,
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "channel_id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Policies: []model.Policy{{
				Name:      "update_own",
				Operation: "UPDATE",
				Role:      "app",
				Using:     "channel_id = current_setting('app.channel_id')::uuid",
				WithCheck: "channel_id = current_setting('app.channel_id')::uuid",
			}},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E215")
	if len(found) > 0 {
		t.Fatal("expected no E215 for UPDATE policy with both using and with_check")
	}
}

func TestW009_PolicyErrorCodeNotSnakeCase(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:      "messages",
			Schema:    "public",
			Comment:   "Messages table",
			PK:        []string{"id"},
			EnableRLS: true,
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "channel_id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Policies: []model.Policy{{
				Name:      "insert_own",
				Operation: "INSERT",
				Role:      "app",
				WithCheck: "true",
				ErrorCode: "ChatDisabled",
			}},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W009")
	if len(found) == 0 {
		t.Fatal("expected W009 for non-snake_case error_code")
	}
}

func TestW009_PolicyErrorCodeSnakeCase_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:      "messages",
			Schema:    "public",
			Comment:   "Messages table",
			PK:        []string{"id"},
			EnableRLS: true,
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "channel_id", PGType: typeinfo.T("uuid")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Policies: []model.Policy{{
				Name:      "insert_own",
				Operation: "INSERT",
				Role:      "app",
				WithCheck: "true",
				ErrorCode: "chat_disabled",
			}},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W009")
	if len(found) > 0 {
		t.Fatal("expected no W009 for valid snake_case error_code")
	}
}

func TestSuppressW004_ColumnLevel(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "tags", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	cfg := &Config{
		Suppress: map[string]string{
			"users.tags.W004": "tags column is intentionally denormalized",
		},
	}

	diags, suppressed := Validate(schema, cfg)
	found := findByCode(diags, "W004")
	if len(found) > 0 {
		t.Fatal("expected W004 to be suppressed, but it appeared in active diagnostics")
	}

	// Verify it appears in suppressed list with the correct reason.
	var foundSuppressed bool
	for _, s := range suppressed {
		if s.Code == "W004" && s.Table == "users" && s.Column == "tags" {
			foundSuppressed = true
			if s.Reason != "tags column is intentionally denormalized" {
				t.Errorf("suppressed reason = %q, want %q", s.Reason, "tags column is intentionally denormalized")
			}
		}
	}
	if !foundSuppressed {
		t.Fatal("expected W004 in suppressed diagnostics")
	}
}

func TestSuppressW004_TableLevel(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "tags", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	cfg := &Config{
		Suppress: map[string]string{
			"users.W004": "all jsonb columns on users are intentional",
		},
	}

	diags, suppressed := Validate(schema, cfg)
	found := findByCode(diags, "W004")
	if len(found) > 0 {
		t.Fatal("expected W004 to be suppressed via table-level key")
	}

	var foundSuppressed bool
	for _, s := range suppressed {
		if s.Code == "W004" {
			foundSuppressed = true
			if s.Reason != "all jsonb columns on users are intentional" {
				t.Errorf("suppressed reason = %q, want %q", s.Reason, "all jsonb columns on users are intentional")
			}
		}
	}
	if !foundSuppressed {
		t.Fatal("expected W004 in suppressed diagnostics with table-level suppression")
	}
}

func TestSuppressW004_NoSuppression(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "public",
			Comment: "Users table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "tags", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	diags, suppressed := Validate(schema, nil)
	found := findByCode(diags, "W004")
	if len(found) == 0 {
		t.Fatal("expected W004 without suppression config")
	}
	if len(suppressed) > 0 {
		t.Error("expected no suppressed diagnostics without suppression config")
	}
}

func TestSuppressProgrammatic(t *testing.T) {
	// The Suppressed field on diagnostic.Diagnostic is for future use
	// (Phase 6.3 W004 auto-suppression for JSONB shapes). This test verifies
	// the field exists and is usable in the SuppressedDiagnostic type.
	sd := SuppressedDiagnostic{
		Diagnostic: diagnostic.Diagnostic{
			Severity:   diagnostic.Warning,
			Code:       "W004",
			Table:      "users",
			Column:     "tags",
			Message:    "test",
			Suppressed: true,
		},
		Reason: "programmatically suppressed",
	}
	if !sd.Suppressed {
		t.Error("expected Suppressed to be true")
	}
	if sd.Reason != "programmatically suppressed" {
		t.Errorf("reason = %q, want %q", sd.Reason, "programmatically suppressed")
	}
}

func TestW004_SuppressedWithJSONSchema(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "tags", PGType: typeinfo.T("jsonb"), NotNull: true, Default: model.StrPtr("'[]'::jsonb"), JSONSchema: "tags_schema.json"},
				},
				PK: []string{"id"},
			},
		},
	}

	diags, suppressed := Validate(schema, nil)

	// W004 should NOT appear in active diagnostics.
	found := findByCode(diags, "W004")
	if len(found) > 0 {
		t.Fatal("expected W004 to be auto-suppressed when JSONSchema is set, but it appeared in active diagnostics")
	}

	// W004 should appear in suppressed diagnostics.
	var foundSuppressed bool
	for _, s := range suppressed {
		if s.Code == "W004" && s.Table == "users" && s.Column == "tags" {
			foundSuppressed = true
			if s.Reason != "programmatically suppressed" {
				t.Errorf("suppressed reason = %q, want %q", s.Reason, "programmatically suppressed")
			}
		}
	}
	if !foundSuppressed {
		t.Fatal("expected W004 in suppressed diagnostics when JSONSchema is set")
	}
}

func TestW004_NotSuppressedWithoutJSONSchema(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "tags", PGType: typeinfo.T("jsonb"), NotNull: true, Default: model.StrPtr("'[]'::jsonb")},
				},
				PK: []string{"id"},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W004")
	if len(found) == 0 {
		t.Fatal("expected W004 to fire for plural jsonb column without JSONSchema")
	}
}

func TestAppendOnlyUpdatedAtWarning(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:    "events",
				Schema:  "app",
				Comment: "Event log",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "updated_at", PGType: typeinfo.T("timestamptz"), NotNull: true, SemanticTypeName: "timestamp"},
				},
				PK:         []string{"id"},
				AppendOnly: true,
			},
		},
	}

	diags, _ := Validate(schema, DefaultConfig())
	found := false
	for _, d := range diags {
		if d.Code == "W010" && d.Table == "events" && d.Column == "updated_at" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected W010 warning for append-only table with updated_at column")
	}
}

func TestE205_ColumnDefaultEmbeddedQuotes(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "test table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid")},
					{Name: "status", PGType: typeinfo.T("text"), Default: model.StrPtr("'pending'")},
					{Name: "data", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'{}'")},
					{Name: "count", PGType: typeinfo.T("int4"), Default: model.StrPtr("0")},
					{Name: "name", PGType: typeinfo.T("text"), Default: model.StrPtr("unknown")},
				},
			},
		},
	}
	active, _ := Validate(schema, nil)
	e205s := findByCode(active, "E205")
	if len(e205s) != 2 {
		t.Errorf("expected 2 E205 diagnostics, got %d", len(e205s))
		for _, d := range e205s {
			t.Logf("  E205: %s", d.Message)
		}
	}
	// Check that the two are for "status" and "data" columns
	cols := make(map[string]bool)
	for _, d := range e205s {
		cols[d.Column] = true
	}
	if !cols["status"] {
		t.Error("expected E205 for column 'status'")
	}
	if !cols["data"] {
		t.Error("expected E205 for column 'data'")
	}
}

func TestSuppressionPipeline_Integration(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "items", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb")},
				{Name: "tags", PGType: typeinfo.T("jsonb"), Default: model.StrPtr("'[]'::jsonb"), JSONSchema: "tags_schema.json"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
		}},
	}

	cfg := &Config{
		Suppress: map[string]string{
			"orders.items.W004": "intentional denormalization",
		},
	}

	diags, suppressed := Validate(schema, cfg)

	// W004 must not appear in active diagnostics.
	w004Active := findByCode(diags, "W004")
	if len(w004Active) > 0 {
		t.Fatalf("expected no active W004 diagnostics, got %d", len(w004Active))
	}

	// W004 must appear exactly 2 times in suppressed list.
	var w004Suppressed []SuppressedDiagnostic
	for _, s := range suppressed {
		if s.Code == "W004" {
			w004Suppressed = append(w004Suppressed, s)
		}
	}
	if len(w004Suppressed) != 2 {
		t.Fatalf("expected 2 suppressed W004 diagnostics, got %d", len(w004Suppressed))
	}

	// Verify config-based suppression for "items".
	var foundItems bool
	for _, s := range w004Suppressed {
		if s.Column == "items" {
			foundItems = true
			if s.Reason != "intentional denormalization" {
				t.Errorf("items suppression reason = %q, want %q", s.Reason, "intentional denormalization")
			}
		}
	}
	if !foundItems {
		t.Error("expected suppressed W004 for column 'items'")
	}

	// Verify programmatic suppression for "tags" (auto-suppressed via JSONSchema).
	var foundTags bool
	for _, s := range w004Suppressed {
		if s.Column == "tags" {
			foundTags = true
			if s.Reason != "programmatically suppressed" {
				t.Errorf("tags suppression reason = %q, want %q", s.Reason, "programmatically suppressed")
			}
		}
	}
	if !foundTags {
		t.Error("expected suppressed W004 for column 'tags'")
	}
}

func TestE216_InvalidWithParam(t *testing.T) {
	reg := extregistry.NewBuiltinRegistry()
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "t",
			Schema:  "public",
			Comment: "test",
			PK:      []string{"id"},
			Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			Indexes: []model.Index{{
				Name:    "idx_t_id",
				Columns: []string{"id"},
				Method:  "btree",
				With:    map[string]string{"invalid_param": "100"},
			}},
		}},
	}
	diags, _ := Validate(schema, &Config{
		ExtRegistry: reg,
		Disabled:    []string{"E202", "W002", "W005"},
	})
	found := findByCode(diags, "E216")
	if len(found) == 0 {
		t.Fatal("expected E216 diagnostic for invalid btree WITH param")
	}
}

func TestE216_ValidHnswParams_NoDiag(t *testing.T) {
	reg := extregistry.NewBuiltinRegistry()
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "t",
			Schema:  "public",
			Comment: "test",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
				{Name: "embedding", PGType: typeinfo.MustParse("vector(384)"), NotNull: true},
			},
			Indexes: []model.Index{{
				Name:    "idx_t_embedding",
				Columns: []string{"embedding"},
				Method:  "hnsw",
				With:    map[string]string{"m": "16", "ef_construction": "200"},
			}},
		}},
	}
	diags, _ := Validate(schema, &Config{
		ExtRegistry: reg,
		Disabled:    []string{"E202", "W002", "W005"},
	})
	found := findByCode(diags, "E216")
	if len(found) != 0 {
		t.Fatalf("unexpected E216 diagnostic: %s", found[0].Message)
	}
}

func TestE218_VirtualRequiresPG18_Error(t *testing.T) {
	schema := &model.Schema{
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Order records",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "val", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "computed", PGType: typeinfo.T("int4"), NotNull: true, Generated: "val * 2", Stored: false},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E218")
	if len(found) != 1 {
		t.Fatalf("expected 1 E218 diagnostic, got %d: %v", len(found), found)
	}
	if found[0].Severity != diagnostic.Error {
		t.Errorf("E218 severity = %v, want Error", found[0].Severity)
	}
	if found[0].Column != "computed" {
		t.Errorf("E218 column = %q, want %q", found[0].Column, "computed")
	}
}

func TestE218_VirtualRequiresPG18_UnknownVersion(t *testing.T) {
	// PGVersion 0 (unknown): pgcap.Has returns false, so the check treats
	// it as a version that lacks VirtualGeneratedCols support -> Error.
	// PG version is mandatory in production, so this is a safety net.
	schema := &model.Schema{
		PGVersion: 0,
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Order records",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "val", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "computed", PGType: typeinfo.T("int4"), NotNull: true, Generated: "val * 2", Stored: false},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E218")
	if len(found) != 1 {
		t.Fatalf("expected 1 E218 diagnostic, got %d: %v", len(found), found)
	}
	if found[0].Severity != diagnostic.Error {
		t.Errorf("E218 severity = %v, want Error", found[0].Severity)
	}
}

func TestE218_VirtualRequiresPG18_NoDiag(t *testing.T) {
	// PG 18+ should not trigger E218.
	schema := &model.Schema{
		PGVersion: 18,
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Order records",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "val", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "computed", PGType: typeinfo.T("int4"), NotNull: true, Generated: "val * 2", Stored: false},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E218")
	if len(found) != 0 {
		t.Errorf("expected no E218 on PG18, got %d: %v", len(found), found)
	}
}

func TestE218_StoredGenerated_NoDiag(t *testing.T) {
	// Stored generated columns should never trigger E218, regardless of PG version.
	schema := &model.Schema{
		PGVersion: 12,
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "Order records",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "val", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "computed", PGType: typeinfo.T("int4"), NotNull: true, Generated: "val * 2", Stored: true},
				},
			},
		},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E218")
	if len(found) != 0 {
		t.Errorf("expected no E218 for stored generated column, got %d: %v", len(found), found)
	}
}

func TestE217_HnswWithPgvectorDeclared_NoDiag(t *testing.T) {
	reg := extregistry.NewBuiltinRegistry()
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "items",
			Schema:  "public",
			Comment: "Items table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "embedding", PGType: typeinfo.MustParse("vector(384)")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{{
				Name:    "idx_items_embedding",
				Columns: []string{"embedding"},
				Method:  "hnsw",
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		ExtRegistry: reg,
		Extensions:  []string{"pgvector"},
		Disabled:    []string{"W002"},
	})
	found := findByCode(diags, "E217")
	if len(found) > 0 {
		t.Fatalf("expected no E217 when pgvector is declared, got: %s", found[0].Message)
	}
}

func TestE219_HnswWithoutPgvectorDeclared(t *testing.T) {
	reg := extregistry.NewBuiltinRegistry()
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "items",
			Schema:  "public",
			Comment: "Items table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "embedding", PGType: typeinfo.MustParse("vector(384)")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{{
				Name:    "idx_items_embedding",
				Columns: []string{"embedding"},
				Method:  "hnsw",
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		ExtRegistry: reg,
		Extensions:  nil, // pgvector NOT declared
		Disabled:    []string{"W002"},
	})
	found := findByCode(diags, "E219")
	if len(found) == 0 {
		t.Fatal("expected E219 when hnsw used without pgvector declared")
	}
	if found[0].Table != "items" {
		t.Errorf("expected table 'items', got %q", found[0].Table)
	}
}

func TestE217_BtreeIndex_NoDiag(t *testing.T) {
	reg := extregistry.NewBuiltinRegistry()
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "items",
			Schema:  "public",
			Comment: "Items table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "name", PGType: typeinfo.T("text")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{{
				Name:    "idx_items_name",
				Columns: []string{"name"},
				Method:  "btree",
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		ExtRegistry: reg,
		Disabled:    []string{"W002"},
	})
	found := findByCode(diags, "E217")
	if len(found) > 0 {
		t.Fatalf("expected no E217 for builtin btree method, got: %s", found[0].Message)
	}
}

func TestE217_UnknownMethod(t *testing.T) {
	reg := extregistry.NewBuiltinRegistry()
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "items",
			Schema:  "public",
			Comment: "Items table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid")},
				{Name: "name", PGType: typeinfo.T("text")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz")},
			},
			Indexes: []model.Index{{
				Name:    "idx_items_name",
				Columns: []string{"name"},
				Method:  "foo",
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		ExtRegistry: reg,
		Disabled:    []string{"W002"},
	})
	found := findByCode(diags, "E217")
	if len(found) == 0 {
		t.Fatal("expected E217 for unknown index method 'foo'")
	}
	if found[0].Table != "items" {
		t.Errorf("expected table 'items', got %q", found[0].Table)
	}
}

func TestE221_ExclusionBtreeGistMissing(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Schema:  "public",
			Comment: "Room bookings",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int4")},
				{Name: "room_id", PGType: typeinfo.T("int4")},
				{Name: "during", PGType: typeinfo.T("tsrange")},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "room_id", Operator: "="},
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		Extensions: nil, // btree_gist NOT declared
	})
	found := findByCode(diags, "E221")
	if len(found) == 0 {
		t.Fatal("expected E221 when exclusion uses = operator without btree_gist")
	}
	if found[0].Table != "bookings" {
		t.Errorf("expected table 'bookings', got %q", found[0].Table)
	}
}

func TestE221_ExclusionBtreeGistPresent(t *testing.T) {
	schema := &model.Schema{
		Extensions: []string{"btree_gist"},
		Tables: []model.Table{{
			Name:    "bookings",
			Schema:  "public",
			Comment: "Room bookings",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int4")},
				{Name: "room_id", PGType: typeinfo.T("int4")},
				{Name: "during", PGType: typeinfo.T("tsrange")},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "room_id", Operator: "="},
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		Extensions: schema.Extensions,
	})
	found := findByCode(diags, "E221")
	if len(found) > 0 {
		t.Fatalf("expected no E221 when btree_gist is declared, got: %s", found[0].Message)
	}
}

func TestE221_ExclusionRangeOperatorOnly(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Schema:  "public",
			Comment: "Room bookings",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int4")},
				{Name: "during", PGType: typeinfo.T("tsrange")},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_time_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}

	diags, _ := Validate(schema, &Config{
		Extensions: nil, // no btree_gist needed
	})
	found := findByCode(diags, "E221")
	if len(found) > 0 {
		t.Fatalf("expected no E221 when only && operator is used, got: %s", found[0].Message)
	}
}

func TestE222_RestrictivePolicyPG9_Error(t *testing.T) {
	schema := &model.Schema{
		PGVersion: 9,
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{{
					Name:      "restrict_read",
					Type:      "RESTRICTIVE",
					Operation: "SELECT",
					Using:     "false",
				}},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := false
	for _, d := range diags {
		if d.Code == "E222" && d.Severity == diagnostic.Error {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected E222 error for RESTRICTIVE policy on PG 9")
	}
}

func TestE222_RestrictivePolicyPGUnknown_Error(t *testing.T) {
	// PGVersion 0 (unknown): pgcap.Has returns false, so the check treats
	// it as a version that lacks RestrictiveRLS support -> Error.
	// PG version is mandatory in production, so this is a safety net.
	schema := &model.Schema{
		PGVersion: 0,
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{{
					Name:      "restrict_read",
					Type:      "RESTRICTIVE",
					Operation: "SELECT",
					Using:     "false",
				}},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := false
	for _, d := range diags {
		if d.Code == "E222" && d.Severity == diagnostic.Error {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected E222 error for RESTRICTIVE policy with unknown PG version")
	}
}

func TestE222_RestrictivePolicyPG10_NoDiag(t *testing.T) {
	schema := &model.Schema{
		PGVersion: 10,
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{{
					Name:      "restrict_read",
					Type:      "RESTRICTIVE",
					Operation: "SELECT",
					Using:     "false",
				}},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "E222" {
			t.Fatalf("unexpected E222 for PG 10: %v", d)
		}
	}
}

func TestE222_PermissivePolicyPG9_NoDiag(t *testing.T) {
	schema := &model.Schema{
		PGVersion: 9,
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{{
					Name:      "allow_read",
					Type:      "PERMISSIVE",
					Operation: "SELECT",
					Using:     "true",
				}},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "E222" {
			t.Fatalf("unexpected E222 for PERMISSIVE policy: %v", d)
		}
	}
}

func TestW011_RLSWithoutPolicies(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:      "secrets",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := false
	for _, d := range diags {
		if d.Code == "W011" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected W011 for enable_rls with no policies")
	}
}

func TestW011_RLSWithPolicies_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{{
					Name:      "read_all",
					Type:      "PERMISSIVE",
					Operation: "ALL",
					Using:     "true",
				}},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W011" {
			t.Fatalf("unexpected W011 when policies exist: %v", d)
		}
	}
}

func TestW012_RLSOperationGap(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{
					{Name: "read", Type: "PERMISSIVE", Operation: "SELECT", Using: "true"},
					{Name: "write", Type: "PERMISSIVE", Operation: "INSERT", WithCheck: "true"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := false
	for _, d := range diags {
		if d.Code == "W012" {
			found = true
			if !strings.Contains(d.Message, "UPDATE") || !strings.Contains(d.Message, "DELETE") {
				t.Errorf("expected W012 to mention UPDATE and DELETE, got: %s", d.Message)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected W012 for missing UPDATE and DELETE policies")
	}
}

func TestW012_ALLCoversEverything_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{{
					Name:      "full_access",
					Type:      "PERMISSIVE",
					Operation: "ALL",
					Using:     "true",
				}},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W012" {
			t.Fatalf("unexpected W012 when ALL operation covers everything: %v", d)
		}
	}
}

func TestW012_AllFourOperations_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
				Policies: []model.Policy{
					{Name: "read", Type: "PERMISSIVE", Operation: "SELECT", Using: "true"},
					{Name: "create", Type: "PERMISSIVE", Operation: "INSERT", WithCheck: "true"},
					{Name: "edit", Type: "PERMISSIVE", Operation: "UPDATE", Using: "true", WithCheck: "true"},
					{Name: "remove", Type: "PERMISSIVE", Operation: "DELETE", Using: "true"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W012" {
			t.Fatalf("unexpected W012 when all operations covered: %v", d)
		}
	}
}

func TestW012_NoPolicies_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:      "docs",
				Schema:    "public",
				EnableRLS: true,
				Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:        []string{"id"},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W012" {
			t.Fatalf("unexpected W012 when no policies exist (W011 handles this case): %v", d)
		}
	}
}

// --- W013: CASCADE depth exceeds threshold ---

func TestW013_CascadeDepthExceedsThreshold(t *testing.T) {
	// Chain: a -> b -> c -> d -> e, all CASCADE. CascadeDepth(a) = 4 > 3.
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "a", Schema: "public", Comment: "A", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "b_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_b", Columns: []string{"b_id"}, RefSchema: "public", RefTable: "b", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "b", Schema: "public", Comment: "B", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "c_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_c", Columns: []string{"c_id"}, RefSchema: "public", RefTable: "c", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "c", Schema: "public", Comment: "C", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "d_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_d", Columns: []string{"d_id"}, RefSchema: "public", RefTable: "d", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "d", Schema: "public", Comment: "D", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "e_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_e", Columns: []string{"e_id"}, RefSchema: "public", RefTable: "e", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "e", Schema: "public", Comment: "E", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
		},
	}
	schema.BuildFKGraph()
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W013")
	if len(found) == 0 {
		t.Fatal("expected W013 for cascade depth > 3")
	}
	foundA := false
	for _, d := range found {
		if d.Table == "a" {
			foundA = true
			if !strings.Contains(d.Message, "4") {
				t.Errorf("expected depth 4 in message, got: %s", d.Message)
			}
		}
	}
	if !foundA {
		t.Error("expected W013 on table 'a'")
	}
}

func TestW013_CascadeDepthAtThreshold_NoDiag(t *testing.T) {
	// Chain: a -> b -> c -> d, all CASCADE. CascadeDepth(a) = 3 = threshold. No trigger.
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "a", Schema: "public", Comment: "A", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "b_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_b", Columns: []string{"b_id"}, RefSchema: "public", RefTable: "b", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "b", Schema: "public", Comment: "B", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "c_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_c", Columns: []string{"c_id"}, RefSchema: "public", RefTable: "c", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "c", Schema: "public", Comment: "C", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "d_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_d", Columns: []string{"d_id"}, RefSchema: "public", RefTable: "d", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "d", Schema: "public", Comment: "D", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
		},
	}
	schema.BuildFKGraph()
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W013" && d.Table == "a" {
			t.Fatalf("unexpected W013 for table 'a' when cascade depth equals threshold: %v", d)
		}
	}
}

// --- W014: CASCADE breadth exceeds threshold ---

func TestW014_CascadeBreadthExceedsThreshold(t *testing.T) {
	// Table "hub" has CASCADE FKs to 5 leaf tables. CascadeBreadth(hub) = 5 >= 5.
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "hub", Schema: "public", Comment: "Hub", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "a_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "b_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "c_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "d_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "e_id", PGType: typeinfo.T("uuid"), NotNull: true},
				},
				FKs: []model.FK{
					{Name: "fk_a", Columns: []string{"a_id"}, RefSchema: "public", RefTable: "leaf_a", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					{Name: "fk_b", Columns: []string{"b_id"}, RefSchema: "public", RefTable: "leaf_b", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					{Name: "fk_c", Columns: []string{"c_id"}, RefSchema: "public", RefTable: "leaf_c", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					{Name: "fk_d", Columns: []string{"d_id"}, RefSchema: "public", RefTable: "leaf_d", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					{Name: "fk_e", Columns: []string{"e_id"}, RefSchema: "public", RefTable: "leaf_e", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				}},
			{Name: "leaf_a", Schema: "public", Comment: "A", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "leaf_b", Schema: "public", Comment: "B", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "leaf_c", Schema: "public", Comment: "C", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "leaf_d", Schema: "public", Comment: "D", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "leaf_e", Schema: "public", Comment: "E", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
		},
	}
	schema.BuildFKGraph()
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W014")
	if len(found) == 0 {
		t.Fatal("expected W014 for cascade breadth >= 5")
	}
	foundHub := false
	for _, d := range found {
		if d.Table == "hub" {
			foundHub = true
		}
	}
	if !foundHub {
		t.Error("expected W014 on table 'hub'")
	}
}

func TestW014_CascadeBreadthBelowThreshold_NoDiag(t *testing.T) {
	// Table "hub" cascades to 3 leaf tables. CascadeBreadth(hub) = 3 < 5. No trigger.
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "hub", Schema: "public", Comment: "Hub", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "a_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "b_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "c_id", PGType: typeinfo.T("uuid"), NotNull: true},
				},
				FKs: []model.FK{
					{Name: "fk_a", Columns: []string{"a_id"}, RefSchema: "public", RefTable: "leaf_a", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					{Name: "fk_b", Columns: []string{"b_id"}, RefSchema: "public", RefTable: "leaf_b", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					{Name: "fk_c", Columns: []string{"c_id"}, RefSchema: "public", RefTable: "leaf_c", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				}},
			{Name: "leaf_a", Schema: "public", Comment: "A", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "leaf_b", Schema: "public", Comment: "B", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "leaf_c", Schema: "public", Comment: "C", PK: []string{"id"}, Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
		},
	}
	schema.BuildFKGraph()
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W014" && d.Table == "hub" {
			t.Fatalf("unexpected W014 for table 'hub' with breadth 3 < 5: %v", d)
		}
	}
}

// --- W015: Mixed ON DELETE actions ---

func TestW015_MixedOnDeleteActions(t *testing.T) {
	// Table "target" referenced by two FKs with different ON DELETE actions.
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "target", Schema: "public", Comment: "Target", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "child_a", Schema: "public", Comment: "Child A", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "target_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_target_a", Columns: []string{"target_id"}, RefSchema: "public", RefTable: "target", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "child_b", Schema: "public", Comment: "Child B", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "target_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_target_b", Columns: []string{"target_id"}, RefSchema: "public", RefTable: "target", RefColumns: []string{"id"}, OnDelete: "RESTRICT"}}},
		},
	}
	schema.BuildFKGraph()
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W015")
	if len(found) == 0 {
		t.Fatal("expected W015 for mixed ON DELETE actions on table 'target'")
	}
	if found[0].Table != "target" {
		t.Errorf("expected W015 on table 'target', got %q", found[0].Table)
	}
	if !strings.Contains(found[0].Message, "CASCADE") || !strings.Contains(found[0].Message, "RESTRICT") {
		t.Errorf("expected message to mention both CASCADE and RESTRICT, got: %s", found[0].Message)
	}
}

func TestW015_ConsistentOnDelete_NoDiag(t *testing.T) {
	// All incoming FKs to "target" use CASCADE. No W015.
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "target", Schema: "public", Comment: "Target", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}}},
			{Name: "child_a", Schema: "public", Comment: "Child A", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "target_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_target_a", Columns: []string{"target_id"}, RefSchema: "public", RefTable: "target", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "child_b", Schema: "public", Comment: "Child B", PK: []string{"id"},
				Columns: []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}, {Name: "target_id", PGType: typeinfo.T("uuid"), NotNull: true}},
				FKs: []model.FK{{Name: "fk_target_b", Columns: []string{"target_id"}, RefSchema: "public", RefTable: "target", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
		},
	}
	schema.BuildFKGraph()
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W015" && d.Table == "target" {
			t.Fatalf("unexpected W015 when all incoming FKs use CASCADE: %v", d)
		}
	}
}

// --- I001: Natural key candidate ---

func TestI001_NaturalKeyCandidate(t *testing.T) {
	// Table with PK [id] and FDs declaring [email] as a candidate key.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "Users", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true, SemanticTypeName: "id"},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true, SemanticTypeName: "email"},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
				Dependencies: []fd.FuncDep{
					{Determinant: []string{"id"}, Dependent: []string{"email", "name"}, Source: "declared"},
					{Determinant: []string{"email"}, Dependent: []string{"id", "name"}, Source: "declared"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I001")
	if len(found) == 0 {
		t.Fatal("expected I001 for natural key candidate [email]")
	}
	if !strings.Contains(found[0].Message, "email") {
		t.Errorf("expected I001 message to mention email, got: %s", found[0].Message)
	}
	if found[0].Severity != diagnostic.Info {
		t.Errorf("expected Info severity, got %v", found[0].Severity)
	}
}

func TestI001_OnlySurrogateKeys_NoDiag(t *testing.T) {
	// All candidate keys contain surrogate columns (id/auto_id/ref). No I001.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "items", Schema: "public", Comment: "Items", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true, SemanticTypeName: "id"},
					{Name: "ref_code", PGType: typeinfo.T("uuid"), NotNull: true, SemanticTypeName: "ref"},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
				Dependencies: []fd.FuncDep{
					{Determinant: []string{"id"}, Dependent: []string{"ref_code", "name"}, Source: "declared"},
					{Determinant: []string{"ref_code"}, Dependent: []string{"id", "name"}, Source: "declared"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "I001" {
			t.Fatalf("unexpected I001 when all candidate keys contain surrogate columns: %v", d)
		}
	}
}

func TestI001_NoDependencies_NoDiag(t *testing.T) {
	// Table has no declared functional dependencies. No I001.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "simple", Schema: "public", Comment: "Simple", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "I001" {
			t.Fatalf("unexpected I001 when no dependencies declared: %v", d)
		}
	}
}

// --- W016: PK subsumes UNIQUE ---

func TestW016_PKSubsumesUnique(t *testing.T) {
	// UNIQUE constraint on [id] which is already the PK.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "docs", Schema: "public", Comment: "Docs", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "title", PGType: typeinfo.T("text"), NotNull: true},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_id", Columns: []string{"id"}},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W016")
	if len(found) == 0 {
		t.Fatal("expected W016 for UNIQUE on PK columns")
	}
	if found[0].Table != "docs" {
		t.Errorf("expected W016 on table 'docs', got %q", found[0].Table)
	}
	if !strings.Contains(found[0].Message, "uq_id") {
		t.Errorf("expected message to mention constraint name 'uq_id', got: %s", found[0].Message)
	}
}

func TestW016_UniqueOnDifferentColumns_NoDiag(t *testing.T) {
	// UNIQUE on [email] while PK is [id]. No W016.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "Users", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_email", Columns: []string{"email"}},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W016" {
			t.Fatalf("unexpected W016 when UNIQUE is on different columns than PK: %v", d)
		}
	}
}

// --- W017: Redundant IS NOT NULL CHECK ---

func TestW017_RedundantIsNotNullCheck(t *testing.T) {
	// Column "name" is NOT NULL with CHECK "name IS NOT NULL".
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "items", Schema: "public", Comment: "Items", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_name_not_null", Expr: "name IS NOT NULL"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W017")
	if len(found) == 0 {
		t.Fatal("expected W017 for redundant IS NOT NULL CHECK on NOT NULL column")
	}
	if !strings.Contains(found[0].Message, "name") {
		t.Errorf("expected message to mention column 'name', got: %s", found[0].Message)
	}
}

func TestW017_NullableColumn_NoDiag(t *testing.T) {
	// Column "name" is nullable, so CHECK "name IS NOT NULL" is not redundant.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "items", Schema: "public", Comment: "Items", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: false},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_name_not_null", Expr: "name IS NOT NULL"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W017" {
			t.Fatalf("unexpected W017 when column is nullable: %v", d)
		}
	}
}

// --- W018: Domain CHECK duplicate ---

func TestW018_DomainCheckDuplicate(t *testing.T) {
	// Domain "email_type" has CHECK "VALUE ~ '^.+@.+$'".
	// Column of semantic type "email_type" has column-level CHECK with same expression.
	schema := &model.Schema{
		Domains: []model.Domain{
			{Name: "email_type", BaseType: typeinfo.T("text"), Check: "VALUE ~ '^.+@.+$'"},
		},
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "Users", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true, SemanticTypeName: "email_type"},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_email", Expr: "email ~ '^.+@.+$'"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W018")
	if len(found) == 0 {
		t.Fatal("expected W018 for column CHECK identical to domain CHECK")
	}
	if !strings.Contains(found[0].Message, "email") {
		t.Errorf("expected message to mention column 'email', got: %s", found[0].Message)
	}
	if !strings.Contains(found[0].Message, "email_type") {
		t.Errorf("expected message to mention domain 'email_type', got: %s", found[0].Message)
	}
}

func TestW018_DifferentCheck_NoDiag(t *testing.T) {
	// Domain CHECK and column CHECK differ. No W018.
	schema := &model.Schema{
		Domains: []model.Domain{
			{Name: "email_type", BaseType: typeinfo.T("text"), Check: "VALUE ~ '^.+@.+$'"},
		},
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "Users", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true, SemanticTypeName: "email_type"},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_email_len", Expr: "length(email) > 5"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W018" {
			t.Fatalf("unexpected W018 when domain and column CHECKs differ: %v", d)
		}
	}
}

// --- W019: Range CHECK subsumption ---

func TestW019_RangeSubsumption(t *testing.T) {
	// Two CHECKs on same column: [0,200] subsumes [1,50]. The narrower is reported redundant.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "people", Schema: "public", Comment: "People", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "age", PGType: typeinfo.T("int4"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_age_wide", Expr: "age >= 0 AND age <= 200"},
					{Name: "chk_age_narrow", Expr: "age >= 1 AND age <= 50"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W019")
	if len(found) == 0 {
		t.Fatal("expected W019 for range subsumption")
	}
	// The wider range [0,200] is redundant because the stricter [1,50] already enforces the range.
	if !strings.Contains(found[0].Message, "chk_age_wide") {
		t.Errorf("expected message to mention 'chk_age_wide' as redundant, got: %s", found[0].Message)
	}
	if !strings.Contains(found[0].Message, "chk_age_narrow") {
		t.Errorf("expected message to mention 'chk_age_narrow' as the stricter constraint, got: %s", found[0].Message)
	}
}

func TestW019_OpenEndedRange(t *testing.T) {
	// age >= 0 (open-ended high) subsumes age >= 0 AND age <= 200.
	// The open-ended constraint is wider and should be flagged redundant.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "people", Schema: "public", Comment: "People", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "age", PGType: typeinfo.T("int4"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_age_positive", Expr: "age >= 0"},
					{Name: "chk_age_bounded", Expr: "age >= 0 AND age <= 200"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W019")
	if len(found) == 0 {
		t.Fatal("expected W019 for open-ended range subsumption")
	}
	if !strings.Contains(found[0].Message, "chk_age_positive") {
		t.Errorf("expected open-ended 'chk_age_positive' to be flagged as wider/redundant, got: %s", found[0].Message)
	}
}

func TestW019_NonInclusiveBounds(t *testing.T) {
	// age > 0 AND age < 100 is subsumed by age >= 0 AND age <= 100 (wider).
	// The wider constraint should be flagged redundant.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "items", Schema: "public", Comment: "Items", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "score", PGType: typeinfo.T("int4"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_score_wide", Expr: "score >= 0 AND score <= 100"},
					{Name: "chk_score_strict", Expr: "score > 0 AND score < 100"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W019")
	if len(found) == 0 {
		t.Fatal("expected W019 for non-inclusive bound subsumption")
	}
	if !strings.Contains(found[0].Message, "chk_score_wide") {
		t.Errorf("expected 'chk_score_wide' to be flagged as wider/redundant, got: %s", found[0].Message)
	}
}

func TestW019_NegativeNumbers(t *testing.T) {
	// balance >= -1000 AND balance <= 1000 subsumes balance >= -500 AND balance <= 500.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "accounts", Schema: "public", Comment: "Accounts", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "balance", PGType: typeinfo.T("numeric"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_balance_wide", Expr: "balance >= -1000 AND balance <= 1000"},
					{Name: "chk_balance_narrow", Expr: "balance >= -500 AND balance <= 500"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W019")
	if len(found) == 0 {
		t.Fatal("expected W019 for negative number range subsumption")
	}
	if !strings.Contains(found[0].Message, "chk_balance_wide") {
		t.Errorf("expected 'chk_balance_wide' to be flagged as wider/redundant, got: %s", found[0].Message)
	}
}

func TestW019_EqualRanges_NoRedundancy(t *testing.T) {
	// Two constraints with identical bounds are not redundant (neither is strictly wider).
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "items", Schema: "public", Comment: "Items", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "qty", PGType: typeinfo.T("int4"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_qty_a", Expr: "qty >= 0 AND qty <= 100"},
					{Name: "chk_qty_b", Expr: "qty >= 0 AND qty <= 100"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W019" {
			t.Fatalf("unexpected W019 for equal ranges: %v", d)
		}
	}
}

func TestW019_NoOverlap_NoDiag(t *testing.T) {
	// CHECKs on different columns. No subsumption possible.
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "products", Schema: "public", Comment: "Products", PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "price", PGType: typeinfo.T("numeric"), NotNull: true},
					{Name: "quantity", PGType: typeinfo.T("int4"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_price", Expr: "price >= 0"},
					{Name: "chk_qty", Expr: "quantity >= 0"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "W019" {
			t.Fatalf("unexpected W019 when CHECKs are on different columns: %v", d)
		}
	}
}

func TestI002_DeadColumn(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "orders", Schema: "public", Comment: "Orders",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "status", PGType: typeinfo.T("text"), NotNull: true},
					{Name: "orphan_col", PGType: typeinfo.T("text"), NotNull: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_status", Expr: "status IN ('active', 'cancelled')"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I002")
	if len(found) != 1 {
		t.Fatalf("expected 1 I002 diagnostic, got %d: %v", len(found), found)
	}
	if found[0].Column != "orphan_col" {
		t.Errorf("expected I002 on orphan_col, got %s", found[0].Column)
	}
}

func TestI002_AllColumnsReferenced_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "Users",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "org_id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
					{Name: "age", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "full_name", PGType: typeinfo.T("text"), NotNull: true, Generated: "name || ' (user)'"},
				},
				FKs: []model.FK{
					{Name: "fk_org", Columns: []string{"org_id"}, RefTable: "orgs", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_email", Columns: []string{"email"}},
				},
				Indexes: []model.Index{
					{Name: "idx_name", Columns: []string{"name"}},
					{Name: "idx_full_name", Columns: []string{"full_name"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "chk_age", Expr: "age >= 0"},
				},
			},
			{
				Name: "orgs", Schema: "public", Comment: "Organizations",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I002")
	if len(found) != 0 {
		t.Fatalf("expected no I002 diagnostics, got %d: %v", len(found), found)
	}
}

func TestI002_FKRefColumnsReferenced(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "orders", Schema: "public", Comment: "Orders",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "code", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
			{
				Name: "items", Schema: "public", Comment: "Items",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "order_id", PGType: typeinfo.T("uuid"), NotNull: true},
				},
				FKs: []model.FK{
					{Name: "fk_order", Columns: []string{"order_id"}, RefTable: "orders", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I002")
	// "code" on orders should be flagged (not in PK, not referenced by FK refcolumns, not in any constraint)
	if len(found) != 1 {
		t.Fatalf("expected 1 I002 diagnostic (for orders.code), got %d: %v", len(found), found)
	}
	if found[0].Table != "orders" || found[0].Column != "code" {
		t.Errorf("expected I002 on orders.code, got %s.%s", found[0].Table, found[0].Column)
	}
}

func TestI002_ViewReferenceSuppresses(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "products", Schema: "public", Comment: "Products",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "unreferenced", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
		},
		Views: []model.View{
			{Name: "v_products", Query: "SELECT * FROM products"},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I002")
	if len(found) != 0 {
		t.Fatalf("expected no I002 when view references table, got %d: %v", len(found), found)
	}
}

func TestI002_PolicyUsingSuppresses(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "secrets", Schema: "public", Comment: "Secrets",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "owner_id", PGType: typeinfo.T("uuid"), NotNull: true},
				},
				EnableRLS: true,
				Policies: []model.Policy{
					{Name: "owner_only", Operation: "ALL", Using: "owner_id = current_user_id()"},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I002")
	if len(found) != 0 {
		t.Fatalf("expected no I002 when policy USING references column, got %d: %v", len(found), found)
	}
}

func TestI003_RowSizeToastThreshold(t *testing.T) {
	cols := []model.Column{
		{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
	}
	for i := 0; i < 32; i++ {
		cols = append(cols, model.Column{
			Name:    fmt.Sprintf("data_%d", i),
			PGType: typeinfo.T("jsonb"),
			NotNull: true,
		})
	}
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "wide_table", Schema: "public", Comment: "Wide table",
				PK: []string{"id"},
				Columns: cols,
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I003")
	if len(found) != 1 {
		t.Fatalf("expected 1 I003 diagnostic, got %d: %v", len(found), found)
	}
}

func TestW021_RowSizeExceedsPage(t *testing.T) {
	cols := []model.Column{
		{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
	}
	for i := 0; i < 130; i++ {
		cols = append(cols, model.Column{
			Name:    fmt.Sprintf("blob_%d", i),
			PGType: typeinfo.T("jsonb"),
			NotNull: true,
		})
	}
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "mega_table", Schema: "public", Comment: "Mega table",
				PK: []string{"id"},
				Columns: cols,
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "W021")
	if len(found) != 1 {
		t.Fatalf("expected 1 W021 diagnostic, got %d: %v", len(found), found)
	}
	// Should NOT also have I003 (W021 is exclusive with I003)
	i003 := findByCode(diags, "I003")
	if len(i003) != 0 {
		t.Errorf("expected no I003 when W021 fires, got %d", len(i003))
	}
}

func TestI003_SmallTable_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "Users",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
					{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	for _, d := range diags {
		if d.Code == "I003" || d.Code == "W021" {
			t.Fatalf("unexpected row size diagnostic on small table: %v", d)
		}
	}
}

func TestI004_ColumnReordering(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "padded", Schema: "public", Comment: "Padded table",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "flag1", PGType: typeinfo.T("bool"), NotNull: true},
					{Name: "big1", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "flag2", PGType: typeinfo.T("bool"), NotNull: true},
					{Name: "big2", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "flag3", PGType: typeinfo.T("bool"), NotNull: true},
					{Name: "big3", PGType: typeinfo.T("int8"), NotNull: true},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I004")
	if len(found) != 1 {
		t.Fatalf("expected 1 I004 diagnostic, got %d: %v", len(found), found)
	}
}

func TestI004_OptimalOrder_NoDiag(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name: "optimal", Schema: "public", Comment: "Optimal table",
				PK: []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "big1", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "big2", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "big3", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "flag1", PGType: typeinfo.T("bool"), NotNull: true},
					{Name: "flag2", PGType: typeinfo.T("bool"), NotNull: true},
					{Name: "flag3", PGType: typeinfo.T("bool"), NotNull: true},
				},
			},
		},
	}
	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "I004")
	if len(found) != 0 {
		t.Fatalf("expected no I004 for optimally ordered columns, got %d: %v", len(found), found)
	}
}

func TestEstimateRowSize_KnownSchema(t *testing.T) {
	cols := []model.Column{
		{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
		{Name: "count", PGType: typeinfo.T("int4"), NotNull: true},
		{Name: "total", PGType: typeinfo.T("int8"), NotNull: true},
	}
	size, padding := estimateRowSize(cols)
	// Header(24) + uuid(16)=40, int(4)=44, align-to-8=48, bigint(8)=56, +4 ItemId=60
	// Padding: totalSize(60) - headerEnd(24) - rawData(16+4+8=28) = 8
	if size != 60 {
		t.Errorf("expected row size 60, got %d", size)
	}
	if padding != 8 {
		t.Errorf("expected padding 8, got %d", padding)
	}
}

// --- State machine validation tests ---

// newSMRegistry creates a semtype registry with a state machine type registered.
func newSMRegistry(t *testing.T) *semtype.Registry {
	t.Helper()
	reg := semtype.NewBuiltinRegistry()
	smType := semtype.UserTypeDef{
		Name: "order_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "pending"},
			{Name: "confirmed"},
			{Name: "shipped"},
			{Name: "delivered", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "confirm", From: []string{"pending"}, To: "confirmed"},
			{Name: "ship", From: []string{"confirmed"}, To: "shipped"},
			{Name: "deliver", From: []string{"shipped"}, To: "delivered"},
		},
		InitialState: "pending",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}
	return reg
}

func TestW027_SMUnreachableState(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	// SM with an unreachable state "orphan".
	smType := semtype.UserTypeDef{
		Name: "flow_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "start"},
			{Name: "middle"},
			{Name: "orphan"},
			{Name: "end", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "advance", From: []string{"start"}, To: "middle"},
			{Name: "finish", From: []string{"middle"}, To: "end"},
			// No transition leads to "orphan".
		},
		InitialState: "start",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "items",
			Schema:  "public",
			Comment: "Items table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("flow_status"), NotNull: true, SemanticTypeName: "flow_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "W027")
	if len(found) != 1 {
		t.Fatalf("expected 1 W027 for unreachable state, got %d: %v", len(found), found)
	}
	if !strings.Contains(found[0].Message, "orphan") {
		t.Errorf("expected message to mention 'orphan', got %q", found[0].Message)
	}
}

func TestW027_SMNoUnreachableState(t *testing.T) {
	reg := newSMRegistry(t)

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "W027")
	if len(found) != 0 {
		t.Errorf("expected no W027 for fully reachable SM, got %d: %v", len(found), found)
	}
}

func TestW028_SMDeadEndState(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	// SM with non-terminal "stuck" state that has no outgoing transitions.
	smType := semtype.UserTypeDef{
		Name: "task_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "open"},
			{Name: "stuck"}, // not terminal, no outgoing transitions
			{Name: "done", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "block", From: []string{"open"}, To: "stuck"},
			{Name: "complete", From: []string{"open"}, To: "done"},
			// No transition FROM stuck.
		},
		InitialState: "open",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "tasks",
			Schema:  "public",
			Comment: "Tasks table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("task_status"), NotNull: true, SemanticTypeName: "task_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "W028")
	if len(found) != 1 {
		t.Fatalf("expected 1 W028 for dead-end state, got %d: %v", len(found), found)
	}
	if !strings.Contains(found[0].Message, "stuck") {
		t.Errorf("expected message to mention 'stuck', got %q", found[0].Message)
	}
}

func TestW028_SMNoDeadEnd(t *testing.T) {
	reg := newSMRegistry(t)

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "W028")
	if len(found) != 0 {
		t.Errorf("expected no W028 for SM with no dead-end states, got %d: %v", len(found), found)
	}
}

func TestE223_SMRequiresColumnMissing(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	smType := semtype.UserTypeDef{
		Name: "payment_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "pending"},
			{Name: "paid", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "pay", From: []string{"pending"}, To: "paid", Requires: map[string]string{
				"payment_method": "IS NOT NULL",
			}},
		},
		InitialState: "pending",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "payments",
			Schema:  "public",
			Comment: "Payments table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("payment_status"), NotNull: true, SemanticTypeName: "payment_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
				// payment_method column is MISSING.
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "E223")
	if len(found) != 1 {
		t.Fatalf("expected 1 E223 for missing requires column, got %d: %v", len(found), found)
	}
	if !strings.Contains(found[0].Message, "payment_method") {
		t.Errorf("expected message to mention 'payment_method', got %q", found[0].Message)
	}
}

func TestE223_SMRequiresColumnPresent(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	smType := semtype.UserTypeDef{
		Name: "payment_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "pending"},
			{Name: "paid", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "pay", From: []string{"pending"}, To: "paid", Requires: map[string]string{
				"payment_method": "IS NOT NULL",
			}},
		},
		InitialState: "pending",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "payments",
			Schema:  "public",
			Comment: "Payments table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("payment_status"), NotNull: true, SemanticTypeName: "payment_status"},
				{Name: "payment_method", PGType: typeinfo.T("text")},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "E223")
	if len(found) != 0 {
		t.Errorf("expected no E223 when required column exists, got %d: %v", len(found), found)
	}
}

func TestE224_SMDefaultMismatch(t *testing.T) {
	reg := newSMRegistry(t)

	wrongDefault := "confirmed"
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status", Default: &wrongDefault},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "E224")
	if len(found) != 1 {
		t.Fatalf("expected 1 E224 for default mismatch, got %d: %v", len(found), found)
	}
	if !strings.Contains(found[0].Message, "confirmed") {
		t.Errorf("expected message to mention 'confirmed', got %q", found[0].Message)
	}
	if !strings.Contains(found[0].Message, "pending") {
		t.Errorf("expected message to mention initial state 'pending', got %q", found[0].Message)
	}
}

func TestE224_SMDefaultMatchesInitial(t *testing.T) {
	reg := newSMRegistry(t)

	correctDefault := "pending"
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status", Default: &correctDefault},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "E224")
	if len(found) != 0 {
		t.Errorf("expected no E224 when default matches initial state, got %d: %v", len(found), found)
	}
}

func TestE224_SMNoDefault(t *testing.T) {
	reg := newSMRegistry(t)

	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	config := &Config{TypeRegistry: reg}
	result, _ := Validate(schema, config)
	found := findByCode(result, "E224")
	if len(found) != 0 {
		t.Errorf("expected no E224 when no default set, got %d: %v", len(found), found)
	}
}

func TestE226_SMReservedTriggerPrefix(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
			Triggers: []model.Trigger{
				{
					Name:     "_pgdesign_sm_enforce_status",
					Function: "some_func",
					Events:   []string{"UPDATE"},
					Timing:   "BEFORE",
					ForEach:  "ROW",
				},
			},
		}},
	}

	result, _ := Validate(schema, nil)
	found := findByCode(result, "E226")
	if len(found) != 1 {
		t.Fatalf("expected 1 E226 for reserved trigger prefix, got %d: %v", len(found), found)
	}
	if !strings.Contains(found[0].Message, "_pgdesign_sm_") {
		t.Errorf("expected message to mention reserved prefix, got %q", found[0].Message)
	}
}

func TestE226_SMNoReservedPrefix(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
			Triggers: []model.Trigger{
				{
					Name:     "audit_log",
					Function: "audit_func",
					Events:   []string{"INSERT"},
					Timing:   "AFTER",
					ForEach:  "ROW",
				},
			},
		}},
	}

	result, _ := Validate(schema, nil)
	found := findByCode(result, "E226")
	if len(found) != 0 {
		t.Errorf("expected no E226 for normal trigger name, got %d: %v", len(found), found)
	}
}

func TestSM_NilRegistrySkipsChecks(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "orders",
			Schema:  "public",
			Comment: "Orders table",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
				{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
			},
		}},
	}

	// No TypeRegistry set (nil) -- SM checks should silently skip.
	config := &Config{}
	result, _ := Validate(schema, config)
	for _, d := range result {
		if d.Code == "W027" || d.Code == "W028" || d.Code == "E223" || d.Code == "E224" {
			t.Errorf("unexpected SM diagnostic %s when TypeRegistry is nil: %s", d.Code, d.Message)
		}
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
