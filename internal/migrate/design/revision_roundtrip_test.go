package design

import (
	"bytes"
	"encoding/json"
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/rev"
)

// This mechanical check pins the migrations/revisions/ file form (store_layout.md):
// a class-tagged, kind-qualified SORTED MAP of manifest-key -> object-id, plus
// the authoritative revision string and codec epoch. It proves the reconstruction
// path 5.9 relies on — "deserialize the head manifest via objstore" — by
// resolving each id back through the store, re-encoding, and re-deriving the
// revision, which must equal the recorded one CLASS-AWARE (the handoff note:
// chain.Manifest is class-blind, so the file MUST carry the class).

// revisionFile is the JSON shape of one file in migrations/revisions/.
type revisionFile struct {
	Revision string            `json:"revision"` // "<class>:<hex>" — authoritative rev.Revision.String()
	Class    string            `json:"class"`    // model class (registry_present | registry_absent)
	Codec    int               `json:"codec"`    // enc.CodecVersion at write time (epoch homogeneity)
	Entries  map[string]string `json:"entries"`  // Key.String() -> object-id, emitted in sorted order
}

func TestRevisionManifestRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	s := buildFixtureModel()
	store, err := objstore.New(t.TempDir(), enc.CodecVersion)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := chain.BuildManifestInto(s, store)
	if err != nil {
		t.Fatalf("BuildManifestInto: %v", err)
	}
	revision, err := rev.Compute(s, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Build the entries map keyed by Key.String() (collision-free across kinds).
	entries := make(map[string]string, len(manifest))
	for k, id := range manifest {
		entries[k.String()] = id
	}
	rf := revisionFile{
		Revision: revision.String(),
		Class:    string(rev.RegistryPresent),
		Codec:    enc.CodecVersion,
		Entries:  entries,
	}
	// encoding/json sorts map keys, so the serialized form is deterministic.
	got := compactJSON(t, rf)

	fixPath := filepath.Join("testdata", "revision-shop.json")
	if _, statErr := os.Stat(fixPath); os.IsNotExist(statErr) {
		if err := os.WriteFile(fixPath, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("seed fixture: %v", err)
		}
		t.Logf("seeded committed fixture %s (commit it)", fixPath)
	}
	raw, err := os.ReadFile(fixPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(bytes.TrimRight(raw, "\n"), got) {
		t.Fatalf("committed revision fixture is not the canonical serialization\n got:  %s\n want: %s", bytes.TrimRight(raw, "\n"), got)
	}

	// Reconstruction path (5.9): resolve every id via objstore, re-decode into a
	// fresh schema, re-derive the revision CLASS-AWARE, and assert it matches.
	var parsed revisionFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if parsed.Codec != enc.CodecVersion {
		t.Fatalf("fixture codec epoch %d != current %d (mixed-epoch chain)", parsed.Codec, enc.CodecVersion)
	}
	// Resolve in sorted key order (determinism) and decode each object.
	keys := make([]string, 0, len(parsed.Entries))
	for k := range parsed.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	objs := make(map[enc.Key][]byte, len(keys))
	for k, id := range parsed.Entries {
		content, err := store.Get(id)
		if err != nil {
			t.Fatalf("resolve %s (%s): %v", k, id, err)
		}
		_ = k
		// Re-key by decoding is unnecessary; DecodeObjects rebuilds the schema.
		objs[keyFromString(t, k)] = content
	}
	rebuilt, err := enc.DecodeObjects(objs)
	if err != nil {
		t.Fatalf("DecodeObjects: %v", err)
	}
	rebuilt.Canonicalize()
	class := rev.ModelClass(parsed.Class)
	reRev, err := rev.Compute(rebuilt, class)
	if err != nil {
		t.Fatalf("re-Compute: %v", err)
	}
	eq, err := reRev.Equal(revision)
	if err != nil {
		t.Fatalf("cross-class Equal error (class marker lost): %v", err)
	}
	if !eq {
		t.Fatalf("reconstructed revision %s != recorded %s", reRev, revision)
	}
	if reRev.String() != parsed.Revision {
		t.Fatalf("reconstructed revision string %s != fixture %s", reRev, parsed.Revision)
	}
}

// keyFromString parses an enc.Key back from its String() form for the kinds this
// fixture uses (schema-meta and table). enc.Key.String() is collision-free; this
// inverse is sufficient for the worked example (the production reader will build
// keys during decode, not re-parse strings).
func keyFromString(t *testing.T, s string) enc.Key {
	t.Helper()
	// forms: "schema:name", "table:name", "table:schema.name"
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			kind := enc.Kind(s[:i])
			rest := s[i+1:]
			if kind == enc.KindSchemaMeta {
				return enc.Key{Kind: kind, Name: rest}
			}
			// no schema qualifier in the worked example
			return enc.Key{Kind: kind, Name: rest}
		}
	}
	t.Fatalf("malformed key string %q", s)
	return enc.Key{}
}
