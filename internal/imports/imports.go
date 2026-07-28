// Package imports implements cross-repository schema imports (roadmap 7.2): the
// surface snapshot, pinning, and offline drift check for another pgdesign
// project's schema referenced via an [imports.<alias>] declaration.
//
// The import surface is a sub-model under the kernel's encoder (internal/enc,
// law L1) and its content-addressed store (internal/objstore, law L2): the
// referenced tables plus the transitive composition-closure of their type
// definitions are vendored, each as its canonical per-object form, into a store
// rooted at imports/<alias>/. A lockfile (imports/<alias>/lock.json) pins the
// git URL, ref, resolved commit, per-object keys+ids, and two hashes:
//
//   - SurfaceHash — hash of the sorted per-object content ids. Integrity: it
//     changes iff the vendored bytes change. Stable across re-lock of the same
//     commit (identical bytes -> identical ids).
//   - SemanticHash — hash of the sorted N-normalized per-object forms. Drift:
//     it is INVARIANT under equivalently-spelled defaults (N folds them), so it
//     does not false-drift where SurfaceHash (raw ids) would. Roadmap 1.2's N
//     dependency made explicit.
//
// This package is pure aside from git plumbing (git.go) and the filesystem: it
// depends on config, parse, model, enc, objstore, sqlparse, typeinfo. It never
// touches a database — offline builds and the drift check never need the remote.
package imports

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LockfileName is the per-alias lockfile, co-located with the vendored objects
// under the alias's store root (imports/<alias>/lock.json). Per-alias placement
// (rather than one aggregate pgdesign.lock) keeps each import self-contained:
// the store root and its pin travel together, and `import update <alias>` writes
// exactly one directory. Visible (non-dot) name per the roadmap's [%%]
// committed-load-bearing-data convention.
const LockfileName = "lock.json"

// ObjectEntry pins one vendored surface object: its kind-qualified manifest key
// (enc.Key.String()) and its content id in the alias store.
type ObjectEntry struct {
	Key string `json:"key"`
	ID  string `json:"id"`
}

// Lockfile is the pinned, committed record of one import alias's vendored
// surface. It is written by `import lock`/`import update` and read by the
// offline `check --tag imports`.
type Lockfile struct {
	Alias        string        `json:"alias"`
	URL          string        `json:"url"`
	Ref          string        `json:"ref"`
	Commit       string        `json:"commit"`
	Schema       string        `json:"schema"`        // target PG schema the surface is stamped into
	SurfaceHash  string        `json:"surface_hash"`  // hash of sorted per-object content ids
	SemanticHash string        `json:"semantic_hash"` // hash of sorted N-normalized per-object forms
	PGVersion    int           `json:"pg_version"`    // framework pg_version floor (consumer re-declares >=; error wiring is 7.3)
	Extensions   []string      `json:"extensions"`    // inferred extension requirements (superset; per-object refinement is coarse in 7.2)
	Objects      []ObjectEntry `json:"objects"`
}

// AliasDir returns the store root directory for an alias under projectDir.
func AliasDir(projectDir, alias string) string {
	return filepath.Join(projectDir, "imports", alias)
}

// LockfilePath returns the lockfile path for an alias under projectDir.
func LockfilePath(projectDir, alias string) string {
	return filepath.Join(AliasDir(projectDir, alias), LockfileName)
}

// WriteLockfile writes lf to imports/<alias>/lock.json under projectDir,
// creating directories as needed. Objects are sorted by key so the on-disk form
// is deterministic (a committed, diff-stable artifact).
func WriteLockfile(projectDir string, lf *Lockfile) error {
	sort.Slice(lf.Objects, func(i, j int) bool { return lf.Objects[i].Key < lf.Objects[j].Key })
	sort.Strings(lf.Extensions)
	dir := AliasDir(projectDir, lf.Alias)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("imports: creating %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return fmt.Errorf("imports: marshaling lockfile for %q: %w", lf.Alias, err)
	}
	b = append(b, '\n')
	path := LockfilePath(projectDir, lf.Alias)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("imports: writing %s: %w", path, err)
	}
	return nil
}

// ReadLockfile reads and parses imports/<alias>/lock.json under projectDir.
func ReadLockfile(projectDir, alias string) (*Lockfile, error) {
	path := LockfilePath(projectDir, alias)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("imports: reading %s: %w", path, err)
	}
	var lf Lockfile
	if err := json.Unmarshal(b, &lf); err != nil {
		return nil, fmt.Errorf("imports: parsing %s: %w", path, err)
	}
	return &lf, nil
}

// LockfileExists reports whether an alias has a committed lockfile.
func LockfileExists(projectDir, alias string) bool {
	_, err := os.Stat(LockfilePath(projectDir, alias))
	return err == nil
}
