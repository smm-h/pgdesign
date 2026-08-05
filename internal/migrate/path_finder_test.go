package migrate

import (
	"errors"
	"fmt"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/rev"
)

// revAt returns a distinct valid registry-present revision for a small integer.
func revAt(t *testing.T, n int) rev.Revision {
	t.Helper()
	r, err := rev.ParseRevision(fmt.Sprintf("registry_present:%064x", n))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// edgeOf builds an in-memory edge (no ops needed for graph reasoning).
func edgeOf(parent, target rev.Revision, slug string) Edge {
	return Edge{Parent: parent, Target: target, Slug: slug, Class: rev.RegistryPresent}
}

func ids(edges []Edge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = e.ID()
	}
	return out
}

func TestFindPathLinear(t *testing.T) {
	testenv.Isolate(t)
	r0, r1, r2 := revAt(t, 0), revAt(t, 1), revAt(t, 2)
	e1 := edgeOf(r0, r1, "e1")
	e2 := edgeOf(r1, r2, "e2")
	live := []Edge{e1, e2}

	// From R0: apply both.
	path, err := FindPath(r0.String(), nil, live, live)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(path), []string{e1.ID(), e2.ID()}; !equalSeq(got, want) {
		t.Fatalf("from R0: %v, want %v", got, want)
	}
	// From R1: apply e2 only.
	path, err = FindPath(r1.String(), nil, live, live)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(path), []string{e2.ID()}; !equalSeq(got, want) {
		t.Fatalf("from R1: %v, want %v", got, want)
	}
	// From R2 (head): up to date.
	path, err = FindPath(r2.String(), nil, live, live)
	if err != nil || len(path) != 0 {
		t.Fatalf("from R2 (head): path=%v err=%v, want empty/nil", ids(path), err)
	}
	// Off-chain position: NoPathError.
	_, err = FindPath(revAt(t, 99).String(), nil, live, live)
	var np *NoPathError
	if !errors.As(err, &np) {
		t.Fatalf("off-chain: want NoPathError, got %v", err)
	}
}

func TestFindPathGenesisStart(t *testing.T) {
	testenv.Isolate(t)
	r0, r1 := revAt(t, 0), revAt(t, 1)
	e0 := edgeOf(rev.Revision{}, r0, "genesis") // null parent
	e1 := edgeOf(r0, r1, "e1")
	live := []Edge{e0, e1}
	path, err := FindPath("", nil, live, live) // never-stamped DB
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(path), []string{e0.ID(), e1.ID()}; !equalSeq(got, want) {
		t.Fatalf("from genesis: %v, want %v", got, want)
	}
}

func TestFindPathFork(t *testing.T) {
	testenv.Isolate(t)
	r0, r1, r2 := revAt(t, 0), revAt(t, 1), revAt(t, 2)
	live := []Edge{edgeOf(r0, r1, "a"), edgeOf(r0, r2, "b")}
	_, err := FindPath(r0.String(), nil, live, live)
	var fe *ForkError
	if !errors.As(err, &fe) {
		t.Fatalf("fork: want ForkError, got %v", err)
	}
	if len(fe.Heads) != 2 {
		t.Fatalf("fork heads: %v", fe.Heads)
	}
}

// TestFindPathTieBreakLexicographic: two parallel length-1 paths to one head are
// disambiguated by the lexicographically-least edge id (rule 4b, total order).
func TestFindPathTieBreakLexicographic(t *testing.T) {
	testenv.Isolate(t)
	r0, r1 := revAt(t, 0), revAt(t, 1)
	ea := edgeOf(r0, r1, "alpha")
	eb := edgeOf(r0, r1, "beta")
	live := []Edge{ea, eb}
	path, err := FindPath(r0.String(), nil, live, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 1 {
		t.Fatalf("tie-break path len %d, want 1", len(path))
	}
	want := ea.ID()
	if eb.ID() < ea.ID() {
		want = eb.ID()
	}
	if path[0].ID() != want {
		t.Fatalf("tie-break chose %s, want lexicographically-least %s", path[0].ID(), want)
	}
}

// TestFindPathArchiveInclusive: a consolidation edge wins by shortest count from
// its range start; a mid-range DB traverses the archived originals.
func TestFindPathArchiveInclusive(t *testing.T) {
	testenv.Isolate(t)
	r0, r1, r2 := revAt(t, 0), revAt(t, 1), revAt(t, 2)
	consol := edgeOf(r0, r2, "consolidated")
	consol.Consolidation = true
	e1 := edgeOf(r0, r1, "e1")
	e2 := edgeOf(r1, r2, "e2")
	e1.Archived, e2.Archived = true, true
	live := []Edge{consol}
	all := []Edge{consol, e1, e2}

	// From R0: the consolidation edge (1 edge) beats the originals (2).
	path, err := FindPath(r0.String(), nil, live, all)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(path), []string{consol.ID()}; !equalSeq(got, want) {
		t.Fatalf("from R0: %v, want consolidation %v", got, want)
	}
	// From R1 (mid-range): consolidation's parent R0 != R1, so traverse archive.
	path, err = FindPath(r1.String(), nil, live, all)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(path), []string{e2.ID()}; !equalSeq(got, want) {
		t.Fatalf("from R1: %v, want archived e2 %v", got, want)
	}
}

// TestFindPathRebasedAwayServed: a database stamped at a rebased-away revision is
// canonicalized through the remap to the live head and served (up to date).
func TestFindPathRebasedAwayServed(t *testing.T) {
	testenv.Isolate(t)
	r0, r1, r2old, r3 := revAt(t, 0), revAt(t, 1), revAt(t, 2), revAt(t, 3)
	e1 := edgeOf(r0, r1, "e1")
	e2p := edgeOf(r1, r3, "e2prime") // re-parented tail (live)
	e2 := edgeOf(r1, r2old, "e2")    // rebased-away original (archived)
	e2.Archived = true
	live := []Edge{e1, e2p}
	all := []Edge{e1, e2p, e2}
	remap := RemapTable{r2old.String(): r3.String()}

	path, err := FindPath(r2old.String(), remap, live, all)
	if err != nil {
		t.Fatalf("rebased-away served: %v", err)
	}
	if len(path) != 0 {
		t.Fatalf("rebased-away DB should be up to date (R3 is head), got %v", ids(path))
	}
	_ = e2p

	// A mid-range DB at R1 reaches the live head R3. NOTE: because traversal
	// canonicalizes the archived e2's target (R2old) through the remap to R3, the
	// archived e2 ALSO reads as a length-1 R1->R3 path, tying with the live e2'.
	// Rules 3/4a/4b then pick the lexicographically-least edge id — a surfaced
	// tension: the path-finder worked case implies live-preference, but the pinned
	// rules do not encode it. The chosen edge is deterministic and total.
	path, err = FindPath(r1.String(), remap, live, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 1 {
		t.Fatalf("from R1 post-rebase: want 1 edge, got %v", ids(path))
	}
	least := e2p.ID()
	if e2.ID() < least {
		least = e2.ID()
	}
	if path[0].ID() != least {
		t.Fatalf("from R1 post-rebase: chose %s, want lexicographically-least %s", path[0].ID(), least)
	}
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
