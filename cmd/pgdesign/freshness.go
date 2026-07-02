package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file holds the shared freshness and orphan-detection logic used by
// `pgdesign build`, `pgdesign check --tag build`, and `pgdesign codegen --check`.
//
// Ownership model: a multi-file codegen output owns its entire output
// directory. Every file found inside an owned directory must either be
// produced by the current configuration or be on the (deliberately tiny)
// ignore list; anything else is an orphan and a hard error. Orphans appear
// when a schema source file is renamed or an output's split_mode changes:
// without this check the files from the previous configuration stay on disk
// forever, committed and green.
//
// Ignore list: __pycache__ directories (and everything inside them) and
// *.pyc files. Nothing else is exempt.

// orphanExplanation tells the consumer what an orphan is and how to resolve it.
const orphanExplanation = "output directory is owned by pgdesign build; remove or relocate these files (only __pycache__/ and *.pyc are ignored)"

// orphanIgnored reports whether the slash-separated relative path rel is
// exempt from orphan detection: __pycache__ contents and *.pyc files.
func orphanIgnored(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == "__pycache__" {
			return true
		}
	}
	return strings.HasSuffix(rel, ".pyc")
}

// scanOrphans walks the owned output directory dir and returns the absolute
// paths of files that are neither in the owned set (slash-separated paths
// relative to dir) nor ignored. A directory that does not exist yields no
// orphans.
func scanOrphans(dir string, owned map[string]bool) ([]string, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	var orphans []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if orphanIgnored(rel) || owned[rel] {
			return nil
		}
		orphans = append(orphans, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(orphans)
	return orphans, nil
}

// scanAllOrphans runs scanOrphans over every owned directory and returns the
// combined sorted list of orphan paths.
func scanAllOrphans(ownedDirs map[string]map[string]bool) ([]string, error) {
	dirs := make([]string, 0, len(ownedDirs))
	for d := range ownedDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var all []string
	for _, d := range dirs {
		orphans, err := scanOrphans(d, ownedDirs[d])
		if err != nil {
			return nil, err
		}
		all = append(all, orphans...)
	}
	sort.Strings(all)
	return all, nil
}

// freshnessResult classifies planned files against their on-disk state.
type freshnessResult struct {
	Missing []string
	Stale   []string
	Fresh   []string
}

// compareFreshness compares each planned file against disk (byte-exact) and
// classifies it as missing, stale, or fresh. Result slices are sorted.
func compareFreshness(files map[string][]byte) freshnessResult {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var r freshnessResult
	for _, p := range paths {
		existing, err := os.ReadFile(p)
		switch {
		case err != nil:
			r.Missing = append(r.Missing, p)
		case !bytes.Equal(existing, files[p]):
			r.Stale = append(r.Stale, p)
		default:
			r.Fresh = append(r.Fresh, p)
		}
	}
	return r
}

// reportFreshness prints per-file [missing]/[stale]/[orphan] status lines
// (plus [fresh] lines unless quiet) and a summary to stderr, and returns 1
// if anything is missing, stale, or orphaned, 0 when everything is clean.
// Used by `codegen --check`.
func reportFreshness(files map[string][]byte, ownedDirs map[string]map[string]bool, quiet bool) int {
	fr := compareFreshness(files)
	orphans, err := scanAllOrphans(ownedDirs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: orphan scan: %v\n", err)
		return 1
	}
	for _, p := range fr.Missing {
		fmt.Fprintf(os.Stderr, "[missing] %s\n", p)
	}
	for _, p := range fr.Stale {
		fmt.Fprintf(os.Stderr, "[stale]   %s\n", p)
	}
	for _, p := range orphans {
		fmt.Fprintf(os.Stderr, "[orphan]  %s\n", p)
	}
	if !quiet {
		for _, p := range fr.Fresh {
			fmt.Fprintf(os.Stderr, "[fresh]   %s\n", p)
		}
	}
	if len(orphans) > 0 {
		fmt.Fprintf(os.Stderr, "orphans: %s\n", orphanExplanation)
	}
	total := len(fr.Missing) + len(fr.Stale) + len(fr.Fresh)
	fmt.Fprintf(os.Stderr, "%d file(s): %d missing, %d stale, %d fresh; %d orphan(s)\n",
		total, len(fr.Missing), len(fr.Stale), len(fr.Fresh), len(orphans))
	if len(fr.Missing)+len(fr.Stale)+len(orphans) > 0 {
		return 1
	}
	return 0
}
