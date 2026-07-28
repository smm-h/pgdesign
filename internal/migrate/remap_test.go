package migrate

import (
	"testing"
)

const (
	remapRevA = "registry_present:29a14f632dbd1ab6a54a5b02007c51d6dceeb3b0366e79153d20f078ce0292be"
	remapRevB = "registry_present:db89cca8ab63d522331825cd00a77c84d31ca525d41a5433420bc801251dfdb3"
	remapRevC = "registry_present:5aef6eb2f05f051181db9e960e9431ccb6d94ba29eaf0d0f6a5d6a887269fcd2"
)

func TestLoadRemapAbsentIsEmpty(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remap, err := p.LoadRemap()
	if err != nil {
		t.Fatalf("LoadRemap on absent file: %v", err)
	}
	if len(remap) != 0 {
		t.Fatalf("absent remap should be empty, got %v", remap)
	}
}

func TestWriteThenLoadRemapRoundTrips(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteRemap(RemapTable{remapRevA: remapRevB}); err != nil {
		t.Fatalf("WriteRemap: %v", err)
	}
	got, err := p.LoadRemap()
	if err != nil {
		t.Fatalf("LoadRemap: %v", err)
	}
	if got[remapRevA] != remapRevB {
		t.Fatalf("remap[%s] = %q, want %q", remapRevA, got[remapRevA], remapRevB)
	}
}

func TestWriteRemapMergesAndDetectsCollision(t *testing.T) {
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteRemap(RemapTable{remapRevA: remapRevB}); err != nil {
		t.Fatalf("first WriteRemap: %v", err)
	}
	// A disjoint addition merges.
	if err := p.WriteRemap(RemapTable{remapRevC: remapRevB}); err != nil {
		t.Fatalf("merging WriteRemap: %v", err)
	}
	got, err := p.LoadRemap()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("merged remap should have 2 entries, got %d", len(got))
	}
	// A conflicting re-map of an existing key is a hard error.
	if err := p.WriteRemap(RemapTable{remapRevA: remapRevC}); err == nil {
		t.Fatal("expected collision error remapping an existing key to a different target")
	}
}

// TestCanonFollowsRemapToFixpoint pins that canon chases a chain of remaps to a
// fixpoint (a rebase over an already-rebased chain).
func TestCanonFollowsRemapToFixpoint(t *testing.T) {
	remap := RemapTable{remapRevA: remapRevB, remapRevB: remapRevC}
	if got := canon(remapRevA, remap); got != remapRevC {
		t.Fatalf("canon(%s) = %q, want %q (fixpoint)", remapRevA, got, remapRevC)
	}
	if got := canon(remapRevC, remap); got != remapRevC {
		t.Fatalf("canon of a terminal revision must be identity, got %q", got)
	}
	if got := canon("registry_present:ffff", RemapTable{}); got != "registry_present:ffff" {
		t.Fatalf("empty remap must be the identity, got %q", got)
	}
}
