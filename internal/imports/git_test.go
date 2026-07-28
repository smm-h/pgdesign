package imports

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeGitRepo initializes a git repo at dir with one committed file and an
// annotated tag, returning the commit sha and tag name. Requires git on PATH.
func makeGitRepo(t *testing.T) (dir, commit, tag string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	runGit := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README")
	runGit("commit", "-q", "-m", "initial")
	runGit("tag", "v1")
	commit = ""
	{
		cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		commit = trim(string(out))
	}
	return dir, commit, "v1"
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

func TestCloneAt_ResolvesTagToCommit(t *testing.T) {
	src, commit, tag := makeGitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	got, err := CloneAt(src, tag, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got != commit {
		t.Errorf("resolved commit %q, want %q", got, commit)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Errorf("clone did not check out working tree: %v", err)
	}
}

func TestCloneAt_UnreachableRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dest := filepath.Join(t.TempDir(), "clone")
	_, err := CloneAt(filepath.Join(t.TempDir(), "does-not-exist"), "v1", dest)
	if err == nil {
		t.Fatal("expected error cloning nonexistent remote")
	}
}

func TestCloneAt_BadRef(t *testing.T) {
	src, _, _ := makeGitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	_, err := CloneAt(src, "no-such-ref", dest)
	if err == nil {
		t.Fatal("expected error for bad ref")
	}
}

func TestResolveRemoteRef_TagReachable(t *testing.T) {
	src, commit, tag := makeGitRepo(t)
	got, err := ResolveRemoteRef(src, tag)
	if err != nil {
		t.Fatal(err)
	}
	// ls-remote for a lightweight tag returns the tag's commit.
	if got != commit {
		t.Errorf("ls-remote resolved %q, want %q", got, commit)
	}
}

func TestResolveRemoteRef_Unreachable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := ResolveRemoteRef(filepath.Join(t.TempDir(), "nope"), "v1"); err == nil {
		t.Fatal("expected error for unreachable remote")
	}
}
