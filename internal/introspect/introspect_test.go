package introspect

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/model"
)

const testConnStr = "postgres:///pgdesign_test"
const testSchema = "pgdesign_test"

// canConnect checks if we can connect to local PostgreSQL.
// canSetup connects to the test database and verifies that we can create
// schemas. Returns a connected pgx.Conn on success, or nil if the test
// database is unavailable (tests should be skipped).
func canSetup() *pgx.Conn {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testConnStr)
	if err != nil {
		return nil
	}
	// Verify we have CREATE privilege by attempting a probe schema.
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS pgdesign_probe_test")
	if err != nil {
		conn.Close(ctx)
		return nil
	}
	conn.Exec(ctx, "DROP SCHEMA IF EXISTS pgdesign_probe_test")
	return conn
}

func TestMain(m *testing.M) {
	conn := canSetup()
	if conn == nil {
		// Skip all tests if PG is not available or lacks permissions.
		os.Exit(0)
	}

	ctx := context.Background()

	// Clean up any previous test schema.
	conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")

	setupSQL := `
		CREATE SCHEMA ` + testSchema + `;

		CREATE TYPE ` + testSchema + `.status AS ENUM ('active', 'inactive', 'banned');
		COMMENT ON TYPE ` + testSchema + `.status IS 'User account status';

		CREATE TABLE ` + testSchema + `.users (
			id bigserial PRIMARY KEY,
			name text NOT NULL,
			email text NOT NULL,
			status ` + testSchema + `.status NOT NULL DEFAULT 'active',
			bio text,
			tags text[] NOT NULL DEFAULT '{}',
			created_at timestamptz NOT NULL DEFAULT now()
		);
		COMMENT ON TABLE ` + testSchema + `.users IS 'User accounts';
		COMMENT ON COLUMN ` + testSchema + `.users.name IS 'Full name';
		COMMENT ON COLUMN ` + testSchema + `.users.email IS 'Email address';

		ALTER TABLE ` + testSchema + `.users
			ADD CONSTRAINT uq_users_email UNIQUE (email);

		ALTER TABLE ` + testSchema + `.users
			ADD CONSTRAINT ck_users_name_not_empty CHECK (length(name) > 0);

		CREATE INDEX idx_users_status ON ` + testSchema + `.users (status);
		CREATE INDEX idx_users_email_lower ON ` + testSchema + `.users (lower(email));

		CREATE TABLE ` + testSchema + `.posts (
			id bigserial PRIMARY KEY,
			author_id bigint NOT NULL,
			title text NOT NULL,
			body text,
			published boolean NOT NULL DEFAULT false,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		COMMENT ON TABLE ` + testSchema + `.posts IS 'Blog posts';

		ALTER TABLE ` + testSchema + `.posts
			ADD CONSTRAINT fk_posts_author
			FOREIGN KEY (author_id) REFERENCES ` + testSchema + `.users(id) ON DELETE CASCADE;

		CREATE INDEX idx_posts_author ON ` + testSchema + `.posts (author_id);

		ALTER TABLE ` + testSchema + `.posts
			ADD CONSTRAINT ck_posts_title_not_empty CHECK (length(title) > 0);

		-- Partitioned table: range partitioning on created_at
		CREATE TABLE ` + testSchema + `.events (
			id bigserial,
			event_type text NOT NULL,
			created_at timestamptz NOT NULL,
			payload jsonb,
			PRIMARY KEY (id, created_at)
		) PARTITION BY RANGE (created_at);

		CREATE TABLE ` + testSchema + `.events_2024
			PARTITION OF ` + testSchema + `.events
			FOR VALUES FROM ('2024-01-01') TO ('2025-01-01');

		CREATE TABLE ` + testSchema + `.events_2025
			PARTITION OF ` + testSchema + `.events
			FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');

		-- Partitioned table: list partitioning on region
		CREATE TABLE ` + testSchema + `.orders (
			id bigserial,
			region text NOT NULL,
			total numeric NOT NULL,
			PRIMARY KEY (id, region)
		) PARTITION BY LIST (region);

		CREATE TABLE ` + testSchema + `.orders_us
			PARTITION OF ` + testSchema + `.orders
			FOR VALUES IN ('us-east', 'us-west');

		CREATE TABLE ` + testSchema + `.orders_eu
			PARTITION OF ` + testSchema + `.orders
			FOR VALUES IN ('eu-west', 'eu-central');
	`

	_, execErr := conn.Exec(ctx, setupSQL)
	if execErr != nil {
		conn.Close(ctx)
		panic("test setup failed: " + execErr.Error())
	}
	conn.Close(ctx)

	// Run tests.
	code := m.Run()

	// Teardown: drop test schema.
	conn2, connErr := pgx.Connect(ctx, testConnStr)
	if connErr == nil {
		conn2.Exec(ctx, "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
		conn2.Close(ctx)
	}

	os.Exit(code)
}

func TestIntrospectTables(t *testing.T) {
	schema, diags, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}
	_ = diags

	if schema.PGVersion < 10 {
		t.Errorf("PGVersion = %d, expected >= 10", schema.PGVersion)
	}

	if schema.Name != testSchema {
		t.Errorf("Name = %q, want %q", schema.Name, testSchema)
	}

	// Expect 8 tables: events, events_2024, events_2025, orders, orders_eu, orders_us, posts, users.
	if len(schema.Tables) != 8 {
		names := make([]string, len(schema.Tables))
		for i, t := range schema.Tables {
			names[i] = t.Name
		}
		t.Fatalf("len(Tables) = %d, want 8; got: %v", len(schema.Tables), names)
	}

	// Tables are ordered alphabetically.
	if schema.Tables[0].Name != "events" {
		t.Errorf("Tables[0].Name = %q, want %q", schema.Tables[0].Name, "events")
	}
	if schema.Tables[len(schema.Tables)-1].Name != "users" {
		t.Errorf("Tables[last].Name = %q, want %q", schema.Tables[len(schema.Tables)-1].Name, "users")
	}
}

func TestIntrospectColumns(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	// Users should have 7 columns in attnum order.
	if len(users.Columns) != 7 {
		t.Fatalf("users columns = %d, want 7", len(users.Columns))
	}

	// Check column names in order.
	expectedCols := []string{"id", "name", "email", "status", "bio", "tags", "created_at"}
	for i, want := range expectedCols {
		if users.Columns[i].Name != want {
			t.Errorf("users.Columns[%d].Name = %q, want %q", i, users.Columns[i].Name, want)
		}
	}

	// Check specific column properties.
	nameCol := users.Columns[1]
	if nameCol.PGType != "text" {
		t.Errorf("name.PGType = %q, want %q", nameCol.PGType, "text")
	}
	if !nameCol.NotNull {
		t.Error("name.NotNull = false, want true")
	}
	if nameCol.Comment != "Full name" {
		t.Errorf("name.Comment = %q, want %q", nameCol.Comment, "Full name")
	}

	// bio is nullable.
	bioCol := users.Columns[4]
	if bioCol.NotNull {
		t.Error("bio.NotNull = true, want false")
	}

	// tags is an array column.
	tagsCol := users.Columns[5]
	if tagsCol.PGType != "text" {
		t.Errorf("tags.PGType = %q, want %q", tagsCol.PGType, "text")
	}
	if !tagsCol.Array {
		t.Error("tags.Array = false, want true")
	}

	// Non-array columns should have Array = false.
	if nameCol.Array {
		t.Error("name.Array = true, want false")
	}

	// created_at has a default.
	createdCol := users.Columns[6]
	if createdCol.DefaultExpr != "now()" {
		t.Errorf("created_at.DefaultExpr = %q, want %q", createdCol.DefaultExpr, "now()")
	}
}

func TestIntrospectPrimaryKey(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	if len(users.PK) != 1 || users.PK[0] != "id" {
		t.Errorf("users.PK = %v, want [id]", users.PK)
	}
}

func TestIntrospectForeignKeys(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	posts := findTable(schema.Tables, "posts")
	if posts == nil {
		t.Fatal("posts table not found")
	}

	if len(posts.FKs) != 1 {
		t.Fatalf("posts.FKs = %d, want 1", len(posts.FKs))
	}

	fk := posts.FKs[0]
	if fk.Name != "fk_posts_author" {
		t.Errorf("FK.Name = %q, want %q", fk.Name, "fk_posts_author")
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != "author_id" {
		t.Errorf("FK.Columns = %v, want [author_id]", fk.Columns)
	}
	if fk.RefTable != "users" {
		t.Errorf("FK.RefTable = %q, want %q", fk.RefTable, "users")
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" {
		t.Errorf("FK.RefColumns = %v, want [id]", fk.RefColumns)
	}
	if fk.OnDelete != "CASCADE" {
		t.Errorf("FK.OnDelete = %q, want %q", fk.OnDelete, "CASCADE")
	}
	if fk.RefSchema != testSchema {
		t.Errorf("FK.RefSchema = %q, want %q", fk.RefSchema, testSchema)
	}
}

func TestIntrospectIndexes(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	// Users should have 2 explicit indexes (idx_users_status, idx_users_email_lower).
	// The unique constraint index is reported under Uniques, not here.
	if len(users.Indexes) < 2 {
		t.Fatalf("users.Indexes = %d, want >= 2", len(users.Indexes))
	}

	// Find idx_users_status.
	var statusIdx *indexInfo
	for _, idx := range users.Indexes {
		if idx.Name == "idx_users_status" {
			statusIdx = &indexInfo{idx.Name, idx.Columns, idx.Method}
			break
		}
	}
	if statusIdx == nil {
		t.Error("idx_users_status not found in indexes")
	} else {
		if len(statusIdx.columns) != 1 || statusIdx.columns[0] != "status" {
			t.Errorf("idx_users_status.Columns = %v, want [status]", statusIdx.columns)
		}
	}
}

type indexInfo struct {
	name    string
	columns []string
	method  string
}

func TestIntrospectUniqueConstraints(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	if len(users.Uniques) != 1 {
		t.Fatalf("users.Uniques = %d, want 1", len(users.Uniques))
	}

	uq := users.Uniques[0]
	if uq.Name != "uq_users_email" {
		t.Errorf("Unique.Name = %q, want %q", uq.Name, "uq_users_email")
	}
	if len(uq.Columns) != 1 || uq.Columns[0] != "email" {
		t.Errorf("Unique.Columns = %v, want [email]", uq.Columns)
	}
}

func TestIntrospectCheckConstraints(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	if len(users.Checks) != 1 {
		t.Fatalf("users.Checks = %d, want 1", len(users.Checks))
	}

	ck := users.Checks[0]
	if ck.Name != "ck_users_name_not_empty" {
		t.Errorf("Check.Name = %q, want %q", ck.Name, "ck_users_name_not_empty")
	}
	// pg_get_constraintdef may wrap the expression; we strip CHECK (...).
	if ck.Expr == "" {
		t.Error("Check.Expr is empty")
	}
}

func TestIntrospectEnums(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	if len(schema.Enums) != 1 {
		t.Fatalf("len(Enums) = %d, want 1", len(schema.Enums))
	}

	e := schema.Enums[0]
	if e.Name != "status" {
		t.Errorf("Enum.Name = %q, want %q", e.Name, "status")
	}
	if e.Schema != testSchema {
		t.Errorf("Enum.Schema = %q, want %q", e.Schema, testSchema)
	}
	if len(e.Values) != 3 || e.Values[0] != "active" || e.Values[1] != "inactive" || e.Values[2] != "banned" {
		t.Errorf("Enum.Values = %v, want [active inactive banned]", e.Values)
	}
	if e.Comment != "User account status" {
		t.Errorf("Enum.Comment = %q, want %q", e.Comment, "User account status")
	}
}

func TestIntrospectTableComment(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}
	if users.Comment != "User accounts" {
		t.Errorf("users.Comment = %q, want %q", users.Comment, "User accounts")
	}
}

func TestExport(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	data, err := Export(schema)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	toml := string(data)

	// Basic structure checks.
	if !containsStr(toml, "[meta]") {
		t.Error("export missing [meta]")
	}
	if !containsStr(toml, "schema = \""+testSchema+"\"") {
		t.Error("export missing schema name")
	}
	if !containsStr(toml, "[types.status]") {
		t.Error("export missing enum type")
	}
	if !containsStr(toml, "[tables.users]") {
		t.Error("export missing users table")
	}
	if !containsStr(toml, "[tables.posts]") {
		t.Error("export missing posts table")
	}
	if !containsStr(toml, "[tables.users.columns.id]") {
		t.Error("export missing users.id column")
	}
	if !containsStr(toml, "[tables.posts.fks.fk_posts_author]") {
		t.Error("export missing posts FK")
	}
}

func TestParseIndexDef(t *testing.T) {
	tests := []struct {
		name      string
		def       string
		cols      []string
		desc      []bool
		where     string
		include   []string
		opclasses map[string]string
	}{
		{
			name: "simple btree",
			def:  `CREATE INDEX idx_users_status ON pgdesign_test.users USING btree (status)`,
			cols: []string{"status"},
		},
		{
			name: "expression index",
			def:  `CREATE INDEX idx_users_email_lower ON pgdesign_test.users USING btree (lower(email))`,
			cols: []string{"lower(email)"},
		},
		{
			name:  "partial index",
			def:   `CREATE INDEX idx_active ON myschema.users USING btree (created_at) WHERE (status = 'active')`,
			cols:  []string{"created_at"},
			where: "(status = 'active')",
		},
		{
			name:    "with include",
			def:     `CREATE INDEX idx_covering ON myschema.orders USING btree (customer_id) INCLUDE (total, created_at)`,
			cols:    []string{"customer_id"},
			include: []string{"total", "created_at"},
		},
		{
			name:      "with opclass",
			def:       `CREATE INDEX idx_pattern ON myschema.users USING btree (email varchar_pattern_ops)`,
			cols:      []string{"email"},
			opclasses: map[string]string{"email": "varchar_pattern_ops"},
		},
		{
			name: "one DESC column",
			def:  `CREATE INDEX idx ON myschema.t USING btree (a, b DESC)`,
			cols: []string{"a", "b"},
			desc: []bool{false, true},
		},
		{
			name: "all DESC",
			def:  `CREATE INDEX idx ON myschema.t USING btree (a DESC, b DESC)`,
			cols: []string{"a", "b"},
			desc: []bool{true, true},
		},
		{
			name: "explicit ASC",
			def:  `CREATE INDEX idx ON myschema.t USING btree (a ASC)`,
			cols: []string{"a"},
			// All ASC => desc is nil.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := parseIndexDef(tt.def)

			if len(p.columns) != len(tt.cols) {
				t.Fatalf("columns = %v, want %v", p.columns, tt.cols)
			}
			for i := range tt.cols {
				if p.columns[i] != tt.cols[i] {
					t.Errorf("columns[%d] = %q, want %q", i, p.columns[i], tt.cols[i])
				}
			}

			if len(p.desc) != len(tt.desc) {
				t.Fatalf("desc = %v, want %v", p.desc, tt.desc)
			}
			for i := range tt.desc {
				if p.desc[i] != tt.desc[i] {
					t.Errorf("desc[%d] = %v, want %v", i, p.desc[i], tt.desc[i])
				}
			}

			if p.where != tt.where {
				t.Errorf("where = %q, want %q", p.where, tt.where)
			}

			if len(p.include) != len(tt.include) {
				t.Fatalf("include = %v, want %v", p.include, tt.include)
			}
			for i := range tt.include {
				if p.include[i] != tt.include[i] {
					t.Errorf("include[%d] = %q, want %q", i, p.include[i], tt.include[i])
				}
			}

			if len(p.opclasses) != len(tt.opclasses) {
				t.Fatalf("opclasses = %v, want %v", p.opclasses, tt.opclasses)
			}
			for k, v := range tt.opclasses {
				if got, ok := p.opclasses[k]; !ok || got != v {
					t.Errorf("opclasses[%q] = %q, want %q", k, got, v)
				}
			}
		})
	}
}

// findTable looks up a table by name in a slice.
func findTable(tables []model.Table, name string) *model.Table {
	for i := range tables {
		if tables[i].Name == name {
			return &tables[i]
		}
	}
	return nil
}

// containsStr checks if s contains substr.
func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && strings.Contains(s, substr)
}

// --- Unit tests for partition pure logic (no DB required) ---

func TestMapPartStrategy(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"r", "range"},
		{"l", "list"},
		{"h", "hash"},
		{"x", "x"}, // unknown passes through
		{"", ""},
	}
	for _, tt := range tests {
		got := mapPartStrategy(tt.code)
		if got != tt.want {
			t.Errorf("mapPartStrategy(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestResolvePartColumn(t *testing.T) {
	columns := []model.Column{
		{Name: "id"},
		{Name: "created_at"},
		{Name: "region"},
	}

	tests := []struct {
		name     string
		attNums  []int32
		want     string
	}{
		{"single column", []int32{2}, "created_at"},
		{"first column", []int32{1}, "id"},
		{"multi column", []int32{1, 2}, "id, created_at"},
		{"expression key", []int32{0}, "(expression)"},
		{"unknown attnum", []int32{99}, "attnum_99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePartColumn(tt.attNums, columns)
			if got != tt.want {
				t.Errorf("resolvePartColumn(%v) = %q, want %q", tt.attNums, got, tt.want)
			}
		})
	}
}

// --- Integration tests for partition introspection ---

func TestIntrospectRangePartitioning(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	events := findTable(schema.Tables, "events")
	if events == nil {
		t.Fatal("events table not found")
	}

	if events.Partitioning == nil {
		t.Fatal("events.Partitioning is nil, expected partition spec")
	}

	ps := events.Partitioning
	if ps.Strategy != "range" {
		t.Errorf("Strategy = %q, want %q", ps.Strategy, "range")
	}
	if ps.Column != "created_at" {
		t.Errorf("Column = %q, want %q", ps.Column, "created_at")
	}
	if len(ps.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2", len(ps.Children))
	}

	// Children are ordered by name.
	if ps.Children[0].Strategy != "events_2024" {
		t.Errorf("Children[0].Strategy = %q, want %q", ps.Children[0].Strategy, "events_2024")
	}
	if ps.Children[0].Column == "" {
		t.Error("Children[0].Column (bound expr) is empty")
	}
	if ps.Children[1].Strategy != "events_2025" {
		t.Errorf("Children[1].Strategy = %q, want %q", ps.Children[1].Strategy, "events_2025")
	}
}

func TestIntrospectListPartitioning(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	orders := findTable(schema.Tables, "orders")
	if orders == nil {
		t.Fatal("orders table not found")
	}

	if orders.Partitioning == nil {
		t.Fatal("orders.Partitioning is nil, expected partition spec")
	}

	ps := orders.Partitioning
	if ps.Strategy != "list" {
		t.Errorf("Strategy = %q, want %q", ps.Strategy, "list")
	}
	if ps.Column != "region" {
		t.Errorf("Column = %q, want %q", ps.Column, "region")
	}
	if len(ps.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2", len(ps.Children))
	}
}

func TestIntrospectRegularTableNoPartitioning(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	if users.Partitioning != nil {
		t.Errorf("users.Partitioning = %v, want nil (regular table)", users.Partitioning)
	}
}
