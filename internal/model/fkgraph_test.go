package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/parse"
)

func TestFKGraph_Construction(t *testing.T) {
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
			{
				Name:    "comments",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "post_id", Type: "ref"}, {Name: "body", Type: "short_text"}},
				FKs: map[string]parse.RawFK{
					"fk_comments_post_id": {Columns: []string{"post_id"}, RefTable: "posts", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", diags)
	}

	if schema.FKGraph == nil {
		t.Fatal("FKGraph is nil after Build")
	}
	g := schema.FKGraph

	// Forward edges: posts -> users, comments -> posts.
	if len(g.Forward["posts"]) != 1 {
		t.Fatalf("expected 1 forward edge from posts, got %d", len(g.Forward["posts"]))
	}
	if g.Forward["posts"][0].ToTable != "users" {
		t.Errorf("expected posts forward edge to users, got %q", g.Forward["posts"][0].ToTable)
	}
	if len(g.Forward["comments"]) != 1 {
		t.Fatalf("expected 1 forward edge from comments, got %d", len(g.Forward["comments"]))
	}
	if g.Forward["comments"][0].ToTable != "posts" {
		t.Errorf("expected comments forward edge to posts, got %q", g.Forward["comments"][0].ToTable)
	}

	// Reverse edges: users <- posts, posts <- comments.
	if len(g.Reverse["users"]) != 1 {
		t.Fatalf("expected 1 reverse edge to users, got %d", len(g.Reverse["users"]))
	}
	if g.Reverse["users"][0].FromTable != "posts" {
		t.Errorf("expected users reverse edge from posts, got %q", g.Reverse["users"][0].FromTable)
	}
	if len(g.Reverse["posts"]) != 1 {
		t.Fatalf("expected 1 reverse edge to posts, got %d", len(g.Reverse["posts"]))
	}
	if g.Reverse["posts"][0].FromTable != "comments" {
		t.Errorf("expected posts reverse edge from comments, got %q", g.Reverse["posts"][0].FromTable)
	}

	// FanIn/FanOut.
	if g.FanIn["users"] != 1 {
		t.Errorf("expected users FanIn=1, got %d", g.FanIn["users"])
	}
	if g.FanIn["posts"] != 1 {
		t.Errorf("expected posts FanIn=1, got %d", g.FanIn["posts"])
	}
	if g.FanOut["posts"] != 1 {
		t.Errorf("expected posts FanOut=1, got %d", g.FanOut["posts"])
	}
	if g.FanOut["comments"] != 1 {
		t.Errorf("expected comments FanOut=1, got %d", g.FanOut["comments"])
	}
}

func TestFKGraph_MultiColumnFK(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{
			Schema: "public",
		},
		Tables: []parse.RawTable{
			{
				Name: "parent",
				PK:   []string{"region", "id"},
				Columns: []parse.RawColumn{
					{Name: "region", Type: "short_text"},
					{Name: "id", Type: "ref"},
				},
			},
			{
				Name: "child",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "parent_region", Type: "short_text"},
					{Name: "parent_id", Type: "ref"},
				},
				FKs: map[string]parse.RawFK{
					"fk_child_parent": {
						Columns:    []string{"parent_region", "parent_id"},
						RefTable:   "parent",
						RefColumns: []string{"region", "id"},
						OnDelete:   "CASCADE",
					},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", diags)
	}

	g := schema.FKGraph

	// Two edges (one per column) in forward direction.
	if len(g.Forward["child"]) != 2 {
		t.Fatalf("expected 2 forward edges from child, got %d", len(g.Forward["child"]))
	}
	// Two edges in reverse direction.
	if len(g.Reverse["parent"]) != 2 {
		t.Fatalf("expected 2 reverse edges to parent, got %d", len(g.Reverse["parent"]))
	}

	// FanIn/FanOut count constraints, not columns: should be 1 each.
	if g.FanOut["child"] != 1 {
		t.Errorf("expected child FanOut=1, got %d", g.FanOut["child"])
	}
	if g.FanIn["parent"] != 1 {
		t.Errorf("expected parent FanIn=1, got %d", g.FanIn["parent"])
	}
}

// cascadeSchema builds a chain: a references b, b references c, c references d
// (all CASCADE), plus e references a (SET NULL, non-cascade). Forward edges
// follow the reference direction: a -> b -> c -> d.
func cascadeSchema() *Schema {
	s := &Schema{
		Tables: []Table{
			{Name: "d", Schema: "public"},
			{Name: "c", Schema: "public", FKs: []FK{{Name: "fk_c_d", Columns: []string{"d_id"}, RefTable: "d", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "b", Schema: "public", FKs: []FK{{Name: "fk_b_c", Columns: []string{"c_id"}, RefTable: "c", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "a", Schema: "public", FKs: []FK{{Name: "fk_a_b", Columns: []string{"b_id"}, RefTable: "b", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "e", Schema: "public", FKs: []FK{{Name: "fk_e_a", Columns: []string{"a_id"}, RefTable: "a", RefColumns: []string{"id"}, OnDelete: "SET NULL"}}},
		},
	}
	s.BuildFKGraph()
	return s
}

func TestFKGraph_CascadeDepth(t *testing.T) {
	s := cascadeSchema()
	g := s.FKGraph

	tests := []struct {
		table string
		want  int
	}{
		{"a", 3},
		{"b", 2},
		{"c", 1},
		{"d", 0},
		{"e", 0},
	}
	for _, tt := range tests {
		got := g.CascadeDepth(tt.table)
		if got != tt.want {
			t.Errorf("CascadeDepth(%q) = %d, want %d", tt.table, got, tt.want)
		}
	}
}

func TestFKGraph_CascadeBreadth(t *testing.T) {
	s := cascadeSchema()
	g := s.FKGraph

	tests := []struct {
		table string
		want  int
	}{
		{"a", 3},
		{"b", 2},
		{"c", 1},
		{"d", 0},
		{"e", 0},
	}
	for _, tt := range tests {
		got := g.CascadeBreadth(tt.table)
		if got != tt.want {
			t.Errorf("CascadeBreadth(%q) = %d, want %d", tt.table, got, tt.want)
		}
	}
}

func TestFKGraph_CascadeChain(t *testing.T) {
	s := cascadeSchema()
	g := s.FKGraph

	// From "a", BFS follows Forward CASCADE edges: a -> b -> c -> d.
	chain := g.CascadeChain("a")
	if len(chain) != 3 {
		t.Fatalf("CascadeChain(a): expected 3 elements, got %d: %v", len(chain), chain)
	}
	expected := []string{"b", "c", "d"}
	for i, want := range expected {
		if chain[i] != want {
			t.Errorf("CascadeChain(a)[%d] = %q, want %q", i, chain[i], want)
		}
	}

	// From "d", no Forward CASCADE edges.
	chain = g.CascadeChain("d")
	if chain != nil {
		t.Errorf("CascadeChain(d): expected nil, got %v", chain)
	}
}

func TestFKGraph_CascadeHandlesCycles(t *testing.T) {
	s := &Schema{
		Tables: []Table{
			{Name: "x", Schema: "public", FKs: []FK{{Name: "fk_x_y", Columns: []string{"y_id"}, RefTable: "y", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "y", Schema: "public", FKs: []FK{{Name: "fk_y_x", Columns: []string{"x_id"}, RefTable: "x", RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
		},
	}
	s.BuildFKGraph()
	g := s.FKGraph

	// Must not hang. Depth should be finite (1: x -> y, then y -> x is
	// blocked by visited set).
	depth := g.CascadeDepth("x")
	if depth < 0 {
		t.Errorf("CascadeDepth(x) returned negative: %d", depth)
	}

	chain := g.CascadeChain("x")
	if chain == nil {
		t.Fatal("CascadeChain(x): expected non-nil (y is reachable)")
	}
	found := false
	for _, v := range chain {
		if v == "y" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CascadeChain(x) should contain 'y', got %v", chain)
	}
}

func TestTablesByName_PopulatedByBuild(t *testing.T) {
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
				Name:    "orders",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_orders_user": {Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", diags)
	}

	if schema.TablesByName == nil {
		t.Fatal("TablesByName is nil after Build")
	}

	// Lookup existing table.
	users := schema.TablesByName["public.users"]
	if users == nil {
		t.Fatal("TablesByName[public.users] returned nil")
	}
	if users.Name != "users" {
		t.Errorf("expected table name 'users', got %q", users.Name)
	}

	// Lookup nonexistent table.
	ghost := schema.TablesByName["public.nonexistent"]
	if ghost != nil {
		t.Errorf("expected nil for nonexistent table, got %+v", ghost)
	}

	// TableByName method.
	orders := schema.TableByName("public", "orders")
	if orders == nil {
		t.Fatal("TableByName(public, orders) returned nil")
	}
	if orders.Name != "orders" {
		t.Errorf("expected table name 'orders', got %q", orders.Name)
	}

	// TableByName for nonexistent.
	missing := schema.TableByName("public", "nonexistent")
	if missing != nil {
		t.Errorf("expected nil from TableByName for nonexistent, got %+v", missing)
	}
}

func TestBuildFKGraph_CalledExplicitly(t *testing.T) {
	s := &Schema{
		Tables: []Table{
			{Name: "alpha", Schema: "public"},
			{Name: "beta", Schema: "public", FKs: []FK{
				{Name: "fk_beta_alpha", Columns: []string{"alpha_id"}, RefTable: "alpha", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			}},
		},
	}

	// FKGraph should be nil before explicit call.
	if s.FKGraph != nil {
		t.Fatal("FKGraph should be nil before BuildFKGraph")
	}

	s.BuildFKGraph()

	if s.FKGraph == nil {
		t.Fatal("FKGraph is nil after BuildFKGraph")
	}

	g := s.FKGraph

	// Forward: beta -> alpha.
	if len(g.Forward["beta"]) != 1 {
		t.Fatalf("expected 1 forward edge from beta, got %d", len(g.Forward["beta"]))
	}
	if g.Forward["beta"][0].ToTable != "alpha" {
		t.Errorf("expected beta forward edge to alpha, got %q", g.Forward["beta"][0].ToTable)
	}

	// Reverse: alpha <- beta.
	if len(g.Reverse["alpha"]) != 1 {
		t.Fatalf("expected 1 reverse edge to alpha, got %d", len(g.Reverse["alpha"]))
	}
	if g.Reverse["alpha"][0].FromTable != "beta" {
		t.Errorf("expected alpha reverse edge from beta, got %q", g.Reverse["alpha"][0].FromTable)
	}

	// Fan counts.
	if g.FanOut["beta"] != 1 {
		t.Errorf("expected beta FanOut=1, got %d", g.FanOut["beta"])
	}
	if g.FanIn["alpha"] != 1 {
		t.Errorf("expected alpha FanIn=1, got %d", g.FanIn["alpha"])
	}
}
