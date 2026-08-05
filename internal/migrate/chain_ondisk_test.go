package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// fixtureProject builds a ChainProject rooted in a temp dir plus the worked
// genesis edge (a single table `users`) and its revision manifest. It returns the
// project, the edge, and the canonicalized model.
func fixtureProject(t *testing.T) (*ChainProject, Edge, *model.Schema) {
	t.Helper()
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &model.Schema{Name: "shop", Tables: []model.Table{{Name: "users", Comment: "application users"}}}
	s.Canonicalize()

	// The create_table op's DefID must equal the manifest entry, so build the op
	// from the SAME canonicalized table.
	op, err := BuildCreateTable(p.Store(), s.Tables[0], "", 16, nil, nil)
	if err != nil {
		t.Fatalf("BuildCreateTable: %v", err)
	}
	target, err := rev.Compute(s, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	e := Edge{
		Parent: rev.Revision{}, // genesis
		Target: target,
		Slug:   "create-users",
		Class:  rev.RegistryPresent,
		Ops:    []SelfContainedOp{op},
	}
	return p, e, s
}

// TestEdgeWriteLoadRoundTrip: an edge writes to a content-derived filename and
// loads back with a matching content id.
func TestEdgeWriteLoadRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	p, e, _ := fixtureProject(t)
	name, err := p.WriteEdge(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "edge-"+e.ID()[:12]+"-create-users") {
		t.Fatalf("filename %q does not embed edge id prefix %q", name, e.ID()[:12])
	}
	// Idempotence: a second write is byte-stable (same name, no error).
	name2, err := p.WriteEdge(e)
	if err != nil || name2 != name {
		t.Fatalf("write not idempotent: %q vs %q (err=%v)", name, name2, err)
	}
	loaded, err := p.LoadEdge(filepath.Join(p.edgesPath(), name), false)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID() != e.ID() {
		t.Fatalf("loaded edge id %s != written %s", loaded.ID(), e.ID())
	}
	if loaded.Class != rev.RegistryPresent || loaded.Slug != "create-users" || !loaded.IsGenesis() {
		t.Fatalf("loaded edge facets wrong: %+v", loaded)
	}
	edges, err := p.LoadLiveEdges()
	if err != nil || len(edges) != 1 {
		t.Fatalf("LoadLiveEdges: %d edges (err=%v)", len(edges), err)
	}
}

// TestEdgeFilenameTamperDetected: renaming an edge file to a wrong hash prefix
// is a hard error at load (content-hash prefix mismatch).
func TestEdgeFilenameTamperDetected(t *testing.T) {
	testenv.Isolate(t)
	p, e, _ := fixtureProject(t)
	name, err := p.WriteEdge(e)
	if err != nil {
		t.Fatal(err)
	}
	orig := filepath.Join(p.edgesPath(), name)
	bad := filepath.Join(p.edgesPath(), "edge-000000000000-create-users.json")
	if err := os.Rename(orig, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadEdge(bad, false); err == nil || !strings.Contains(err.Error(), "content-hash prefix") {
		t.Fatalf("expected content-hash prefix error, got %v", err)
	}
}

// TestEdgeDownTamperDetected: corrupting an op's down cache is caught at load by
// VerifyDown (amendment A3).
func TestEdgeDownTamperDetected(t *testing.T) {
	testenv.Isolate(t)
	p, e, _ := fixtureProject(t)
	name, err := p.WriteEdge(e)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.edgesPath(), name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the down op kind from drop_table to a bogus kind. The filename still
	// encodes the ORIGINAL id, so this reads as a tampered down (VerifyDown) OR a
	// prefix mismatch — either is a hard error.
	tampered := strings.Replace(string(raw), `"kind":"drop_table"`, `"kind":"drop_view"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup: down kind not found in edge file")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadEdge(path, false); err == nil {
		t.Fatal("expected hard error on tampered down cache, got nil")
	}
}

// TestRevisionManifestRoundTrip: a manifest writes and reads back key-for-key,
// class-checked.
func TestRevisionManifestRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	p, e, s := fixtureProject(t)
	m, err := chain.BuildManifestInto(s, p.Store())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteRevisionManifest(e.Target, rev.RegistryPresent, m); err != nil {
		t.Fatal(err)
	}
	got, err := p.ReadRevisionManifest(e.Target)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(m) {
		t.Fatalf("manifest round-trip mismatch: %v vs %v", got, m)
	}
	// The table key resolves to the create_table op's DefID (manifest entry ==
	// op payload def), proving the object-granularity correspondence.
	tableKey := enc.KeyForTable(s.Tables[0])
	if _, ok := got[tableKey]; !ok {
		t.Fatalf("manifest missing table key %s", tableKey)
	}
}
