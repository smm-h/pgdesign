package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSafegitCommit_MissingBinaryActionableError verifies that when safegit is
// not on PATH the commit helper fails LOUDLY with an ACTIONABLE message: it names
// the missing tool, links where to get it, and points at --no-auto-commit so a
// user without safegit can still complete a build. Auto-commit is a by-design
// hard dependency, so this is a hard error (never a silent skip) — but the
// message must tell the user how to proceed.
func TestSafegitCommit_MissingBinaryActionableError(t *testing.T) {
	// Strip PATH to a directory that cannot contain safegit (the package's
	// TestMain installs a fake safegit on the real PATH; override it here).
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	f := filepath.Join(t.TempDir(), "artifact.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := safegitCommit("test", []string{f})
	if err == nil {
		t.Fatal("expected a hard error when safegit is absent, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"safegit not found", "--no-auto-commit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q, got: %v", want, msg)
		}
	}
}
