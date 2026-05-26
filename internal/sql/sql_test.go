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

	got := CreateTable(table, "blog", false, 0)

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

	got := CreateTable(table, "public", false, 0)

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

	got := CreateTable(table, "public", false, 0)

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

	got := CreateTable(table, "public", false, 0)

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

	got := CreateTable(table, "public", false, 0)

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

	got := CreateTable(table, "public", false, 9)

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

	got := CreateTable(table, "public", false, 10)

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
	got := CreateTable(table, "public", false, 0)

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
	got = CreateTable(table, "public", true, 0)
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
