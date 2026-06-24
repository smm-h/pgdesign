package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func testRegistry() *semtype.Registry {
	return semtype.NewBuiltinRegistry()
}

func TestBuild_SimpleTwoTables(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{
			Schema:     "public",
			Extensions: []string{"uuid-ossp"},
		},
		Tables: []parse.RawTable{
			{
				Name:    "users",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "name", Type: "short_text"}},
			},
			{
				Name:    "posts",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}, {Name: "title", Type: "short_text"}},
				FKs: map[string]parse.RawFK{
					"fk_posts_user_id": {Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if schema.Name != "public" {
		t.Errorf("expected schema name 'public', got %q", schema.Name)
	}
	if len(schema.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(schema.Tables))
	}

	// users should come before posts (topo order).
	if schema.Tables[0].Name != "users" {
		t.Errorf("expected first table to be 'users', got %q", schema.Tables[0].Name)
	}
	if schema.Tables[1].Name != "posts" {
		t.Errorf("expected second table to be 'posts', got %q", schema.Tables[1].Name)
	}

	// posts should have FK resolved.
	posts := schema.Tables[1]
	if len(posts.FKs) != 1 {
		t.Fatalf("expected 1 FK on posts, got %d", len(posts.FKs))
	}
	if posts.FKs[0].RefTable != "users" {
		t.Errorf("expected FK ref table 'users', got %q", posts.FKs[0].RefTable)
	}
}

func TestPK_ExplicitPK(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "items",
				PK:      []string{"a", "b"},
				Columns: []parse.RawColumn{{Name: "a", Type: "ref"}, {Name: "b", Type: "ref"}, {Name: "val", Type: "short_text"}},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schema.Tables[0].PK) != 2 || schema.Tables[0].PK[0] != "a" || schema.Tables[0].PK[1] != "b" {
		t.Errorf("expected PK [a, b], got %v", schema.Tables[0].PK)
	}
}

func TestPK_AutoDetectID(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "things",
				Columns: []parse.RawColumn{{Name: "thing_id", Type: "id"}, {Name: "name", Type: "short_text"}},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schema.Tables[0].PK) != 1 || schema.Tables[0].PK[0] != "thing_id" {
		t.Errorf("expected PK [thing_id], got %v", schema.Tables[0].PK)
	}
}

func TestPK_AutoDetectAutoID(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "events",
				Columns: []parse.RawColumn{{Name: "event_id", Type: "auto_id"}, {Name: "name", Type: "short_text"}},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schema.Tables[0].PK) != 1 || schema.Tables[0].PK[0] != "event_id" {
		t.Errorf("expected PK [event_id], got %v", schema.Tables[0].PK)
	}
}

func TestPK_MissingPKError(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "nokey",
				Columns: []parse.RawColumn{{Name: "a", Type: "short_text"}, {Name: "b", Type: "short_text"}},
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error for missing PK")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E120" && d.Table == "nokey" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E120 diagnostic for table 'nokey'")
	}
}

func TestTopoSort_ThreeTableChain(t *testing.T) {
	tables := []Table{
		{Name: "c", FKs: []FK{{Columns: []string{"b_id"}, RefTable: "b"}}},
		{Name: "a"},
		{Name: "b", FKs: []FK{{Columns: []string{"a_id"}, RefTable: "a"}}},
	}

	sorted, cycles := topoSort(tables)
	if len(cycles) != 0 {
		t.Errorf("expected no cycles, got %v", cycles)
	}

	// a must come before b, b must come before c.
	order := make(map[string]int)
	for i, tbl := range sorted {
		order[tbl.Name] = i
	}
	if order["a"] > order["b"] {
		t.Errorf("a should come before b, got a=%d b=%d", order["a"], order["b"])
	}
	if order["b"] > order["c"] {
		t.Errorf("b should come before c, got b=%d c=%d", order["b"], order["c"])
	}
}

func TestTopoSort_CycleDetection(t *testing.T) {
	tables := []Table{
		{Name: "x", FKs: []FK{{Columns: []string{"y_id"}, RefTable: "y"}}},
		{Name: "y", FKs: []FK{{Columns: []string{"x_id"}, RefTable: "x"}}},
	}

	sorted, cycles := topoSort(tables)
	if len(sorted) != 2 {
		t.Errorf("expected 2 tables in output, got %d", len(sorted))
	}
	if len(cycles) == 0 {
		t.Fatal("expected at least one cycle group")
	}
	if len(cycles[0]) != 2 {
		t.Errorf("expected cycle group of size 2, got %d", len(cycles[0]))
	}
}

func TestHasIndexCovering(t *testing.T) {
	tbl := Table{
		Indexes: []Index{
			{Name: "idx_ab", Columns: []string{"a", "b"}},
		},
	}

	// Index on (a, b) covers FK on (a).
	if !tbl.HasIndexCovering([]string{"a"}) {
		t.Error("expected index (a, b) to cover FK on (a)")
	}

	// Index on (a, b) covers FK on (a, b).
	if !tbl.HasIndexCovering([]string{"a", "b"}) {
		t.Error("expected index (a, b) to cover FK on (a, b)")
	}

	// Index on (a, b) does NOT cover FK on (b) — prefix must match.
	if tbl.HasIndexCovering([]string{"b"}) {
		t.Error("expected index (a, b) NOT to cover FK on (b)")
	}

	// Index on (a, b) does NOT cover FK on (a, b, c).
	if tbl.HasIndexCovering([]string{"a", "b", "c"}) {
		t.Error("expected index (a, b) NOT to cover FK on (a, b, c)")
	}
}

func TestAutoFKIndexGeneration(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "parents",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
			},
			{
				Name:    "children",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "parent_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_children_parent_id": {Columns: []string{"parent_id"}, RefTable: "parents", RefColumns: []string{"id"}},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	children := schema.TableByName("public", "children")
	if children == nil {
		t.Fatal("children table not found")
	}

	// Should have an auto-generated index for the FK column.
	var autoIdx *Index
	for i := range children.Indexes {
		if children.Indexes[i].IsAutoFK {
			autoIdx = &children.Indexes[i]
			break
		}
	}
	if autoIdx == nil {
		t.Fatal("expected auto FK index on children.parent_id")
	}
	if len(autoIdx.Columns) != 1 || autoIdx.Columns[0] != "parent_id" {
		t.Errorf("expected auto index on [parent_id], got %v", autoIdx.Columns)
	}
}

func TestAutoFKIndex_SkippedWhenCovered(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "parents",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
			},
			{
				Name:    "children",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "parent_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_children_parent_id": {Columns: []string{"parent_id"}, RefTable: "parents", RefColumns: []string{"id"}},
				},
				Indexes: map[string]parse.RawIndex{
					"idx_children_parent_id": {Columns: []string{"parent_id", "id"}},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	children := schema.TableByName("public", "children")
	if children == nil {
		t.Fatal("children table not found")
	}

	// Should NOT have an auto-generated index since the explicit one covers it.
	for _, idx := range children.Indexes {
		if idx.IsAutoFK {
			t.Error("expected no auto FK index when explicit index covers FK columns")
		}
	}
}

func TestCandidateKeys(t *testing.T) {
	tbl := Table{
		Columns: []Column{
			{Name: "a"},
			{Name: "b"},
			{Name: "c"},
		},
		Dependencies: []fd.FuncDep{
			{Determinant: []string{"a"}, Dependent: []string{"b", "c"}},
		},
	}

	keys := tbl.CandidateKeys()
	if len(keys) == 0 {
		t.Fatal("expected at least one candidate key")
	}
	// With FD a -> b, c and columns {a, b, c}, the only candidate key is {a}.
	if len(keys) != 1 || len(keys[0]) != 1 || keys[0][0] != "a" {
		t.Errorf("expected candidate key [[a]], got %v", keys)
	}
}

func TestTableByName(t *testing.T) {
	schema := &Schema{
		Tables: []Table{
			{Name: "foo", Schema: "public"},
			{Name: "bar", Schema: "other"},
		},
	}

	tbl := schema.TableByName("public", "foo")
	if tbl == nil || tbl.Name != "foo" {
		t.Error("expected to find table 'foo' in schema 'public'")
	}

	tbl = schema.TableByName("other", "bar")
	if tbl == nil || tbl.Name != "bar" {
		t.Error("expected to find table 'bar' in schema 'other'")
	}

	tbl = schema.TableByName("public", "nonexistent")
	if tbl != nil {
		t.Error("expected nil for nonexistent table")
	}
}

func TestBuildMulti_CrossSchemaFK(t *testing.T) {
	reg := testRegistry()

	authRaw := &parse.RawSchema{
		Meta: parse.RawMeta{
			Version: 1,
			Schema:  "auth",
		},
		Tables: []parse.RawTable{
			{
				Name:    "users",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "email", Type: "short_text"}},
			},
		},
	}

	gameRaw := &parse.RawSchema{
		Meta: parse.RawMeta{
			Version: 1,
			Schema:  "game",
		},
		Tables: []parse.RawTable{
			{
				Name:    "players",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "auth_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_players_auth": {
						Columns:    []string{"auth_id"},
						RefTable:   "auth.users",
						RefColumns: []string{"id"},
						OnDelete:   "SET NULL",
					},
				},
			},
		},
	}

	schema, diags := BuildMulti([]*parse.RawSchema{authRaw, gameRaw}, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Should have 2 tables.
	if len(schema.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(schema.Tables))
	}

	// auth.users should come before game.players (topo order).
	var usersIdx, playersIdx int
	for i, tbl := range schema.Tables {
		if tbl.Name == "users" && tbl.Schema == "auth" {
			usersIdx = i
		}
		if tbl.Name == "players" && tbl.Schema == "game" {
			playersIdx = i
		}
	}
	if usersIdx >= playersIdx {
		t.Errorf("auth.users (idx %d) should come before game.players (idx %d)", usersIdx, playersIdx)
	}

	// Players should have FK pointing to auth.users.
	players := schema.TableByName("game", "players")
	if players == nil {
		t.Fatal("players table not found")
	}
	if len(players.FKs) != 1 {
		t.Fatalf("expected 1 FK on players, got %d", len(players.FKs))
	}
	fk := players.FKs[0]
	if fk.RefSchema != "auth" {
		t.Errorf("FK ref schema = %q, want %q", fk.RefSchema, "auth")
	}
	if fk.RefTable != "users" {
		t.Errorf("FK ref table = %q, want %q", fk.RefTable, "users")
	}

	// TableByName should find auth.users.
	authUsers := schema.TableByName("auth", "users")
	if authUsers == nil {
		t.Error("expected to find auth.users in combined schema")
	}
}

func TestBuildMulti_MergesExtensions(t *testing.T) {
	reg := testRegistry()

	raw1 := &parse.RawSchema{
		Meta: parse.RawMeta{
			Version:    1,
			Schema:     "s1",
			Extensions: []string{"pgcrypto", "uuid-ossp"},
		},
		Tables: []parse.RawTable{
			{
				Name:    "t1",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
			},
		},
	}
	raw2 := &parse.RawSchema{
		Meta: parse.RawMeta{
			Version:    1,
			Schema:     "s2",
			Extensions: []string{"pgcrypto", "pg_trgm"},
		},
		Tables: []parse.RawTable{
			{
				Name:    "t2",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
			},
		},
	}

	schema, diags := BuildMulti([]*parse.RawSchema{raw1, raw2}, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Extensions should be deduplicated.
	expected := map[string]bool{"pgcrypto": true, "uuid-ossp": true, "pg_trgm": true}
	if len(schema.Extensions) != 3 {
		t.Fatalf("expected 3 extensions, got %d: %v", len(schema.Extensions), schema.Extensions)
	}
	for _, ext := range schema.Extensions {
		if !expected[ext] {
			t.Errorf("unexpected extension: %q", ext)
		}
	}
}

func TestBuildMulti_MergesEnums(t *testing.T) {
	reg := testRegistry()

	// Register enum types in the registry.
	for _, name := range []string{"role", "status"} {
		err := reg.Register(&semtype.TypeDef{
			Name:       name,
			Kind:       semtype.KindEnum,
			BaseType:   typeinfo.T(name),
			NotNull:    true,
			EnumValues: []string{"a", "b"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	raw1 := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "s1"},
		Types: []parse.RawType{
			{Name: "role", Kind: "enum", Values: []string{"admin", "user"}},
		},
		Tables: []parse.RawTable{
			{
				Name:    "t1",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "role", Type: "role"}},
			},
		},
	}
	raw2 := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "s2"},
		Types: []parse.RawType{
			{Name: "status", Kind: "enum", Values: []string{"active", "inactive"}},
		},
		Tables: []parse.RawTable{
			{
				Name:    "t2",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "status", Type: "status"}},
			},
		},
	}

	schema, diags := BuildMulti([]*parse.RawSchema{raw1, raw2}, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schema.Enums) != 2 {
		t.Fatalf("expected 2 enums, got %d", len(schema.Enums))
	}
}

func TestBuildMulti_SingleSchema(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "users",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
			},
		},
	}

	schema, diags := BuildMulti([]*parse.RawSchema{raw}, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if schema.Name != "public" {
		t.Errorf("expected single-schema name %q, got %q", "public", schema.Name)
	}
	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
}

func TestEnumResolution(t *testing.T) {
	reg := testRegistry()
	// Register an enum type.
	err := reg.Register(&semtype.TypeDef{
		Name:       "status",
		Kind:       semtype.KindEnum,
		BaseType:   typeinfo.T("status"),
		NotNull:    true,
		EnumValues: []string{"active", "inactive"},
	})
	if err != nil {
		t.Fatal(err)
	}

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "status", Kind: "enum", Values: []string{"active", "inactive"}},
		},
		Tables: []parse.RawTable{
			{
				Name:    "accounts",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "status", Type: "status"}},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Enums) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(schema.Enums))
	}
	if schema.Enums[0].Name != "status" {
		t.Errorf("expected enum name 'status', got %q", schema.Enums[0].Name)
	}
	if len(schema.Enums[0].Values) != 2 {
		t.Errorf("expected 2 enum values, got %d", len(schema.Enums[0].Values))
	}
}

func TestBuild_PoliciesResolved(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "messages",
				PK:      []string{"id"},
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "channel_id", Type: "ref"}, {Name: "body", Type: "short_text"}},
				Policies: map[string]parse.RawPolicy{
					"select_own": {
						For:   "SELECT",
						To:    "game_app",
						Using: "channel_id = current_setting('app.channel_id')::uuid",
					},
					"insert_own": {
						For:          "INSERT",
						To:           "game_app",
						WithCheck:    "channel_id = current_setting('app.channel_id')::uuid",
						ErrorCode:    "chat_disabled",
						ErrorMessage: "Cannot send messages here",
					},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	msgs := schema.TableByName("public", "messages")
	if msgs == nil {
		t.Fatal("messages table not found")
	}
	if !msgs.EnableRLS {
		t.Error("expected EnableRLS = true when policies exist")
	}
	if len(msgs.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(msgs.Policies))
	}

	// Find each policy by name.
	policyMap := make(map[string]Policy)
	for _, p := range msgs.Policies {
		policyMap[p.Name] = p
	}

	sel, ok := policyMap["select_own"]
	if !ok {
		t.Fatal("missing policy 'select_own'")
	}
	if sel.Operation != "SELECT" {
		t.Errorf("select_own.Operation = %q, want %q", sel.Operation, "SELECT")
	}
	if sel.Role != "game_app" {
		t.Errorf("select_own.Role = %q, want %q", sel.Role, "game_app")
	}
	if sel.Using == "" {
		t.Error("select_own.Using should not be empty")
	}

	ins, ok := policyMap["insert_own"]
	if !ok {
		t.Fatal("missing policy 'insert_own'")
	}
	if ins.Operation != "INSERT" {
		t.Errorf("insert_own.Operation = %q, want %q", ins.Operation, "INSERT")
	}
	if ins.WithCheck == "" {
		t.Error("insert_own.WithCheck should not be empty")
	}
	if ins.ErrorCode != "chat_disabled" {
		t.Errorf("insert_own.ErrorCode = %q, want %q", ins.ErrorCode, "chat_disabled")
	}
	if ins.ErrorMessage != "Cannot send messages here" {
		t.Errorf("insert_own.ErrorMessage = %q, want %q", ins.ErrorMessage, "Cannot send messages here")
	}
}

func TestBuild_PolicyInvalidOperation(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "items",
				PK:      []string{"id"},
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
				Policies: map[string]parse.RawPolicy{
					"bad_policy": {
						For:   "TRUNCATE",
						Using: "true",
					},
				},
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error for invalid policy operation")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E122" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E122 diagnostic for invalid policy operation")
	}
}

func TestBuild_PolicyMissingExpr(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "items",
				PK:      []string{"id"},
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
				Policies: map[string]parse.RawPolicy{
					"empty_policy": {
						For: "SELECT",
						// No using or with_check
					},
				},
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error for policy missing both using and with_check")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E123" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E123 diagnostic for policy missing expressions")
	}
}

func TestResolveIndex_PlainColumns(t *testing.T) {
	// Plain column names without direction: should produce nil Desc (all ASC).
	raw := parse.RawIndex{
		Name:    "idx_test",
		Columns: []string{"a", "b"},
	}
	idx := resolveIndex("idx_test", raw)
	if len(idx.Columns) != 2 || idx.Columns[0] != "a" || idx.Columns[1] != "b" {
		t.Errorf("expected columns [a, b], got %v", idx.Columns)
	}
	if idx.Desc != nil {
		t.Errorf("expected nil Desc for all-ASC columns, got %v", idx.Desc)
	}
}

func TestResolveIndex_DESCColumn(t *testing.T) {
	// "b DESC" should produce Desc[1]=true.
	raw := parse.RawIndex{
		Name:    "idx_test",
		Columns: []string{"a", "b DESC"},
	}
	idx := resolveIndex("idx_test", raw)
	if len(idx.Columns) != 2 || idx.Columns[0] != "a" || idx.Columns[1] != "b" {
		t.Errorf("expected columns [a, b], got %v", idx.Columns)
	}
	if len(idx.Desc) != 2 || idx.Desc[0] != false || idx.Desc[1] != true {
		t.Errorf("expected Desc [false, true], got %v", idx.Desc)
	}
}

func TestResolveIndex_ExplicitASC(t *testing.T) {
	// "a ASC" should strip the ASC suffix and leave Desc as nil (all ASC).
	raw := parse.RawIndex{
		Name:    "idx_test",
		Columns: []string{"a ASC", "b ASC"},
	}
	idx := resolveIndex("idx_test", raw)
	if len(idx.Columns) != 2 || idx.Columns[0] != "a" || idx.Columns[1] != "b" {
		t.Errorf("expected columns [a, b], got %v", idx.Columns)
	}
	if idx.Desc != nil {
		t.Errorf("expected nil Desc for all-ASC, got %v", idx.Desc)
	}
}

func TestResolveIndex_MixedDirections(t *testing.T) {
	raw := parse.RawIndex{
		Name:    "idx_test",
		Columns: []string{"a DESC", "b", "c DESC"},
	}
	idx := resolveIndex("idx_test", raw)
	if len(idx.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(idx.Columns))
	}
	if idx.Columns[0] != "a" || idx.Columns[1] != "b" || idx.Columns[2] != "c" {
		t.Errorf("expected columns [a, b, c], got %v", idx.Columns)
	}
	if len(idx.Desc) != 3 || idx.Desc[0] != true || idx.Desc[1] != false || idx.Desc[2] != true {
		t.Errorf("expected Desc [true, false, true], got %v", idx.Desc)
	}
}

func TestResolveIndex_CaseInsensitiveDirection(t *testing.T) {
	raw := parse.RawIndex{
		Name:    "idx_test",
		Columns: []string{"a desc", "b Asc"},
	}
	idx := resolveIndex("idx_test", raw)
	if idx.Columns[0] != "a" || idx.Columns[1] != "b" {
		t.Errorf("expected columns [a, b], got %v", idx.Columns)
	}
	if len(idx.Desc) != 2 || idx.Desc[0] != true || idx.Desc[1] != false {
		t.Errorf("expected Desc [true, false], got %v", idx.Desc)
	}
}

func TestBuild_IndexDESCEndToEnd(t *testing.T) {
	reg := testRegistry()
	trueVal := true
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "messages",
				PK:      []string{"id"},
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "channel", Type: "short_text"},
					{Name: "sent_at", Type: "timestamp"},
				},
				Indexes: map[string]parse.RawIndex{
					"idx_messages_channel_sent": {
						Columns: []string{"channel", "sent_at DESC"},
						Unique:  &trueVal,
					},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	msgs := schema.TableByName("public", "messages")
	if msgs == nil {
		t.Fatal("messages table not found")
	}

	var found bool
	for _, idx := range msgs.Indexes {
		if idx.Name == "idx_messages_channel_sent" {
			found = true
			if len(idx.Columns) != 2 || idx.Columns[0] != "channel" || idx.Columns[1] != "sent_at" {
				t.Errorf("expected columns [channel, sent_at], got %v", idx.Columns)
			}
			if len(idx.Desc) != 2 || idx.Desc[0] != false || idx.Desc[1] != true {
				t.Errorf("expected Desc [false, true], got %v", idx.Desc)
			}
			if !idx.Unique {
				t.Error("expected index to be unique")
			}
			break
		}
	}
	if !found {
		t.Error("idx_messages_channel_sent not found in indexes")
	}
}

func TestBuild_FDUnknownColumn_E221(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "orders",
				PK:   []string{"id"},
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "amount", Type: "money"},
					{Name: "status", Type: "short_text"},
				},
				Dependencies: []parse.RawDependency{
					{Determinant: []string{"order_num"}, Dependent: []string{"amount"}},
				},
			},
		},
	}

	_, diags := Build(raw, reg)
	found := false
	for _, d := range diags {
		if d.Code == "E221" && d.Table == "orders" && d.Column == "order_num" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E221 diagnostic for unknown FD column 'order_num'")
	}
}

func TestBuild_FDValidColumns_NoE221(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "orders",
				PK:   []string{"id"},
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "amount", Type: "money"},
					{Name: "status", Type: "short_text"},
				},
				Dependencies: []parse.RawDependency{
					{Determinant: []string{"id"}, Dependent: []string{"amount"}},
				},
			},
		},
	}

	_, diags := Build(raw, reg)
	for _, d := range diags {
		if d.Code == "E221" {
			t.Errorf("unexpected E221 diagnostic: table=%s column=%s message=%s", d.Table, d.Column, d.Message)
		}
	}
}

func TestBuild_PolicyType(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "docs",
				PK:      []string{"id"},
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
				Policies: map[string]parse.RawPolicy{
					"permissive_explicit": {
						For:   "SELECT",
						Type:  "permissive",
						Using: "true",
					},
					"restrictive_explicit": {
						For:   "SELECT",
						Type:  "RESTRICTIVE",
						Using: "true",
					},
					"default_type": {
						For:   "SELECT",
						Using: "true",
					},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	docs := schema.TableByName("public", "docs")
	if docs == nil {
		t.Fatal("docs table not found")
	}

	policyMap := make(map[string]Policy)
	for _, p := range docs.Policies {
		policyMap[p.Name] = p
	}

	// Explicit permissive should be uppercased.
	if p, ok := policyMap["permissive_explicit"]; !ok {
		t.Fatal("missing policy 'permissive_explicit'")
	} else if p.Type != "PERMISSIVE" {
		t.Errorf("permissive_explicit.Type = %q, want %q", p.Type, "PERMISSIVE")
	}

	// Explicit restrictive should be uppercased.
	if p, ok := policyMap["restrictive_explicit"]; !ok {
		t.Fatal("missing policy 'restrictive_explicit'")
	} else if p.Type != "RESTRICTIVE" {
		t.Errorf("restrictive_explicit.Type = %q, want %q", p.Type, "RESTRICTIVE")
	}

	// Empty type should default to PERMISSIVE.
	if p, ok := policyMap["default_type"]; !ok {
		t.Fatal("missing policy 'default_type'")
	} else if p.Type != "PERMISSIVE" {
		t.Errorf("default_type.Type = %q, want %q", p.Type, "PERMISSIVE")
	}
}

func TestBuild_PolicyInvalidType(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "items",
				PK:      []string{"id"},
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}},
				Policies: map[string]parse.RawPolicy{
					"bad_type": {
						For:   "SELECT",
						Type:  "exclusive",
						Using: "true",
					},
				},
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error for invalid policy type")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E124" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E124 diagnostic for invalid policy type")
	}
}

func TestBuild_ForceRLSImpliesEnableRLS(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:     "secrets",
				PK:       []string{"id"},
				Columns:  []parse.RawColumn{{Name: "id", Type: "id"}},
				ForceRLS: true,
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	secrets := schema.TableByName("public", "secrets")
	if secrets == nil {
		t.Fatal("secrets table not found")
	}
	if !secrets.ForceRLS {
		t.Error("expected ForceRLS = true")
	}
	if !secrets.EnableRLS {
		t.Error("expected EnableRLS = true when ForceRLS is set")
	}
}

func TestBuild_GroupsValid(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{Name: "users", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
			{Name: "orders", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
			{Name: "products", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
		},
		Groups: map[string][]string{
			"core":    {"users", "orders"},
			"catalog": {"products"},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if schema.Groups == nil {
		t.Fatal("expected groups on schema, got nil")
	}
	if len(schema.Groups["core"]) != 2 {
		t.Errorf("core group len = %d, want 2", len(schema.Groups["core"]))
	}
	if len(schema.Groups["catalog"]) != 1 {
		t.Errorf("catalog group len = %d, want 1", len(schema.Groups["catalog"]))
	}
}

func TestBuild_GroupsUnknownTable(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{Name: "users", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
		},
		Groups: map[string][]string{
			"core": {"users", "nonexistent"},
		},
	}

	_, diags := Build(raw, reg)
	found := false
	for _, d := range diags {
		if d.Code == "E227" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected E227 for unknown table in group, got: %v", diags)
	}
}

func TestFilterByGroups(t *testing.T) {
	schema := &Schema{
		Tables: []Table{
			{Name: "users", Schema: "public"},
			{Name: "orders", Schema: "public"},
			{Name: "products", Schema: "public"},
		},
		Groups: map[string][]string{
			"core":    {"users", "orders"},
			"catalog": {"products"},
		},
	}
	schema.buildTablesByName()

	filtered := schema.FilterByGroups([]string{"core"})
	if len(filtered.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(filtered.Tables))
	}
	names := make(map[string]bool)
	for _, tbl := range filtered.Tables {
		names[tbl.Name] = true
	}
	if !names["users"] || !names["orders"] {
		t.Errorf("expected users and orders, got %v", names)
	}
	if names["products"] {
		t.Errorf("products should not be in filtered result")
	}
}

func TestFilterByGroupsEmpty(t *testing.T) {
	schema := &Schema{
		Tables: []Table{
			{Name: "users", Schema: "public"},
		},
	}
	// Empty group list returns original schema.
	result := schema.FilterByGroups(nil)
	if result != schema {
		t.Error("expected same schema pointer when groupNames is empty")
	}
}
