package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain installs a hermetic fake `safegit` on PATH for the whole cmd/pgdesign
// test package. Tests that exercise the auto-commit path (`revise`, which always
// commits; `build --auto-commit`) shell out to safegit via safegitCommit. The
// real safegit is a developer-machine tool that CI runners and other environments
// do not have, so relying on it made those tests fail wherever it was absent.
//
// The shim translates the SINGLE invocation form pgdesign uses —
// `safegit commit -m <msg> -- <paths...>` — into plain git. Any git failure (for
// example, running in a directory that is not a git repository) propagates as a
// non-zero exit, so the "commit failure is a hard error" tests still observe a
// failing commit exactly as they would with the real tool.
func TestMain(m *testing.M) {
	cleanup, err := installFakeSafegit()
	if err != nil {
		fmt.Fprintf(os.Stderr, "test setup: install fake safegit: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// installFakeSafegit writes an executable `safegit` shim into a temp dir and
// prepends that dir to PATH (leaving the real `git` reachable). It returns a
// cleanup func that restores PATH and removes the temp dir.
func installFakeSafegit() (func(), error) {
	dir, err := os.MkdirTemp("", "pgdesign-fake-safegit-")
	if err != nil {
		return nil, err
	}
	const script = `#!/bin/sh
set -e
[ "$1" = commit ] || { echo "fake-safegit: unsupported invocation: $*" >&2; exit 2; }
shift
msg=""
if [ "$1" = -m ]; then shift; msg="$1"; shift; fi
if [ "$1" = -- ]; then shift; fi
git add -- "$@"
git commit -q -m "$msg" -- "$@"
`
	shimPath := filepath.Join(dir, "safegit")
	if err := os.WriteFile(shimPath, []byte(script), 0o755); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	return func() {
		os.Setenv("PATH", oldPath)
		os.RemoveAll(dir)
	}, nil
}
