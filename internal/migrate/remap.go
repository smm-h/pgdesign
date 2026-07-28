package migrate

// The rebase revision-remap table (roadmap 5.10, store_layout.md § remap).
//
// A REBASE-ONLY on-disk chain artifact (migrations/remap.json) mapping a
// rebased-away revision's String() form to its live re-parented revision's
// String() form. It is NOT a database structure and NOT part of
// pgdesign_chain_position. Outside a rebase it is empty/absent.
//
// The path-finder consults it (canon) so a database stamped at a rebased-away
// revision is SERVED FORWARD to the live head, never orphaned. rebase (5.10)
// writes it; apply and the consistency checker read it.
//
// The file uses the same canonical byte discipline as the edge / revision-manifest
// artifacts (canonicalOpJSON), so identical remaps serialize byte-identically and
// git never sees a spurious conflict. A missing file is an EMPTY remap (the
// identity), never an error — a chain that has never been rebased has no remap.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/rev"
)

const chainRemapFile = "remap.json"

// remapFileJSON is the on-disk remap body. entries maps a rebased-away revision
// string to its live re-parented revision string.
type remapFileJSON struct {
	FormatVersion int               `json:"format_version"`
	Codec         int               `json:"codec"`
	Entries       map[string]string `json:"entries"`
}

func (p *ChainProject) remapPath() string { return filepath.Join(p.root, chainRemapFile) }

// LoadRemap reads migrations/remap.json and returns the rebase remap table. A
// missing file is an EMPTY table (the identity), never an error. The file's
// format/codec framing is verified against this build; the entries are validated
// as parseable, same-epoch revision strings so a corrupt remap is a hard error,
// never a silent mis-canonicalization.
func (p *ChainProject) LoadRemap() (RemapTable, error) {
	raw, err := os.ReadFile(p.remapPath())
	if err != nil {
		if os.IsNotExist(err) {
			return RemapTable{}, nil
		}
		return nil, fmt.Errorf("migrate: reading remap %q: %w", p.remapPath(), err)
	}
	var f remapFileJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("migrate: parsing remap %q: %w", p.remapPath(), err)
	}
	if f.FormatVersion != rev.FormatVersion {
		return nil, fmt.Errorf("migrate: remap %q format_version %d, want %d", p.remapPath(), f.FormatVersion, rev.FormatVersion)
	}
	if f.Codec != enc.CodecVersion {
		return nil, fmt.Errorf("migrate: remap %q codec epoch %d, want %d", p.remapPath(), f.Codec, enc.CodecVersion)
	}
	out := make(RemapTable, len(f.Entries))
	for from, to := range f.Entries {
		if _, err := rev.ParseRevision(from); err != nil {
			return nil, fmt.Errorf("migrate: remap %q: from-revision %q: %w", p.remapPath(), from, err)
		}
		if _, err := rev.ParseRevision(to); err != nil {
			return nil, fmt.Errorf("migrate: remap %q: to-revision %q: %w", p.remapPath(), to, err)
		}
		out[from] = to
	}
	return out, nil
}

// WriteRemap merges additions into the on-disk remap and writes it back. rebase
// calls it with the rebased-away -> live-re-parented mappings. Merging (rather
// than overwriting) makes successive rebases accumulate: a second rebase over an
// already-remapped chain never loses the first rebase's served-forward mappings.
// A collision that maps an existing key to a DIFFERENT target is a hard error
// (never silently overwrite a served-forward mapping). The write is idempotent for
// identical content.
func (p *ChainProject) WriteRemap(additions RemapTable) error {
	if len(additions) == 0 {
		return nil
	}
	existing, err := p.LoadRemap()
	if err != nil {
		return err
	}
	merged := make(map[string]string, len(existing)+len(additions))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range additions {
		if prev, ok := merged[k]; ok && prev != v {
			return fmt.Errorf("migrate: remap collision: revision %s already maps to %s, refusing to remap it to %s", k, prev, v)
		}
		merged[k] = v
	}
	f := remapFileJSON{
		FormatVersion: rev.FormatVersion,
		Codec:         enc.CodecVersion,
		Entries:       merged,
	}
	data, err := canonicalOpJSON(f)
	if err != nil {
		return fmt.Errorf("migrate: encode remap: %w", err)
	}
	return writeFileAtomic(p.remapPath(), append(data, '\n'))
}
