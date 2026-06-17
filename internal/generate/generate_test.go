package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// intPtr returns a pointer to the given int.
func intPtr(n int) *int { return &n }

// mustGenerate calls Generate and fails the test on error.
func mustGenerate(t *testing.T, schema *model.Schema, opts Options) string {
	t.Helper()
	out, _, err := Generate(schema, opts)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	return out
}

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "CREATE TYPE game.status AS ENUM ('active', 'banned', 'deleted');") {
		t.Errorf("expected CREATE TYPE, got:\n%s", out)
	}

	// Enum column type must be schema-qualified in CREATE TABLE.
	if !strings.Contains(out, "game.status NOT NULL") {
		t.Errorf("expected schema-qualified enum type game.status in column def, got:\n%s", out)
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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out1 := mustGenerate(t, schema, opts)
	out2 := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

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

	// Enum column type in CREATE TABLE must be schema-qualified.
	if !strings.Contains(out, "auth.role NOT NULL") {
		t.Errorf("expected schema-qualified enum type auth.role in column def, got:\n%s", out)
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
	out := mustGenerate(t, schema, opts)

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
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "id bigserial NOT NULL") {
		t.Errorf("PGVersion=9: expected bigserial fallback, got:\n%s", out)
	}
	if strings.Contains(out, "GENERATED") {
		t.Errorf("PGVersion=9: should not contain GENERATED, got:\n%s", out)
	}

	// PGVersion 10: identity column should use GENERATED AS IDENTITY.
	opts.PGVersion = 10
	out = mustGenerate(t, schema, opts)

	if !strings.Contains(out, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("PGVersion=10: expected GENERATED ALWAYS AS IDENTITY, got:\n%s", out)
	}

	// PGVersion 0 (unspecified): should use GENERATED AS IDENTITY.
	opts.PGVersion = 0
	out = mustGenerate(t, schema, opts)

	if !strings.Contains(out, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("PGVersion=0: expected GENERATED ALWAYS AS IDENTITY, got:\n%s", out)
	}
}

func TestPartitionChildrenGeneration(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
					Children: []model.PartitionSpec{
						{
							Name:  "events_2024_01",
							Bound: "FROM ('2024-01-01') TO ('2024-02-01')",
						},
						{
							Name:  "events_2024_02",
							Bound: "FROM ('2024-02-01') TO ('2024-03-01')",
						},
					},
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "PARTITION BY RANGE (created_at)") {
		t.Errorf("expected PARTITION BY on parent table, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE app.events_2024_01 PARTITION OF app.events") {
		t.Errorf("expected child partition events_2024_01, got:\n%s", out)
	}
	if !strings.Contains(out, "FOR VALUES FROM ('2024-01-01') TO ('2024-02-01')") {
		t.Errorf("expected bound for events_2024_01, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE app.events_2024_02 PARTITION OF app.events") {
		t.Errorf("expected child partition events_2024_02, got:\n%s", out)
	}

	// Child partitions must come after parent table.
	parentPos := strings.Index(out, "CREATE TABLE app.events (")
	childPos := strings.Index(out, "CREATE TABLE app.events_2024_01 PARTITION OF")
	if parentPos >= childPos {
		t.Errorf("child partition should appear after parent table, parent=%d child=%d", parentPos, childPos)
	}
}

func TestPartitionChildrenRecursive(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
					{Name: "region", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
					Children: []model.PartitionSpec{
						{
							Name:     "events_2024",
							Bound:    "FROM ('2024-01-01') TO ('2025-01-01')",
							Strategy: "list",
							Columns:  []string{"region"},
							Children: []model.PartitionSpec{
								{
									Name:  "events_2024_us",
									Bound: "IN ('us-east', 'us-west')",
								},
								{
									Name:  "events_2024_eu",
									Bound: "IN ('eu-west')",
								},
							},
						},
					},
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// Parent partition.
	if !strings.Contains(out, "CREATE TABLE app.events_2024 PARTITION OF app.events") {
		t.Errorf("expected top-level child partition, got:\n%s", out)
	}
	// Sub-partitions of events_2024.
	if !strings.Contains(out, "CREATE TABLE app.events_2024_us PARTITION OF app.events_2024") {
		t.Errorf("expected sub-partition events_2024_us, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE app.events_2024_eu PARTITION OF app.events_2024") {
		t.Errorf("expected sub-partition events_2024_eu, got:\n%s", out)
	}
}

func TestPartmanGeneration(t *testing.T) {
	schema := &model.Schema{
		Name:       "app",
		Extensions: []string{"pg_partman"},
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
				},
				Maintenance: &model.MaintenanceConfig{
					Premake:            4,
					Retention:          "6 months",
					RetentionKeepTable: true,
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "partman.create_parent(") {
		t.Errorf("expected partman.create_parent call, got:\n%s", out)
	}
	if !strings.Contains(out, "p_parent_table := 'app.events'") {
		t.Errorf("expected p_parent_table, got:\n%s", out)
	}
	if !strings.Contains(out, "p_control := 'created_at'") {
		t.Errorf("expected p_control, got:\n%s", out)
	}
	if !strings.Contains(out, "p_premake := 4") {
		t.Errorf("expected p_premake, got:\n%s", out)
	}
	if !strings.Contains(out, "UPDATE partman.part_config") {
		t.Errorf("expected UPDATE partman.part_config, got:\n%s", out)
	}
	if !strings.Contains(out, "retention = '6 months'") {
		t.Errorf("expected retention value, got:\n%s", out)
	}
	if !strings.Contains(out, "retention_keep_table = true") {
		t.Errorf("expected retention_keep_table, got:\n%s", out)
	}

	// pg_partman SQL must come after CREATE TABLE.
	tablePos := strings.Index(out, "CREATE TABLE app.events (")
	partmanPos := strings.Index(out, "partman.create_parent(")
	if tablePos >= partmanPos {
		t.Errorf("partman SQL should appear after CREATE TABLE, table=%d partman=%d", tablePos, partmanPos)
	}
}

func TestPartmanNotEmittedWithoutExtension(t *testing.T) {
	// Without pg_partman in extensions, no partman SQL should be emitted.
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
				},
				Maintenance: &model.MaintenanceConfig{
					Premake:            4,
					Retention:          "6 months",
					RetentionKeepTable: true,
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if strings.Contains(out, "partman") {
		t.Errorf("should not contain partman SQL without pg_partman extension, got:\n%s", out)
	}
}

func TestPartmanMultiColumnError(t *testing.T) {
	schema := &model.Schema{
		Name:       "app",
		Extensions: []string{"pg_partman"},
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "year", PGType: "integer", NotNull: true},
					{Name: "region", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"year", "region"},
				},
				Maintenance: &model.MaintenanceConfig{
					Premake:   3,
					Retention: "90 days",
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	_, diags, err := Generate(schema, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatal("expected diagnostic error for pg_partman + multi-column, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E010" && strings.Contains(d.Message, "pg_partman") && strings.Contains(d.Message, "multi-column") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected pg_partman multi-column error diagnostic (E010), got: %v", diags)
	}
}

func TestRLSPolicyGeneration(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "documents",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "owner_id", PGType: "uuid", NotNull: true},
					{Name: "content", PGType: "text", NotNull: true},
				},
				PK:        []string{"id"},
				EnableRLS: true,
				Policies: []model.Policy{
					{
						Name:      "owners_select",
						Operation: "SELECT",
						Role:      "app_user",
						Using:     "owner_id = current_user_id()",
					},
					{
						Name:      "owners_insert",
						Operation: "INSERT",
						Role:      "app_user",
						WithCheck: "owner_id = current_user_id()",
					},
					{
						Name:      "owners_update",
						Operation: "UPDATE",
						Role:      "app_user",
						Using:     "owner_id = current_user_id()",
						WithCheck: "owner_id = current_user_id()",
					},
				},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// ENABLE RLS must be present.
	if !strings.Contains(out, "ALTER TABLE app.documents ENABLE ROW LEVEL SECURITY;") {
		t.Errorf("expected ALTER TABLE ENABLE RLS, got:\n%s", out)
	}

	// CREATE POLICY statements.
	if !strings.Contains(out, "CREATE POLICY owners_select ON app.documents FOR SELECT TO app_user USING (owner_id = current_user_id());") {
		t.Errorf("expected SELECT policy, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE POLICY owners_insert ON app.documents FOR INSERT TO app_user WITH CHECK (owner_id = current_user_id());") {
		t.Errorf("expected INSERT policy, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE POLICY owners_update ON app.documents FOR UPDATE TO app_user USING (owner_id = current_user_id()) WITH CHECK (owner_id = current_user_id());") {
		t.Errorf("expected UPDATE policy, got:\n%s", out)
	}

	// ENABLE RLS must come before CREATE POLICY.
	enablePos := strings.Index(out, "ENABLE ROW LEVEL SECURITY")
	policyPos := strings.Index(out, "CREATE POLICY")
	if enablePos < 0 || policyPos < 0 {
		t.Fatalf("missing RLS statements in output:\n%s", out)
	}
	if enablePos > policyPos {
		t.Errorf("ENABLE RLS should appear before CREATE POLICY, enable=%d policy=%d", enablePos, policyPos)
	}
}

func TestRLSWithoutPolicies(t *testing.T) {
	// enable_rls = true but no policies: should still emit ALTER TABLE ENABLE RLS.
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "secrets",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK:        []string{"id"},
				EnableRLS: true,
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "ALTER TABLE app.secrets ENABLE ROW LEVEL SECURITY;") {
		t.Errorf("expected ENABLE RLS even without policies, got:\n%s", out)
	}
	if strings.Contains(out, "CREATE POLICY") {
		t.Errorf("should not contain CREATE POLICY when no policies defined, got:\n%s", out)
	}
}

func TestNoPoliciesNoRLS(t *testing.T) {
	// No policies, no enable_rls: no RLS statements at all.
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if strings.Contains(out, "ROW LEVEL SECURITY") {
		t.Errorf("should not contain RLS statements, got:\n%s", out)
	}
	if strings.Contains(out, "CREATE POLICY") {
		t.Errorf("should not contain CREATE POLICY, got:\n%s", out)
	}
}

func TestRLSPolicyAllOperation(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "data",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "tenant_id", PGType: "uuid", NotNull: true},
				},
				PK:        []string{"id"},
				EnableRLS: true,
				Policies: []model.Policy{
					{
						Name:      "tenant_isolation",
						Operation: "ALL",
						Using:     "tenant_id = current_setting('app.tenant_id')::uuid",
					},
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// ALL operation should not include FOR clause.
	if strings.Contains(out, "FOR ALL") {
		t.Errorf("should not contain FOR ALL, got:\n%s", out)
	}
	// Should not include TO clause when role is empty.
	policyLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "CREATE POLICY") {
			policyLine = line
			break
		}
	}
	if strings.Contains(policyLine, " TO ") {
		t.Errorf("should not contain TO clause when role is empty, got:\n%s", policyLine)
	}
}

func TestAppendOnlyTrigger(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "payload", PGType: "jsonb", NotNull: true},
				},
				PK:         []string{"id"},
				AppendOnly: true,
			},
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// Shared function should appear once.
	if !strings.Contains(out, "CREATE OR REPLACE FUNCTION app.pgdesign_deny_mutation()") {
		t.Errorf("expected deny_mutation function, got:\n%s", out)
	}

	// Trigger on events table.
	if !strings.Contains(out, "CREATE TRIGGER deny_mutation BEFORE UPDATE OR DELETE ON app.events") {
		t.Errorf("expected trigger on events, got:\n%s", out)
	}

	// No trigger on users table.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "TRIGGER") && strings.Contains(line, "app.users") {
			t.Errorf("unexpected trigger on users table")
		}
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
	got := mustGenerate(t, schema, opts)

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

func TestJSONSchemaCheckConstraints(t *testing.T) {
	schema := &model.Schema{
		Name: "shop",
		Tables: []model.Table{
			{
				Name:    "products",
				Schema:  "shop",
				Comment: "Product catalog",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "metadata", PGType: "jsonb", NotNull: true, JSONSchema: "test_schema.json"},
				},
				PK: []string{"id"},
				Checks: []model.CheckConstraint{
					{Name: "ck_metadata_title_type", Expr: "metadata ? 'title' AND jsonb_typeof(metadata->'title') = 'string'"},
					{Name: "ck_metadata_price_type", Expr: "metadata ? 'price' AND jsonb_typeof(metadata->'price') = 'number'"},
				},
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "ck_metadata_title_type") {
		t.Errorf("expected CHECK for title type, got:\n%s", out)
	}
	if !strings.Contains(out, "metadata ? 'title' AND jsonb_typeof(metadata->'title') = 'string'") {
		t.Errorf("expected jsonb_typeof check for title, got:\n%s", out)
	}
	if !strings.Contains(out, "ck_metadata_price_type") {
		t.Errorf("expected CHECK for price type, got:\n%s", out)
	}
	if !strings.Contains(out, "metadata ? 'price' AND jsonb_typeof(metadata->'price') = 'number'") {
		t.Errorf("expected jsonb_typeof check for price, got:\n%s", out)
	}
}

func TestGenerate_UnsupportedFormat(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "invalid_format"}
	out, _, err := Generate(schema, opts)

	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
	if out != "" {
		t.Errorf("expected empty output for unsupported format, got: %q", out)
	}
	if !strings.Contains(err.Error(), "invalid_format") {
		t.Errorf("expected error to mention the format name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected error to mention 'unsupported', got: %v", err)
	}
}

func TestJSONSchemaEndToEnd(t *testing.T) {
	inputPath := filepath.Join("testdata", "json_schema.toml")

	raw, diags := parse.File(inputPath)
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %s", d.Message)
		}
	}

	reg := semtype.NewBuiltinRegistry()
	built, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := mustGenerate(t, built, opts)

	if !strings.Contains(out, "ck_metadata_title_type") {
		t.Errorf("expected CHECK for title, got:\n%s", out)
	}
	if !strings.Contains(out, "ck_metadata_price_type") {
		t.Errorf("expected CHECK for price, got:\n%s", out)
	}
	if !strings.Contains(out, "jsonb_typeof") {
		t.Errorf("expected jsonb_typeof in output, got:\n%s", out)
	}
}

func TestDocFormat(t *testing.T) {
	schema := &model.Schema{
		Name: "myapp",
		Enums: []model.Enum{
			{Schema: "myapp", Name: "status", Values: []string{"active", "inactive", "banned"}, Comment: "User account status"},
		},
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "myapp",
				Comment: "All registered users",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "name", PGType: "text", NotNull: true, Comment: "Full name"},
					{Name: "email", PGType: "text", NotNull: true},
					{Name: "status", PGType: "status", NotNull: true, Default: model.StrPtr("active")},
				},
				PK: []string{"id"},
				Indexes: []model.Index{
					{Name: "idx_users_email", Columns: []string{"email"}, Unique: true},
					{Name: "idx_users_name", Columns: []string{"name"}, Method: "btree"},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_users_email", Columns: []string{"email"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_users_email", Expr: "email LIKE '%@%'"},
				},
				EnableRLS: true,
				Policies: []model.Policy{
					{
						Name:      "users_select",
						Operation: "SELECT",
						Role:      "app_user",
						Using:     "id = current_user_id()",
					},
				},
			},
			{
				Name:    "posts",
				Schema:  "myapp",
				Comment: "User blog posts",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "user_id", PGType: "uuid", NotNull: true},
					{Name: "title", PGType: "text", NotNull: true},
					{Name: "body", PGType: "text", NotNull: false},
					{Name: "created_at", PGType: "timestamptz", NotNull: true, DefaultExpr: "now()"},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{
						Name:       "fk_posts_users",
						Columns:    []string{"user_id"},
						RefSchema:  "myapp",
						RefTable:   "users",
						RefColumns: []string{"id"},
						OnDelete:   "CASCADE",
					},
				},
				Indexes: []model.Index{
					{Name: "idx_posts_user_id", Columns: []string{"user_id"}},
					{Name: "idx_posts_created", Columns: []string{"created_at"}, Where: "body IS NOT NULL"},
				},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
				},
				AppendOnly: true,
			},
		},
	}

	opts := Options{Format: "doc"}
	out := mustGenerate(t, schema, opts)

	// Schema heading
	if !strings.Contains(out, "# Schema: myapp") {
		t.Errorf("expected schema heading, got:\n%s", out)
	}

	// Enum section
	if !strings.Contains(out, "## Enums") {
		t.Errorf("expected Enums section, got:\n%s", out)
	}
	if !strings.Contains(out, "### status") {
		t.Errorf("expected status enum heading, got:\n%s", out)
	}
	if !strings.Contains(out, "`active`") {
		t.Errorf("expected enum value active, got:\n%s", out)
	}
	if !strings.Contains(out, "`inactive`") {
		t.Errorf("expected enum value inactive, got:\n%s", out)
	}
	if !strings.Contains(out, "User account status") {
		t.Errorf("expected enum comment, got:\n%s", out)
	}

	// Table headings
	if !strings.Contains(out, "## users") {
		t.Errorf("expected users table heading, got:\n%s", out)
	}
	if !strings.Contains(out, "## posts") {
		t.Errorf("expected posts table heading, got:\n%s", out)
	}

	// Table comments
	if !strings.Contains(out, "All registered users") {
		t.Errorf("expected users table comment, got:\n%s", out)
	}

	// Column table headers
	if !strings.Contains(out, "| Column | Type | Nullable | Default | Comment |") {
		t.Errorf("expected column table header, got:\n%s", out)
	}

	// Specific columns
	if !strings.Contains(out, "| id | uuid | NOT NULL | gen_random_uuid() |") {
		t.Errorf("expected id column, got:\n%s", out)
	}
	if !strings.Contains(out, "| name | text | NOT NULL |") {
		t.Errorf("expected name column, got:\n%s", out)
	}
	if !strings.Contains(out, "| body | text | nullable |") {
		t.Errorf("expected body column as nullable, got:\n%s", out)
	}
	// Column with Default (not DefaultExpr)
	if !strings.Contains(out, "| status | status | NOT NULL | active |") {
		t.Errorf("expected status column with default, got:\n%s", out)
	}

	// Primary Key
	if !strings.Contains(out, "**Primary Key:** id") {
		t.Errorf("expected primary key section, got:\n%s", out)
	}

	// Foreign Keys
	if !strings.Contains(out, "**Foreign Keys:**") {
		t.Errorf("expected FK section, got:\n%s", out)
	}
	// FK within same schema should omit schema prefix
	if !strings.Contains(out, "user_id -> users.id (ON DELETE CASCADE)") {
		t.Errorf("expected FK arrow notation, got:\n%s", out)
	}

	// Indexes
	if !strings.Contains(out, "**Indexes:**") {
		t.Errorf("expected indexes section, got:\n%s", out)
	}
	if !strings.Contains(out, "idx_users_email") {
		t.Errorf("expected idx_users_email, got:\n%s", out)
	}
	if !strings.Contains(out, "idx_posts_created") {
		t.Errorf("expected idx_posts_created, got:\n%s", out)
	}
	if !strings.Contains(out, "WHERE body IS NOT NULL") {
		t.Errorf("expected WHERE clause on index, got:\n%s", out)
	}

	// Unique constraints
	if !strings.Contains(out, "**Unique Constraints:**") {
		t.Errorf("expected unique constraints section, got:\n%s", out)
	}
	if !strings.Contains(out, "uq_users_email") {
		t.Errorf("expected uq_users_email, got:\n%s", out)
	}

	// Check constraints
	if !strings.Contains(out, "**Check Constraints:**") {
		t.Errorf("expected check constraints section, got:\n%s", out)
	}
	if !strings.Contains(out, "ck_users_email: email LIKE '%@%'") {
		t.Errorf("expected check constraint expression, got:\n%s", out)
	}

	// Policies
	if !strings.Contains(out, "**Policies:**") {
		t.Errorf("expected policies section, got:\n%s", out)
	}
	if !strings.Contains(out, "users_select") {
		t.Errorf("expected users_select policy, got:\n%s", out)
	}
	if !strings.Contains(out, "TO app_user") {
		t.Errorf("expected policy role, got:\n%s", out)
	}

	// Partitioning
	if !strings.Contains(out, "**Partitioning:** range on created_at") {
		t.Errorf("expected partitioning info, got:\n%s", out)
	}

	// RLS
	if !strings.Contains(out, "**Row Level Security:** enabled") {
		t.Errorf("expected RLS mention, got:\n%s", out)
	}

	// Append Only
	if !strings.Contains(out, "**Append Only:** yes") {
		t.Errorf("expected append-only mention, got:\n%s", out)
	}
}

func TestGenerateDoc_Views(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		Views: []model.View{
			{
				Name:      "active_users",
				Schema:    "app",
				Query:     "SELECT id, name FROM users WHERE active = true",
				Comment:   "Users who are currently active",
				DependsOn: []string{"users"},
			},
			{
				Name:   "all_names",
				Schema: "app",
				Query:  "SELECT name FROM users",
			},
		},
	}

	opts := Options{Format: "doc"}
	out := mustGenerate(t, schema, opts)

	// View name heading exists.
	if !strings.Contains(out, "## active_users") {
		t.Errorf("expected active_users heading, got:\n%s", out)
	}
	if !strings.Contains(out, "## all_names") {
		t.Errorf("expected all_names heading, got:\n%s", out)
	}

	// Comment appears.
	if !strings.Contains(out, "Users who are currently active") {
		t.Errorf("expected view comment, got:\n%s", out)
	}

	// Query code block exists.
	if !strings.Contains(out, "```sql\nSELECT id, name FROM users WHERE active = true\n```") {
		t.Errorf("expected query code block, got:\n%s", out)
	}

	// Dependencies listed.
	if !strings.Contains(out, "### Dependencies") {
		t.Errorf("expected Dependencies section, got:\n%s", out)
	}
	if !strings.Contains(out, "- users") {
		t.Errorf("expected users dependency, got:\n%s", out)
	}

	// View with no comment should not have a bare comment paragraph.
	// View with no dependencies should not have Dependencies section for it.
	allNamesIdx := strings.Index(out, "## all_names")
	if allNamesIdx < 0 {
		t.Fatal("all_names heading not found")
	}
	allNamesSection := out[allNamesIdx:]
	// Find the next heading or end of string.
	nextHeading := strings.Index(allNamesSection[1:], "\n## ")
	if nextHeading >= 0 {
		allNamesSection = allNamesSection[:nextHeading+1]
	}
	if strings.Contains(allNamesSection, "### Dependencies") {
		t.Errorf("all_names should not have Dependencies section (no depends_on), got:\n%s", allNamesSection)
	}
}

func TestGenerate_Views(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
					{Name: "active", PGType: "boolean", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		Views: []model.View{
			{
				Name:    "user_stats",
				Schema:  "app",
				Query:   "SELECT count(*) AS total FROM active_users",
				Comment: "Aggregate user statistics",
				DependsOn: []string{"active_users"},
			},
			{
				Name:    "active_users",
				Schema:  "app",
				Query:   "SELECT id, name FROM users WHERE active = true",
				Comment: "Users who are currently active",
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// Views must appear after tables.
	tablePos := strings.Index(out, "CREATE TABLE app.users")
	viewPos := strings.Index(out, "CREATE VIEW")
	if tablePos < 0 {
		t.Fatalf("missing CREATE TABLE in output:\n%s", out)
	}
	if viewPos < 0 {
		t.Fatalf("missing CREATE VIEW in output:\n%s", out)
	}
	if tablePos > viewPos {
		t.Errorf("tables should appear before views, table=%d view=%d", tablePos, viewPos)
	}

	// View ordering must respect DependsOn: active_users before user_stats.
	activePos := strings.Index(out, "CREATE VIEW app.active_users AS")
	statsPos := strings.Index(out, "CREATE VIEW app.user_stats AS")
	if activePos < 0 {
		t.Fatalf("missing active_users view in output:\n%s", out)
	}
	if statsPos < 0 {
		t.Fatalf("missing user_stats view in output:\n%s", out)
	}
	if activePos > statsPos {
		t.Errorf("active_users should appear before user_stats (dependency order), active=%d stats=%d", activePos, statsPos)
	}

	// Comments are emitted.
	if !strings.Contains(out, "COMMENT ON VIEW app.active_users IS 'Users who are currently active';") {
		t.Errorf("expected comment on active_users view, got:\n%s", out)
	}
	if !strings.Contains(out, "COMMENT ON VIEW app.user_stats IS 'Aggregate user statistics';") {
		t.Errorf("expected comment on user_stats view, got:\n%s", out)
	}
}

func TestGenerate_ViewsNoComments(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		Views: []model.View{
			{
				Name:    "all_items",
				Schema:  "app",
				Query:   "SELECT id FROM items",
				Comment: "Should not appear",
			},
		},
	}

	opts := Options{IncludeComments: false, Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "CREATE VIEW app.all_items AS") {
		t.Errorf("expected CREATE VIEW, got:\n%s", out)
	}
	if strings.Contains(out, "COMMENT ON VIEW") {
		t.Errorf("should not contain view comments when IncludeComments=false, got:\n%s", out)
	}
}

func TestGenerateSQL_MaterializedView(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "app",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
				Comment:  "Monthly order statistics",
			},
		},
	}

	opts := Options{IncludeComments: true, Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "CREATE MATERIALIZED VIEW app.monthly_stats AS") {
		t.Errorf("expected CREATE MATERIALIZED VIEW, got:\n%s", out)
	}
	if !strings.Contains(out, "WITH DATA;") {
		t.Errorf("expected WITH DATA clause, got:\n%s", out)
	}
	if !strings.Contains(out, "date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1") {
		t.Errorf("expected query text in output, got:\n%s", out)
	}
	if !strings.Contains(out, "COMMENT ON MATERIALIZED VIEW app.monthly_stats IS 'Monthly order statistics';") {
		t.Errorf("expected COMMENT ON MATERIALIZED VIEW, got:\n%s", out)
	}

	tableIdx := strings.Index(out, "CREATE TABLE")
	matviewIdx := strings.Index(out, "CREATE MATERIALIZED VIEW")
	if tableIdx == -1 || matviewIdx == -1 {
		t.Fatalf("expected both CREATE TABLE and CREATE MATERIALIZED VIEW in output, got:\n%s", out)
	}
	if matviewIdx <= tableIdx {
		t.Errorf("materialized view should appear after tables, table at %d, matview at %d", tableIdx, matviewIdx)
	}
}

func TestGenerateSQL_MaterializedViewWithIndex(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "app",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
				Indexes: []model.Index{
					{Name: "idx_monthly_stats_month", Columns: []string{"month"}, Unique: false},
				},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "CREATE MATERIALIZED VIEW app.monthly_stats AS") {
		t.Errorf("expected CREATE MATERIALIZED VIEW, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE INDEX idx_monthly_stats_month ON app.monthly_stats") {
		t.Errorf("expected CREATE INDEX on materialized view, got:\n%s", out)
	}

	matviewIdx := strings.Index(out, "CREATE MATERIALIZED VIEW")
	indexIdx := strings.Index(out, "CREATE INDEX idx_monthly_stats_month")
	if matviewIdx == -1 || indexIdx == -1 {
		t.Fatalf("expected both CREATE MATERIALIZED VIEW and CREATE INDEX in output, got:\n%s", out)
	}
	if indexIdx <= matviewIdx {
		t.Errorf("CREATE INDEX should appear after CREATE MATERIALIZED VIEW, matview at %d, index at %d", matviewIdx, indexIdx)
	}
}

func TestSetStatistics(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "kind", PGType: "text", NotNull: true, Statistics: intPtr(1000)},
					{Name: "payload", PGType: "jsonb", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if !strings.Contains(out, "ALTER TABLE app.events ALTER COLUMN kind SET STATISTICS 1000;") {
		t.Errorf("expected SET STATISTICS for kind column, got:\n%s", out)
	}
	// Columns without Statistics should not appear in SET STATISTICS.
	if strings.Contains(out, "SET STATISTICS") && strings.Contains(out, "payload") && strings.Contains(out, "ALTER COLUMN payload") {
		t.Errorf("should not contain SET STATISTICS for payload, got:\n%s", out)
	}
}

func TestSetStatistics_NotEmittedWhenNil(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	if strings.Contains(out, "SET STATISTICS") {
		t.Errorf("should not contain SET STATISTICS when no column has Statistics set, got:\n%s", out)
	}
}

func TestDomainDDL(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Domains: []model.Domain{
			{
				Name:     "slug",
				Schema:   "app",
				BaseType: "text",
				NotNull:  true,
				Check:    "VALUE ~ '^[a-z0-9-]+$'",
			},
			{
				Name:     "email_addr",
				Schema:   "app",
				BaseType: "text",
				NotNull:  true,
				Check:    "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'",
			},
		},
		Enums: []model.Enum{
			{Schema: "app", Name: "role", Values: []string{"admin", "user"}},
		},
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// Domains should be present.
	if !strings.Contains(out, "CREATE DOMAIN app.slug AS text NOT NULL CHECK (VALUE ~ '^[a-z0-9-]+$');") {
		t.Errorf("expected CREATE DOMAIN for slug, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE DOMAIN app.email_addr AS text NOT NULL CHECK (VALUE ~ '^[^@]+@[^@]+\\.[^@]+$');") {
		t.Errorf("expected CREATE DOMAIN for email_addr, got:\n%s", out)
	}

	// Domains should appear after enums but before tables.
	enumPos := strings.Index(out, "CREATE TYPE")
	domainPos := strings.Index(out, "CREATE DOMAIN")
	tablePos := strings.Index(out, "CREATE TABLE")

	if enumPos < 0 {
		t.Fatal("missing CREATE TYPE in output")
	}
	if domainPos < 0 {
		t.Fatal("missing CREATE DOMAIN in output")
	}
	if tablePos < 0 {
		t.Fatal("missing CREATE TABLE in output")
	}

	if domainPos < enumPos {
		t.Errorf("CREATE DOMAIN should appear after CREATE TYPE, domain=%d enum=%d", domainPos, enumPos)
	}
	if domainPos > tablePos {
		t.Errorf("CREATE DOMAIN should appear before CREATE TABLE, domain=%d table=%d", domainPos, tablePos)
	}
}

func TestDomainResolution_DDL(t *testing.T) {
	// End-to-end test: schema with domains and tables referencing them.
	// Verifies CREATE DOMAIN appears, columns reference domain names,
	// and no spurious CHECK constraints exist.
	schema := &model.Schema{
		Name: "app",
		Domains: []model.Domain{
			{
				Name:     "slug",
				Schema:   "app",
				BaseType: "text",
				Check:    "VALUE ~ '^[a-z0-9-]+$'",
			},
			{
				Name:     "email",
				Schema:   "app",
				BaseType: "text",
				Check:    "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'",
			},
		},
		Tables: []model.Table{
			{
				Name:   "profiles",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "handle", PGType: "slug", NotNull: true, SemanticTypeName: "slug"},
					{Name: "contact", PGType: "email", NotNull: true, SemanticTypeName: "email"},
				},
				PK: []string{"id"},
			},
		},
	}

	opts := Options{Format: "sql"}
	out := mustGenerate(t, schema, opts)

	// CREATE DOMAIN should appear.
	if !strings.Contains(out, "CREATE DOMAIN app.slug AS text CHECK (VALUE ~ '^[a-z0-9-]+$');") {
		t.Errorf("expected CREATE DOMAIN for slug, got:\n%s", out)
	}
	if !strings.Contains(out, "CREATE DOMAIN app.email AS text CHECK (VALUE ~ '^[^@]+@[^@]+\\.[^@]+$');") {
		t.Errorf("expected CREATE DOMAIN for email, got:\n%s", out)
	}

	// Columns should reference domain names, not base types.
	if !strings.Contains(out, "handle slug NOT NULL") {
		t.Errorf("expected column to use domain name 'slug', got:\n%s", out)
	}
	if !strings.Contains(out, "contact email NOT NULL") {
		t.Errorf("expected column to use domain name 'email', got:\n%s", out)
	}

	// No semantic CHECK constraints on the table.
	if strings.Contains(out, "chk_profiles_handle") {
		t.Errorf("unexpected semantic CHECK constraint chk_profiles_handle in output:\n%s", out)
	}
	if strings.Contains(out, "chk_profiles_contact") {
		t.Errorf("unexpected semantic CHECK constraint chk_profiles_contact in output:\n%s", out)
	}

	// CREATE DOMAIN should appear before CREATE TABLE.
	domainPos := strings.Index(out, "CREATE DOMAIN")
	tablePos := strings.Index(out, "CREATE TABLE")
	if domainPos < 0 {
		t.Fatal("missing CREATE DOMAIN in output")
	}
	if tablePos < 0 {
		t.Fatal("missing CREATE TABLE in output")
	}
	if domainPos > tablePos {
		t.Errorf("CREATE DOMAIN should appear before CREATE TABLE, domain=%d table=%d", domainPos, tablePos)
	}
}

func TestExclusionConstraintDDL(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
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

	out := mustGenerate(t, schema, Options{Format: "sql"})

	expected := `ALTER TABLE "".bookings ADD CONSTRAINT no_overlap EXCLUDE USING gist (room_id WITH =, during WITH &&);`
	if !strings.Contains(out, expected) {
		t.Errorf("expected exclusion DDL:\n%s\n\ngot:\n%s", expected, out)
	}
}

func TestExclusionConstraintDDLWithWhere(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "room_id", Operator: "="},
					{Column: "during", Operator: "&&"},
				},
				Where:             "active = true",
				Deferrable:        true,
				InitiallyDeferred: true,
			}},
		}},
	}

	out := mustGenerate(t, schema, Options{Format: "sql"})

	expected := `ALTER TABLE "".bookings ADD CONSTRAINT no_overlap EXCLUDE USING gist (room_id WITH =, during WITH &&) WHERE (active = true) DEFERRABLE INITIALLY DEFERRED;`
	if !strings.Contains(out, expected) {
		t.Errorf("expected exclusion DDL:\n%s\n\ngot:\n%s", expected, out)
	}
}
