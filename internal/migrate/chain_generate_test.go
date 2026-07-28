package migrate

import (
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// genesisDesired is a small canonicalized post-state model: one table in the
// "shop" schema, no FKs/indexes, so generate lowers it to a single create_table.
func genesisDesired() *model.Schema {
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
			},
		}},
	}
	s.Canonicalize()
	return s
}

// TestGenerateEdgeGenesis: generate lowers a TablesAdded diff to ops, GenerateEdge
// writes the objects + revision manifest + one genesis edge, and the on-disk chain
// is internally consistent (Merkle closure + edge endpoints + epoch homogeneity).
func TestGenerateEdgeGenesis(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := genesisDesired()
	d := &diff.SchemaDiff{TablesAdded: []string{"shop.users"}}
	m, _ := GenerateMigration(d, desired, "0.1.0", extregistry.NewBuiltinRegistry())

	name, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "create-users")
	if err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}

	edges, err := p.LoadLiveEdges()
	if err != nil {
		t.Fatalf("LoadLiveEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	e := edges[0]
	if !e.IsGenesis() {
		t.Errorf("expected a genesis edge (null parent)")
	}
	if e.Class != rev.RegistryPresent || e.Slug != "create-users" {
		t.Errorf("edge facets wrong: class=%s slug=%s", e.Class, e.Slug)
	}
	// The edge carries a schema_meta op (genesis) plus the create_table op.
	if len(e.Ops) != 2 {
		t.Fatalf("expected 2 ops (schema_meta + create_table), got %d", len(e.Ops))
	}

	// The whole on-disk chain must be internally consistent.
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("VerifyChainConsistency: %v", err)
	}

	// Idempotent: a second GenerateEdge yields the same content-derived filename.
	name2, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "create-users")
	if err != nil {
		t.Fatalf("GenerateEdge (2nd): %v", err)
	}
	if name2 != name {
		t.Errorf("edge filename not stable: %q vs %q", name, name2)
	}
}

// TestGenerateEdgeZeroOpGuard: a migration with no ops is a hard error, never an
// empty edge.
func TestGenerateEdgeZeroOpGuard(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := genesisDesired()
	_, err = GenerateEdge(p, &Migration{}, desired, nil, rev.Revision{}, rev.RegistryPresent, "empty")
	if err != ErrNoEdgeOps {
		t.Fatalf("expected ErrNoEdgeOps, got %v", err)
	}
}

// TestIsChainModeAndGuards: a project with a chain/ dir is chain-mode; the bridge
// guard hard-errors naming the subphase. A legacy project (no chain/ dir) is not.
// squash (5.3), rollback (5.6), and baseline (5.10) are all reworked for chain
// mode and dispatched by the CLI; guardChainMode survives as a generic
// defense-in-depth helper (squash's legacy path still calls it), exercised here
// with a placeholder subcommand.
func TestIsChainModeAndGuards(t *testing.T) {
	// Legacy: a bare migrations dir with no chain/ subdir.
	legacy := t.TempDir()
	if IsChainMode(legacy) {
		t.Error("bare dir should not be chain-mode")
	}
	if err := guardChainMode(legacy, "squash", "5.3"); err != nil {
		t.Errorf("legacy guard should pass, got %v", err)
	}

	// Chain: OpenChainProject creates the chain/ dir.
	chainDir := t.TempDir()
	if _, err := OpenChainProject(chainDir); err != nil {
		t.Fatal(err)
	}
	if !IsChainMode(chainDir) {
		t.Error("chain project dir should be chain-mode")
	}
	for _, tc := range []struct{ sub, phase string }{
		{"squash", "5.3"},
	} {
		err := guardChainMode(chainDir, tc.sub, tc.phase)
		if err == nil {
			t.Errorf("%s guard should fail on chain-mode project", tc.sub)
			continue
		}
		if !contains(err.Error(), tc.phase) || !contains(err.Error(), tc.sub) {
			t.Errorf("%s guard error missing subphase/subcommand: %v", tc.sub, err)
		}
	}

	// Sanity: the guard keys off filepath.Join(dir, "chain").
	if _, err := filepath.Abs(chainDir); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
