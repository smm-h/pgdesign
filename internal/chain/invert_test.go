package chain

import (
	"errors"
	"fmt"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/enc"
	"pgregory.net/rapid"
)

// mockOp is a test-only Op over the ABSTRACT op vocabulary. modelgen makes
// models, not op-lists, so the inverse-law property tests need their own
// generator (below). A mockOp's inverse is precomputed: for an invertible op
// (mechanical or declared) inv points at its inverse, and for a mechanical pair
// the two ops are mutually involutive (up.inv == down, down.inv == up), so
// double-inversion returns the original. A non-invertible op has inv == nil.
type mockOp struct {
	kind    string
	target  enc.Key
	class   InvertibilityClass
	payload string
	inv     *mockOp
}

func (o mockOp) Kind() string                      { return o.kind }
func (o mockOp) Target() enc.Key                   { return o.target }
func (o mockOp) Invertibility() InvertibilityClass { return o.class }
func (o mockOp) PayloadID() string                 { return o.payload }
func (o mockOp) Inverse() (Op, bool) {
	if o.class == NonInvertible {
		return nil, false
	}
	return *o.inv, true
}

// mkInvertiblePair builds an op and its mutually-involutive inverse under the
// given (invertible) class.
func mkInvertiblePair(kind string, tgt enc.Key, payload string, class InvertibilityClass) mockOp {
	up := &mockOp{kind: kind, target: tgt, class: class, payload: payload}
	down := &mockOp{kind: kind + "^-1", target: tgt, class: class, payload: payload + "^-1"}
	up.inv = down
	down.inv = up
	return *up
}

// --- the op-list generator over the abstract vocabulary ---

func genKey(t *rapid.T, label string) enc.Key {
	name := rapid.StringMatching(`[a-z]{1,6}`).Draw(t, label+"_name")
	return enc.Key{Kind: enc.KindTable, Schema: "public", Name: name}
}

func genOp(t *rapid.T, label string) Op {
	class := InvertibilityClass(rapid.IntRange(int(MechanicallyInvertible), int(NonInvertible)).Draw(t, label+"_class"))
	kind := rapid.StringMatching(`[a-z]{2,8}`).Draw(t, label+"_kind")
	tgt := genKey(t, label)
	payload := rapid.StringMatching(`[0-9a-f]{4}`).Draw(t, label+"_payload")
	if class == NonInvertible {
		return mockOp{kind: kind, target: tgt, class: class, payload: payload}
	}
	return mkInvertiblePair(kind, tgt, payload, class)
}

func genOps(t *rapid.T) []Op {
	n := rapid.IntRange(0, 6).Draw(t, "op_count")
	ops := make([]Op, n)
	for i := 0; i < n; i++ {
		ops[i] = genOp(t, fmt.Sprintf("op_%d", i))
	}
	return ops
}

func hasNonInvertible(ops []Op) bool {
	for _, o := range ops {
		if o.Invertibility() == NonInvertible {
			return true
		}
	}
	return false
}

func opFacetsEqual(a, b Op) bool {
	return a.Kind() == b.Kind() && a.Target() == b.Target() &&
		a.Invertibility() == b.Invertibility() && a.PayloadID() == b.PayloadID()
}

// TestInverseOfList_DefinedIffEveryComponentHasOne is L4's composite-inverse
// rule: InverseOfList is defined WHEN AND ONLY WHEN every component has an
// inverse (mechanical or declared), and is (nil,false) as soon as one component
// is non-invertible. When defined it is the REVERSED composition of component
// inverses.
func TestInverseOfList_DefinedIffEveryComponentHasOne(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		ops := genOps(rt)
		inv, ok := InverseOfList(ops)
		wantOK := !hasNonInvertible(ops)
		if ok != wantOK {
			rt.Fatalf("InverseOfList ok=%v, want %v (hasNonInvertible=%v)", ok, wantOK, hasNonInvertible(ops))
		}
		if !ok {
			if inv != nil {
				rt.Fatalf("undefined inverse should return nil op-list, got %d ops", len(inv))
			}
			return
		}
		if len(inv) != len(ops) {
			rt.Fatalf("inverse length %d != op-list length %d", len(inv), len(ops))
		}
		// Reversed composition: inv[len-1-i] == ops[i].Inverse().
		for i, op := range ops {
			want, _ := op.Inverse()
			if !opFacetsEqual(inv[len(ops)-1-i], want) {
				rt.Fatalf("inverse not reversed component-inverse at %d", i)
			}
		}
	})
}

// TestMechanicalRange_ConstructibleIffAllMechanical enforces the type-level L4
// rule: a MechanicalRange (the ONLY carrier of a manifest-diff down) can be
// constructed IFF every op is MechanicallyInvertible. Crucially, a list
// containing a DECLARED-INVERSE op — which DOES have an inverse — still cannot
// form a MechanicalRange ("declared-inverse-containing included").
func TestMechanicalRange_ConstructibleIffAllMechanical(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		ops := genOps(rt)
		_, err := NewMechanicalRange(ops)
		wantOK := AllMechanicallyInvertible(ops)
		if (err == nil) != wantOK {
			rt.Fatalf("NewMechanicalRange err=%v, want ok=%v", err, wantOK)
		}
		if err != nil && !errors.Is(err, ErrNotFullyMechanical) {
			rt.Fatalf("expected ErrNotFullyMechanical, got %v", err)
		}
	})
}

// TestMechanicalRange_ManifestDiffDown_DoubleInverse pins that for a
// fully-mechanical range the down is total and inverting it returns to the
// original op facets (the mocks' involution). Composed with the reversal, this
// is InverseOfList(ManifestDiffDown) ~= original.
func TestMechanicalRange_ManifestDiffDown_DoubleInverse(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		// Draw ops then force all to mechanical by regenerating any non-mechanical.
		n := rapid.IntRange(0, 5).Draw(rt, "n")
		ops := make([]Op, n)
		for i := 0; i < n; i++ {
			tgt := genKey(rt, fmt.Sprintf("m_%d", i))
			ops[i] = mkInvertiblePair(
				rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("m_%d_kind", i)),
				tgt,
				rapid.StringMatching(`[0-9a-f]{4}`).Draw(rt, fmt.Sprintf("m_%d_pl", i)),
				MechanicallyInvertible,
			)
		}
		r, err := NewMechanicalRange(ops)
		if err != nil {
			rt.Fatalf("NewMechanicalRange: %v", err)
		}
		down := r.ManifestDiffDown()
		if len(down) != n {
			rt.Fatalf("down length %d != %d", len(down), n)
		}
		back, ok := InverseOfList(down)
		if !ok {
			rt.Fatal("inverting an all-mechanical down must be defined")
		}
		if len(back) != n {
			rt.Fatalf("double-inverse length %d != %d", len(back), n)
		}
		for i := range ops {
			if !opFacetsEqual(back[i], ops[i]) {
				rt.Fatalf("double-inverse changed op %d", i)
			}
		}
	})
}

// TestDeclaredInverseRangeHasNoManifestDiffDown is the concrete L4 boundary
// case: a range containing a declared-inverse (e.g. vacuous DML) op has a
// composite inverse (InverseOfList succeeds) but NO manifest-diff down BY TYPE
// (NewMechanicalRange refuses it). This is the distinction the whole rollback
// design rests on.
func TestDeclaredInverseRangeHasNoManifestDiffDown(t *testing.T) {
	testenv.Isolate(t)
	tgt := enc.Key{Kind: enc.KindTable, Schema: "public", Name: "orders"}
	mech := mkInvertiblePair("add_column", tgt, "aaaa", MechanicallyInvertible)
	dml := mkInvertiblePair("backfill", tgt, "bbbb", DeclaredInverse) // vacuous DML inverse
	ops := []Op{mech, dml}

	if _, ok := InverseOfList(ops); !ok {
		t.Fatal("a declared-inverse-containing list should still have a composite inverse")
	}
	if _, err := NewMechanicalRange(ops); !errors.Is(err, ErrNotFullyMechanical) {
		t.Fatalf("a declared-inverse op must block a MechanicalRange, got err=%v", err)
	}
}

// TestNonInvertibleRangeHasNeither confirms a non-invertible op blocks both the
// composite inverse and the mechanical range.
func TestNonInvertibleRangeHasNeither(t *testing.T) {
	testenv.Isolate(t)
	tgt := enc.Key{Kind: enc.KindTable, Schema: "public", Name: "audit"}
	bad := mockOp{kind: "drop_with_data", target: tgt, class: NonInvertible, payload: "cccc"}
	ops := []Op{bad}
	if _, ok := InverseOfList(ops); ok {
		t.Fatal("a non-invertible op must make the composite inverse undefined")
	}
	if _, err := NewMechanicalRange(ops); err == nil {
		t.Fatal("a non-invertible op must block a MechanicalRange")
	}
}
