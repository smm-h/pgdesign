package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/pkg/genkit"
)

// This file holds the shared freshness and orphan-detection logic used by
// `pgdesign build`, `pgdesign check --tag build`, and `pgdesign codegen --check`.
//
// The actual algorithms live in pkg/genkit; this file provides the CLI-specific
// wrappers that print status lines to stderr.
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
const orphanExplanation = genkit.OrphanExplanation

// orphanIgnored reports whether the slash-separated relative path rel is
// exempt from orphan detection: __pycache__ contents and *.pyc files.
func orphanIgnored(rel string) bool {
	return genkit.OrphanIgnored(rel)
}

// scanOrphans walks the owned output directory dir and returns the absolute
// paths of files that are neither in the owned set (slash-separated paths
// relative to dir) nor ignored. A directory that does not exist yields no
// orphans.
func scanOrphans(dir string, owned map[string]bool) ([]string, error) {
	return genkit.ScanOrphans(dir, owned)
}

// scanAllOrphans runs scanOrphans over every owned directory and returns the
// combined sorted list of orphan paths.
func scanAllOrphans(ownedDirs map[string]map[string]bool) ([]string, error) {
	return genkit.ScanAllOrphans(ownedDirs)
}

// freshnessResult classifies planned files against their on-disk state.
type freshnessResult = genkit.FreshnessResult

// compareFreshness compares each planned file against disk (byte-exact) and
// classifies it as missing, stale, or fresh. Result slices are sorted.
func compareFreshness(files map[string][]byte) freshnessResult {
	return genkit.CompareFreshness(files)
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
