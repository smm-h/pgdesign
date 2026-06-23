package introspect

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/testdb"
)

const testSchema = "public"

// Package-level state set by TestMain.
var (
	ephemeralDB  *testdb.EphemeralDB
	testConnStr  string
	testManager  *testdb.Manager
)

func TestMain(m *testing.M) {
	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/postgres?sslmode=disable"
	}

	mgr, err := testdb.NewManager(dbURL)
	if err != nil {
		// Cannot set up manager; skip all tests.
		fmt.Fprintf(os.Stderr, "testdb.NewManager: %v (skipping tests)\n", err)
		os.Exit(0)
	}
	testManager = mgr

	ctx := context.Background()

	ddlFile, err := os.Open("testdata/setup.sql")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open testdata/setup.sql: %v\n", err)
		os.Exit(1)
	}

	db, err := mgr.Create(ctx, testdb.CreateOptions{DDL: ddlFile})
	ddlFile.Close()
	if err != nil {
		// If Postgres is not available, skip rather than fail.
		fmt.Fprintf(os.Stderr, "create ephemeral database: %v (skipping tests)\n", err)
		os.Exit(0)
	}
	ephemeralDB = db
	testConnStr = db.URL

	code := m.Run()

	// Teardown: drop ephemeral database.
	if err := mgr.Drop(ctx, ephemeralDB); err != nil {
		fmt.Fprintf(os.Stderr, "drop ephemeral database %s: %v\n", ephemeralDB.Name, err)
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

	// Expect 9 tables: events, events_2024, events_2025, identity_test, orders, orders_eu, orders_us, posts, users.
	if len(schema.Tables) != 9 {
		names := make([]string, len(schema.Tables))
		for i, t := range schema.Tables {
			names[i] = t.Name
		}
		t.Fatalf("len(Tables) = %d, want 9; got: %v", len(schema.Tables), names)
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

func TestIntrospectIdentityColumns(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	tbl := findTable(schema.Tables, "identity_test")
	if tbl == nil {
		t.Fatal("identity_test table not found")
	}

	if len(tbl.Columns) != 3 {
		t.Fatalf("identity_test columns = %d, want 3", len(tbl.Columns))
	}

	// id_always: GENERATED ALWAYS AS IDENTITY
	always := tbl.Columns[0]
	if always.Name != "id_always" {
		t.Fatalf("Columns[0].Name = %q, want %q", always.Name, "id_always")
	}
	if always.Identity != "ALWAYS" {
		t.Errorf("id_always.Identity = %q, want %q", always.Identity, "ALWAYS")
	}
	if always.DefaultExpr != "" {
		t.Errorf("id_always.DefaultExpr = %q, want empty (no stale nextval)", always.DefaultExpr)
	}
	if always.Default != nil {
		t.Errorf("id_always.Default = %v, want nil", always.Default)
	}

	// id_default: GENERATED BY DEFAULT AS IDENTITY
	byDefault := tbl.Columns[1]
	if byDefault.Name != "id_default" {
		t.Fatalf("Columns[1].Name = %q, want %q", byDefault.Name, "id_default")
	}
	if byDefault.Identity != "BY DEFAULT" {
		t.Errorf("id_default.Identity = %q, want %q", byDefault.Identity, "BY DEFAULT")
	}
	if byDefault.DefaultExpr != "" {
		t.Errorf("id_default.DefaultExpr = %q, want empty (no stale nextval)", byDefault.DefaultExpr)
	}
	if byDefault.Default != nil {
		t.Errorf("id_default.Default = %v, want nil", byDefault.Default)
	}

	// name: regular column, no identity
	nameCol := tbl.Columns[2]
	if nameCol.Name != "name" {
		t.Fatalf("Columns[2].Name = %q, want %q", nameCol.Name, "name")
	}
	if nameCol.Identity != "" {
		t.Errorf("name.Identity = %q, want empty", nameCol.Identity)
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
			where: "status = 'active'",
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

func TestResolvePartColumns(t *testing.T) {
	columns := []model.Column{
		{Name: "id"},
		{Name: "created_at"},
		{Name: "region"},
	}

	tests := []struct {
		name    string
		attNums []int32
		want    []string
	}{
		{"single column", []int32{2}, []string{"created_at"}},
		{"first column", []int32{1}, []string{"id"}},
		{"multi column", []int32{1, 2}, []string{"id", "created_at"}},
		{"expression key", []int32{0}, []string{"(expression)"}},
		{"unknown attnum", []int32{99}, []string{"attnum_99"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePartColumns(tt.attNums, columns)
			if len(got) != len(tt.want) {
				t.Fatalf("resolvePartColumns(%v) returned %d items, want %d", tt.attNums, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("resolvePartColumns(%v)[%d] = %q, want %q", tt.attNums, i, got[i], tt.want[i])
				}
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
	if len(ps.Columns) != 1 || ps.Columns[0] != "created_at" {
		t.Errorf("Columns = %v, want [created_at]", ps.Columns)
	}
	if len(ps.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2", len(ps.Children))
	}

	// Children are ordered by name.
	if ps.Children[0].Name != "events_2024" {
		t.Errorf("Children[0].Name = %q, want %q", ps.Children[0].Name, "events_2024")
	}
	if ps.Children[0].Bound == "" {
		t.Error("Children[0].Bound (bound expr) is empty")
	}
	if ps.Children[1].Name != "events_2025" {
		t.Errorf("Children[1].Name = %q, want %q", ps.Children[1].Name, "events_2025")
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
	if len(ps.Columns) != 1 || ps.Columns[0] != "region" {
		t.Errorf("Columns = %v, want [region]", ps.Columns)
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

func TestParseReloptions(t *testing.T) {
	tests := []struct {
		input []string
		want  map[string]string
	}{
		{nil, nil},
		{[]string{"m=16", "ef_construction=200"}, map[string]string{"m": "16", "ef_construction": "200"}},
		{[]string{"fillfactor=90"}, map[string]string{"fillfactor": "90"}},
	}
	for _, tt := range tests {
		got := parseReloptions(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseReloptions(%v) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("parseReloptions(%v)[%q] = %q, want %q", tt.input, k, got[k], v)
			}
		}
	}
}

func TestParseSimpleDefault(t *testing.T) {
	tests := []struct {
		input    string
		wantVal  string
		wantSimp bool
	}{
		// Bare integers.
		{"0", "0", true},
		{"42", "42", true},
		{"-1", "-1", true},
		// Bare booleans.
		{"true", "true", true},
		{"false", "false", true},
		// Single-quoted strings.
		{"'hello'", "hello", true},
		// Quoted with type cast.
		{"'created'::status_enum", "created", true},
		{"'{}'::jsonb", "{}", true},
		// Schema-qualified cast.
		{"'active'::pgdesign_test.status", "active", true},
		// Escaped quotes inside string.
		{"'it''s'", "it's", true},
		// Function calls are complex.
		{"now()", "", false},
		{"gen_random_uuid()", "", false},
		// Expression with operator is complex.
		{"'hello'::text || ' world'", "", false},
		// NULL is not a default.
		{"NULL", "", false},
		// Nextval (sequence default) is complex.
		{"nextval('users_id_seq'::regclass)", "", false},
		// Double cast: strip all casts after closing quote.
		{"'5'::integer::text", "5", true},
		// COLLATE clause after quoted literal.
		{"'hello' COLLATE \"C\"", "hello", true},
		// Cast + COLLATE combined.
		{"'hello'::text COLLATE \"C\"", "hello", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			val, ok := parseSimpleDefault(tt.input)
			if ok != tt.wantSimp {
				t.Errorf("parseSimpleDefault(%q) simple = %v, want %v", tt.input, ok, tt.wantSimp)
			}
			if val != tt.wantVal {
				t.Errorf("parseSimpleDefault(%q) value = %q, want %q", tt.input, val, tt.wantVal)
			}
		})
	}
}

func TestIntrospectViewDependsOn(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	// active_users depends on users only.
	var activeUsers *model.View
	for i := range schema.Views {
		if schema.Views[i].Name == "active_users" {
			activeUsers = &schema.Views[i]
			break
		}
	}
	if activeUsers == nil {
		t.Fatal("active_users view not found")
	}
	if len(activeUsers.DependsOn) != 1 || activeUsers.DependsOn[0] != "users" {
		t.Errorf("active_users.DependsOn = %v, want [users]", activeUsers.DependsOn)
	}

	// user_posts depends on both users and posts.
	var userPosts *model.View
	for i := range schema.Views {
		if schema.Views[i].Name == "user_posts" {
			userPosts = &schema.Views[i]
			break
		}
	}
	if userPosts == nil {
		t.Fatal("user_posts view not found")
	}
	if len(userPosts.DependsOn) != 2 {
		t.Fatalf("user_posts.DependsOn = %v, want 2 entries", userPosts.DependsOn)
	}
	// pg_depend returns sorted by relname.
	depSet := map[string]bool{}
	for _, d := range userPosts.DependsOn {
		depSet[d] = true
	}
	if !depSet["users"] || !depSet["posts"] {
		t.Errorf("user_posts.DependsOn = %v, want to contain users and posts", userPosts.DependsOn)
	}
}

func TestIntrospectMaterializedViewDependsOn(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	if len(schema.MaterializedViews) < 1 {
		t.Fatal("no materialized views found")
	}

	var recentPosts *model.MaterializedView
	for i := range schema.MaterializedViews {
		if schema.MaterializedViews[i].Name == "recent_posts" {
			recentPosts = &schema.MaterializedViews[i]
			break
		}
	}
	if recentPosts == nil {
		t.Fatal("recent_posts materialized view not found")
	}
	if len(recentPosts.DependsOn) != 1 || recentPosts.DependsOn[0] != "posts" {
		t.Errorf("recent_posts.DependsOn = %v, want [posts]", recentPosts.DependsOn)
	}
}

func TestIntrospectViewDependsOn_CTE(t *testing.T) {
	ctx := context.Background()
	conn, err := ephemeralDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Create tables and a CTE-based view.
	// Use cte_ prefix to avoid collision with the shared orders table.
	setup := `
		CREATE TABLE cte_orders (
			id SERIAL PRIMARY KEY,
			customer_name TEXT NOT NULL
		);
		CREATE TABLE cte_order_items (
			id SERIAL PRIMARY KEY,
			order_id INT NOT NULL REFERENCES cte_orders(id),
			quantity INT NOT NULL
		);
		CREATE VIEW order_summary AS
		WITH item_totals AS (
			SELECT order_id, SUM(quantity) AS total_items
			FROM cte_order_items
			GROUP BY order_id
		)
		SELECT o.id, o.customer_name, it.total_items
		FROM cte_orders o
		JOIN item_totals it ON it.order_id = o.id;
	`
	_, err = conn.Exec(ctx, setup)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer func() {
		conn.Exec(ctx, "DROP VIEW IF EXISTS order_summary")
		conn.Exec(ctx, "DROP TABLE IF EXISTS cte_order_items")
		conn.Exec(ctx, "DROP TABLE IF EXISTS cte_orders")
	}()

	schema, _, err := Introspect(ctx, testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	var orderSummary *model.View
	for i := range schema.Views {
		if schema.Views[i].Name == "order_summary" {
			orderSummary = &schema.Views[i]
			break
		}
	}
	if orderSummary == nil {
		t.Fatal("order_summary view not found")
	}

	depSet := map[string]bool{}
	for _, d := range orderSummary.DependsOn {
		depSet[d] = true
	}
	if !depSet["cte_orders"] || !depSet["cte_order_items"] {
		t.Errorf("order_summary.DependsOn = %v, want to contain cte_orders and cte_order_items", orderSummary.DependsOn)
	}
}

func TestIntrospectViewDependsOn_Subquery(t *testing.T) {
	ctx := context.Background()
	conn, err := ephemeralDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Create tables and a subquery-based view.
	// Use subq_ prefix to avoid collision with the shared users table.
	setup := `
		CREATE TABLE subq_users (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL
		);
		CREATE TABLE subq_logins (
			id SERIAL PRIMARY KEY,
			user_id INT NOT NULL REFERENCES subq_users(id),
			logged_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE VIEW user_activity AS
		SELECT u.id, u.name,
			(SELECT COUNT(*) FROM subq_logins l WHERE l.user_id = u.id) AS login_count
		FROM subq_users u;
	`
	_, err = conn.Exec(ctx, setup)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer func() {
		conn.Exec(ctx, "DROP VIEW IF EXISTS user_activity")
		conn.Exec(ctx, "DROP TABLE IF EXISTS subq_logins")
		conn.Exec(ctx, "DROP TABLE IF EXISTS subq_users")
	}()

	schema, _, err := Introspect(ctx, testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	var userActivity *model.View
	for i := range schema.Views {
		if schema.Views[i].Name == "user_activity" {
			userActivity = &schema.Views[i]
			break
		}
	}
	if userActivity == nil {
		t.Fatal("user_activity view not found")
	}

	depSet := map[string]bool{}
	for _, d := range userActivity.DependsOn {
		depSet[d] = true
	}
	if !depSet["subq_users"] || !depSet["subq_logins"] {
		t.Errorf("user_activity.DependsOn = %v, want to contain subq_users and subq_logins", userActivity.DependsOn)
	}
}

func TestParseExclusionDef(t *testing.T) {
	tests := []struct {
		name          string
		defInput      string
		wantMethod    string
		wantElems     []model.ExclusionElement
		wantWhere     string
		wantDefer     bool
		wantInitDefer bool
	}{
		{
			name:       "basic",
			defInput:   "EXCLUDE USING gist (room_id WITH =, during WITH &&)",
			wantMethod: "gist",
			wantElems: []model.ExclusionElement{
				{Column: "room_id", Operator: "="},
				{Column: "during", Operator: "&&"},
			},
		},
		{
			name:       "with where",
			defInput:   "EXCLUDE USING gist (room_id WITH =, during WITH &&) WHERE (active = true)",
			wantMethod: "gist",
			wantElems: []model.ExclusionElement{
				{Column: "room_id", Operator: "="},
				{Column: "during", Operator: "&&"},
			},
			wantWhere: "active = true",
		},
		{
			name:       "deferrable",
			defInput:   "EXCLUDE USING gist (room_id WITH =) DEFERRABLE INITIALLY DEFERRED",
			wantMethod: "gist",
			wantElems: []model.ExclusionElement{
				{Column: "room_id", Operator: "="},
			},
			wantDefer:     true,
			wantInitDefer: true,
		},
		{
			name:       "spgist method",
			defInput:   "EXCLUDE USING spgist (ip_range WITH &&)",
			wantMethod: "spgist",
			wantElems: []model.ExclusionElement{
				{Column: "ip_range", Operator: "&&"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exc := parseExclusionDef("test_constraint", tt.defInput)
			if exc.Name != "test_constraint" {
				t.Errorf("Name = %q, want %q", exc.Name, "test_constraint")
			}
			if exc.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", exc.Method, tt.wantMethod)
			}
			if len(exc.Elements) != len(tt.wantElems) {
				t.Fatalf("Elements count = %d, want %d", len(exc.Elements), len(tt.wantElems))
			}
			for i, want := range tt.wantElems {
				got := exc.Elements[i]
				if got.Column != want.Column || got.Operator != want.Operator {
					t.Errorf("Element[%d] = {%q, %q}, want {%q, %q}", i, got.Column, got.Operator, want.Column, want.Operator)
				}
			}
			if exc.Where != tt.wantWhere {
				t.Errorf("Where = %q, want %q", exc.Where, tt.wantWhere)
			}
			if exc.Deferrable != tt.wantDefer {
				t.Errorf("Deferrable = %v, want %v", exc.Deferrable, tt.wantDefer)
			}
			if exc.InitiallyDeferred != tt.wantInitDefer {
				t.Errorf("InitiallyDeferred = %v, want %v", exc.InitiallyDeferred, tt.wantInitDefer)
			}
		})
	}
}

func TestExportDomains(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
		Domains: []model.Domain{
			{
				Name:     "slug",
				Schema:   "test",
				BaseType: "text",
				NotNull:  true,
				Check:    "VALUE ~ '^[a-z0-9-]+$'",
				Comment:  "URL-safe identifier",
			},
			{
				Name:     "counter",
				Schema:   "test",
				BaseType: "bigint",
				Default:  "0",
			},
		},
	}
	data, err := Export(schema)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	out := string(data)

	// Check slug domain
	if !strings.Contains(out, "[types.slug]") {
		t.Errorf("expected [types.slug] in output, got:\n%s", out)
	}
	if !strings.Contains(out, `kind = "scalar"`) {
		t.Errorf("expected kind = scalar in output, got:\n%s", out)
	}
	if !strings.Contains(out, `base_type = "text"`) {
		t.Errorf("expected base_type = text in output, got:\n%s", out)
	}
	if !strings.Contains(out, "not_null = true") {
		t.Errorf("expected not_null = true in output, got:\n%s", out)
	}
	if !strings.Contains(out, `check = "VALUE ~ '^[a-z0-9-]+$'"`) {
		t.Errorf("expected check in output, got:\n%s", out)
	}
	if !strings.Contains(out, `comment = "URL-safe identifier"`) {
		t.Errorf("expected comment in output, got:\n%s", out)
	}

	// Check counter domain
	if !strings.Contains(out, "[types.counter]") {
		t.Errorf("expected [types.counter] in output, got:\n%s", out)
	}
	if !strings.Contains(out, `default = "0"`) {
		t.Errorf("expected default = 0 in output, got:\n%s", out)
	}
}

func TestIntrospectRLSFlags(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	if !users.EnableRLS {
		t.Error("users.EnableRLS = false, want true")
	}
	if !users.ForceRLS {
		t.Error("users.ForceRLS = false, want true")
	}

	// posts should NOT have RLS enabled.
	posts := findTable(schema.Tables, "posts")
	if posts == nil {
		t.Fatal("posts table not found")
	}
	if posts.EnableRLS {
		t.Error("posts.EnableRLS = true, want false")
	}
	if posts.ForceRLS {
		t.Error("posts.ForceRLS = true, want false")
	}
}

func TestIntrospectPolicies(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	if len(users.Policies) != 3 {
		names := make([]string, len(users.Policies))
		for i, p := range users.Policies {
			names[i] = p.Name
		}
		t.Fatalf("len(users.Policies) = %d, want 3; got: %v", len(users.Policies), names)
	}

	// Policies are ordered by name: users_insert_policy, users_restrictive, users_select_policy
	insert := users.Policies[0]
	if insert.Name != "users_insert_policy" {
		t.Errorf("Policies[0].Name = %q, want %q", insert.Name, "users_insert_policy")
	}
	if insert.Operation != "INSERT" {
		t.Errorf("insert.Operation = %q, want %q", insert.Operation, "INSERT")
	}
	if insert.Using != "" {
		t.Errorf("insert.Using = %q, want empty", insert.Using)
	}
	if insert.WithCheck != "(length(name) > 0)" && insert.WithCheck != "length(name) > 0" {
		// PG may or may not add outer parens
		t.Errorf("insert.WithCheck = %q, want contains 'length(name) > 0'", insert.WithCheck)
	}
	if insert.Type != "" {
		t.Errorf("insert.Type = %q, want empty (PERMISSIVE default)", insert.Type)
	}
	if insert.Role != "" {
		t.Errorf("insert.Role = %q, want empty (PUBLIC)", insert.Role)
	}

	restrictive := users.Policies[1]
	if restrictive.Name != "users_restrictive" {
		t.Errorf("Policies[1].Name = %q, want %q", restrictive.Name, "users_restrictive")
	}
	if restrictive.Type != "RESTRICTIVE" {
		t.Errorf("restrictive.Type = %q, want %q", restrictive.Type, "RESTRICTIVE")
	}
	if restrictive.Operation != "UPDATE" {
		t.Errorf("restrictive.Operation = %q, want %q", restrictive.Operation, "UPDATE")
	}
	if restrictive.Using == "" {
		t.Error("restrictive.Using is empty, want non-empty")
	}
	if restrictive.WithCheck == "" {
		t.Error("restrictive.WithCheck is empty, want non-empty")
	}

	selectPol := users.Policies[2]
	if selectPol.Name != "users_select_policy" {
		t.Errorf("Policies[2].Name = %q, want %q", selectPol.Name, "users_select_policy")
	}
	if selectPol.Operation != "SELECT" {
		t.Errorf("select.Operation = %q, want %q", selectPol.Operation, "SELECT")
	}

	// posts should have no policies.
	posts := findTable(schema.Tables, "posts")
	if posts == nil {
		t.Fatal("posts table not found")
	}
	if len(posts.Policies) != 0 {
		t.Errorf("posts.Policies = %d, want 0", len(posts.Policies))
	}
}

func TestDecodeTriggerTiming(t *testing.T) {
	tests := []struct {
		tgtype int16
		want   string
	}{
		{0x02, "BEFORE"},
		{0x00, "AFTER"},
		{0x40, "INSTEAD OF"},
		{0x06, "BEFORE"},
		{0x44, "INSTEAD OF"},
	}
	for _, tt := range tests {
		got := decodeTriggerTiming(tt.tgtype)
		if got != tt.want {
			t.Errorf("decodeTriggerTiming(%#x) = %q, want %q", tt.tgtype, got, tt.want)
		}
	}
}

func TestDecodeTriggerEvents(t *testing.T) {
	tests := []struct {
		tgtype int16
		want   []string
	}{
		{0x04, []string{"INSERT"}},
		{0x08, []string{"DELETE"}},
		{0x10, []string{"UPDATE"}},
		{0x20, []string{"TRUNCATE"}},
		{0x0C, []string{"INSERT", "DELETE"}},
		{0x14, []string{"INSERT", "UPDATE"}},
		{0x1C, []string{"INSERT", "DELETE", "UPDATE"}},
	}
	for _, tt := range tests {
		got := decodeTriggerEvents(tt.tgtype)
		if len(got) != len(tt.want) {
			t.Errorf("decodeTriggerEvents(%#x) = %v, want %v", tt.tgtype, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("decodeTriggerEvents(%#x)[%d] = %q, want %q", tt.tgtype, i, got[i], tt.want[i])
			}
		}
	}
}

func TestDecodeTriggerForEach(t *testing.T) {
	tests := []struct {
		tgtype int16
		want   string
	}{
		{0x01, "ROW"},
		{0x00, "STATEMENT"},
		{0x05, "ROW"},
		{0x04, "STATEMENT"},
	}
	for _, tt := range tests {
		got := decodeTriggerForEach(tt.tgtype)
		if got != tt.want {
			t.Errorf("decodeTriggerForEach(%#x) = %q, want %q", tt.tgtype, got, tt.want)
		}
	}
}

func TestExtractTriggerWhen(t *testing.T) {
	tests := []struct {
		triggerdef string
		want       string
	}{
		{
			triggerdef: `CREATE TRIGGER audit_insert AFTER INSERT ON public.orders FOR EACH ROW WHEN (NEW.status = 'active') EXECUTE FUNCTION audit_fn()`,
			want:       "NEW.status = 'active'",
		},
		{
			triggerdef: `CREATE TRIGGER simple AFTER INSERT ON public.orders FOR EACH ROW EXECUTE FUNCTION audit_fn()`,
			want:       "",
		},
		{
			triggerdef: `CREATE TRIGGER nested AFTER INSERT ON public.orders FOR EACH ROW WHEN ((NEW.x > 0) AND (NEW.y < 10)) EXECUTE FUNCTION check_fn()`,
			want:       "(NEW.x > 0) AND (NEW.y < 10)",
		},
	}
	for _, tt := range tests {
		got := extractTriggerWhen(tt.triggerdef)
		if got != tt.want {
			t.Errorf("extractTriggerWhen(%q) = %q, want %q", tt.triggerdef, got, tt.want)
		}
	}
}

func TestMapPolCmd(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"*", "ALL"},
		{"r", "SELECT"},
		{"a", "INSERT"},
		{"w", "UPDATE"},
		{"d", "DELETE"},
		{"x", "ALL"}, // unknown defaults to ALL
	}
	for _, tt := range tests {
		got := mapPolCmd(tt.cmd)
		if got != tt.want {
			t.Errorf("mapPolCmd(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestExportRLSPolicies(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
		Tables: []model.Table{{
			Name:      "items",
			Comment:   "Test table",
			EnableRLS: true,
			ForceRLS:  true,
			Policies: []model.Policy{
				{
					Name:      "items_select",
					Type:      "RESTRICTIVE",
					Operation: "SELECT",
					Role:      "app_user",
					Using:     "owner_id = current_user_id()",
				},
				{
					Name:      "items_insert",
					Operation: "INSERT",
					WithCheck: "owner_id = current_user_id()",
				},
			},
		}},
	}

	out, err := Export(schema)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	s := string(out)

	// Check enable_rls and force_rls
	if !strings.Contains(s, "enable_rls = true") {
		t.Errorf("expected enable_rls = true in output, got:\n%s", s)
	}
	if !strings.Contains(s, "force_rls = true") {
		t.Errorf("expected force_rls = true in output, got:\n%s", s)
	}

	// Check policy table headers
	if !strings.Contains(s, "[tables.items.policies.items_select]") {
		t.Errorf("expected [tables.items.policies.items_select] in output, got:\n%s", s)
	}
	if !strings.Contains(s, "[tables.items.policies.items_insert]") {
		t.Errorf("expected [tables.items.policies.items_insert] in output, got:\n%s", s)
	}

	// Check policy fields
	if !strings.Contains(s, `type = "RESTRICTIVE"`) {
		t.Errorf("expected type = RESTRICTIVE in output, got:\n%s", s)
	}
	if !strings.Contains(s, `for = "SELECT"`) {
		t.Errorf("expected for = SELECT in output, got:\n%s", s)
	}
	if !strings.Contains(s, `to = "app_user"`) {
		t.Errorf("expected to = app_user in output, got:\n%s", s)
	}
	if !strings.Contains(s, `using = "owner_id = current_user_id()"`) {
		t.Errorf("expected using expr in output, got:\n%s", s)
	}
	if !strings.Contains(s, `with_check = "owner_id = current_user_id()"`) {
		t.Errorf("expected with_check expr in output, got:\n%s", s)
	}

	// error_code and error_message should NOT be exported
	if strings.Contains(s, "error_code") {
		t.Errorf("error_code should not be in output, got:\n%s", s)
	}
	if strings.Contains(s, "error_message") {
		t.Errorf("error_message should not be in output, got:\n%s", s)
	}

	// A permissive ALL policy should NOT emit type or for
	schema2 := &model.Schema{
		Name: "test",
		Tables: []model.Table{{
			Name:      "things",
			Comment:   "Test",
			EnableRLS: true,
			Policies: []model.Policy{{
				Name:  "things_all",
				Using: "true",
			}},
		}},
	}
	out2, err := Export(schema2)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	s2 := string(out2)
	if !strings.Contains(s2, "[tables.things.policies.things_all]") {
		t.Errorf("expected policy header in output, got:\n%s", s2)
	}
	// Should NOT contain type or for keys for default values
	// Check that the policy section doesn't contain "type" or "for" keys
	if strings.Contains(s2, `type = "PERMISSIVE"`) {
		t.Errorf("should not emit type = PERMISSIVE (it's the default), got:\n%s", s2)
	}
	if strings.Contains(s2, `for = "ALL"`) {
		t.Errorf("should not emit for = ALL (it's the default), got:\n%s", s2)
	}
}

func TestIntrospectTriggerFilter_StateMachine(t *testing.T) {
	schema, _, err := Introspect(context.Background(), testConnStr, []string{testSchema})
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}

	users := findTable(schema.Tables, "users")
	if users == nil {
		t.Fatal("users table not found")
	}

	// _pgdesign_sm_users_status trigger should be filtered out.
	for _, trig := range users.Triggers {
		if strings.HasPrefix(trig.Name, "_pgdesign_sm_") {
			t.Errorf("introspect should filter _pgdesign_sm_ triggers, found: %s", trig.Name)
		}
		if trig.Function == "pgdesign_deny_mutation" {
			t.Errorf("introspect should filter pgdesign_deny_mutation triggers, found: %s", trig.Name)
		}
	}

	// The normal user_audit trigger should still be present.
	found := false
	for _, trig := range users.Triggers {
		if trig.Name == "user_audit" {
			found = true
		}
	}
	if !found {
		names := make([]string, len(users.Triggers))
		for i, trig := range users.Triggers {
			names[i] = trig.Name
		}
		t.Errorf("expected user_audit trigger in introspected users table, got triggers: %v", names)
	}

	// Verify posts table also filters deny_mutation.
	posts := findTable(schema.Tables, "posts")
	if posts == nil {
		t.Fatal("posts table not found")
	}
	for _, trig := range posts.Triggers {
		if trig.Function == "pgdesign_deny_mutation" {
			t.Errorf("introspect should filter pgdesign_deny_mutation triggers on posts, found: %s", trig.Name)
		}
	}
}
