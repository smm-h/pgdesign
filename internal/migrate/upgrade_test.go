package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/extregistry"
)

// TestSyntheticPrefixEdgeID: distinct per version, deterministic across calls.
func TestSyntheticPrefixEdgeID(t *testing.T) {
	testenv.Isolate(t)
	a1 := syntheticPrefixEdgeID("0.1.0")
	a2 := syntheticPrefixEdgeID("0.1.0")
	b := syntheticPrefixEdgeID("0.2.0")
	if a1 != a2 {
		t.Errorf("synthetic edge id not deterministic: %q != %q", a1, a2)
	}
	if a1 == b {
		t.Errorf("synthetic edge ids for distinct versions collided: %q", a1)
	}
}

// TestWriteChainFilesConsistencyGreen: the prefix fold produces a genesis edge,
// its objects, and its to-revision manifest, and the consistency checker passes
// over the written store (no database needed).
func TestWriteChainFilesConsistencyGreen(t *testing.T) {
	testenv.Isolate(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := twoTableDesired()
	name, target, err := writeChainFiles(p, desired, extregistry.NewBuiltinRegistry())
	if err != nil {
		t.Fatalf("writeChainFiles: %v", err)
	}
	if name == "" || target.IsZero() {
		t.Fatalf("expected a written edge and non-zero target, got name=%q target=%v", name, target)
	}
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("consistency check red after prefix fold: %v", err)
	}

	// Idempotent: a second fold writes byte-identical files and yields the same
	// target revision.
	name2, target2, err := writeChainFiles(p, desired, extregistry.NewBuiltinRegistry())
	if err != nil {
		t.Fatalf("writeChainFiles (re-run): %v", err)
	}
	if name2 != name || target2.String() != target.String() {
		t.Errorf("prefix fold not idempotent: (%q,%s) vs (%q,%s)", name, target, name2, target2)
	}

	// The single live head is the boundary revision.
	head, _, err := ChainHead(p)
	if err != nil {
		t.Fatalf("ChainHead: %v", err)
	}
	if head.String() != target.String() {
		t.Errorf("head %s != boundary %s", head, target)
	}
}

// TestCheckCleanSchemaFiles exercises the dirty-tree guard: a committed file is
// clean, a modified file is dirty (refusal), and a non-repo path is a caveat
// (proceeds).
func TestCheckCleanSchemaFiles(t *testing.T) {
	testenv.Isolate(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	schemaFile := filepath.Join(dir, "schema.toml")
	if err := os.WriteFile(schemaFile, []byte("# schema\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "schema.toml")
	run("commit", "-m", "add schema")

	// Committed and unmodified: clean.
	if err := checkCleanSchemaFiles([]string{schemaFile}); err != nil {
		t.Errorf("committed schema should be clean, got %v", err)
	}

	// Modified: dirty -> refusal.
	if err := os.WriteFile(schemaFile, []byte("# schema edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkCleanSchemaFiles([]string{schemaFile}); err == nil {
		t.Error("modified schema should be refused as dirty")
	}

	// A path outside any git repo: caveat, proceeds.
	outside := filepath.Join(t.TempDir(), "loose.toml")
	if err := os.WriteFile(outside, []byte("# loose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkCleanSchemaFiles([]string{outside}); err != nil {
		t.Errorf("non-repo path should proceed (caveat), got %v", err)
	}
}
