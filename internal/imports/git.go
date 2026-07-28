package imports

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Git plumbing for import fetches (roadmap boundary item 13). This is the ONE
// place import resolution touches the network. Every failure is surfaced loudly
// with the git stderr attached — an unreachable remote, a bad ref, or a missing
// git binary is a hard error, never a silent skip. Offline builds and the drift
// check never call any of this: they read the vendored surface only.

// gitError wraps a failed git invocation with its captured stderr so the caller
// can report exactly why the fetch failed.
func gitError(op string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("imports: git %s failed: %w", op, err)
	}
	return fmt.Errorf("imports: git %s failed: %w: %s", op, err, msg)
}

// CheckGitAvailable returns a hard error if the git binary is not on PATH. Import
// lock/update require git; the absence of git is a loud failure, not a fallback.
func CheckGitAvailable() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("imports: git is required for import lock/update but was not found on PATH: %w", err)
	}
	return nil
}

// ResolveRemoteRef probes the remote with `git ls-remote` to confirm reachability
// and that ref exists, returning the resolved commit sha when ls-remote reports
// one. A ref that ls-remote does not list (e.g. a bare commit sha, which
// ls-remote never returns) yields ("", nil) — reachability succeeded but the ref
// must be resolved by CloneAt. A non-zero git exit (unreachable/auth) is a hard
// error naming the remote.
func ResolveRemoteRef(url, ref string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", "ls-remote", url, ref)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", gitError(fmt.Sprintf("ls-remote %s %s (remote unreachable or authentication failed)", url, ref), err, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
	if line == "" {
		return "", nil
	}
	// Format: "<sha>\t<refname>" (possibly multiple lines; take the first).
	first := strings.SplitN(line, "\n", 2)[0]
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// CloneAt clones url into dest and checks out ref, returning the resolved commit
// sha (rev-parse HEAD). It handles tags, branches, and bare commit shas uniformly
// via a full clone + checkout. A clone failure (unreachable remote) or a checkout
// failure (bad ref) is a hard error with git's stderr attached.
func CloneAt(url, ref, dest string) (string, error) {
	if err := run(dest, "clone", "--quiet", url, dest); err != nil {
		return "", err
	}
	if err := run(dest, "-C", dest, "checkout", "--quiet", ref); err != nil {
		return "", fmt.Errorf("%w (ref %q not found in %s)", err, ref, url)
	}
	return revParse(dest)
}

// run executes a git subcommand, attaching stderr on failure. label names the op
// for the error message; the actual args follow.
func run(label string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return gitError(strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// revParse returns the commit sha of HEAD in the working tree at dest.
func revParse(dest string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", "-C", dest, "rev-parse", "HEAD")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", gitError("rev-parse HEAD", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}
