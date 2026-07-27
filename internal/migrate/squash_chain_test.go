package migrate

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// --- fixtures: a progressive linear chain in one schema ---

func tableModel(names ...string) *model.Schema {
	s := &model.Schema{Name: "public", PGVersion: 16}
	for _, n := range names {
		s.Tables = append(s.Tables, model.Table{
			Name: n, Schema: "public", PK: []string{"id"}, Comment: n + " table",
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				{Name: "label", PGType: typeinfo.T("text"), NotNull: true},
			},
		})
	}
	s.Canonicalize()
	return s
}

// appendEdge generates a chain edge for desired parented at (parent, prev) and
// returns the new head revision. extraDML, when non-empty, injects a backfill DML
// op into the migration so the edge carries a data op (declared-inverse).
func appendEdge(t *testing.T, p *ChainProject, desired, prev *model.Schema, parent rev.Revision, slug, extraDML string) rev.Revision {
	t.Helper()
	reg := extregistry.NewBuiltinRegistry()
	var d *diff.SchemaDiff
	if prev == nil {
		names := make([]string, 0, len(desired.Tables))
		for _, tb := range desired.Tables {
			names = append(names, tb.Schema+"."+tb.Name)
		}
		d = &diff.SchemaDiff{TablesAdded: names}
	} else {
		d = diff.Diff(desired, prev)
	}
	m, _ := GenerateMigration(d, desired, "", nil, 0, 0, reg)
	if extraDML != "" {
		m.DMLOps = append(m.DMLOps, DMLOp{Op: "backfill", SQL: extraDML})
	}
	if _, err := GenerateEdge(p, m, desired, prev, parent, rev.RegistryPresent, slug); err != nil {
		t.Fatalf("GenerateEdge(%s): %v", slug, err)
	}
	r, err := rev.Compute(desired, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// threeEdgeChain builds genesis(a) -> e2(a,b) -> e3(a,b,c) and returns the project
// plus the three target revisions [R1, R2, R3].
func threeEdgeChain(t *testing.T) (*ChainProject, *model.Schema, *model.Schema, *model.Schema, rev.Revision, rev.Revision, rev.Revision) {
	t.Helper()
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m1 := tableModel("a")
	m2 := tableModel("a", "b")
	m3 := tableModel("a", "b", "c")
	r1 := appendEdge(t, p, m1, nil, rev.Revision{}, "create-a", "")
	r2 := appendEdge(t, p, m2, m1, r1, "create-b", "")
	r3 := appendEdge(t, p, m3, m2, r2, "create-c", "")
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("pre-squash consistency: %v", err)
	}
	return p, m1, m2, m3, r1, r2, r3
}

// TestSquashChainConsolidation: squashing the middle range mints a consolidation
// edge, retires the originals INTACT to archive/, keeps the chain consistent, and
// the path-finder resolves genesis -> head through the consolidation.
func TestSquashChainConsolidation(t *testing.T) {
	p, _, _, _, r1, _, r3 := threeEdgeChain(t)

	res, err := SquashChain(p, r1.String(), r3.String(), "")
	if err != nil {
		t.Fatalf("SquashChain: %v", err)
	}
	if len(res.SupersededIDs) != 2 {
		t.Fatalf("superseded = %d, want 2", len(res.SupersededIDs))
	}

	live, err := p.LoadLiveEdges()
	if err != nil {
		t.Fatal(err)
	}
	// Live: genesis + consolidation. Archive: the two superseded originals.
	if len(live) != 2 {
		t.Fatalf("live edges = %d, want 2 (genesis + consolidation)", len(live))
	}
	var consol *Edge
	for i := range live {
		if live[i].Consolidation {
			consol = &live[i]
		}
	}
	if consol == nil {
		t.Fatal("no live consolidation edge")
	}
	if len(consol.SupersededEdgeIDs) != 2 {
		t.Errorf("consolidation superseded = %d, want 2", len(consol.SupersededEdgeIDs))
	}
	arch, err := p.LoadArchivedEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(arch) != 2 {
		t.Fatalf("archived edges = %d, want 2", len(arch))
	}

	// The whole chain stays consistent (Merkle closure + endpoints + epochs + A6).
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("post-squash consistency: %v", err)
	}

	// Path-finder: from genesis, the shortest path uses the consolidation edge.
	all, err := p.LoadAllEdges()
	if err != nil {
		t.Fatal(err)
	}
	path, err := FindPath("", RemapTable{}, live, all)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	// genesis edge + consolidation = 2 edges (vs genesis + e2 + e3 = 3).
	if len(path) != 2 {
		t.Fatalf("path len = %d, want 2 (genesis + consolidation)", len(path))
	}
	usedConsol := false
	for _, e := range path {
		if e.Consolidation {
			usedConsol = true
		}
	}
	if !usedConsol {
		t.Error("shortest path did not use the consolidation edge")
	}
}

// TestSquashChainCommutationSmoke: apply(consolidation) lands where apply(sequence)
// lands — definitional under concatenation. Assert equal rendered SQL sets and
// (via the endpoint checker) equal final manifests, on the comprehensive fixture.
func TestSquashChainCommutationSmoke(t *testing.T) {
	p, _, _, _, r1, _, r3 := threeEdgeChain(t)

	// Rendered SQL of the sequence being squashed (the ordered range e2 ; e3),
	// reconstructed via the path-finder before squashing.
	live, _ := p.LoadLiveEdges()
	preAll, _ := p.LoadAllEdges()
	prePath, err := FindPath(r1.String(), RemapTable{}, live, preAll)
	if err != nil {
		t.Fatalf("pre-squash FindPath: %v", err)
	}
	var seqSQL []string
	for _, e := range prePath {
		sqls, err := RenderedEdgeSQL(p.Store(), e)
		if err != nil {
			t.Fatal(err)
		}
		seqSQL = append(seqSQL, sqls...)
	}

	res, err := SquashChain(p, r1.String(), r3.String(), "")
	if err != nil {
		t.Fatalf("SquashChain: %v", err)
	}
	if res.OpCount != len(seqSQL) {
		t.Fatalf("consolidation op count %d != sequence op count %d", res.OpCount, len(seqSQL))
	}

	// The consolidation edge renders the SAME SQL sequence, op-for-op.
	live2, _ := p.LoadLiveEdges()
	var consol Edge
	for _, e := range live2 {
		if e.Consolidation {
			consol = e
		}
	}
	consSQL, err := RenderedEdgeSQL(p.Store(), consol)
	if err != nil {
		t.Fatal(err)
	}
	if len(consSQL) != len(seqSQL) {
		t.Fatalf("rendered op count mismatch: consolidation=%d sequence=%d", len(consSQL), len(seqSQL))
	}
	for i := range seqSQL {
		if consSQL[i] != seqSQL[i] {
			t.Errorf("op %d SQL diverges:\n  seq:   %q\n  consol:%q", i, seqSQL[i], consSQL[i])
		}
	}
	// Final manifests coincide: VerifyChainConsistency asserts the consolidation
	// edge maps its from-manifest (R1) to its to-manifest (R3).
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("post-squash consistency (endpoint = manifest equality): %v", err)
	}
}

// TestSquashChainRollbackEquivalenceStructural: for a fully-mechanically-invertible
// range the consolidation takes the manifest-diff down form, and simulating that
// down on the to-manifest reproduces the from-manifest (structural rollback
// equivalence — L5's codomain is schema states, not data).
func TestSquashChainRollbackEquivalenceStructural(t *testing.T) {
	p, _, _, _, r1, _, r3 := threeEdgeChain(t)

	// Gather the range ops (e2 ; e3) before squashing to classify the down form.
	live, _ := p.LoadLiveEdges()
	all, _ := p.LoadAllEdges()
	path, err := FindPath(r1.String(), RemapTable{}, live, all)
	if err != nil {
		t.Fatal(err)
	}
	var ops []SelfContainedOp
	for _, e := range path {
		ops = append(ops, e.Ops...)
	}
	form, down := classifyConsolidationDown(ops)
	if form != "manifest-diff" {
		t.Fatalf("down form = %q, want manifest-diff (range is pure create_table)", form)
	}

	fromM, err := p.ReadRevisionManifest(r1)
	if err != nil {
		t.Fatal(err)
	}
	toM, err := p.ReadRevisionManifest(r3)
	if err != nil {
		t.Fatal(err)
	}
	sim := opSimulator{store: p.Store()}
	got, err := sim.Simulate(toM, down)
	if err != nil {
		t.Fatalf("simulate down: %v", err)
	}
	if len(got) != len(fromM) {
		t.Fatalf("down-simulated manifest size %d != from-manifest size %d", len(got), len(fromM))
	}
	for k, v := range fromM {
		if got[k] != v {
			t.Errorf("manifest key %v: down-simulated %q != from %q", k, got[k], v)
		}
	}
}

// TestSquashChainDMLComposedDowns: a range containing a DML op takes the
// composed-recorded-downs form BY TYPE, and the DML op is preserved verbatim in
// the consolidation edge (concatenation never drops or folds).
func TestSquashChainDMLComposedDowns(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m1 := tableModel("a")
	m2 := tableModel("a", "b")
	m3 := tableModel("a", "b", "c")
	r1 := appendEdge(t, p, m1, nil, rev.Revision{}, "create-a", "")
	const dml = "UPDATE public.b SET label = 'x'"
	r2 := appendEdge(t, p, m2, m1, r1, "create-b", dml)
	r3 := appendEdge(t, p, m3, m2, r2, "create-c", "")
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("consistency: %v", err)
	}

	res, err := SquashChain(p, r1.String(), r3.String(), "")
	if err != nil {
		t.Fatalf("SquashChain: %v", err)
	}
	if res.DownForm != "composed-recorded-downs" {
		t.Errorf("down form = %q, want composed-recorded-downs (range has a DML op)", res.DownForm)
	}

	// The DML op survives verbatim: find a dml-kind op whose blob is the exact SQL.
	live, _ := p.LoadLiveEdges()
	var consol Edge
	for _, e := range live {
		if e.Consolidation {
			consol = e
		}
	}
	found := false
	for _, op := range consol.Ops {
		if op.Kind() != "backfill" {
			continue
		}
		body, err := loadBody(p.Store(), op.PayloadID())
		if err != nil {
			t.Fatal(err)
		}
		blob, err := p.Store().Get(body.BlobID)
		if err != nil {
			t.Fatal(err)
		}
		if string(blob) == dml {
			found = true
		}
	}
	if !found {
		t.Error("the backfill DML op was not preserved verbatim in the consolidation edge")
	}
}

// TestCheckConsolidationDisjoint: an overlapping superseded set is a hard error
// naming the overlap (A6, enforced at creation).
func TestCheckConsolidationDisjoint(t *testing.T) {
	existing := Edge{
		Slug: "c1", Consolidation: true,
		SupersededEdgeIDs: []string{"aaaaaaaaaaaa1111", "bbbbbbbbbbbb2222"},
	}
	// Disjoint: no overlap -> ok.
	if err := checkConsolidationDisjoint([]Edge{existing}, []string{"cccccccccccc3333"}); err != nil {
		t.Errorf("disjoint sets should pass, got %v", err)
	}
	// Overlap on bbbb...2222 -> hard error naming it.
	err := checkConsolidationDisjoint([]Edge{existing}, []string{"bbbbbbbbbbbb2222", "dddddddddddd4444"})
	if err == nil {
		t.Fatal("overlapping superseded sets must be rejected")
	}
	if !contains(err.Error(), "bbbbbbbbbbbb") || !contains(err.Error(), "disjoint") {
		t.Errorf("error should name the overlap and cite disjointness: %v", err)
	}
}

// TestResolveSquashEndpoint covers the rev-or-edge reference forms.
func TestResolveSquashEndpoint(t *testing.T) {
	p, _, _, _, r1, _, r3 := threeEdgeChain(t)
	live, _ := p.LoadLiveEdges()

	// genesis (from only).
	if r, err := resolveSquashEndpoint(live, "genesis", false); err != nil || !r.IsZero() {
		t.Errorf("genesis from: r=%v err=%v", r, err)
	}
	if _, err := resolveSquashEndpoint(live, "genesis", true); err == nil {
		t.Error("genesis as --to must error")
	}
	// full revision string.
	if r, err := resolveSquashEndpoint(live, r3.String(), true); err != nil {
		t.Errorf("full revision: %v", err)
	} else if eq, _ := r.Equal(r3); !eq {
		t.Errorf("full revision resolved to %s, want %s", r, r3)
	}
	// revision prefix (contains ':').
	pref := r1.String()[:len(r1.String())-10]
	if r, err := resolveSquashEndpoint(live, pref, false); err != nil {
		t.Errorf("revision prefix: %v", err)
	} else if eq, _ := r.Equal(r1); !eq {
		t.Errorf("revision prefix resolved to %s, want %s", r, r1)
	}
	// edge-id prefix -> parent (from) / target (to). Find the middle edge e2.
	var e2 Edge
	for _, e := range live {
		if eq, _ := e.Parent.Equal(r1); eq {
			e2 = e
		}
	}
	if e2.Slug == "" {
		t.Fatal("could not find edge e2 (parent r1)")
	}
	if r, err := resolveSquashEndpoint(live, e2.ID()[:12], false); err != nil {
		t.Errorf("edge-id from: %v", err)
	} else if eq, _ := r.Equal(r1); !eq {
		t.Errorf("edge-id from resolved to %s, want parent %s", r, r1)
	}
	if r, err := resolveSquashEndpoint(live, e2.ID()[:12], true); err != nil {
		t.Errorf("edge-id to: %v", err)
	} else if eq, _ := r.Equal(e2.Target); !eq {
		t.Errorf("edge-id to resolved to %s, want target %s", r, e2.Target)
	}
}

// TestSquashChainRejectsSingleEdge: a range spanning a single edge has nothing to
// consolidate.
func TestSquashChainRejectsSingleEdge(t *testing.T) {
	p, _, _, _, r1, r2, _ := threeEdgeChain(t)
	_, err := SquashChain(p, r1.String(), r2.String(), "")
	if err == nil {
		t.Fatal("single-edge range must be rejected")
	}
	if !contains(err.Error(), "single edge") && !contains(err.Error(), "nothing to consolidate") {
		t.Errorf("error should explain the single-edge range: %v", err)
	}
}
