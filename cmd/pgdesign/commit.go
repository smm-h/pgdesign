package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// safegitCommit is the ONE shared commit helper for every pgdesign command that
// writes and then commits generated artifacts (build, revise). It runs
// `safegit commit -m <message> -- <paths...>`, which handles tracked and
// untracked files and is concurrency-safe across sessions sharing a worktree.
//
// COMMIT FAILURE IS A HARD ERROR (roadmap 6.1): a failed commit leaves generated
// outputs on disk but out of version control, silently diverging the repo from
// the revision the artifacts claim. Callers MUST propagate the returned error as
// a non-zero exit — never warn-and-continue.
//
// An empty paths slice is a no-op (nil): there is nothing to commit.
func safegitCommit(message string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"commit", "-m", message, "--"}, paths...)
	cmd := exec.Command("safegit", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("safegit not found on PATH: pgdesign auto-commits generated outputs with safegit. Install it (https://github.com/smm-h/safegit), or re-run `pgdesign build` with --no-auto-commit to leave the generated files uncommitted in the working tree: %w", err)
		}
		return fmt.Errorf("safegit commit failed: %w", err)
	}
	return nil
}
