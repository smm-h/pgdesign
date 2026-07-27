package chain

import (
	"errors"
	"fmt"

	"github.com/smm-h/pgdesign/internal/enc"
)

// InvertibilityClass is L4's three-way typing of a primitive op's reversibility.
// It is the type-level fact the whole rollback/squash story rests on: whether an
// op can be reversed mechanically, only via a recorded declared inverse (which
// may be vacuous), or not at all.
type InvertibilityClass int

const (
	// MechanicallyInvertible: the inverse is derivable from the op's own
	// structure (e.g. ADD COLUMN <-> DROP COLUMN). These are the ONLY ops a
	// manifest-diff down may represent (see MechanicalRange).
	MechanicallyInvertible InvertibilityClass = iota
	// DeclaredInverse: the op carries a recorded inverse that is NOT derivable
	// from structure. This INCLUDES DML ops whose declared inverse is VACUOUS
	// (data is not restored — today's reversibility semantics, made explicit).
	DeclaredInverse
	// NonInvertible: the op has no inverse.
	NonInvertible
)

func (c InvertibilityClass) String() string {
	switch c {
	case MechanicallyInvertible:
		return "mechanically-invertible"
	case DeclaredInverse:
		return "declared-inverse"
	case NonInvertible:
		return "non-invertible"
	default:
		return fmt.Sprintf("invertibility(%d)", int(c))
	}
}

// valid reports whether c is one of the three defined classes.
func (c InvertibilityClass) valid() bool {
	return c == MechanicallyInvertible || c == DeclaredInverse || c == NonInvertible
}

// Op is the ABSTRACT migration op the kernel reasons about. Roadmap 5.1's
// concrete families (create table, add column, RawSQL, DML, ...) implement it;
// the kernel stays free of migrate imports. An op names its kind, its target
// object (a kind-qualified manifest key), its L4 invertibility class, and
// references its structured payload BY CONTENT ID into the object store
// (no lossy inline mirrors — L1+L2).
type Op interface {
	// Kind is the op-family name (e.g. "add_column").
	Kind() string
	// Target is the manifest key of the object the op acts on.
	Target() enc.Key
	// Invertibility is the op's L4 class.
	Invertibility() InvertibilityClass
	// PayloadID is the objstore content id of the op's structured payload
	// (or of a content-addressed opaque blob for RawSQL/DML bodies).
	PayloadID() string
	// Inverse returns the op's inverse and true when the op is
	// MechanicallyInvertible or DeclaredInverse; it returns (nil, false) exactly
	// when the op is NonInvertible. Implementations MUST keep the boolean in
	// agreement with Invertibility().
	Inverse() (Op, bool)
}

// InverseOfList returns the inverse of a composite op-list: the REVERSED
// composition of each component's inverse. It is defined WHEN AND ONLY WHEN
// every component has an inverse (mechanical or declared); if any component is
// NonInvertible it returns (nil, false).
//
// This is L4's conservative UNDER-approximation, stated in the roadmap: a
// composite CAN be semantically invertible when a component is not (chained
// type changes whose endpoint diff yields a clean structural down), but the
// kernel never assumes it — the manifest-diff down for such a range is
// unrepresentable (see MechanicalRange), and elsewhere recorded downs compose.
// An iff-form of this rule is a ruled-out design (false converse).
func InverseOfList(ops []Op) ([]Op, bool) {
	inv := make([]Op, len(ops))
	for i, op := range ops {
		io, ok := op.Inverse()
		if !ok {
			return nil, false
		}
		// Reversed composition: the inverse of (a then b) is (b^-1 then a^-1).
		inv[len(ops)-1-i] = io
	}
	return inv, true
}

// AllMechanicallyInvertible reports whether every op in the list is
// MechanicallyInvertible. It is the exact precondition NewMechanicalRange
// enforces.
func AllMechanicallyInvertible(ops []Op) bool {
	for _, op := range ops {
		if op.Invertibility() != MechanicallyInvertible {
			return false
		}
	}
	return true
}

// ErrNotFullyMechanical is returned by NewMechanicalRange when the op-list
// contains any op that is not MechanicallyInvertible.
var ErrNotFullyMechanical = errors.New("chain: op-list is not fully mechanically invertible; a manifest-diff down is not representable for it")

// MechanicalRange is a PROOF-CARRYING wrapper around an op-list whose EVERY op
// is MechanicallyInvertible. A manifest-diff down — reversing a range by diffing
// its endpoint manifests into structural inverse ops — is representable ONLY for
// such a range. The constructor REFUSES any list containing a declared-inverse
// (including vacuous DML) or non-invertible op, so the type system makes "a
// manifest-diff down over a data-bearing range" UNREPRESENTABLE by construction
// (L4). This forecloses the ruled-out "net manifest delta" trap: DROP populated
// column then ADD column has an empty net delta and destroys data — per-op
// typing, enforced here, is the correct criterion.
type MechanicalRange struct {
	ops []Op
}

// NewMechanicalRange constructs a MechanicalRange, returning
// ErrNotFullyMechanical unless EVERY op is MechanicallyInvertible. A caller who
// cannot build one must compose the components' recorded declared inverses
// instead (InverseOfList), never a manifest-diff down.
func NewMechanicalRange(ops []Op) (MechanicalRange, error) {
	for _, op := range ops {
		if !op.Invertibility().valid() {
			return MechanicalRange{}, fmt.Errorf("chain: op %q has invalid invertibility class %d", op.Kind(), int(op.Invertibility()))
		}
		if op.Invertibility() != MechanicallyInvertible {
			return MechanicalRange{}, fmt.Errorf("%w: op %q is %s", ErrNotFullyMechanical, op.Kind(), op.Invertibility())
		}
	}
	cp := make([]Op, len(ops))
	copy(cp, ops)
	return MechanicalRange{ops: cp}, nil
}

// Ops returns a copy of the range's ops.
func (r MechanicalRange) Ops() []Op {
	cp := make([]Op, len(r.ops))
	copy(cp, r.ops)
	return cp
}

// ManifestDiffDown yields the down op-list for a fully-mechanical range: the
// reversed composition of the components' mechanical inverses. Because every op
// is MechanicallyInvertible, each Inverse() succeeds, so the result is TOTAL —
// the boolean from InverseOfList can only be true here, which is precisely the
// invariant the MechanicalRange type guarantees. This method existing ONLY on
// MechanicalRange is what confines manifest-diff downs to fully-mechanical
// ranges.
func (r MechanicalRange) ManifestDiffDown() []Op {
	down, ok := InverseOfList(r.ops)
	if !ok {
		// Unreachable: the constructor guarantees all ops are mechanically
		// invertible, so every Inverse() returns ok. Panic rather than return a
		// silently-wrong empty down — this would signal a broken Op
		// implementation whose Inverse()/Invertibility() disagree.
		panic("chain: MechanicalRange contains a non-invertible op (Op implementation violates the Inverse/Invertibility contract)")
	}
	return down
}
