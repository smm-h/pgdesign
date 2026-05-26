package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

func TestMinimalTable(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "name", PGType: "text", NotNull: true},
					{Name: "value", PGType: "integer", NotNull: false},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "CREATE TABLE app.items (") {
		t.Errorf("expected CREATE TABLE, got:\n%s", out)
	}
	if !strings.Contains(out, "id uuid NOT NULL DEFAULT gen_random_uuid()") {
		t.Errorf("expected id column, got:\n%s", out)
	}
	if !strings.Contains(out, "name text NOT NULL") {
		t.Errorf("expected name column, got:\n%s", out)
	}
	if !strings.Contains(out, "value integer") {
		t.Errorf("expected value column, got:\n%s", out)
	}
	if !strings.Contains(out, "CONSTRAINT pk_items PRIMARY KEY (id)") {
		t.Errorf("expected PK constraint, got:\n%s", out)
	}
}

func TestTwoTablesWithFK(t *testing.T) {
	schema := &model.Schema{
		Name: "blog",
		Tables: []model.Table{
			{
				Name:   "authors",
				Schema: "blog",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "posts",
				Schema: "blog",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "author_id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_posts_authors",
						Columns:    []string{"author_id"},
						RefSchema:  "blog",
						RefTable:   "authors",
						RefColumns: []string{"id"},
						OnDelete:   "CASCADE",
					},
				},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	// FK appears as ALTER TABLE, not inline
	if !strings.Contains(out, "ALTER TABLE blog.posts ADD CONSTRAINT fk_posts_authors FOREIGN KEY (author_id) REFERENCES blog.authors (id) ON DELETE CASCADE;") {
		t.Errorf("expected FK ALTER TABLE, got:\n%s", out)
	}

	// Tables in correct order (authors before posts)
	authorsPos := strings.Index(out, "CREATE TABLE blog.authors")
	postsPos := strings.Index(out, "CREATE TABLE blog.posts")
	if authorsPos < 0 || postsPos < 0 {
		t.Fatalf("missing CREATE TABLE statements in output:\n%s", out)
	}
	if authorsPos > postsPos {
		t.Errorf("authors should appear before posts, authors=%d posts=%d", authorsPos, postsPos)
	}
}

func TestEnumGeneration(t *testing.T) {
	schema := &model.Schema{
		Name: "game",
		Enums: []model.Enum{
			{Schema: "game", Name: "status", Values: []string{"active", "banned", "deleted"}},
		},
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "status", PGType: "status", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "CREATE TYPE game.status AS ENUM ('active', 'banned', 'deleted');") {
		t.Errorf("expected CREATE TYPE, got:\n%s", out)
	}

	// Enum should appear before CREATE TABLE
	enumPos := strings.Index(out, "CREATE TYPE")
	tablePos := strings.Index(out, "CREATE TABLE")
	if enumPos < 0 || tablePos < 0 {
		t.Fatalf("missing statements:\n%s", out)
	}
	if enumPos > tablePos {
		t.Errorf("CREATE TYPE should appear before CREATE TABLE")
	}
}

func TestIndexGeneration(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "kind", PGType: "text", NotNull: true},
					{Name: "active", PGType: "boolean", NotNull: true},
				},
				PK: []string{"id"},
				Indexes: []model.Index{
					{Name: "idx_events_kind", Columns: []string{"kind"}},
					{Name: "idx_events_active", Columns: []string{"kind"}, Where: "active = true"},
				},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "CREATE INDEX idx_events_kind ON app.events (kind);") {
		t.Errorf("expected basic index, got:\n%s", out)
	}
	if !strings.Contains(out, "WHERE active = true") {
		t.Errorf("expected partial index with WHERE, got:\n%s", out)
	}
}

func TestCommentsIncluded(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "app",
				Comment: "All registered users",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, Comment: "Primary identifier"},
					{Name: "name", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "COMMENT ON TABLE app.users IS 'All registered users';") {
		t.Errorf("expected table comment, got:\n%s", out)
	}
	if !strings.Contains(out, "COMMENT ON COLUMN app.users.id IS 'Primary identifier';") {
		t.Errorf("expected column comment, got:\n%s", out)
	}
}

func TestCommentsExcluded(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "app",
				Comment: "All registered users",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, Comment: "Primary identifier"},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{IncludeComments: false, Format: "sql"}
	out := Generate(schema, opts)

	if strings.Contains(out, "COMMENT ON") {
		t.Errorf("expected no comments with IncludeComments=false, got:\n%s", out)
	}
}

func TestIdempotentMode(t *testing.T) {
	schema := &model.Schema{
		Name:       "app",
		Extensions: []string{"pgcrypto"},
		Enums: []model.Enum{
			{Schema: "app", Name: "role", Values: []string{"admin", "user"}},
		},
		Tables: []model.Table{
			{
				Name:   "accounts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
				Indexes: []model.Index{
					{Name: "idx_accounts_id", Columns: []string{"id"}},
				},
			},
		},
	}

	opts := Options{Idempotent: true, IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	// All IF NOT EXISTS guards
	if !strings.Contains(out, "CREATE SCHEMA IF NOT EXISTS") {
		t.Errorf("expected IF NOT EXISTS on schema, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE EXTENSION IF NOT EXISTS") {
		t.Errorf("expected IF NOT EXISTS on extension, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TYPE IF NOT EXISTS") {
		t.Errorf("expected IF NOT EXISTS on type, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE IF NOT EXISTS") {
		t.Errorf("expected IF NOT EXISTS on table, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE INDEX IF NOT EXISTS") {
		t.Errorf("expected IF NOT EXISTS on index, got:\n%s", out)
	}
}

func TestDeterminism(t *testing.T) {
	schema := &model.Schema{
		Name:       "det",
		Extensions: []string{"pgcrypto", "uuid-ossp"},
		Enums: []model.Enum{
			{Schema: "det", Name: "color", Values: []string{"red", "blue"}},
		},
		Tables: []model.Table{
			{
				Name:   "things",
				Schema: "det",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true, Comment: "Thing name"},
				},
				PK:      []string{"id"},
				Comment: "All things",
				FKs: []model.FK{
					{Name: "fk_things_self", Columns: []string{"id"}, RefSchema: "det", RefTable: "things", RefColumns: []string{"id"}},
				},
				Indexes: []model.Index{
					{Name: "idx_things_name", Columns: []string{"name"}},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_things_name", Columns: []string{"name"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_things_name_nonempty", Expr: "name <> ''"},
				},
				Owner: "app_role",
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out1 := Generate(schema, opts)
	out2 := Generate(schema, opts)

	if out1 != out2 {
		t.Errorf("Generate is not deterministic:\nfirst:\n%s\nsecond:\n%s", out1, out2)
	}
}

func TestJSONFormat(t *testing.T) {
	schema := &model.Schema{
		Name:       "myapp",
		Extensions: []string{"pgcrypto"},
		Enums: []model.Enum{
			{Schema: "myapp", Name: "role", Values: []string{"admin", "user"}},
		},
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "myapp",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "role", PGType: "role", NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "fk_users_self", Columns: []string{"id"}, RefSchema: "myapp", RefTable: "users", RefColumns: []string{"id"}},
				},
				Indexes: []model.Index{
					{Name: "idx_users_role", Columns: []string{"role"}},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_users_id", Columns: []string{"id"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_users_role", Expr: "role <> ''"},
				},
				Comment: "All users",
				Owner:   "app_role",
			},
		},
		CycleGroups: [][]string{{"users"}},
	}

	opts := Options{Format: "json"}
	out := Generate(schema, opts)

	// Must be valid JSON.
	var roundTripped model.Schema
	if err := json.Unmarshal([]byte(out), &roundTripped); err != nil {
		t.Fatalf("JSON output is not valid JSON: %v\nOutput:\n%s", err, out)
	}

	// Verify key fields survived the round-trip.
	if roundTripped.Name != "myapp" {
		t.Errorf("expected schema name 'myapp', got %q", roundTripped.Name)
	}
	if len(roundTripped.Extensions) != 1 || roundTripped.Extensions[0] != "pgcrypto" {
		t.Errorf("expected extensions [pgcrypto], got %v", roundTripped.Extensions)
	}
	if len(roundTripped.Enums) != 1 || roundTripped.Enums[0].Name != "role" {
		t.Errorf("expected 1 enum named 'role', got %v", roundTripped.Enums)
	}
	if len(roundTripped.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(roundTripped.Tables))
	}

	tbl := roundTripped.Tables[0]
	if tbl.Name != "users" {
		t.Errorf("expected table 'users', got %q", tbl.Name)
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(tbl.Columns))
	}
	if len(tbl.FKs) != 1 || tbl.FKs[0].Name != "fk_users_self" {
		t.Errorf("expected FK 'fk_users_self', got %v", tbl.FKs)
	}
	if len(tbl.Indexes) != 1 || tbl.Indexes[0].Name != "idx_users_role" {
		t.Errorf("expected index 'idx_users_role', got %v", tbl.Indexes)
	}
	if len(tbl.Uniques) != 1 || tbl.Uniques[0].Name != "uq_users_id" {
		t.Errorf("expected unique 'uq_users_id', got %v", tbl.Uniques)
	}
	if len(tbl.Checks) != 1 || tbl.Checks[0].Name != "ck_users_role" {
		t.Errorf("expected check 'ck_users_role', got %v", tbl.Checks)
	}
	if tbl.Comment != "All users" {
		t.Errorf("expected comment 'All users', got %q", tbl.Comment)
	}
	if tbl.Owner != "app_role" {
		t.Errorf("expected owner 'app_role', got %q", tbl.Owner)
	}
	if len(roundTripped.CycleGroups) != 1 || roundTripped.CycleGroups[0][0] != "users" {
		t.Errorf("expected cycle_groups [[users]], got %v", roundTripped.CycleGroups)
	}
}

func TestOwnerGeneration(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
				},
				PK:    []string{"id"},
				Owner: "db_admin",
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "ALTER TABLE app.items OWNER TO db_admin;") {
		t.Errorf("expected OWNER TO, got:\n%s", out)
	}
}

func TestSchemaAndExtensions(t *testing.T) {
	schema := &model.Schema{
		Name:       "myapp",
		Extensions: []string{"pgcrypto", "uuid-ossp"},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "CREATE SCHEMA myapp;") {
		t.Errorf("expected CREATE SCHEMA, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE EXTENSION pgcrypto;") {
		t.Errorf("expected pgcrypto extension, got:\n%s", out)
	}
	if !strings.Contains(out, `CREATE EXTENSION "uuid-ossp";`) {
		t.Errorf("expected uuid-ossp extension (quoted), got:\n%s", out)
	}
}

func TestTrailingNewline(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
		Tables: []model.Table{
			{
				Name:   "t",
				Schema: "test",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output should end with newline, got: %q", out[len(out)-10:])
	}
}

func TestMultiSchemaQualifiedNames(t *testing.T) {
	// In multi-schema mode, schema.Name is empty. Each table carries its own
	// Schema field and all SQL statements must use that per-table schema.
	schema := &model.Schema{
		// Name intentionally empty -- multi-schema mode.
		Enums: []model.Enum{
			{Schema: "auth", Name: "role", Values: []string{"admin", "user"}},
		},
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "auth",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "role", PGType: "role", NotNull: true},
				},
				PK:      []string{"id"},
				Comment: "All users",
				Owner:   "auth_admin",
				Indexes: []model.Index{
					{Name: "idx_users_role", Columns: []string{"role"}},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_users_id", Columns: []string{"id"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_users_role", Expr: "role <> ''"},
				},
			},
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "user_id", PGType: "uuid", NotNull: true},
					{Name: "points", PGType: "integer", NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_scores_users",
						Columns:    []string{"user_id"},
						RefSchema:  "auth",
						RefTable:   "users",
						RefColumns: []string{"id"},
						OnDelete:   "CASCADE",
					},
				},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := Generate(schema, opts)

	// CREATE SCHEMA for both schemas.
	if !strings.Contains(out, "CREATE SCHEMA auth;") {
		t.Errorf("expected CREATE SCHEMA auth, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE SCHEMA game;") {
		t.Errorf("expected CREATE SCHEMA game, got:\n%s", out)
	}

	// CREATE TYPE with correct schema.
	if !strings.Contains(out, "CREATE TYPE auth.role AS ENUM") {
		t.Errorf("expected auth-qualified enum, got:\n%s", out)
	}

	// CREATE TABLE with correct schema (not empty schema).
	if !strings.Contains(out, "CREATE TABLE auth.users (") {
		t.Errorf("expected CREATE TABLE auth.users, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE game.scores (") {
		t.Errorf("expected CREATE TABLE game.scores, got:\n%s", out)
	}
	if strings.Contains(out, `"".`) {
		t.Errorf("output contains empty-schema qualified name (\"\".): \n%s", out)
	}

	// ALTER TABLE FK uses game schema for the source table.
	if !strings.Contains(out, "ALTER TABLE game.scores ADD CONSTRAINT fk_scores_users") {
		t.Errorf("expected ALTER TABLE game.scores for FK, got:\n%s", out)
	}

	// UNIQUE constraint uses auth schema.
	if !strings.Contains(out, "ALTER TABLE auth.users ADD CONSTRAINT uq_users_id") {
		t.Errorf("expected ALTER TABLE auth.users for UNIQUE, got:\n%s", out)
	}

	// CHECK constraint uses auth schema.
	if !strings.Contains(out, "ALTER TABLE auth.users ADD CONSTRAINT ck_users_role") {
		t.Errorf("expected ALTER TABLE auth.users for CHECK, got:\n%s", out)
	}

	// INDEX uses auth schema.
	if !strings.Contains(out, "CREATE INDEX idx_users_role ON auth.users") {
		t.Errorf("expected CREATE INDEX ON auth.users, got:\n%s", out)
	}

	// COMMENT uses auth schema.
	if !strings.Contains(out, "COMMENT ON TABLE auth.users IS") {
		t.Errorf("expected COMMENT ON TABLE auth.users, got:\n%s", out)
	}

	// OWNER uses auth schema.
	if !strings.Contains(out, "ALTER TABLE auth.users OWNER TO auth_admin") {
		t.Errorf("expected ALTER TABLE auth.users OWNER TO, got:\n%s", out)
	}
}

func TestUniqueIndex(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "pairs",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "a", PGType: "integer", NotNull: true},
					{Name: "b", PGType: "integer", NotNull: true},
				},
				PK: []string{"id"},
				Indexes: []model.Index{
					{Name: "idx_pairs_ab", Columns: []string{"a", "b"}, Unique: true},
					{Name: "idx_pairs_b", Columns: []string{"b"}, Unique: false},
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := Generate(schema, opts)

	if !strings.Contains(out, "CREATE UNIQUE INDEX idx_pairs_ab ON app.pairs (a, b);") {
		t.Errorf("expected CREATE UNIQUE INDEX for idx_pairs_ab, got:\n%s", out)
	}
	// Non-unique index should NOT have UNIQUE keyword.
	if !strings.Contains(out, "CREATE INDEX idx_pairs_b ON app.pairs (b);") {
		t.Errorf("expected plain CREATE INDEX for idx_pairs_b, got:\n%s", out)
	}
}

func TestIdentityColumnPGVersionGate(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true, Identity: "ALWAYS"},
					{Name: "name", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	// PGVersion 9: identity column should fall back to bigserial.
	opts := Options{Format: "sql", PGVersion: 9}
	out := Generate(schema, opts)

	if !strings.Contains(out, "id bigserial NOT NULL") {
		t.Errorf("PGVersion=9: expected bigserial fallback, got:\n%s", out)
	}
	if strings.Contains(out, "GENERATED") {
		t.Errorf("PGVersion=9: should not contain GENERATED, got:\n%s", out)
	}

	// PGVersion 10: identity column should use GENERATED AS IDENTITY.
	opts.PGVersion = 10
	out = Generate(schema, opts)

	if !strings.Contains(out, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("PGVersion=10: expected GENERATED ALWAYS AS IDENTITY, got:\n%s", out)
	}

	// PGVersion 0 (unspecified): should use GENERATED AS IDENTITY.
	opts.PGVersion = 0
	out = Generate(schema, opts)

	if !strings.Contains(out, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("PGVersion=0: expected GENERATED ALWAYS AS IDENTITY, got:\n%s", out)
	}
}

func TestGoldenFile(t *testing.T) {
	inputPath := filepath.Join("testdata", "simple_input.toml")

	raw, diags := parse.File(inputPath)
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	for _, d := range diags {
		if d.Severity == 0 { // Error
			t.Fatalf("parse error: %s", d.Message)
		}
	}

	reg := semtype.NewBuiltinRegistry()
	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	got := Generate(schema, opts)

	expectedPath := filepath.Join("testdata", "simple_expected.sql")
	expectedBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("cannot read expected file: %v", err)
	}
	expected := string(expectedBytes)

	if got != expected {
		t.Errorf("golden file mismatch.\n--- got ---\n%s\n--- expected ---\n%s", got, expected)
	}
}
