package migrate

// On-disk revision manifests (roadmap 5.2, store_layout.md § migrations/revisions/).
//
// One file per revision: a class-tagged, kind-qualified SORTED MAP of manifest
// key -> object-id, plus the authoritative revision string and codec epoch. The
// keys are enc.Key.String() (collision-free across kinds); JSON map encoding
// sorts keys, so the serialized form is deterministic. The map + the object store
// reconstruct the identical whole-model bytes (5.9's deserialize path), and the
// consistency checker resolves each id via the store (Merkle closure).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/rev"
)

// revisionFileJSON is the on-disk revision-manifest body (store_layout.md).
type revisionFileJSON struct {
	Revision string            `json:"revision"`
	Class    string            `json:"class"`
	Codec    int               `json:"codec"`
	Entries  map[string]string `json:"entries"`
}

// revisionFileName returns the content-derived filename for a revision manifest:
// revision-<hex[:12]>.json. The hex is a hash prefix; the file body carries the
// full class-tagged revision string and class marker.
func revisionFileName(r rev.Revision) string {
	return fmt.Sprintf("revision-%s.json", r.Hex()[:12])
}

// WriteRevisionManifest serializes manifest m (the revision r's key->id map)
// into migrations/revisions/ under its content-derived name. class must be valid
// and must equal r's class (L7). The write is idempotent.
func (p *ChainProject) WriteRevisionManifest(r rev.Revision, class rev.ModelClass, m chain.Manifest) error {
	if !validModelClass(class) {
		return fmt.Errorf("migrate: revision manifest has unknown model class %q", class)
	}
	if r.Class() != class {
		return fmt.Errorf("migrate: revision %q class %q != declared class %q", r, r.Class(), class)
	}
	entries := make(map[string]string, len(m))
	for k, id := range m {
		entries[k.String()] = id
	}
	f := revisionFileJSON{
		Revision: r.String(),
		Class:    string(class),
		Codec:    enc.CodecVersion,
		Entries:  entries,
	}
	data, err := canonicalOpJSON(f)
	if err != nil {
		return fmt.Errorf("migrate: encode revision manifest: %w", err)
	}
	return writeFileAtomic(filepath.Join(p.revisionsPath(), revisionFileName(r)), append(data, '\n'))
}

// ReadRevisionManifest reads the manifest file for revision r and reconstructs it
// as a chain.Manifest (enc.Key -> object-id). It verifies the file's revision
// string and class match r (L7 class-awareness). A missing file is reported so
// callers can distinguish "not written" from a parse error.
func (p *ChainProject) ReadRevisionManifest(r rev.Revision) (chain.Manifest, error) {
	path := filepath.Join(p.revisionsPath(), revisionFileName(r))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("migrate: revision manifest for %s not found", r)
		}
		return nil, fmt.Errorf("migrate: reading revision manifest %q: %w", path, err)
	}
	var f revisionFileJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("migrate: parsing revision manifest %q: %w", path, err)
	}
	if f.Codec != enc.CodecVersion {
		return nil, fmt.Errorf("migrate: revision manifest %q codec epoch %d, want %d", path, f.Codec, enc.CodecVersion)
	}
	if f.Revision != r.String() {
		return nil, fmt.Errorf("migrate: revision manifest %q records revision %q, want %q", path, f.Revision, r.String())
	}
	if rev.ModelClass(f.Class) != r.Class() {
		return nil, fmt.Errorf("migrate: revision manifest %q class %q != revision class %q", path, f.Class, r.Class())
	}
	m := make(chain.Manifest, len(f.Entries))
	for ks, id := range f.Entries {
		k, err := enc.ParseKey(ks)
		if err != nil {
			return nil, fmt.Errorf("migrate: revision manifest %q: %w", path, err)
		}
		m[k] = id
	}
	return m, nil
}
