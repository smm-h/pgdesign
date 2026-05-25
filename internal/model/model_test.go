package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
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
		if d.Code == "E100" && d.Table == "nokey" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E100 diagnostic for table 'nokey'")
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
			BaseType:   name,
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
		BaseType:   "status",
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
