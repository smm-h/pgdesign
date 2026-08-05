package model

import (
	"encoding/json"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"
)

// projectionTestSchema builds a two-schema graph whose tables share names
// across schemas (public.account/public.entry and archive.account/
// archive.entry), plus a multi-column FK, so the projection exercises
// qualification, fan counts, and per-column edges.
func projectionTestSchema() *Schema {
	s := &Schema{
		Tables: []Table{
			{Name: "account", Schema: "public"},
			{Name: "entry", Schema: "public", FKs: []FK{
				{Name: "fk_entry_account", Columns: []string{"account_id"}, RefSchema: "public", RefTable: "account", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			}},
			{Name: "account", Schema: "archive"},
			{Name: "entry", Schema: "archive", FKs: []FK{
				{Name: "fk_entry_account", Columns: []string{"account_id"}, RefSchema: "archive", RefTable: "account", RefColumns: []string{"id"}, OnDelete: "SET NULL"},
			}},
			{Name: "region", Schema: "public"},
			{Name: "ledger", Schema: "public", FKs: []FK{
				{Name: "fk_ledger_region", Columns: []string{"region", "account_id"}, RefSchema: "public", RefTable: "region", RefColumns: []string{"name", "acct"}, OnDelete: "RESTRICT"},
			}},
		},
	}
	s.BuildFKGraph()
	return s
}

// TestFKGraphProjection_Deterministic asserts that Project produces identical
// JSON bytes across repeated calls, independent of the graph's map iteration
// order.
func TestFKGraphProjection_Deterministic(t *testing.T) {
	testenv.Isolate(t)
	s := projectionTestSchema()
	g := s.FKGraph

	var first []byte
	for i := 0; i < 25; i++ {
		b, err := json.Marshal(g.Project())
		if err != nil {
			t.Fatalf("marshal projection: %v", err)
		}
		if first == nil {
			first = b
			continue
		}
		if string(b) != string(first) {
			t.Fatalf("projection not deterministic:\n  first: %s\n  got:   %s", first, b)
		}
	}
}

// TestFKGraphProjection_RoundTrip asserts that reconstructing a graph from its
// projection and re-projecting yields byte-identical JSON: Project∘
// FKGraphFromProjection∘Project == Project.
func TestFKGraphProjection_RoundTrip(t *testing.T) {
	testenv.Isolate(t)
	s := projectionTestSchema()
	p := s.FKGraph.Project()

	b1, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	g2 := FKGraphFromProjection(p)
	p2 := g2.Project()
	b2, err := json.Marshal(p2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(b1) != string(b2) {
		t.Fatalf("round-trip mismatch:\n  before: %s\n  after:  %s", b1, b2)
	}

	// The reconstructed graph must answer cascade queries identically.
	if got := g2.CascadeChain("public.account"); len(got) != 1 || got[0] != "public.entry" {
		t.Errorf("reconstructed CascadeChain(public.account) = %v, want [public.entry]", got)
	}
	if got := g2.CascadeChain("archive.account"); got != nil {
		t.Errorf("reconstructed CascadeChain(archive.account) = %v, want nil (SET NULL, no cascade)", got)
	}
}

// TestFKGraphProjection_Qualified pins the (schema, name) keying and fan counts
// in the projection for same-named tables across schemas.
func TestFKGraphProjection_Qualified(t *testing.T) {
	testenv.Isolate(t)
	s := projectionTestSchema()
	p := s.FKGraph.Project()

	// Nodes: two accounts, two entries, region, ledger — six distinct nodes.
	byKey := make(map[string]FKNodeProjection)
	for _, n := range p.Nodes {
		byKey[TableKey(n.Schema, n.Name)] = n
	}
	if len(byKey) != 6 {
		t.Fatalf("expected 6 distinct nodes, got %d: %v", len(byKey), p.Nodes)
	}
	if n := byKey["public.account"]; n.FanIn != 1 || n.FanOut != 0 {
		t.Errorf("public.account node = %+v, want FanIn=1 FanOut=0", n)
	}
	if n := byKey["archive.account"]; n.FanIn != 1 || n.FanOut != 0 {
		t.Errorf("archive.account node = %+v, want FanIn=1 FanOut=0", n)
	}
	if n := byKey["public.ledger"]; n.FanOut != 1 {
		t.Errorf("public.ledger node = %+v, want FanOut=1 (constraint count, not columns)", n)
	}

	// Edges: 1 (public entry) + 1 (archive entry) + 2 (ledger multi-column) = 4.
	if len(p.Edges) != 4 {
		t.Fatalf("expected 4 edges, got %d: %v", len(p.Edges), p.Edges)
	}
	// No edge should be marked Imported (nothing sets it yet).
	for _, e := range p.Edges {
		if e.Imported {
			t.Errorf("unexpected Imported edge: %+v", e)
		}
	}
	// Edges must be sorted (archive before public).
	if p.Edges[0].FromSchema != "archive" {
		t.Errorf("edges not sorted: first FromSchema = %q, want archive", p.Edges[0].FromSchema)
	}
}
