package sql

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal", "normal"},
		{"user", `"user"`},
		{"has space", `"has space"`},
		{"table", `"table"`},
		{"MyTable", `"MyTable"`},
		{"1starts_with_digit", `"1starts_with_digit"`},
		{"plain_name", "plain_name"},
		{"select", `"select"`},
	}

	for _, tt := range tests {
		got := QuoteIdent(tt.input)
		if got != tt.want {
			t.Errorf("QuoteIdent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestQualifiedName(t *testing.T) {
	tests := []struct {
		schema string
		name   string
		want   string
	}{
		{"game", "players", "game.players"},
		{"public", "user", `public."user"`},
		{"schema", "items", `"schema".items`},
	}

	for _, tt := range tests {
		got := QualifiedName(tt.schema, tt.name)
		if got != tt.want {
			t.Errorf("QualifiedName(%q, %q) = %q, want %q", tt.schema, tt.name, got, tt.want)
		}
	}
}

func TestLiteralValue(t *testing.T) {
	tests := []struct {
		value  string
		pgType string
		want   string
	}{
		{"hello", "text", "'hello'"},
		{"42", "integer", "42"},
		{"true", "boolean", "true"},
		{"", "text", "NULL"},
		{"it's", "text", "'it''s'"},
		{"3.14", "numeric", "3.14"},
		{"100", "bigint", "100"},
	}

	for _, tt := range tests {
		got := LiteralValue(tt.value, tt.pgType)
		if got != tt.want {
			t.Errorf("LiteralValue(%q, %q) = %q, want %q", tt.value, tt.pgType, got, tt.want)
		}
	}
}

func TestExprValue(t *testing.T) {
	expr := "now()"
	got := ExprValue(expr)
	if got != expr {
		t.Errorf("ExprValue(%q) = %q, want %q", expr, got, expr)
	}
}

func TestConstraintName(t *testing.T) {
	tests := []struct {
		table string
		kind  string
		refs  []string
		want  string
	}{
		{"users", "pk", nil, "pk_users"},
		{"posts", "fk", []string{"users"}, "fk_posts_users"},
		{"posts", "idx", []string{"author_id", "created_at"}, "idx_posts_author_id_created_at"},
		{"users", "uq", []string{"email"}, "uq_users_email"},
		{"orders", "ck", []string{"positive_amount"}, "ck_orders_positive_amount"},
	}

	for _, tt := range tests {
		got := ConstraintName(tt.table, tt.kind, tt.refs...)
		if got != tt.want {
			t.Errorf("ConstraintName(%q, %q, %v) = %q, want %q",
				tt.table, tt.kind, tt.refs, got, tt.want)
		}
	}
}

func TestCreateTable(t *testing.T) {
	table := &model.Table{
		Name:   "posts",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "uuid", NotNull: true, DefaultExpr: "gen_random_uuid()"},
			{Name: "title", PGType: "text", NotNull: true},
			{Name: "body", PGType: "text", NotNull: false},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "blog", false, 0, nil)

	// Verify key parts of the output.
	if !strings.Contains(got, "CREATE TABLE blog.posts (") {
		t.Errorf("expected CREATE TABLE blog.posts, got:\n%s", got)
	}
	if !strings.Contains(got, "id uuid NOT NULL DEFAULT gen_random_uuid()") {
		t.Errorf("expected id column definition, got:\n%s", got)
	}
	if !strings.Contains(got, "title text NOT NULL") {
		t.Errorf("expected title column definition, got:\n%s", got)
	}
	if !strings.Contains(got, "body text") {
		t.Errorf("expected body column definition, got:\n%s", got)
	}
	if !strings.Contains(got, "CONSTRAINT pk_posts PRIMARY KEY (id)") {
		t.Errorf("expected PK constraint, got:\n%s", got)
	}
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("should not contain IF NOT EXISTS when idempotent=false, got:\n%s", got)
	}
}

func TestCreateTable_WithPartitioning(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true},
			{Name: "created_at", PGType: "timestamptz", NotNull: true},
		},
		PK: []string{"id"},
		Partitioning: &model.PartitionSpec{
			Strategy: "range",
			Column:   "created_at",
		},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "PARTITION BY RANGE (created_at)") {
		t.Errorf("expected PARTITION BY clause, got:\n%s", got)
	}
}

func TestCreateTable_GeneratedColumn(t *testing.T) {
	table := &model.Table{
		Name:   "products",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "integer", NotNull: true},
			{Name: "price", PGType: "numeric", NotNull: true},
			{Name: "tax", PGType: "numeric", NotNull: true, Generated: "price * 0.2", Stored: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "GENERATED ALWAYS AS (price * 0.2) STORED") {
		t.Errorf("expected GENERATED ALWAYS AS clause, got:\n%s", got)
	}
}

func TestCreateTable_IdentityColumn(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "id bigint NOT NULL GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("expected GENERATED ALWAYS AS IDENTITY, got:\n%s", got)
	}
	// Must not contain the malformed generated-column syntax.
	if strings.Contains(got, "GENERATED ALWAYS AS (ALWAYS") {
		t.Errorf("identity column must not use generated-column syntax, got:\n%s", got)
	}
}

func TestCreateTable_IdentityByDefault(t *testing.T) {
	table := &model.Table{
		Name:   "logs",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true, Identity: "BY DEFAULT"},
			{Name: "message", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "id bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY") {
		t.Errorf("expected GENERATED BY DEFAULT AS IDENTITY, got:\n%s", got)
	}
}

func TestCreateTable_IdentityFallbackPrePG10(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 9, nil)

	// Pre-PG10: identity column should fall back to bigserial NOT NULL.
	if !strings.Contains(got, "id bigserial NOT NULL") {
		t.Errorf("expected bigserial fallback for PG9, got:\n%s", got)
	}
	if strings.Contains(got, "GENERATED") {
		t.Errorf("pre-PG10 should not contain GENERATED, got:\n%s", got)
	}
}

func TestCreateTable_IdentityNoFallbackPG10(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 10, nil)

	// PG10+: identity column should use GENERATED AS IDENTITY.
	if !strings.Contains(got, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("expected GENERATED ALWAYS AS IDENTITY for PG10, got:\n%s", got)
	}
}

func TestCreateTable_IdentityNoFallbackUnspecified(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	// PGVersion 0 means unspecified -- should behave like latest (no fallback).
	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("expected GENERATED ALWAYS AS IDENTITY for unspecified PGVersion, got:\n%s", got)
	}
}

func TestCreateIndex(t *testing.T) {
	index := &model.Index{
		Name:      "idx_posts_author_active",
		Columns:   []string{"author_id"},
		Opclasses: map[string]string{"author_id": "varchar_pattern_ops"},
		Where:     "active = true",
	}

	got := CreateIndex("blog", index, "posts", false, false)

	if !strings.Contains(got, "CREATE INDEX idx_posts_author_active ON blog.posts") {
		t.Errorf("expected CREATE INDEX statement, got:\n%s", got)
	}
	if !strings.Contains(got, "varchar_pattern_ops") {
		t.Errorf("expected opclass, got:\n%s", got)
	}
	if !strings.Contains(got, "WHERE active = true") {
		t.Errorf("expected WHERE clause, got:\n%s", got)
	}
}

func TestCreateIndex_GinMethod(t *testing.T) {
	index := &model.Index{
		Name:      "idx_docs_content",
		Columns:   []string{"content"},
		Method:    "gin",
		Opclasses: map[string]string{"content": "gin_trgm_ops"},
	}

	got := CreateIndex("public", index, "docs", false, false)

	if !strings.Contains(got, "USING gin") {
		t.Errorf("expected USING gin, got:\n%s", got)
	}
}

func TestCreateIndex_WithInclude(t *testing.T) {
	index := &model.Index{
		Name:    "idx_orders_status",
		Columns: []string{"status"},
		Include: []string{"total", "created_at"},
	}

	got := CreateIndex("public", index, "orders", false, false)

	if !strings.Contains(got, "INCLUDE (total, created_at)") {
		t.Errorf("expected INCLUDE clause, got:\n%s", got)
	}
}

func TestCreateEnum(t *testing.T) {
	got := CreateEnum("game", "status", []string{"active", "inactive"}, false)

	expected := "CREATE TYPE game.status AS ENUM ('active', 'inactive');"
	if got != expected {
		t.Errorf("CreateEnum:\n  got:  %s\n  want: %s", got, expected)
	}
}

func TestCreateEnum_Idempotent(t *testing.T) {
	got := CreateEnum("game", "status", []string{"active"}, true)

	if !strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("expected IF NOT EXISTS, got:\n%s", got)
	}
}

func TestCreateTable_EnumColumnSchemaQualified(t *testing.T) {
	enums := []model.Enum{
		{Schema: "game", Name: "server_type", Values: []string{"pvp", "pve"}},
		{Schema: "game", Name: "status", Values: []string{"active", "inactive"}},
	}

	table := &model.Table{
		Name:   "servers",
		Schema: "game",
		Columns: []model.Column{
			{Name: "id", PGType: "uuid", NotNull: true},
			{Name: "kind", PGType: "server_type", NotNull: true},
			{Name: "status", PGType: "status", NotNull: true},
			{Name: "name", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "game", false, 0, enums)

	// Enum columns must be schema-qualified.
	if !strings.Contains(got, "kind game.server_type NOT NULL") {
		t.Errorf("expected schema-qualified enum type game.server_type, got:\n%s", got)
	}
	if !strings.Contains(got, "game.status NOT NULL") {
		t.Errorf("expected schema-qualified enum type game.status, got:\n%s", got)
	}
	// Non-enum columns must NOT be schema-qualified.
	if !strings.Contains(got, "name text NOT NULL") {
		t.Errorf("expected plain text type, got:\n%s", got)
	}
	if !strings.Contains(got, "id uuid NOT NULL") {
		t.Errorf("expected plain uuid type, got:\n%s", got)
	}
}

func TestCreateTable_ArrayColumn(t *testing.T) {
	table := &model.Table{
		Name:   "posts",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: "uuid", NotNull: true},
			{Name: "tags", PGType: "text", NotNull: true, Array: true},
			{Name: "scores", PGType: "integer", NotNull: false, Array: true},
			{Name: "title", PGType: "text", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "tags text[] NOT NULL") {
		t.Errorf("expected tags text[] NOT NULL, got:\n%s", got)
	}
	if !strings.Contains(got, "scores integer[]") {
		t.Errorf("expected scores integer[], got:\n%s", got)
	}
	// Non-array column should not have []
	if !strings.Contains(got, "title text NOT NULL") {
		t.Errorf("expected title text NOT NULL (no []), got:\n%s", got)
	}
}

func TestCreateTable_ArrayColumnWithEnum(t *testing.T) {
	enums := []model.Enum{
		{Schema: "app", Name: "tag_type", Values: []string{"a", "b"}},
	}
	table := &model.Table{
		Name:   "items",
		Schema: "app",
		Columns: []model.Column{
			{Name: "id", PGType: "uuid", NotNull: true},
			{Name: "tags", PGType: "tag_type", NotNull: true, Array: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "app", false, 0, enums)

	// Enum array: schema-qualified enum name + []
	if !strings.Contains(got, "tags app.tag_type[] NOT NULL") {
		t.Errorf("expected tags app.tag_type[] NOT NULL, got:\n%s", got)
	}
}

func TestCreateTable_CrossSchemaEnum(t *testing.T) {
	// Enum defined in a different schema than the table.
	enums := []model.Enum{
		{Schema: "shared", Name: "priority", Values: []string{"low", "medium", "high"}},
	}

	table := &model.Table{
		Name:   "tasks",
		Schema: "app",
		Columns: []model.Column{
			{Name: "id", PGType: "uuid", NotNull: true},
			{Name: "priority", PGType: "priority", NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "app", false, 0, enums)

	// Enum from different schema must use its own schema prefix.
	if !strings.Contains(got, "shared.priority NOT NULL") {
		t.Errorf("expected cross-schema enum type shared.priority, got:\n%s", got)
	}
}

func TestAlterTableAddFK(t *testing.T) {
	table := &model.Table{
		Name:   "posts",
		Schema: "blog",
	}
	fk := &model.FK{
		Name:       "fk_posts_users",
		Columns:    []string{"author_id"},
		RefSchema:  "public",
		RefTable:   "users",
		RefColumns: []string{"id"},
		OnDelete:   "CASCADE",
	}

	got := AlterTableAddFK("blog", table, fk, false)

	if !strings.Contains(got, "ALTER TABLE blog.posts ADD CONSTRAINT fk_posts_users") {
		t.Errorf("expected ALTER TABLE statement, got:\n%s", got)
	}
	if !strings.Contains(got, "FOREIGN KEY (author_id)") {
		t.Errorf("expected FOREIGN KEY clause, got:\n%s", got)
	}
	if !strings.Contains(got, "REFERENCES public.users (id)") {
		t.Errorf("expected REFERENCES clause, got:\n%s", got)
	}
	if !strings.Contains(got, "ON DELETE CASCADE") {
		t.Errorf("expected ON DELETE CASCADE, got:\n%s", got)
	}
}

func TestAlterTableAddUnique(t *testing.T) {
	uq := &model.UniqueConstraint{
		Name:    "uq_users_email",
		Columns: []string{"email"},
	}

	got := AlterTableAddUnique("public", "users", uq, false)

	if !strings.Contains(got, "ALTER TABLE public.users ADD CONSTRAINT uq_users_email UNIQUE (email)") {
		t.Errorf("expected UNIQUE constraint, got:\n%s", got)
	}
}

func TestAlterTableAddCheck(t *testing.T) {
	ck := &model.CheckConstraint{
		Name: "ck_orders_positive_amount",
		Expr: "amount > 0",
	}

	got := AlterTableAddCheck("public", "orders", ck, false)

	if !strings.Contains(got, "ALTER TABLE public.orders ADD CONSTRAINT ck_orders_positive_amount CHECK (amount > 0)") {
		t.Errorf("expected CHECK constraint, got:\n%s", got)
	}
}

func TestIdempotentMode(t *testing.T) {
	// CreateSchema
	got := CreateSchema("myapp", true)
	if !strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("CreateSchema idempotent should have IF NOT EXISTS, got: %s", got)
	}

	// CreateExtension
	got = CreateExtension("uuid-ossp", true)
	if !strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("CreateExtension idempotent should have IF NOT EXISTS, got: %s", got)
	}

	// CreateTable
	table := &model.Table{
		Name:    "items",
		Schema:  "public",
		Columns: []model.Column{{Name: "id", PGType: "integer", NotNull: true}},
		PK:      []string{"id"},
	}
	got = CreateTable(table, "public", true, 0, nil)
	if !strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("CreateTable idempotent should have IF NOT EXISTS, got: %s", got)
	}

	// CreateIndex
	index := &model.Index{Name: "idx_test", Columns: []string{"col"}}
	got = CreateIndex("public", index, "items", true, false)
	if !strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("CreateIndex idempotent should have IF NOT EXISTS, got: %s", got)
	}

	// CreateEnum
	got = CreateEnum("public", "mood", []string{"happy", "sad"}, true)
	if !strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("CreateEnum idempotent should have IF NOT EXISTS, got: %s", got)
	}
}

func TestCreateSchema(t *testing.T) {
	got := CreateSchema("myapp", false)
	if got != "CREATE SCHEMA myapp;" {
		t.Errorf("CreateSchema = %q, want %q", got, "CREATE SCHEMA myapp;")
	}
}

func TestCreateExtension(t *testing.T) {
	got := CreateExtension("pgcrypto", false)
	if got != "CREATE EXTENSION pgcrypto;" {
		t.Errorf("CreateExtension = %q, want %q", got, "CREATE EXTENSION pgcrypto;")
	}
}

func TestCommentOn(t *testing.T) {
	got := CommentOn("TABLE", "public.users", "All registered users")
	expected := "COMMENT ON TABLE public.users IS 'All registered users';"
	if got != expected {
		t.Errorf("CommentOn:\n  got:  %s\n  want: %s", got, expected)
	}
}

func TestCommentOn_EscapesSingleQuotes(t *testing.T) {
	got := CommentOn("COLUMN", "public.users.name", "User's full name")
	if !strings.Contains(got, "User''s full name") {
		t.Errorf("expected escaped single quote, got: %s", got)
	}
}

func TestAlterTableOwner(t *testing.T) {
	got := AlterTableOwner("public", "users", "app_role")
	expected := "ALTER TABLE public.users OWNER TO app_role;"
	if got != expected {
		t.Errorf("AlterTableOwner = %q, want %q", got, expected)
	}
}

func TestCreateIndex_AllASC(t *testing.T) {
	// All ASC (default) -- no Desc field set. Backward compatible.
	index := &model.Index{
		Name:    "idx_events_channel_sent",
		Columns: []string{"channel", "sent_at"},
	}

	got := CreateIndex("chat", index, "events", false, false)

	if !strings.Contains(got, "(channel, sent_at)") {
		t.Errorf("expected plain column list without direction, got:\n%s", got)
	}
	if strings.Contains(got, "DESC") {
		t.Errorf("should not contain DESC when all columns are ASC, got:\n%s", got)
	}
}

func TestCreateIndex_SomeDESC(t *testing.T) {
	// Mixed: first column ASC, second column DESC.
	index := &model.Index{
		Name:    "idx_events_channel_sent",
		Columns: []string{"channel", "sent_at"},
		Desc:    []bool{false, true},
	}

	got := CreateIndex("chat", index, "events", false, false)

	if !strings.Contains(got, "sent_at DESC") {
		t.Errorf("expected sent_at DESC, got:\n%s", got)
	}
	if strings.Contains(got, "channel DESC") {
		t.Errorf("should not have DESC on channel, got:\n%s", got)
	}
	// Verify the full column expression.
	if !strings.Contains(got, "(channel, sent_at DESC)") {
		t.Errorf("expected (channel, sent_at DESC), got:\n%s", got)
	}
}

func TestCreateIndex_AllDESC(t *testing.T) {
	index := &model.Index{
		Name:    "idx_events_recent",
		Columns: []string{"created_at", "id"},
		Desc:    []bool{true, true},
	}

	got := CreateIndex("public", index, "events", false, false)

	if !strings.Contains(got, "(created_at DESC, id DESC)") {
		t.Errorf("expected both columns DESC, got:\n%s", got)
	}
}

func TestCreateIndex_DESCWithOpclass(t *testing.T) {
	index := &model.Index{
		Name:      "idx_docs_title",
		Columns:   []string{"title"},
		Desc:      []bool{true},
		Opclasses: map[string]string{"title": "varchar_pattern_ops"},
	}

	got := CreateIndex("public", index, "docs", false, false)

	// Opclass should come before DESC.
	if !strings.Contains(got, "title varchar_pattern_ops DESC") {
		t.Errorf("expected opclass before DESC, got:\n%s", got)
	}
}

func TestCreateIndex_Concurrently(t *testing.T) {
	index := &model.Index{
		Name:    "idx_posts_author_id",
		Columns: []string{"author_id"},
	}

	got := CreateIndex("blog", index, "posts", false, true)

	if !strings.Contains(got, "CREATE INDEX CONCURRENTLY idx_posts_author_id ON blog.posts") {
		t.Errorf("expected CREATE INDEX CONCURRENTLY, got:\n%s", got)
	}
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("should not contain IF NOT EXISTS when concurrently=true, got:\n%s", got)
	}
}

func TestCreateIndex_ConcurrentlyWithIdempotent(t *testing.T) {
	// When both concurrently and idempotent are true, CONCURRENTLY wins
	// and IF NOT EXISTS is omitted (incompatible in some PG versions).
	index := &model.Index{
		Name:    "idx_posts_author_id",
		Columns: []string{"author_id"},
	}

	got := CreateIndex("blog", index, "posts", true, true)

	if !strings.Contains(got, "CREATE INDEX CONCURRENTLY") {
		t.Errorf("expected CONCURRENTLY, got:\n%s", got)
	}
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("should NOT contain IF NOT EXISTS when concurrently=true, got:\n%s", got)
	}
}

func TestAlterTableAddFK_Idempotent(t *testing.T) {
	table := &model.Table{
		Name:   "posts",
		Schema: "blog",
	}
	fk := &model.FK{
		Name:       "fk_posts_users",
		Columns:    []string{"author_id"},
		RefSchema:  "public",
		RefTable:   "users",
		RefColumns: []string{"id"},
		OnDelete:   "CASCADE",
	}

	got := AlterTableAddFK("blog", table, fk, true)

	if !strings.Contains(got, "DO $$") {
		t.Errorf("expected DO $$ wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "pg_constraint") {
		t.Errorf("expected pg_constraint check, got:\n%s", got)
	}
	if !strings.Contains(got, "conname = 'fk_posts_users'") {
		t.Errorf("expected constraint name check, got:\n%s", got)
	}
	if !strings.Contains(got, "conrelid = 'blog.posts'::regclass") {
		t.Errorf("expected regclass check, got:\n%s", got)
	}
	if !strings.Contains(got, "ALTER TABLE blog.posts ADD CONSTRAINT fk_posts_users FOREIGN KEY (author_id) REFERENCES public.users (id) ON DELETE CASCADE;") {
		t.Errorf("expected inner ALTER TABLE statement, got:\n%s", got)
	}
}

func TestAlterTableAddUnique_Idempotent(t *testing.T) {
	uq := &model.UniqueConstraint{
		Name:    "uq_users_email",
		Columns: []string{"email"},
	}

	got := AlterTableAddUnique("public", "users", uq, true)

	if !strings.Contains(got, "DO $$") {
		t.Errorf("expected DO $$ wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "pg_constraint") {
		t.Errorf("expected pg_constraint check, got:\n%s", got)
	}
	if !strings.Contains(got, "conname = 'uq_users_email'") {
		t.Errorf("expected constraint name check, got:\n%s", got)
	}
	if !strings.Contains(got, "conrelid = 'public.users'::regclass") {
		t.Errorf("expected regclass check, got:\n%s", got)
	}
	if !strings.Contains(got, "ALTER TABLE public.users ADD CONSTRAINT uq_users_email UNIQUE (email);") {
		t.Errorf("expected inner ALTER TABLE statement, got:\n%s", got)
	}
}

func TestCreatePartitionOf(t *testing.T) {
	child := &model.PartitionSpec{
		Name:  "events_2024_01",
		Bound: "FROM ('2024-01-01') TO ('2024-02-01')",
	}

	got := CreatePartitionOf("app", child, "events", false)

	if !strings.Contains(got, "CREATE TABLE app.events_2024_01 PARTITION OF app.events") {
		t.Errorf("expected CREATE TABLE PARTITION OF, got:\n%s", got)
	}
	if !strings.Contains(got, "FOR VALUES FROM ('2024-01-01') TO ('2024-02-01')") {
		t.Errorf("expected FOR VALUES bound, got:\n%s", got)
	}
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Errorf("should not contain IF NOT EXISTS when idempotent=false, got:\n%s", got)
	}
}

func TestCreatePartitionOf_Idempotent(t *testing.T) {
	child := &model.PartitionSpec{
		Name:  "events_2024_01",
		Bound: "FROM ('2024-01-01') TO ('2024-02-01')",
	}

	got := CreatePartitionOf("app", child, "events", true)

	if !strings.Contains(got, "CREATE TABLE IF NOT EXISTS app.events_2024_01 PARTITION OF app.events") {
		t.Errorf("expected IF NOT EXISTS, got:\n%s", got)
	}
}

func TestCreatePartmanParent(t *testing.T) {
	got := CreatePartmanParent("app", "events", "created_at", "1 month", 4)

	if !strings.Contains(got, "partman.create_parent(") {
		t.Errorf("expected partman.create_parent call, got:\n%s", got)
	}
	if !strings.Contains(got, "p_parent_table := 'app.events'") {
		t.Errorf("expected p_parent_table, got:\n%s", got)
	}
	if !strings.Contains(got, "p_control := 'created_at'") {
		t.Errorf("expected p_control, got:\n%s", got)
	}
	if !strings.Contains(got, "p_interval := '1 month'") {
		t.Errorf("expected p_interval, got:\n%s", got)
	}
	if !strings.Contains(got, "p_premake := 4") {
		t.Errorf("expected p_premake, got:\n%s", got)
	}
}

func TestUpdatePartmanConfig(t *testing.T) {
	got := UpdatePartmanConfig("app", "events", "6 months", true)

	if !strings.Contains(got, "UPDATE partman.part_config") {
		t.Errorf("expected UPDATE partman.part_config, got:\n%s", got)
	}
	if !strings.Contains(got, "retention = '6 months'") {
		t.Errorf("expected retention value, got:\n%s", got)
	}
	if !strings.Contains(got, "retention_keep_table = true") {
		t.Errorf("expected retention_keep_table = true, got:\n%s", got)
	}
	if !strings.Contains(got, "parent_table = 'app.events'") {
		t.Errorf("expected parent_table WHERE clause, got:\n%s", got)
	}
}

func TestUpdatePartmanConfig_KeepTableFalse(t *testing.T) {
	got := UpdatePartmanConfig("app", "events", "3 months", false)

	if !strings.Contains(got, "retention_keep_table = false") {
		t.Errorf("expected retention_keep_table = false, got:\n%s", got)
	}
}

func TestAlterTableEnableRLS(t *testing.T) {
	got := AlterTableEnableRLS("app", "documents")
	expected := "ALTER TABLE app.documents ENABLE ROW LEVEL SECURITY;"
	if got != expected {
		t.Errorf("AlterTableEnableRLS = %q, want %q", got, expected)
	}
}

func TestAlterTableEnableRLS_ReservedTableName(t *testing.T) {
	got := AlterTableEnableRLS("public", "user")
	expected := `ALTER TABLE public."user" ENABLE ROW LEVEL SECURITY;`
	if got != expected {
		t.Errorf("AlterTableEnableRLS = %q, want %q", got, expected)
	}
}

func TestCreatePolicy_SelectUsingOnly(t *testing.T) {
	p := model.Policy{
		Name:      "users_see_own",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "user_id = current_user_id()",
	}

	got := CreatePolicy("app", "documents", p)

	if !strings.Contains(got, "CREATE POLICY users_see_own ON app.documents") {
		t.Errorf("expected CREATE POLICY header, got:\n%s", got)
	}
	if !strings.Contains(got, "FOR SELECT") {
		t.Errorf("expected FOR SELECT, got:\n%s", got)
	}
	if !strings.Contains(got, "TO app_user") {
		t.Errorf("expected TO app_user, got:\n%s", got)
	}
	if !strings.Contains(got, "USING (user_id = current_user_id())") {
		t.Errorf("expected USING clause, got:\n%s", got)
	}
	if strings.Contains(got, "WITH CHECK") {
		t.Errorf("should not contain WITH CHECK, got:\n%s", got)
	}
}

func TestCreatePolicy_InsertWithCheckOnly(t *testing.T) {
	p := model.Policy{
		Name:      "users_insert_own",
		Operation: "INSERT",
		Role:      "app_user",
		WithCheck: "user_id = current_user_id()",
	}

	got := CreatePolicy("app", "documents", p)

	if !strings.Contains(got, "FOR INSERT") {
		t.Errorf("expected FOR INSERT, got:\n%s", got)
	}
	if !strings.Contains(got, "WITH CHECK (user_id = current_user_id())") {
		t.Errorf("expected WITH CHECK clause, got:\n%s", got)
	}
	if strings.Contains(got, "USING") {
		t.Errorf("should not contain USING, got:\n%s", got)
	}
}

func TestCreatePolicy_UpdateBothClauses(t *testing.T) {
	p := model.Policy{
		Name:      "users_update_own",
		Operation: "UPDATE",
		Role:      "app_user",
		Using:     "user_id = current_user_id()",
		WithCheck: "user_id = current_user_id()",
	}

	got := CreatePolicy("app", "documents", p)

	if !strings.Contains(got, "FOR UPDATE") {
		t.Errorf("expected FOR UPDATE, got:\n%s", got)
	}
	if !strings.Contains(got, "USING (user_id = current_user_id())") {
		t.Errorf("expected USING clause, got:\n%s", got)
	}
	if !strings.Contains(got, "WITH CHECK (user_id = current_user_id())") {
		t.Errorf("expected WITH CHECK clause, got:\n%s", got)
	}
}

func TestCreatePolicy_AllOmitsFOR(t *testing.T) {
	p := model.Policy{
		Name:      "users_all",
		Operation: "ALL",
		Role:      "app_user",
		Using:     "user_id = current_user_id()",
	}

	got := CreatePolicy("app", "documents", p)

	if strings.Contains(got, "FOR ALL") {
		t.Errorf("should not contain FOR ALL (ALL is the default), got:\n%s", got)
	}
	if strings.Contains(got, "FOR ") {
		t.Errorf("should not contain any FOR clause, got:\n%s", got)
	}
}

func TestCreatePolicy_NoRole(t *testing.T) {
	p := model.Policy{
		Name:      "public_read",
		Operation: "SELECT",
		Using:     "published = true",
	}

	got := CreatePolicy("app", "articles", p)

	if strings.Contains(got, " TO ") {
		t.Errorf("should not contain TO clause when role is empty, got:\n%s", got)
	}
	if !strings.Contains(got, "USING (published = true)") {
		t.Errorf("expected USING clause, got:\n%s", got)
	}
}

func TestCreatePolicy_SchemaQualified(t *testing.T) {
	p := model.Policy{
		Name:      "tenant_isolation",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "tenant_id = current_setting('app.tenant_id')::uuid",
	}

	got := CreatePolicy("multi_tenant", "orders", p)
	expected := `CREATE POLICY tenant_isolation ON multi_tenant.orders FOR SELECT TO app_user USING (tenant_id = current_setting('app.tenant_id')::uuid);`

	if got != expected {
		t.Errorf("CreatePolicy =\n  got:  %s\n  want: %s", got, expected)
	}
}

func TestAlterTableAddCheck_Idempotent(t *testing.T) {
	ck := &model.CheckConstraint{
		Name: "ck_orders_positive_amount",
		Expr: "amount > 0",
	}

	got := AlterTableAddCheck("public", "orders", ck, true)

	if !strings.Contains(got, "DO $$") {
		t.Errorf("expected DO $$ wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "pg_constraint") {
		t.Errorf("expected pg_constraint check, got:\n%s", got)
	}
	if !strings.Contains(got, "conname = 'ck_orders_positive_amount'") {
		t.Errorf("expected constraint name check, got:\n%s", got)
	}
	if !strings.Contains(got, "ALTER TABLE public.orders ADD CONSTRAINT ck_orders_positive_amount CHECK (amount > 0);") {
		t.Errorf("expected inner ALTER TABLE statement, got:\n%s", got)
	}
}

func TestLiteralValue_EnumDefault_Correct(t *testing.T) {
	// Correct flow: col.Default = "created", col.PGType = "status" (enum name)
	// LiteralValue should produce 'created' (single-quoted, as enum literals are strings)
	got := LiteralValue("created", "status")
	want := "'created'"
	if got != want {
		t.Errorf("LiteralValue(%q, %q) = %q, want %q", "created", "status", got, want)
	}
}

func TestCreateDenyMutationFunction(t *testing.T) {
	got := CreateDenyMutationFunction("app")
	if !strings.Contains(got, "CREATE OR REPLACE FUNCTION app.pgdesign_deny_mutation()") {
		t.Errorf("expected function creation, got:\n%s", got)
	}
	if !strings.Contains(got, "RAISE EXCEPTION") {
		t.Errorf("expected RAISE EXCEPTION, got:\n%s", got)
	}
	if !strings.Contains(got, "LANGUAGE plpgsql") {
		t.Errorf("expected plpgsql, got:\n%s", got)
	}
}

func TestCreateAppendOnlyTrigger(t *testing.T) {
	got := CreateAppendOnlyTrigger("app", "events")
	want := "CREATE TRIGGER deny_mutation BEFORE UPDATE OR DELETE ON app.events FOR EACH ROW EXECUTE FUNCTION app.pgdesign_deny_mutation();"
	if got != want {
		t.Errorf("CreateAppendOnlyTrigger = %q, want %q", got, want)
	}
}

func TestLiteralValue_ArrayDefaults(t *testing.T) {
	// Array defaults are always array literals (e.g., {}, {1,2,3}) which
	// PostgreSQL expects as single-quoted strings regardless of element type.
	tests := []struct {
		value  string
		pgType string
		want   string
	}{
		// When the full array type is passed (text[]), non-numeric path quotes correctly.
		{"{}", "text[]", "'{}'"},
		// When the base type is passed for a non-numeric type, also quotes correctly.
		{"{}", "text", "'{}'"},
		// When the full array type is passed (integer[]), the [] suffix makes it
		// non-numeric (isNumericType doesn't match "integer[]"), so it quotes correctly.
		{"{1,2,3}", "integer[]", "'{1,2,3}'"},
	}

	for _, tt := range tests {
		got := LiteralValue(tt.value, tt.pgType)
		if got != tt.want {
			t.Errorf("LiteralValue(%q, %q) = %q, want %q", tt.value, tt.pgType, got, tt.want)
		}
	}
}

func TestLiteralValue_EnumDefault_DoubleQuotedBug(t *testing.T) {
	// Wrong pattern: if someone writes default = "'created'" in TOML,
	// the value reaching LiteralValue is "'created'" (with embedded single quotes).
	// LiteralValue escapes the quotes: '''created''' -- this is bad SQL.
	got := LiteralValue("'created'", "status")
	want := "'''created'''"
	if got != want {
		t.Errorf("LiteralValue(%q, %q) = %q, want %q", "'created'", "status", got, want)
	}
}
