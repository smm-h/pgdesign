package migrate

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// usersWithName is genesisDesired() plus a nullable "name" column, so the second
// edge lowers to a single add_column op.
func usersWithName() *model.Schema {
	s := &model.Schema{
		Name: "shop",
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "shop",
			PK:      []string{"id"},
			Comment: "application users",
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
				{Name: "name", PGType: typeinfo.T("text")},
			},
		}},
	}
	s.Canonicalize()
	return s
}

// TestPlanChainEdges_Pure verifies the pure plan engine (5.9): PlanChainEdges
// enumerates the ordered edges from a starting revision to the head, reading
// only on-disk edges — no database. From genesis it lists the whole chain; from
// a mid revision it lists the tail; from the head it lists nothing.
func TestPlanChainEdges_Pure(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := extregistry.NewBuiltinRegistry()

	// Edge 1: genesis create-users.
	desired1 := genesisDesired()
	d1 := &diff.SchemaDiff{TablesAdded: []string{"shop.users"}}
	m1, _ := GenerateMigration(d1, desired1, "", reg)
	if _, err := GenerateEdge(p, m1, desired1, nil, rev.Revision{}, rev.RegistryPresent, "create-users"); err != nil {
		t.Fatalf("GenerateEdge 1: %v", err)
	}
	head1, prev1, err := ChainHead(p)
	if err != nil {
		t.Fatalf("ChainHead after edge 1: %v", err)
	}

	// Edge 2: add the "name" column, parented on head1.
	desired2 := usersWithName()
	d2 := diff.Diff(desired2, prev1)
	if d2.IsEmpty() {
		t.Fatal("expected a non-empty diff for the add-name edge")
	}
	m2, _ := GenerateMigration(d2, desired2, "", reg)
	if _, err := GenerateEdge(p, m2, desired2, prev1, head1, rev.RegistryPresent, "add-name"); err != nil {
		t.Fatalf("GenerateEdge 2: %v", err)
	}
	head2, _, err := ChainHead(p)
	if err != nil {
		t.Fatalf("ChainHead after edge 2: %v", err)
	}

	// From genesis: the whole chain, genesis edge first.
	fromGenesis, err := PlanChainEdges(p, "")
	if err != nil {
		t.Fatalf("PlanChainEdges(genesis): %v", err)
	}
	if len(fromGenesis) != 2 {
		t.Fatalf("from genesis: expected 2 edges, got %d", len(fromGenesis))
	}
	if !fromGenesis[0].IsGenesis() {
		t.Error("from genesis: first edge should be the genesis edge")
	}
	if fromGenesis[0].Slug != "create-users" || fromGenesis[1].Slug != "add-name" {
		t.Errorf("from genesis: wrong order/slugs: %q, %q", fromGenesis[0].Slug, fromGenesis[1].Slug)
	}

	// From the mid revision (head1): only the tail edge.
	fromMid, err := PlanChainEdges(p, head1.String())
	if err != nil {
		t.Fatalf("PlanChainEdges(head1): %v", err)
	}
	if len(fromMid) != 1 || fromMid[0].Slug != "add-name" {
		t.Fatalf("from head1: expected [add-name], got %d edges", len(fromMid))
	}

	// From the head: nothing pending.
	fromHead, err := PlanChainEdges(p, head2.String())
	if err != nil {
		t.Fatalf("PlanChainEdges(head2): %v", err)
	}
	if len(fromHead) != 0 {
		t.Fatalf("from head: expected 0 edges, got %d", len(fromHead))
	}
}
