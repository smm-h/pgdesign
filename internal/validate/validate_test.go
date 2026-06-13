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
				{Name: "id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "data", PGType: "jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "email", PGType: "varchar(255)"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
				},
			},
			{
				Name:    "players",
				Schema:  "game",
				Comment: "Game players",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "auth_id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "auth_id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "data", PGType: ""}, // missing type
				{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "is_active", PGType: "boolean"},
				{Name: "is_verified", PGType: "boolean"},
				{Name: "is_admin", PGType: "boolean"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "is_active", PGType: "boolean"},
				{Name: "is_verified", PGType: "boolean"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "tags", PGType: "jsonb", Default: "'[]'::jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "metadata", PGType: "jsonb", Default: "'[]'::jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
				{Name: "status", PGType: "text"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
				{Name: "status", PGType: "text"},
				{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid"},
					{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "email", PGType: "text"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "first_name", PGType: "text"},
				{Name: "last_name", PGType: "text"},
				{Name: "full_name", PGType: "text", Generated: "first_name || ' ' || last_name", Stored: true},
				{Name: "display_name", PGType: "text", Generated: "lower(full_name)", Stored: true},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "first_name", PGType: "text"},
				{Name: "last_name", PGType: "text"},
				{Name: "full_name", PGType: "text", Generated: "first_name || ' ' || last_name", Stored: true},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "name", PGType: "text"},
				{Name: "price", PGType: "numeric"},
				{Name: "search_name", PGType: "text", Generated: "lower(name)", Stored: true},
				{Name: "display_price", PGType: "text", Generated: "cast(price as text) || ' USD'", Stored: true},
				{Name: "created_at", PGType: "timestamptz"},
			},
		}},
	}

	diags, _ := Validate(schema, nil)
	found := findByCode(diags, "E213")
	if len(found) > 0 {
		t.Fatalf("expected no E213 when generated columns only reference regular columns, got %v", found)
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
			got := extractColumnRefs(tt.expr)
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
				{Name: "id", PGType: "uuid"},
				{Name: "channel_id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "channel_id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "channel_id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "channel_id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "channel_id", PGType: "uuid"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "tags", PGType: "jsonb", Default: "'[]'::jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "tags", PGType: "jsonb", Default: "'[]'::jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
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
				{Name: "id", PGType: "uuid"},
				{Name: "tags", PGType: "jsonb", Default: "'[]'::jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
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
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "tags", PGType: "jsonb", NotNull: true, Default: "'[]'::jsonb", JSONSchema: "tags_schema.json"},
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
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "tags", PGType: "jsonb", NotNull: true, Default: "'[]'::jsonb"},
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
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "updated_at", PGType: "timestamptz", NotNull: true, SemanticTypeName: "timestamp"},
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

func TestE110_ColumnDefaultEmbeddedQuotes(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "orders",
				Schema:  "public",
				Comment: "test table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "status", PGType: "text", Default: "'pending'"},
					{Name: "data", PGType: "jsonb", Default: "'{}'"},
					{Name: "count", PGType: "integer", Default: "0"},
					{Name: "name", PGType: "text", Default: "unknown"},
				},
			},
		},
	}
	active, _ := Validate(schema, nil)
	e110s := findByCode(active, "E110")
	if len(e110s) != 2 {
		t.Errorf("expected 2 E110 diagnostics, got %d", len(e110s))
		for _, d := range e110s {
			t.Logf("  E110: %s", d.Message)
		}
	}
	// Check that the two are for "status" and "data" columns
	cols := make(map[string]bool)
	for _, d := range e110s {
		cols[d.Column] = true
	}
	if !cols["status"] {
		t.Error("expected E110 for column 'status'")
	}
	if !cols["data"] {
		t.Error("expected E110 for column 'data'")
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
