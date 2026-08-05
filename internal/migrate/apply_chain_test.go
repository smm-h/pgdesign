package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// twoTableDesired is a post-state model with an FK and a secondary index, so
// generate lowers it to create_table + add_fk + create_index ops (the ops whose
// separate emission is the "double-render" concern).
func twoTableDesired() *model.Schema {
	s := &model.Schema{
		Name:      "public",
		PGVersion: 16,
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", PK: []string{"id"}, Comment: "users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
			{
				Name: "orders", Schema: "public", PK: []string{"id"}, Comment: "orders",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "user_id", PGType: typeinfo.T("int8"), NotNull: true},
				},
				FKs: []model.FK{{
					Name: "orders_user_id_fkey", Columns: []string{"user_id"},
					RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE",
				}},
				Indexes: []model.Index{{
					Name: "orders_user_id_idx", Columns: []string{"user_id"},
				}},
			},
		},
	}
	s.Canonicalize()
	return s
}

// TestChainApplyRenderMatchesLegacy is the byte-identity test: for the SAME
// Migration, the self-contained renderer used by chain-mode apply produces the
// EXACT SQL sequence (op by op) that legacy apply's OpToSQL produces. This pins
// the double-render resolution: generate's separate create_table / add_fk /
// create_index ops each render byte-identically through both paths (sql.CreateTable
// renders columns + PK only, and the self-contained create_table carries an empty
// enum/domain closure).
func TestChainApplyRenderMatchesLegacy(t *testing.T) {
	testenv.Isolate(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := twoTableDesired()
	d := &diff.SchemaDiff{TablesAdded: []string{"public.users", "public.orders"}}
	m, _ := GenerateMigration(d, desired, "0.1.0", extregistry.NewBuiltinRegistry())
	if len(m.DDLOps) == 0 {
		t.Fatal("expected DDL ops from generate")
	}

	if _, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}
	edges, err := p.LoadLiveEdges()
	if err != nil || len(edges) != 1 {
		t.Fatalf("LoadLiveEdges: %d (err=%v)", len(edges), err)
	}
	e := edges[0]

	// Legacy sequence: OpToSQL for every DDL op, then every DML op's SQL.
	var legacy []string
	for _, op := range m.DDLOps {
		legacy = append(legacy, OpToSQL(op))
	}
	for _, op := range m.DMLOps {
		legacy = append(legacy, op.SQL)
	}

	// Chain sequence: RenderSQL for every op EXCEPT the chain-only schema_meta op
	// (which has no legacy equivalent).
	var chainSQL []string
	for _, op := range e.Ops {
		if op.Kind() == "schema_meta" {
			continue
		}
		s, err := op.RenderSQL(p.Store())
		if err != nil {
			t.Fatalf("RenderSQL(%s): %v", op.Kind(), err)
		}
		chainSQL = append(chainSQL, s)
	}

	if len(chainSQL) != len(legacy) {
		t.Fatalf("op count mismatch: chain=%d legacy=%d", len(chainSQL), len(legacy))
	}
	for i := range legacy {
		if chainSQL[i] != legacy[i] {
			t.Errorf("op %d SQL diverges:\n  legacy: %q\n  chain:  %q", i, legacy[i], chainSQL[i])
		}
	}
}

// TestReconstructModelRoundTrip: reconstructing the head model from its on-disk
// manifest + object store yields a model whose revision equals the recorded head
// (decode∘enc = id on canonicalized models). It also exercises the ChainHead seam.
func TestReconstructModelRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := twoTableDesired()
	d := &diff.SchemaDiff{TablesAdded: []string{"public.users", "public.orders"}}
	m, _ := GenerateMigration(d, desired, "0.1.0", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}

	head, prev, err := ChainHead(p)
	if err != nil {
		t.Fatalf("ChainHead: %v", err)
	}
	if prev == nil {
		t.Fatal("expected a reconstructed head model, got nil")
	}
	got, err := rev.Compute(prev, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	if eq, _ := got.Equal(head); !eq {
		t.Fatalf("reconstructed model revision %s != head %s", got, head)
	}
	// And the reconstructed revision equals the original desired's revision.
	want, err := rev.Compute(desired, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	if eq, _ := got.Equal(want); !eq {
		t.Fatalf("reconstructed revision %s != desired %s", got, want)
	}
}
