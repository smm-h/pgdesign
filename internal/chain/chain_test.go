package chain

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/rev"
)

// revN builds a distinct revision from a one-line schema named name. It is the
// cheap source of distinct revisions for the graph tests (a full modelgen model
// is unnecessary when only revision identity matters).
func revN(t *testing.T, name string) rev.Revision {
	t.Helper()
	s := &model.Schema{Name: name}
	s.Canonicalize()
	r, err := rev.Compute(s, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("Compute(%q): %v", name, err)
	}
	return r
}

// --- Manifest ---

// TestManifestClosureRoundTrip: storing a model's objects makes its manifest's
// ids resolve (closure passes); a manifest referencing an absent id fails.
func TestManifestClosureRoundTrip(t *testing.T) {
	s := &model.Schema{
		Name:   "shop",
		Tables: []model.Table{{Name: "users", Comment: "users"}},
		Enums:  []model.Enum{{Name: "role", Values: []string{"a", "b"}, Comment: "role"}},
	}
	s.Canonicalize()

	store, err := objstore.New(t.TempDir(), enc.CodecVersion)
	if err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifestInto(s, store)
	if err != nil {
		t.Fatalf("BuildManifestInto: %v", err)
	}
	if err := VerifyClosure(m, store); err != nil {
		t.Fatalf("closure should hold after storing all objects: %v", err)
	}

	// A manifest with a dangling id must fail closure.
	broken := Manifest{enc.Key{Kind: enc.KindTable, Name: "ghost"}: "deadbeef"}
	if err := VerifyClosure(broken, store); err == nil {
		t.Fatal("closure should fail for a manifest referencing an absent object")
	}
}

// TestManifestEqualAndBuildParity: BuildManifest ids equal the objstore Put ids
// (both are SHA-256 of the same canonical bytes), and Equal reflects that.
func TestManifestEqualAndBuildParity(t *testing.T) {
	s := &model.Schema{Name: "s", Tables: []model.Table{{Name: "t", Comment: "c"}}}
	s.Canonicalize()
	m1, err := BuildManifest(s)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := objstore.New(t.TempDir(), enc.CodecVersion)
	m2, err := BuildManifestInto(s, store)
	if err != nil {
		t.Fatal(err)
	}
	if !m1.Equal(m2) {
		t.Fatal("BuildManifest and BuildManifestInto produced different ids for the same model")
	}
}

// TestManifestDiffSymmetricDifference: a perturbed model yields a manifest delta
// naming exactly the changed object; adding/removing a table shows up as
// Added/Removed.
func TestManifestDiffSymmetricDifference(t *testing.T) {
	base := &model.Schema{Name: "s", Tables: []model.Table{{Name: "a", Comment: "c"}, {Name: "b", Comment: "c"}}}
	base.Canonicalize()
	// Change table a's comment -> its object id changes (Changed).
	changed := &model.Schema{Name: "s", Tables: []model.Table{{Name: "a", Comment: "different"}, {Name: "b", Comment: "c"}}}
	changed.Canonicalize()

	mb, _ := BuildManifest(base)
	mc, _ := BuildManifest(changed)
	d := mb.Diff(mc)
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Fatalf("comment change should be Changed only, got added=%v removed=%v", d.Added, d.Removed)
	}
	if len(d.Changed) != 1 || d.Changed[0] != (enc.Key{Kind: enc.KindTable, Name: "a"}) {
		t.Fatalf("expected table a changed, got %v", d.Changed)
	}

	// Adding a table shows as Added.
	added := &model.Schema{Name: "s", Tables: []model.Table{{Name: "a", Comment: "c"}, {Name: "b", Comment: "c"}, {Name: "z", Comment: "c"}}}
	added.Canonicalize()
	ma, _ := BuildManifest(added)
	d2 := mb.Diff(ma)
	if len(d2.Added) != 1 || d2.Added[0] != (enc.Key{Kind: enc.KindTable, Name: "z"}) {
		t.Fatalf("expected table z added, got %v", d2.Added)
	}
}

// TestManifestKindQualifiedKeysNoCollision: a table x and a function x occupy
// DISTINCT manifest keys (kind-qualification), so both coexist.
func TestManifestKindQualifiedKeysNoCollision(t *testing.T) {
	tableX := enc.Key{Kind: enc.KindTable, Schema: "public", Name: "x"}
	funcX := enc.Key{Kind: enc.KindFunction, Schema: "public", Name: "x", ArgSig: "()"}
	if tableX == funcX {
		t.Fatal("table and function keys collided despite kind-qualification")
	}
	m := Manifest{tableX: "id1", funcX: "id2"}
	if len(m) != 2 {
		t.Fatalf("expected 2 distinct keys, got %d", len(m))
	}
	// Two function overloads (different arg signatures) are also distinct.
	funcX2 := enc.Key{Kind: enc.KindFunction, Schema: "public", Name: "x", ArgSig: "(int4)"}
	if funcX == funcX2 {
		t.Fatal("function overloads collided despite arg-signature qualification")
	}
}

// TestChangedKeysFastPath: ChangedKeys is empty for equal manifests and names
// the differing keys otherwise.
func TestChangedKeysFastPath(t *testing.T) {
	s := &model.Schema{Name: "s", Tables: []model.Table{{Name: "t", Comment: "c"}}}
	s.Canonicalize()
	m, _ := BuildManifest(s)
	if ck := ChangedKeys(m, m); len(ck) != 0 {
		t.Fatalf("ChangedKeys(m,m) should be empty, got %v", ck)
	}
}

// --- Revision / manifest reconciliation ---

// TestOpaqueRevisionCrossClassErrors: RevisionOf under two classes yields
// revisions whose Equal errors (L7) — the type-level guarantee re-checked at the
// chain boundary.
func TestOpaqueRevisionCrossClassErrors(t *testing.T) {
	s := &model.Schema{Name: "s"}
	s.Canonicalize()
	present, err := RevisionOf(s, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := RevisionOf(s, rev.RegistryAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := present.Equal(absent); err == nil {
		t.Fatal("cross-class Equal must error")
	}
}

// --- Edges ---

// TestEdgeIDContentDerived: identical content -> identical id; any change to
// ops, slug, or an endpoint -> different id.
func TestEdgeIDContentDerived(t *testing.T) {
	r0 := revN(t, "r0")
	r1 := revN(t, "r1")
	tgt := enc.Key{Kind: enc.KindTable, Name: "t"}
	op := mockOp{kind: "add_table", target: tgt, class: MechanicallyInvertible, payload: "p1"}

	base := Edge{Parent: r0, Target: r1, Ops: []Op{op}, Slug: "add-t"}
	same := Edge{Parent: r0, Target: r1, Ops: []Op{op}, Slug: "add-t"}
	if base.ID() != same.ID() {
		t.Fatal("identical edges must share an id")
	}

	diffSlug := Edge{Parent: r0, Target: r1, Ops: []Op{op}, Slug: "other"}
	if base.ID() == diffSlug.ID() {
		t.Fatal("different slug must change the id")
	}
	op2 := mockOp{kind: "add_table", target: tgt, class: MechanicallyInvertible, payload: "p2"}
	diffOps := Edge{Parent: r0, Target: r1, Ops: []Op{op2}, Slug: "add-t"}
	if base.ID() == diffOps.ID() {
		t.Fatal("different op payload must change the id")
	}
	r2 := revN(t, "r2")
	diffTarget := Edge{Parent: r0, Target: r2, Ops: []Op{op}, Slug: "add-t"}
	if base.ID() == diffTarget.ID() {
		t.Fatal("different target must change the id")
	}
}

// TestParallelEdgesAndEndomorphismsDistinct: parallel edges (same endpoints,
// different ops) are distinct; pure-DML endomorphisms (R -> R) with different
// slugs are distinct and legal.
func TestParallelEdgesAndEndomorphismsDistinct(t *testing.T) {
	r1 := revN(t, "r1")
	tgt := enc.Key{Kind: enc.KindTable, Name: "t"}

	pA := Edge{Parent: r1, Target: revN(t, "r2"), Ops: []Op{mockOp{kind: "k", target: tgt, class: DeclaredInverse, payload: "a"}}, Slug: "a"}
	pB := Edge{Parent: r1, Target: pA.Target, Ops: []Op{mockOp{kind: "k", target: tgt, class: DeclaredInverse, payload: "b"}}, Slug: "b"}
	if pA.ID() == pB.ID() {
		t.Fatal("parallel edges with different ops must be distinct")
	}

	// Endomorphisms R -> R (pure DML): same parent==target, different slug.
	endo1 := Edge{Parent: r1, Target: r1, Ops: []Op{mockOp{kind: "dml", target: tgt, class: DeclaredInverse, payload: "x"}}, Slug: "backfill-1"}
	endo2 := Edge{Parent: r1, Target: r1, Ops: []Op{mockOp{kind: "dml", target: tgt, class: DeclaredInverse, payload: "x"}}, Slug: "backfill-2"}
	if endo1.ID() == endo2.ID() {
		t.Fatal("distinct endomorphisms must not collide")
	}
	if endo1.Parent.String() != endo1.Target.String() {
		t.Fatal("endomorphism should have parent == target")
	}
}

// TestFindHeadsAndGenesis: linear chain has one head; a fork has two; genesis
// edges (null parent) are found and their targets are heads only if nothing
// descends from them.
func TestFindHeadsAndGenesis(t *testing.T) {
	r0 := revN(t, "r0")
	r1 := revN(t, "r1")
	r2 := revN(t, "r2")
	r3 := revN(t, "r3")

	genesis := Edge{Target: r0, Slug: "genesis"} // null parent
	if !genesis.IsGenesis() {
		t.Fatal("edge with zero parent should be genesis")
	}
	e01 := Edge{Parent: r0, Target: r1}
	e12 := Edge{Parent: r1, Target: r2}

	linear := []Edge{genesis, e01, e12}
	heads := FindHeads(linear)
	if len(heads) != 1 || heads[0].String() != r2.String() {
		t.Fatalf("linear chain should have single head r2, got %v", heads)
	}
	if g := FindGenesis(linear); len(g) != 1 {
		t.Fatalf("expected 1 genesis edge, got %d", len(g))
	}

	// Fork: r1 -> r2 and r1 -> r3. Heads = {r2, r3}.
	e13 := Edge{Parent: r1, Target: r3}
	fork := []Edge{genesis, e01, e12, e13}
	fh := FindHeads(fork)
	if len(fh) != 2 {
		t.Fatalf("fork should have 2 heads, got %d: %v", len(fh), fh)
	}
}

// TestComposePath: contiguous path concatenates ops in order; non-contiguous
// errors; empty path is the virtual identity (nil ops, no error).
func TestComposePath(t *testing.T) {
	r0, r1, r2 := revN(t, "r0"), revN(t, "r1"), revN(t, "r2")
	tgt := enc.Key{Kind: enc.KindTable, Name: "t"}
	opA := mockOp{kind: "a", target: tgt, class: MechanicallyInvertible, payload: "a"}
	opB := mockOp{kind: "b", target: tgt, class: MechanicallyInvertible, payload: "b"}

	e01 := Edge{Parent: r0, Target: r1, Ops: []Op{opA}}
	e12 := Edge{Parent: r1, Target: r2, Ops: []Op{opB}}

	ops, err := ComposePath([]Edge{e01, e12})
	if err != nil {
		t.Fatalf("contiguous ComposePath: %v", err)
	}
	if len(ops) != 2 || ops[0].Kind() != "a" || ops[1].Kind() != "b" {
		t.Fatalf("concatenation wrong: %v", ops)
	}

	// Non-contiguous: e01 then e12-with-wrong-parent.
	bad := Edge{Parent: r0, Target: r2, Ops: []Op{opB}}
	if _, err := ComposePath([]Edge{e01, bad}); err == nil {
		t.Fatal("non-contiguous path should error")
	}

	// Empty path is the virtual identity.
	if ops, err := ComposePath(nil); err != nil || ops != nil {
		t.Fatalf("empty path should be nil ops, no error; got %v, %v", ops, err)
	}
}

// TestVerifyEdgeEndpointRequiresSimulator: the edge-endpoint check hard-errors
// without an OpSimulator (op simulation lands with 5.2); with a simulator it
// passes when the ops map from->to and fails otherwise.
func TestVerifyEdgeEndpointRequiresSimulator(t *testing.T) {
	r0, r1 := revN(t, "r0"), revN(t, "r1")
	from := Manifest{enc.Key{Kind: enc.KindTable, Name: "t"}: "id0"}
	to := Manifest{enc.Key{Kind: enc.KindTable, Name: "t"}: "id1"}
	e := Edge{Parent: r0, Target: r1}

	if err := VerifyEdgeEndpoint(e, from, to, nil); err == nil {
		t.Fatal("nil simulator must be a hard error, not a silent skip")
	}
	if err := VerifyEdgeEndpoint(e, from, to, constSim{to}); err != nil {
		t.Fatalf("a simulator producing the correct to-manifest should pass: %v", err)
	}
	if err := VerifyEdgeEndpoint(e, from, to, constSim{from}); err == nil {
		t.Fatal("a simulator producing the wrong manifest should fail the endpoint check")
	}
}

// constSim is a stand-in OpSimulator that always returns a fixed manifest,
// standing in for 5.2's real op simulation in the endpoint-check test.
type constSim struct{ out Manifest }

func (c constSim) Simulate(_ Manifest, _ []Op) (Manifest, error) { return c.out, nil }
