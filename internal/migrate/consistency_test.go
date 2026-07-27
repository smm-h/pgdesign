package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/rev"
)

// writeControlledGenesis builds a controlled to-manifest containing exactly the
// create_table op's target -> def id, so the OpSimulator (empty -> [create_table])
// reproduces it exactly. This unit-tests the checker wiring at object granularity
// with a single-object manifest (no schema-meta op needed because the manifest is
// deliberately restricted to the one key the op produces).
func writeControlledGenesis(t *testing.T, p *ChainProject, e Edge) chain.Manifest {
	t.Helper()
	tableKey := e.Ops[0].target
	m := chain.Manifest{tableKey: e.Ops[0].payloadDefID(t, p)}
	if err := p.WriteRevisionManifest(e.Target, rev.RegistryPresent, m); err != nil {
		t.Fatal(err)
	}
	if _, err := p.WriteEdge(e); err != nil {
		t.Fatal(err)
	}
	return m
}

// payloadDefID resolves an op's payload def id (the manifest entry it produces).
func (o SelfContainedOp) payloadDefID(t *testing.T, p *ChainProject) string {
	t.Helper()
	body, err := loadBody(p.Store(), o.payload)
	if err != nil {
		t.Fatal(err)
	}
	return body.DefID
}

func TestConsistencyPasses(t *testing.T) {
	p, e, _ := fixtureProject(t)
	writeControlledGenesis(t, p, e)
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("expected consistent chain, got %v", err)
	}
}

// TestConsistencyEndpointMismatch: a to-manifest whose table id differs from what
// the op produces makes the edge-endpoint check red.
func TestConsistencyEndpointMismatch(t *testing.T) {
	p, e, _ := fixtureProject(t)
	if _, err := p.WriteEdge(e); err != nil {
		t.Fatal(err)
	}
	// Store a bogus object so its id resolves (closure passes) but differs from
	// the op's def id (endpoint fails).
	bogus, err := p.Store().Put([]byte("bogus-object-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	m := chain.Manifest{e.Ops[0].target: bogus}
	if err := p.WriteRevisionManifest(e.Target, rev.RegistryPresent, m); err != nil {
		t.Fatal(err)
	}
	err = VerifyChainConsistency(p)
	if err == nil || !strings.Contains(err.Error(), "do not map its from-manifest to its to-manifest") {
		t.Fatalf("expected endpoint mismatch error, got %v", err)
	}
}

// TestConsistencyClosureViolation: a manifest referencing an object not in the
// store is a closure violation.
func TestConsistencyClosureViolation(t *testing.T) {
	p, e, _ := fixtureProject(t)
	if _, err := p.WriteEdge(e); err != nil {
		t.Fatal(err)
	}
	m := chain.Manifest{e.Ops[0].target: strings.Repeat("0", 64)} // dangling id
	if err := p.WriteRevisionManifest(e.Target, rev.RegistryPresent, m); err != nil {
		t.Fatal(err)
	}
	err := VerifyChainConsistency(p)
	if err == nil || !strings.Contains(err.Error(), "closure violation") {
		t.Fatalf("expected closure violation, got %v", err)
	}
}

// TestConsistencyMixedEpochRejected: an edge file carrying a foreign codec epoch
// is rejected (mixed-epoch = corruption).
func TestConsistencyMixedEpochRejected(t *testing.T) {
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
	tampered := strings.Replace(string(raw),
		`"codec":`+itoa(enc.CodecVersion), `"codec":999`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup: codec field not found")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChainConsistency(p); err == nil {
		t.Fatal("expected mixed-epoch/codec error, got nil")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
