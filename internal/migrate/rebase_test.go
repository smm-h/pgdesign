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

// withExtraTable returns a copy of twoTableDesired with one extra single-column
// table appended, canonicalized.
func withExtraTable(name string) *model.Schema {
	s := twoTableDesired()
	s.Tables = append(s.Tables, model.Table{
		Name: name, Schema: "public", PK: []string{"id"}, Comment: name,
		Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}},
	})
	s.Canonicalize()
	return s
}

// buildEdge generates a chain edge for desired against prev at parent, returning
// the target revision.
func buildEdge(t *testing.T, p *ChainProject, prev, desired *model.Schema, parent rev.Revision, slug string) rev.Revision {
	t.Helper()
	var d *diff.SchemaDiff
	if prev == nil {
		d = diff.Diff(desired, &model.Schema{Name: desired.Name, PGVersion: desired.PGVersion})
	} else {
		d = diff.Diff(desired, prev)
	}
	m, _ := GenerateMigration(d, desired, "", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, desired, prev, parent, rev.RegistryPresent, slug); err != nil {
		t.Fatalf("GenerateEdge(%s): %v", slug, err)
	}
	r, err := rev.Compute(desired, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// forkChain builds: genesis(base) -> R1; keep branch R1->R2 (+tableA);
// rebase branch R1->R3 (+tableB) -> R4 (+tableC). It returns the project and the
// key revisions.
func forkChain(t *testing.T) (p *ChainProject, r1, r2, r3, r4 rev.Revision) {
	t.Helper()
	var err error
	p, err = OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := twoTableDesired()
	r1 = buildEdge(t, p, nil, base, rev.Revision{}, "genesis")

	keep := withExtraTable("kept_a")
	r2 = buildEdge(t, p, base, keep, r1, "keep-a")

	branchB := withExtraTable("rebase_b")
	r3 = buildEdge(t, p, base, branchB, r1, "rebase-b")

	branchBC := withExtraTable("rebase_b")
	branchBC.Tables = append(branchBC.Tables, model.Table{
		Name: "rebase_c", Schema: "public", PK: []string{"id"}, Comment: "rebase_c",
		Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}},
	})
	branchBC.Canonicalize()
	r4 = buildEdge(t, p, branchB, branchBC, r3, "rebase-c")
	return p, r1, r2, r3, r4
}

// TestRebaseResolvesForkAndServesForward pins the rebase mechanics: re-parent the
// rebased-away tail onto the kept head, recompute revisions, archive the
// originals, write the remap, leave a single consistent live head, and serve a
// database at a rebased-away revision forward (never NoPathError).
func TestRebaseResolvesForkAndServesForward(t *testing.T) {
	testenv.Isolate(t)
	p, _, r2, r3, r4 := forkChain(t)

	// Two heads before rebase.
	live, _ := p.LoadLiveEdges()
	if hs := findHeadRevs(live, RemapTable{}); len(hs) != 2 {
		t.Fatalf("expected 2 heads before rebase, got %d", len(hs))
	}

	res, err := RebaseChain(p, r2.String())
	if err != nil {
		t.Fatalf("RebaseChain: %v", err)
	}
	if len(res.ReparentedFrom) != 2 {
		t.Fatalf("expected 2 re-parented edges (the tail), got %d", len(res.ReparentedFrom))
	}

	// Single live head after rebase.
	live, _ = p.LoadLiveEdges()
	remap, err := p.LoadRemap()
	if err != nil {
		t.Fatal(err)
	}
	if hs := findHeadRevs(live, remap); len(hs) != 1 {
		t.Fatalf("expected 1 live head after rebase, got %d: %v", len(hs), headStrings(hs))
	}

	// Store, manifests, edges, and archive are mutually consistent.
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("VerifyChainConsistency after rebase: %v", err)
	}

	// Archived originals are present and reachable via the checker (they loaded).
	arch, err := p.LoadArchivedEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(arch) != 2 {
		t.Fatalf("expected 2 archived originals, got %d", len(arch))
	}

	// The remap serves both rebased-away revisions.
	if remap[r3.String()] == "" || remap[r4.String()] == "" {
		t.Fatalf("remap missing rebased-away revisions: %v", remap)
	}

	// A database stamped at the FIRST rebased-away revision (r3) is SERVED FORWARD,
	// not orphaned: FindPath returns a path to the new head via the remap.
	all, _ := p.LoadAllEdges()
	path, err := FindPath(r3.String(), remap, live, all)
	if err != nil {
		t.Fatalf("FindPath from rebased-away revision must be served, got: %v", err)
	}
	// r3 canon -> its re-parented revision, which is an intermediate (the second
	// re-parented edge remains), so the path is non-empty.
	if len(path) == 0 {
		t.Fatalf("expected a non-empty served-forward path from the rebased-away revision")
	}
}
