package migrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSingleTrackingWritePath enforces the single-write-path invariant of
// design/tracking_write_path.md: the chain-era tracking structures
// (pgdesign_migration_ops, pgdesign_chain_position) are written ONLY through the
// dedicated journal writer in tracking_chain.go, plus the one-time upgrade fold in
// upgrade.go. In particular the apply loop (apply_chain.go) carries NO stray
// inline INSERT/UPDATE against the tracking structures — every tracking write goes
// through a named writer helper.
//
// A new file issuing such a write turns this test red until the write is routed
// through the writer (or the file is added to the reviewed allow-list here).
func TestSingleTrackingWritePath(t *testing.T) {
	// tracking_chain.go: the journal writer (intent/confirm + chain_position).
	// upgrade.go: the one-time legacy->chain fold (5.2 choreography, one bulk INSERT
	// in a single verify-then-stamp transaction).
	allowed := map[string]bool{
		"tracking_chain.go": true,
		"upgrade.go":        true,
	}

	// Any INSERT/UPDATE that targets a chain tracking structure.
	write := regexp.MustCompile(`(?i)(INSERT\s+INTO|UPDATE)\s+pgdesign_(migration_ops|chain_position)`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if write.Match(data) && !allowed[name] {
			t.Errorf("%s issues an inline write to a chain tracking structure; route it through the tracking_chain.go writer (single write path)", name)
		}
	}
}
