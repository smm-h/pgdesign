package migrate

// Chain-on-disk project layout (roadmap 5.2, store_layout.md).
//
// A ChainProject is the migrations/ root and its four visible (non-dot) store
// roots for committed, load-bearing data:
//
//	migrations/objects/     content-addressed object store (objstore.Store root)
//	migrations/revisions/   whole-model revision manifests (one file per revision)
//	migrations/chain/       edge artifacts (one file per LIVE edge; edge_format.md)
//	migrations/archive/     retired originals (squash-superseded / rebased-away)
//
// The object store is bound to the current codec epoch (enc.CodecVersion); reads
// through it verify the epoch, so a store written under a different codec is a
// hard error, never a silent mis-decode (objstore's EpochMismatch). The
// consistency checker adds epoch homogeneity ACROSS edge files on top of that.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
)

const (
	chainObjectsDir   = "objects"
	chainRevisionsDir = "revisions"
	chainEdgesDir     = "chain"
	chainArchiveDir   = "archive"
)

// ChainProject holds the migrations/ root and its content-addressed object
// store. Edge and revision-manifest files are read/written relative to it.
type ChainProject struct {
	root  string
	store *objstore.Store
}

// OpenChainProject opens (creating if necessary) the chain-on-disk layout under
// migrationsDir: the objstore root plus the revisions/, chain/, and archive/
// directories. The object store is bound to the current codec epoch.
func OpenChainProject(migrationsDir string) (*ChainProject, error) {
	if migrationsDir == "" {
		return nil, fmt.Errorf("migrate: chain project root is required")
	}
	store, err := objstore.New(filepath.Join(migrationsDir, chainObjectsDir), enc.CodecVersion)
	if err != nil {
		return nil, err
	}
	p := &ChainProject{root: migrationsDir, store: store}
	for _, d := range []string{p.revisionsPath(), p.edgesPath(), p.archivePath()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("migrate: creating chain dir %q: %w", d, err)
		}
	}
	return p, nil
}

// Store returns the content-addressed object store.
func (p *ChainProject) Store() *objstore.Store { return p.store }

// Root returns the migrations/ root.
func (p *ChainProject) Root() string { return p.root }

func (p *ChainProject) revisionsPath() string { return filepath.Join(p.root, chainRevisionsDir) }
func (p *ChainProject) edgesPath() string     { return filepath.Join(p.root, chainEdgesDir) }
func (p *ChainProject) archivePath() string   { return filepath.Join(p.root, chainArchiveDir) }

// writeFileAtomic writes data to path via a same-directory temp file + rename, so
// a concurrent reader never observes a partial file. It is idempotent for
// content-addressed callers: writing identical bytes to an existing path is a
// no-op the OS makes atomic.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("migrate: creating dir for %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("migrate: temp file for %q: %w", path, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("migrate: writing %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("migrate: closing %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("migrate: committing %q: %w", path, err)
	}
	return nil
}
