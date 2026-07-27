package migrate

import (
	"errors"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/rev"
)

// childEdgeAt builds a synthetic non-genesis edge parented at parentRev. Its
// Target is irrelevant to parentModelForEdge (which reads only Parent), so it
// reuses parentRev; the edge carries no ops.
func childEdgeAt(parentRev rev.Revision) Edge {
	return Edge{
		Parent: parentRev,
		Target: parentRev,
		Slug:   "child",
		Class:  rev.RegistryPresent,
	}
}

// TestParentModelForEdge_GenesisHasNoParent: a genesis edge yields (nil, nil) —
// existence-only checks, no recorded pre-state, no error.
func TestParentModelForEdge_GenesisHasNoParent(t *testing.T) {
	p, e, _ := fixtureProject(t) // e is genesis (Parent == zero revision)
	from, err := parentModelForEdge(p, e)
	if err != nil {
		t.Fatalf("genesis edge: unexpected error: %v", err)
	}
	if from != nil {
		t.Fatalf("genesis edge: expected nil parent model, got %+v", from)
	}
}

// TestParentModelForEdge_ManifestAbsentIsExistenceOnly: a non-genesis edge whose
// parent revision manifest was never written yields (nil, nil). Absence of the
// recorded pre-state selects the existence-only mode; it is NOT an error.
func TestParentModelForEdge_ManifestAbsentIsExistenceOnly(t *testing.T) {
	p, genesis, _ := fixtureProject(t)
	// A child edge parented at the genesis target, but that revision's manifest
	// is never written to disk.
	child := childEdgeAt(genesis.Target)
	from, err := parentModelForEdge(p, child)
	if err != nil {
		t.Fatalf("absent manifest: expected no error (existence-only mode), got: %v", err)
	}
	if from != nil {
		t.Fatalf("absent manifest: expected nil parent model, got %+v", from)
	}
}

// TestParentModelForEdge_BrokenManifestIsHardError: a non-genesis edge whose
// parent manifest is PRESENT but references an object id absent from the store is
// STORE CORRUPTION and must be a HARD ERROR — never silently degraded to an
// existence-only check (which would mask real drift by trusting a corrupt store).
// This is the red half of rider 4: the pre-fix code swallowed every reconstruct
// error into a nil return.
func TestParentModelForEdge_BrokenManifestIsHardError(t *testing.T) {
	p, genesis, _ := fixtureProject(t)
	parentRev := genesis.Target

	// Write a PRESENT parent manifest that points at an object id the store does
	// not contain (a dangling reference — the corruption we must not silence).
	broken := chain.Manifest{
		enc.Key{Kind: enc.KindTable, Schema: "public", Name: "ghost"}: strings.Repeat("de", 32),
	}
	if err := p.WriteRevisionManifest(parentRev, rev.RegistryPresent, broken); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	child := childEdgeAt(parentRev)
	from, err := parentModelForEdge(p, child)
	if err == nil {
		t.Fatalf("broken manifest: expected a hard error, got parent model %+v", from)
	}
	if from != nil {
		t.Fatalf("broken manifest: expected nil model on error, got %+v", from)
	}
	if errors.Is(err, ErrRevisionManifestNotFound) {
		t.Fatalf("broken manifest must not be classified as not-found: %v", err)
	}
	if !strings.Contains(err.Error(), "reconstruction failed") {
		t.Fatalf("broken manifest error should name the reconstruction failure, got: %v", err)
	}
}
