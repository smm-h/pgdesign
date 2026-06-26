package sql

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
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
		{"", "text", "''"},
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
			{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true, DefaultExpr: "gen_random_uuid()"},
			{Name: "title", PGType: typeinfo.T("text"), NotNull: true},
			{Name: "body", PGType: typeinfo.T("text"), NotNull: false},
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
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
		},
		PK: []string{"id"},
		Partitioning: &model.PartitionSpec{
			Strategy: "range",
			Columns: []string{"created_at"},
		},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "PARTITION BY RANGE (created_at)") {
		t.Errorf("expected PARTITION BY clause, got:\n%s", got)
	}
}

func TestCreateTable_WithMultiColumnPartitioning(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "year", PGType: typeinfo.T("int4"), NotNull: true},
			{Name: "region", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
		Partitioning: &model.PartitionSpec{
			Strategy: "range",
			Columns:  []string{"year", "region"},
		},
	}
	got := CreateTable(table, "public", false, 0, nil)
	if !strings.Contains(got, `PARTITION BY RANGE (year, region)`) {
		t.Errorf("expected multi-column PARTITION BY, got:\n%s", got)
	}
}

func TestCreateTable_GeneratedColumn(t *testing.T) {
	table := &model.Table{
		Name:   "products",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
			{Name: "price", PGType: typeinfo.T("numeric"), NotNull: true},
			{Name: "tax", PGType: typeinfo.T("numeric"), NotNull: true, Generated: "price * 0.2", Stored: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "GENERATED ALWAYS AS (price * 0.2) STORED") {
		t.Errorf("expected GENERATED ALWAYS AS clause, got:\n%s", got)
	}
}

func TestColumnDef_VirtualVersionGate(t *testing.T) {
	base := &model.Table{
		Name:   "t",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
			{Name: "val", PGType: typeinfo.T("int4"), NotNull: true},
			{Name: "computed", PGType: typeinfo.T("int4"), NotNull: true, Generated: "val * 2", Stored: false},
		},
		PK: []string{"id"},
	}

	// PG 18 + stored=false -> VIRTUAL
	got := CreateTable(base, "public", false, 18, nil)
	if !strings.Contains(got, "VIRTUAL") {
		t.Errorf("PG18 + stored=false: expected VIRTUAL in output:\n%s", got)
	}
	if strings.Contains(got, "STORED") {
		t.Errorf("PG18 + stored=false: unexpected STORED in output:\n%s", got)
	}

	// PG 17 + stored=false -> defensively STORED (validate catches this)
	got = CreateTable(base, "public", false, 17, nil)
	if !strings.Contains(got, "STORED") {
		t.Errorf("PG17 + stored=false: expected STORED in output:\n%s", got)
	}
	if strings.Contains(got, "VIRTUAL") {
		t.Errorf("PG17 + stored=false: unexpected VIRTUAL in output:\n%s", got)
	}

	// PG 0 (unknown) + stored=false -> STORED (conservative: pgcap.Has returns false)
	got = CreateTable(base, "public", false, 0, nil)
	if !strings.Contains(got, "STORED") {
		t.Errorf("PG0 + stored=false: expected STORED (conservative) in output:\n%s", got)
	}
	if strings.Contains(got, "VIRTUAL") {
		t.Errorf("PG0 + stored=false: unexpected VIRTUAL in output:\n%s", got)
	}

	// PG 0 + stored=true -> STORED
	base.Columns[2].Stored = true
	got = CreateTable(base, "public", false, 0, nil)
	if !strings.Contains(got, "STORED") {
		t.Errorf("PG0 + stored=true: expected STORED in output:\n%s", got)
	}
	if strings.Contains(got, "VIRTUAL") {
		t.Errorf("PG0 + stored=true: unexpected VIRTUAL in output:\n%s", got)
	}

	// PG 18 + stored=true -> STORED (explicit stored always wins)
	got = CreateTable(base, "public", false, 18, nil)
	if !strings.Contains(got, "STORED") {
		t.Errorf("PG18 + stored=true: expected STORED in output:\n%s", got)
	}
}

func TestCreateTable_IdentityColumn(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 16, nil)

	if !strings.Contains(got, "id int8 NOT NULL GENERATED ALWAYS AS IDENTITY") {
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
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true, Identity: "BY DEFAULT"},
			{Name: "message", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 16, nil)

	if !strings.Contains(got, "id int8 NOT NULL GENERATED BY DEFAULT AS IDENTITY") {
		t.Errorf("expected GENERATED BY DEFAULT AS IDENTITY, got:\n%s", got)
	}
}

func TestCreateTable_IdentityFallbackPrePG10(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
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
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 10, nil)

	// PG10+: identity column should use GENERATED AS IDENTITY.
	if !strings.Contains(got, "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("expected GENERATED ALWAYS AS IDENTITY for PG10, got:\n%s", got)
	}
}

func TestCreateTable_IdentityFallbackUnknown(t *testing.T) {
	table := &model.Table{
		Name:   "events",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true, Identity: "ALWAYS"},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
	}

	// PGVersion 0 (unknown): pgcap.Has returns false, so identity falls back
	// to bigserial (conservative behavior). PG version is mandatory in
	// production, so this path is a safety net.
	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "id bigserial NOT NULL") {
		t.Errorf("expected bigserial fallback for unknown PGVersion (conservative), got:\n%s", got)
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
			{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
			{Name: "kind", PGType: typeinfo.T("server_type"), NotNull: true},
			{Name: "status", PGType: typeinfo.T("status"), NotNull: true},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
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
			{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
			{Name: "tags", PGType: typeinfo.T("text"), NotNull: true, Array: true},
			{Name: "scores", PGType: typeinfo.T("int4"), NotNull: false, Array: true},
			{Name: "title", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	if !strings.Contains(got, "tags text[] NOT NULL") {
		t.Errorf("expected tags text[] NOT NULL, got:\n%s", got)
	}
	if !strings.Contains(got, "scores int4[]") {
		t.Errorf("expected scores int4[], got:\n%s", got)
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
			{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
			{Name: "tags", PGType: typeinfo.T("tag_type"), NotNull: true, Array: true},
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
			{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
			{Name: "priority", PGType: typeinfo.T("priority"), NotNull: true},
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
		Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
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

func TestAlterTableAddUnique_Deferrable(t *testing.T) {
	uq := &model.UniqueConstraint{
		Name:       "uq_users_email",
		Columns:    []string{"email"},
		Deferrable: true,
	}

	got := AlterTableAddUnique("public", "users", uq, false)
	want := "ALTER TABLE public.users ADD CONSTRAINT uq_users_email UNIQUE (email) DEFERRABLE;"
	if got != want {
		t.Errorf("AlterTableAddUnique deferrable:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestAlterTableAddUnique_DeferrableInitiallyDeferred(t *testing.T) {
	uq := &model.UniqueConstraint{
		Name:              "uq_users_email",
		Columns:           []string{"email"},
		Deferrable:        true,
		InitiallyDeferred: true,
	}

	got := AlterTableAddUnique("public", "users", uq, false)
	want := "ALTER TABLE public.users ADD CONSTRAINT uq_users_email UNIQUE (email) DEFERRABLE INITIALLY DEFERRED;"
	if got != want {
		t.Errorf("AlterTableAddUnique deferrable initially deferred:\ngot:  %s\nwant: %s", got, want)
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

func TestCreatePolicy_RestrictiveType(t *testing.T) {
	p := model.Policy{
		Name:      "deny_all",
		Type:      "RESTRICTIVE",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "false",
	}

	got := CreatePolicy("app", "documents", p)

	if !strings.Contains(got, "AS RESTRICTIVE") {
		t.Errorf("expected AS RESTRICTIVE, got:\n%s", got)
	}
	// Verify ordering: AS RESTRICTIVE should come before FOR SELECT.
	restrictivePos := strings.Index(got, "AS RESTRICTIVE")
	forPos := strings.Index(got, "FOR SELECT")
	if restrictivePos >= forPos {
		t.Errorf("AS RESTRICTIVE should come before FOR SELECT, got:\n%s", got)
	}
}

func TestCreatePolicy_PermissiveOmitted(t *testing.T) {
	p := model.Policy{
		Name:      "allow_read",
		Type:      "PERMISSIVE",
		Operation: "SELECT",
		Using:     "true",
	}

	got := CreatePolicy("app", "documents", p)

	if strings.Contains(got, "AS PERMISSIVE") {
		t.Errorf("should not contain AS PERMISSIVE (it's the default), got:\n%s", got)
	}
	if strings.Contains(got, "RESTRICTIVE") {
		t.Errorf("should not contain RESTRICTIVE, got:\n%s", got)
	}
}

func TestAlterTableForceRLS(t *testing.T) {
	got := AlterTableForceRLS("app", "secrets")
	expected := "ALTER TABLE app.secrets FORCE ROW LEVEL SECURITY;"
	if got != expected {
		t.Errorf("AlterTableForceRLS = %q, want %q", got, expected)
	}
}

func TestAlterTableForceRLS_ReservedName(t *testing.T) {
	got := AlterTableForceRLS("public", "user")
	expected := `ALTER TABLE public."user" FORCE ROW LEVEL SECURITY;`
	if got != expected {
		t.Errorf("AlterTableForceRLS = %q, want %q", got, expected)
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

func TestIsNumericType_ArraySuffix(t *testing.T) {
	// "integer[]" must NOT be considered numeric -- it's an array type,
	// and array literals like {} need single-quoting.
	if isNumericType("integer[]") {
		t.Errorf("isNumericType(%q) = true, want false", "integer[]")
	}
}

func TestColumnDef_ArrayIntegerDefault_QuotedEmptyArray(t *testing.T) {
	// Regression test: columnDef must pass the resolved pgType (with "[]" suffix)
	// to LiteralValue, so array literals like {} are single-quoted.
	table := &model.Table{
		Name:   "results",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
			{Name: "scores", PGType: typeinfo.T("int4"), NotNull: true, Array: true, Default: model.StrPtr("{}")},
		},
		PK: []string{"id"},
	}

	got := CreateTable(table, "public", false, 0, nil)

	// Fixed: LiteralValue now receives "integer[]" (not "integer"),
	// so array literals are correctly single-quoted.
	if !strings.Contains(got, "DEFAULT '{}'") {
		t.Errorf("expected quoted DEFAULT '{}', got:\n%s", got)
	}
	if strings.Contains(got, "DEFAULT {}") && !strings.Contains(got, "DEFAULT '{}'") {
		t.Errorf("bare DEFAULT {} without quotes is the known bug, got:\n%s", got)
	}
}

func TestCreateIndex_WithParams(t *testing.T) {
	index := &model.Index{
		Name:    "idx_items_embedding",
		Columns: []string{"embedding"},
		Method:  "hnsw",
		With:    map[string]string{"m": "16", "ef_construction": "200"},
	}

	got := CreateIndex("public", index, "items", false, false)

	if !strings.Contains(got, "WITH (ef_construction = 200, m = 16)") {
		t.Errorf("expected WITH clause with sorted keys, got:\n%s", got)
	}
	// WITH must come after the column list and before the semicolon.
	if !strings.Contains(got, "USING hnsw (embedding) WITH (ef_construction = 200, m = 16);") {
		t.Errorf("expected WITH clause in correct position, got:\n%s", got)
	}
}

func TestCreateIndex_WithParamsAndWhere(t *testing.T) {
	// WITH must come before WHERE.
	index := &model.Index{
		Name:    "idx_items_embedding",
		Columns: []string{"embedding"},
		Method:  "hnsw",
		With:    map[string]string{"m": "16"},
		Where:   "active = true",
	}

	got := CreateIndex("public", index, "items", false, false)

	withIdx := strings.Index(got, "WITH (m = 16)")
	whereIdx := strings.Index(got, "WHERE active = true")
	if withIdx < 0 {
		t.Fatalf("expected WITH clause, got:\n%s", got)
	}
	if whereIdx < 0 {
		t.Fatalf("expected WHERE clause, got:\n%s", got)
	}
	if withIdx >= whereIdx {
		t.Errorf("WITH clause must come before WHERE clause, got:\n%s", got)
	}
}

func TestCreateView(t *testing.T) {
	view := &model.View{
		Name:  "active_users",
		Query: "SELECT id, name FROM users WHERE active = true",
	}

	// Basic CREATE VIEW (not idempotent).
	got := CreateView("app", view, false)
	if !strings.Contains(got, "CREATE VIEW app.active_users AS") {
		t.Errorf("expected CREATE VIEW, got:\n%s", got)
	}
	if !strings.Contains(got, "SELECT id, name FROM users WHERE active = true") {
		t.Errorf("expected query in output, got:\n%s", got)
	}
	if strings.Contains(got, "OR REPLACE") {
		t.Errorf("should not contain OR REPLACE when idempotent=false, got:\n%s", got)
	}

	// CREATE OR REPLACE VIEW (idempotent).
	got = CreateView("app", view, true)
	if !strings.Contains(got, "CREATE OR REPLACE VIEW app.active_users AS") {
		t.Errorf("expected CREATE OR REPLACE VIEW, got:\n%s", got)
	}

	// With schema name requiring quoting.
	got = CreateView("schema", view, false)
	if !strings.Contains(got, `CREATE VIEW "schema".active_users AS`) {
		t.Errorf("expected quoted schema name, got:\n%s", got)
	}
}

func TestDropView(t *testing.T) {
	// Basic DROP VIEW (not idempotent).
	got := DropView("app", "active_users", false)
	if got != "DROP VIEW app.active_users;\n" {
		t.Errorf("DropView = %q, want %q", got, "DROP VIEW app.active_users;\n")
	}

	// DROP VIEW IF EXISTS (idempotent).
	got = DropView("app", "active_users", true)
	if got != "DROP VIEW IF EXISTS app.active_users;\n" {
		t.Errorf("DropView idempotent = %q, want %q", got, "DROP VIEW IF EXISTS app.active_users;\n")
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

func TestCreateMaterializedView_WithData(t *testing.T) {
	mv := &model.MaterializedView{
		Name:     "monthly_stats",
		Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
		WithData: true,
	}
	got := CreateMaterializedView("public", mv)
	if !strings.Contains(got, "CREATE MATERIALIZED VIEW public.monthly_stats AS") {
		t.Errorf("expected CREATE MATERIALIZED VIEW, got:\n%s", got)
	}
	if !strings.Contains(got, "WITH DATA;") {
		t.Errorf("expected WITH DATA, got:\n%s", got)
	}
	if strings.Contains(got, "WITH NO DATA") {
		t.Errorf("expected no WITH NO DATA, got:\n%s", got)
	}
}

func TestCreateMaterializedView_WithNoData(t *testing.T) {
	mv := &model.MaterializedView{
		Name:     "monthly_stats",
		Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
		WithData: false,
	}
	got := CreateMaterializedView("public", mv)
	if !strings.Contains(got, "CREATE MATERIALIZED VIEW public.monthly_stats AS") {
		t.Errorf("expected CREATE MATERIALIZED VIEW, got:\n%s", got)
	}
	if !strings.Contains(got, "WITH NO DATA;") {
		t.Errorf("expected WITH NO DATA, got:\n%s", got)
	}
	// "WITH NO DATA;" contains "DATA" -- check there is no standalone "WITH DATA;" line.
	withoutNoData := strings.ReplaceAll(got, "WITH NO DATA;", "")
	if strings.Contains(withoutNoData, "WITH DATA;") {
		t.Errorf("expected no standalone WITH DATA, got:\n%s", got)
	}
}

func TestDropMaterializedView(t *testing.T) {
	got := DropMaterializedView("public", "monthly_stats", false)
	if got != "DROP MATERIALIZED VIEW public.monthly_stats;\n" {
		t.Errorf("DropMaterializedView = %q, want %q", got, "DROP MATERIALIZED VIEW public.monthly_stats;\n")
	}
	if strings.Contains(got, "IF EXISTS") {
		t.Errorf("expected no IF EXISTS, got:\n%s", got)
	}
}

func TestDropMaterializedView_Idempotent(t *testing.T) {
	got := DropMaterializedView("public", "monthly_stats", true)
	if got != "DROP MATERIALIZED VIEW IF EXISTS public.monthly_stats;\n" {
		t.Errorf("DropMaterializedView idempotent = %q, want %q", got, "DROP MATERIALIZED VIEW IF EXISTS public.monthly_stats;\n")
	}
}

func TestRefreshMaterializedView(t *testing.T) {
	got := RefreshMaterializedView("public", "monthly_stats", false)
	if got != "REFRESH MATERIALIZED VIEW public.monthly_stats;\n" {
		t.Errorf("RefreshMaterializedView = %q, want %q", got, "REFRESH MATERIALIZED VIEW public.monthly_stats;\n")
	}
	if strings.Contains(got, "CONCURRENTLY") {
		t.Errorf("expected no CONCURRENTLY, got:\n%s", got)
	}
}

func TestRefreshMaterializedView_Concurrently(t *testing.T) {
	got := RefreshMaterializedView("public", "monthly_stats", true)
	if got != "REFRESH MATERIALIZED VIEW CONCURRENTLY public.monthly_stats;\n" {
		t.Errorf("RefreshMaterializedView concurrent = %q, want %q", got, "REFRESH MATERIALIZED VIEW CONCURRENTLY public.monthly_stats;\n")
	}
}

func TestCreateTable_ColumnCollation(t *testing.T) {
	table := &model.Table{
		Name:   "messages",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "content", PGType: typeinfo.T("text"), NotNull: true, Collation: "de_DE"},
			{Name: "title", PGType: typeinfo.T("text"), NotNull: true, Collation: "C"},
		},
		PK: []string{"id"},
	}
	got := CreateTable(table, "public", false, 0, nil)
	if !strings.Contains(got, `content text COLLATE "de_DE" NOT NULL`) {
		t.Errorf("expected COLLATE de_DE for content, got:\n%s", got)
	}
	if !strings.Contains(got, `title text COLLATE "C" NOT NULL`) {
		t.Errorf("expected COLLATE C for title, got:\n%s", got)
	}
}

func TestCreateTable_ColumnCollation_NoCollation(t *testing.T) {
	table := &model.Table{
		Name:   "items",
		Schema: "public",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
		},
		PK: []string{"id"},
	}
	got := CreateTable(table, "public", false, 0, nil)
	if strings.Contains(got, "COLLATE") {
		t.Errorf("should not contain COLLATE when no collation set, got:\n%s", got)
	}
}

func TestCreateIndex_WithCollation(t *testing.T) {
	index := &model.Index{
		Name:       "idx_messages_content",
		Columns:    []string{"content"},
		Collations: map[string]string{"content": "C"},
	}
	got := CreateIndex("public", index, "messages", false, false)
	if !strings.Contains(got, `content COLLATE "C"`) {
		t.Errorf("expected COLLATE C for content, got:\n%s", got)
	}
}

func TestCreateIndex_WithCollationAndOpclass(t *testing.T) {
	index := &model.Index{
		Name:       "idx_messages_content",
		Columns:    []string{"content"},
		Collations: map[string]string{"content": "C"},
		Opclasses:  map[string]string{"content": "varchar_pattern_ops"},
	}
	got := CreateIndex("public", index, "messages", false, false)
	// PostgreSQL order: column COLLATE collation opclass
	if !strings.Contains(got, `content COLLATE "C" varchar_pattern_ops`) {
		t.Errorf("expected COLLATE before opclass, got:\n%s", got)
	}
}

func TestCreateIndex_WithCollationAndDesc(t *testing.T) {
	index := &model.Index{
		Name:       "idx_messages_content",
		Columns:    []string{"content"},
		Collations: map[string]string{"content": "de_DE"},
		Desc:       []bool{true},
	}
	got := CreateIndex("public", index, "messages", false, false)
	if !strings.Contains(got, `content COLLATE "de_DE" DESC`) {
		t.Errorf("expected COLLATE before DESC, got:\n%s", got)
	}
}

func TestCreateIndex_MultiColumnCollation(t *testing.T) {
	index := &model.Index{
		Name:       "idx_multi",
		Columns:    []string{"first_name", "last_name"},
		Collations: map[string]string{"first_name": "de_DE", "last_name": "C"},
	}
	got := CreateIndex("public", index, "users", false, false)
	if !strings.Contains(got, `first_name COLLATE "de_DE"`) {
		t.Errorf("expected COLLATE de_DE for first_name, got:\n%s", got)
	}
	if !strings.Contains(got, `last_name COLLATE "C"`) {
		t.Errorf("expected COLLATE C for last_name, got:\n%s", got)
	}
}

func TestCreateDomain(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		domain model.Domain
		want   string
	}{
		{
			name:   "basic",
			schema: "app",
			domain: model.Domain{Name: "slug", BaseType: typeinfo.T("text")},
			want:   "CREATE DOMAIN app.slug AS text;",
		},
		{
			name:   "with_not_null",
			schema: "app",
			domain: model.Domain{Name: "email", BaseType: typeinfo.T("text"), NotNull: true},
			want:   "CREATE DOMAIN app.email AS text NOT NULL;",
		},
		{
			name:   "with_check",
			schema: "public",
			domain: model.Domain{Name: "positive_int", BaseType: typeinfo.T("int4"), Check: "VALUE > 0"},
			want:   "CREATE DOMAIN public.positive_int AS int4 CHECK (VALUE > 0);",
		},
		{
			name:   "with_default_literal",
			schema: "app",
			domain: model.Domain{Name: "counter", BaseType: typeinfo.T("int8"), NotNull: true, Default: "0"},
			want:   "CREATE DOMAIN app.counter AS int8 NOT NULL DEFAULT 0;",
		},
		{
			name:   "with_default_expr",
			schema: "app",
			domain: model.Domain{Name: "ts", BaseType: typeinfo.T("timestamptz"), NotNull: true, DefaultExpr: "now()"},
			want:   "CREATE DOMAIN app.ts AS timestamptz NOT NULL DEFAULT now();",
		},
		{
			name:   "full",
			schema: "myapp",
			domain: model.Domain{Name: "slug", BaseType: typeinfo.T("text"), NotNull: true, Check: "VALUE ~ '^[a-z0-9-]+$'"},
			want:   "CREATE DOMAIN myapp.slug AS text NOT NULL CHECK (VALUE ~ '^[a-z0-9-]+$');",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CreateDomain(tt.schema, tt.domain)
			if got != tt.want {
				t.Errorf("CreateDomain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateCompositeType(t *testing.T) {
	ct := model.CompositeType{
		Name:   "address",
		Schema: "public",
		Fields: []model.CompositeField{
			{Name: "city", PGType: typeinfo.T("text")},
			{Name: "state", PGType: typeinfo.T("text")},
			{Name: "street", PGType: typeinfo.T("text")},
			{Name: "zip", PGType: typeinfo.T("text")},
		},
	}
	got := CreateCompositeType("public", ct)
	if !strings.Contains(got, "CREATE TYPE") {
		t.Errorf("missing CREATE TYPE: %s", got)
	}
	if !strings.Contains(got, "AS (") {
		t.Errorf("missing AS (: %s", got)
	}
	if !strings.Contains(got, "city text") {
		t.Errorf("missing field definition: %s", got)
	}
	// Verify all fields are present.
	for _, f := range ct.Fields {
		if !strings.Contains(got, f.Name+" "+typeinfo.Reconstruct(f.PGType)) {
			t.Errorf("missing field %q: %s", f.Name, got)
		}
	}
	// Verify schema qualification.
	if !strings.Contains(got, "public.address") {
		t.Errorf("missing schema-qualified name: %s", got)
	}
}

func TestCreateCompositeType_ReservedFieldName(t *testing.T) {
	ct := model.CompositeType{
		Name:   "meta",
		Schema: "app",
		Fields: []model.CompositeField{
			{Name: "user", PGType: typeinfo.T("text")},
			{Name: "value", PGType: typeinfo.T("int4")},
		},
	}
	got := CreateCompositeType("app", ct)
	// "user" is a reserved word and should be quoted.
	if !strings.Contains(got, `"user" text`) {
		t.Errorf("reserved field name should be quoted: %s", got)
	}
}

func TestDropCompositeType(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		typName string
		cascade bool
		want    string
	}{
		{
			name:    "without_cascade",
			schema:  "public",
			typName: "address",
			cascade: false,
			want:    "DROP TYPE public.address;",
		},
		{
			name:    "with_cascade",
			schema:  "app",
			typName: "point3d",
			cascade: true,
			want:    "DROP TYPE app.point3d CASCADE;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DropCompositeType(tt.schema, tt.typName, tt.cascade)
			if got != tt.want {
				t.Errorf("DropCompositeType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDropDomain(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		domain  string
		cascade bool
		want    string
	}{
		{
			name:    "without_cascade",
			schema:  "app",
			domain:  "slug",
			cascade: false,
			want:    "DROP DOMAIN app.slug;",
		},
		{
			name:    "with_cascade",
			schema:  "app",
			domain:  "email",
			cascade: true,
			want:    "DROP DOMAIN app.email CASCADE;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DropDomain(tt.schema, tt.domain, tt.cascade)
			if got != tt.want {
				t.Errorf("DropDomain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateSequence_Full(t *testing.T) {
	seq := &model.Sequence{
		Name:      "order_seq",
		Start:     model.Int64Ptr(100),
		Increment: model.Int64Ptr(2),
		MinValue:  model.Int64Ptr(1),
		MaxValue:  model.Int64Ptr(999999),
		Cache:     model.Int64Ptr(10),
		Cycle:     true,
		OwnedBy:   "orders.id",
	}
	got := CreateSequence("myapp", seq)
	want := "CREATE SEQUENCE myapp.order_seq START WITH 100 INCREMENT BY 2 MINVALUE 1 MAXVALUE 999999 CACHE 10 CYCLE OWNED BY myapp.orders.id;"
	if got != want {
		t.Errorf("CreateSequence() =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestCreateSequence_Minimal(t *testing.T) {
	seq := &model.Sequence{Name: "simple_seq"}
	got := CreateSequence("public", seq)
	want := "CREATE SEQUENCE public.simple_seq NO MINVALUE NO MAXVALUE NO CYCLE;"
	if got != want {
		t.Errorf("CreateSequence() =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestDropSequence(t *testing.T) {
	got := DropSequence("myapp", "order_seq", false)
	want := "DROP SEQUENCE myapp.order_seq;"
	if got != want {
		t.Errorf("DropSequence() = %q, want %q", got, want)
	}
}

func TestDropSequence_Cascade(t *testing.T) {
	got := DropSequence("myapp", "order_seq", true)
	want := "DROP SEQUENCE myapp.order_seq CASCADE;"
	if got != want {
		t.Errorf("DropSequence() = %q, want %q", got, want)
	}
}

func TestAlterSequence(t *testing.T) {
	seq := &model.Sequence{
		Name:  "order_seq",
		Start: model.Int64Ptr(500),
		Cache: model.Int64Ptr(20),
	}
	got := AlterSequence("myapp", seq)
	want := "ALTER SEQUENCE myapp.order_seq START WITH 500 NO MINVALUE NO MAXVALUE CACHE 20 NO CYCLE;"
	if got != want {
		t.Errorf("AlterSequence() =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestCreateFunction_Full(t *testing.T) {
	f := model.Function{
		Name:       "calc_total",
		Language:   "plpgsql",
		ReturnType: "numeric",
		Args: []model.FunctionArg{
			{Name: "order_id", Type: typeinfo.T("uuid")},
			{Name: "tax_rate", Type: typeinfo.T("numeric"), Default: "0.0"},
		},
		Body:            "BEGIN\n  RETURN 42;\nEND;",
		Volatility:      "STABLE",
		Parallel:        "SAFE",
		SecurityDefiner: true,
		Cost:            model.Float64Ptr(100),
		Rows:            model.Float64Ptr(1000),
	}
	got := CreateFunction("app", f)
	for _, want := range []string{
		"CREATE OR REPLACE FUNCTION app.calc_total",
		"order_id uuid",
		"tax_rate numeric DEFAULT 0.0",
		"RETURNS numeric",
		"$pgdesign$",
		"LANGUAGE plpgsql",
		"STABLE",
		"PARALLEL SAFE",
		"SECURITY DEFINER",
		"COST 100",
		"ROWS 1000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestCreateFunction_Minimal(t *testing.T) {
	f := model.Function{
		Name:       "get_one",
		Language:   "sql",
		ReturnType: "integer",
		Body:       "SELECT 1;",
	}
	got := CreateFunction("public", f)
	for _, want := range []string{
		"CREATE OR REPLACE FUNCTION public.get_one()",
		"RETURNS integer",
		"SELECT 1;",
		"LANGUAGE sql",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{
		"STABLE",
		"VOLATILE",
		"PARALLEL",
		"SECURITY DEFINER",
		"COST",
		"ROWS",
	} {
		if strings.Contains(got, notWant) {
			t.Errorf("expected output NOT to contain %q, got:\n%s", notWant, got)
		}
	}
}

func TestCreateFunction_Procedure(t *testing.T) {
	f := model.Function{
		Name:     "do_cleanup",
		Language: "plpgsql",
		Body:     "DELETE FROM logs;",
		IsProc:   true,
	}
	got := CreateFunction("app", f)
	for _, want := range []string{
		"CREATE OR REPLACE PROCEDURE app.do_cleanup()",
		"LANGUAGE plpgsql",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{
		"RETURNS",
		"FUNCTION",
	} {
		if strings.Contains(got, notWant) {
			t.Errorf("expected output NOT to contain %q, got:\n%s", notWant, got)
		}
	}
}

func TestDropFunction(t *testing.T) {
	f := model.Function{
		Name: "calc_total",
		Args: []model.FunctionArg{
			{Name: "order_id", Type: typeinfo.T("uuid")},
			{Name: "tax_rate", Type: typeinfo.T("numeric")},
		},
	}
	got := DropFunction("app", f, false)
	want := "DROP FUNCTION app.calc_total(uuid, numeric);"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	gotCascade := DropFunction("app", f, true)
	wantCascade := "DROP FUNCTION app.calc_total(uuid, numeric) CASCADE;"
	if gotCascade != wantCascade {
		t.Errorf("expected %q, got %q", wantCascade, gotCascade)
	}
}

func TestDropFunction_Procedure(t *testing.T) {
	f := model.Function{
		Name:   "do_cleanup",
		IsProc: true,
	}
	got := DropFunction("app", f, false)
	want := "DROP PROCEDURE app.do_cleanup();"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestCreateTrigger_Full(t *testing.T) {
	trig := model.Trigger{
		Name:     "audit_changes",
		Function: "audit_func",
		Events:   []string{"INSERT", "UPDATE"},
		Timing:   "AFTER",
		ForEach:  "ROW",
		When:     "NEW.status = 'active'",
	}
	got := CreateTrigger("app", "orders", trig)
	for _, want := range []string{
		"CREATE TRIGGER",
		"audit_changes",
		"AFTER",
		"INSERT OR UPDATE",
		"app.orders",
		"FOR EACH ROW",
		"WHEN (NEW.status = 'active')",
		"EXECUTE FUNCTION",
		"app.audit_func()",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
	// Should NOT contain CONSTRAINT, DEFERRABLE, REFERENCING.
	for _, notWant := range []string{
		"CONSTRAINT",
		"DEFERRABLE",
		"REFERENCING",
	} {
		if strings.Contains(got, notWant) {
			t.Errorf("expected output NOT to contain %q, got:\n%s", notWant, got)
		}
	}
}

func TestStateMachineTriggerFuncName(t *testing.T) {
	got := StateMachineTriggerFuncName("orders", "status")
	want := "_pgdesign_sm_orders_status"
	if got != want {
		t.Errorf("StateMachineTriggerFuncName = %q, want %q", got, want)
	}
}

func TestCreateStateMachineTriggerFunction(t *testing.T) {
	transitions := []semtype.SMTransitionDef{
		{
			Name: "activate",
			From: []string{"pending"},
			To:   "active",
		},
		{
			Name: "suspend",
			From: []string{"active"},
			To:   "suspended",
			Requires: map[string]string{
				"suspended_reason": "text",
			},
		},
		{
			Name: "reactivate",
			From: []string{"suspended"},
			To:   "active",
		},
		{
			Name: "close",
			From: []string{"active", "suspended"},
			To:   "closed",
		},
	}

	got := CreateStateMachineTriggerFunction("app", "orders", "status", transitions)

	// Check function header.
	if !strings.Contains(got, "CREATE OR REPLACE FUNCTION app._pgdesign_sm_orders_status() RETURNS trigger AS $pgdesign$") {
		t.Errorf("expected function header, got:\n%s", got)
	}
	if !strings.Contains(got, "$pgdesign$ LANGUAGE plpgsql;") {
		t.Errorf("expected language footer, got:\n%s", got)
	}

	// Check IS DISTINCT FROM guard.
	if !strings.Contains(got, "OLD.status IS DISTINCT FROM NEW.status") {
		t.Errorf("expected IS DISTINCT FROM, got:\n%s", got)
	}

	// Check valid transition lines (sorted by from-state).
	if !strings.Contains(got, "OLD.status = 'active' AND NEW.status IN ('closed', 'suspended')") {
		t.Errorf("expected active->closed,suspended transition, got:\n%s", got)
	}
	if !strings.Contains(got, "OLD.status = 'pending' AND NEW.status IN ('active')") {
		t.Errorf("expected pending->active transition, got:\n%s", got)
	}
	if !strings.Contains(got, "OLD.status = 'suspended' AND NEW.status IN ('active', 'closed')") {
		t.Errorf("expected suspended->active,closed transition, got:\n%s", got)
	}

	// Check invalid transition exception.
	if !strings.Contains(got, "RAISE EXCEPTION 'invalid state transition: %s -> %s'") {
		t.Errorf("expected invalid transition exception, got:\n%s", got)
	}

	// Check requires check for suspend transition.
	if !strings.Contains(got, "OLD.status = 'active' AND NEW.status = 'suspended' AND NEW.suspended_reason IS NULL") {
		t.Errorf("expected requires check for suspended_reason, got:\n%s", got)
	}
	if !strings.Contains(got, "RAISE EXCEPTION 'transition suspend requires non-null suspended_reason'") {
		t.Errorf("expected requires exception message, got:\n%s", got)
	}

	// Check RETURN NEW.
	if !strings.Contains(got, "RETURN NEW;") {
		t.Errorf("expected RETURN NEW, got:\n%s", got)
	}
}

func TestCreateStateMachineTriggerFunction_NoRequires(t *testing.T) {
	transitions := []semtype.SMTransitionDef{
		{Name: "start", From: []string{"draft"}, To: "active"},
		{Name: "finish", From: []string{"active"}, To: "done"},
	}

	got := CreateStateMachineTriggerFunction("myapp", "tasks", "state", transitions)

	// Should have valid transition check but no requires checks.
	if !strings.Contains(got, "OLD.state = 'active' AND NEW.state IN ('done')") {
		t.Errorf("expected active->done transition, got:\n%s", got)
	}
	if !strings.Contains(got, "OLD.state = 'draft' AND NEW.state IN ('active')") {
		t.Errorf("expected draft->active transition, got:\n%s", got)
	}
	// No requires checks.
	if strings.Contains(got, "IS NULL") {
		t.Errorf("expected no requires checks, got:\n%s", got)
	}
}

func TestCreateStateMachineTrigger(t *testing.T) {
	got := CreateStateMachineTrigger("app", "orders", "status")
	want := "CREATE TRIGGER _pgdesign_sm_orders_status BEFORE UPDATE OF status ON app.orders FOR EACH ROW EXECUTE FUNCTION app._pgdesign_sm_orders_status();"
	if got != want {
		t.Errorf("CreateStateMachineTrigger =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestCreateStateMachineTrigger_ReservedColumnName(t *testing.T) {
	got := CreateStateMachineTrigger("app", "items", "type")
	// "type" is a reserved word and should be quoted.
	if !strings.Contains(got, `UPDATE OF "type"`) {
		t.Errorf("expected quoted column name, got:\n%s", got)
	}
}

func TestCreateTrigger_Minimal(t *testing.T) {
	trig := model.Trigger{
		Name:     "simple",
		Function: "my_func",
		Events:   []string{"INSERT"},
		Timing:   "BEFORE",
		ForEach:  "ROW",
	}
	got := CreateTrigger("app", "orders", trig)
	for _, want := range []string{
		"CREATE TRIGGER simple BEFORE INSERT ON",
		"FOR EACH ROW",
		"EXECUTE FUNCTION",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{
		"CONSTRAINT",
		"WHEN",
		"REFERENCING",
		"DEFERRABLE",
	} {
		if strings.Contains(got, notWant) {
			t.Errorf("expected output NOT to contain %q, got:\n%s", notWant, got)
		}
	}
}

func TestCreateTrigger_Constraint(t *testing.T) {
	trig := model.Trigger{
		Name:              "fk_check",
		Function:          "check_func",
		Events:            []string{"INSERT"},
		Timing:            "AFTER",
		ForEach:           "ROW",
		Constraint:        true,
		Deferrable:        true,
		InitiallyDeferred: true,
	}
	got := CreateTrigger("app", "orders", trig)
	for _, want := range []string{
		"CREATE CONSTRAINT TRIGGER",
		"DEFERRABLE",
		"INITIALLY DEFERRED",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestCreateTrigger_WithReferencing(t *testing.T) {
	trig := model.Trigger{
		Name:           "log_changes",
		Function:       "log_func",
		Events:         []string{"INSERT"},
		Timing:         "AFTER",
		ForEach:        "ROW",
		ReferencingOld: "old_rows",
		ReferencingNew: "new_rows",
	}
	got := CreateTrigger("app", "orders", trig)
	if !strings.Contains(got, "REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows") {
		t.Errorf("expected REFERENCING clause, got:\n%s", got)
	}
}

func TestCreateTrigger_Statement(t *testing.T) {
	trig := model.Trigger{
		Name:     "batch_notify",
		Function: "notify_func",
		Events:   []string{"INSERT", "UPDATE", "DELETE"},
		Timing:   "AFTER",
		ForEach:  "STATEMENT",
	}
	got := CreateTrigger("app", "orders", trig)
	for _, want := range []string{
		"FOR EACH STATEMENT",
		"INSERT OR UPDATE OR DELETE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestDropTrigger(t *testing.T) {
	got := DropTrigger("app", "orders", "audit_changes")
	for _, want := range []string{
		"DROP TRIGGER",
		"audit_changes",
		"ON",
		"app.orders",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}
