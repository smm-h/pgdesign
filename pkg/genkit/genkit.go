// Package genkit provides the generator interface contract and freshness/orphan
// orchestration for deterministic code generation pipelines.
//
// # Generator contract
//
// All generators must produce deterministic output: given the same input schema,
// they must produce byte-identical output every time. This property enables
// freshness checking (--check mode) where the planned output is compared
// byte-for-byte against files on disk. Non-deterministic generators (e.g., those
// using random IDs or timestamps) break this contract and cannot participate in
// freshness checking.
//
// # Single-file vs multi-file
//
// A Generator produces a single output ([]byte). A MultiFileGenerator produces
// multiple files as a map[string][]byte of relative paths to contents. Multi-file
// generators own their output directory: every file found in the directory must
// be either planned output or on the ignore list; anything else is an orphan.
//
// # Freshness orchestration
//
// The freshness system compares planned output against on-disk state:
//   - Missing: planned file does not exist on disk
//   - Stale: planned file exists but differs from planned content
//   - Fresh: planned file matches planned content byte-for-byte
//   - Orphan: file on disk inside an owned directory that is not planned
//
// Orphans are hard errors. They appear when output configuration changes
// (e.g., split_mode switch) and old files are left behind.
package genkit

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/pkg/diagnostic"
)

// Generator produces a single output from a schema. Implementations must be
// deterministic: the same input must always produce byte-identical output.
type Generator interface {
	Generate(schema interface{}) ([]byte, []diagnostic.Diagnostic)
}

// MultiFileGenerator produces multiple output files from a schema.
// The returned map keys are slash-separated relative paths within the output
// directory. Implementations must be deterministic.
type MultiFileGenerator interface {
	GenerateFiles(schema interface{}) (map[string][]byte, []diagnostic.Diagnostic)
}

// FreshnessResult classifies planned files against their on-disk state.
type FreshnessResult struct {
	Missing []string // planned files that do not exist on disk
	Stale   []string // planned files that differ from on-disk content
	Fresh   []string // planned files that match on-disk content
}

// HasProblems returns true if any files are missing or stale.
func (r *FreshnessResult) HasProblems() bool {
	return len(r.Missing) > 0 || len(r.Stale) > 0
}

// CompareFreshness compares each planned file (absolute path -> content) against
// disk and classifies it. Result slices are sorted.
func CompareFreshness(files map[string][]byte) FreshnessResult {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var r FreshnessResult
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

// OrphanExplanation tells the consumer what an orphan is and how to resolve it.
const OrphanExplanation = "output directory is owned by the build system; remove or relocate these files (only __pycache__/ and *.pyc are ignored)"

// OrphanIgnored reports whether the slash-separated relative path is exempt from
// orphan detection. Currently ignores __pycache__ contents and *.pyc files.
func OrphanIgnored(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == "__pycache__" {
			return true
		}
	}
	return strings.HasSuffix(rel, ".pyc")
}

// ScanOrphans walks the owned output directory dir and returns the absolute
// paths of files that are neither in the owned set (slash-separated relative
// paths) nor ignored. A directory that does not exist yields no orphans.
func ScanOrphans(dir string, owned map[string]bool) ([]string, error) {
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
		if OrphanIgnored(rel) || owned[rel] {
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

// ScanAllOrphans runs ScanOrphans over every owned directory and returns the
// combined sorted list of orphan paths.
func ScanAllOrphans(ownedDirs map[string]map[string]bool) ([]string, error) {
	dirs := make([]string, 0, len(ownedDirs))
	for d := range ownedDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var all []string
	for _, d := range dirs {
		orphans, err := ScanOrphans(d, ownedDirs[d])
		if err != nil {
			return nil, err
		}
		all = append(all, orphans...)
	}
	sort.Strings(all)
	return all, nil
}
