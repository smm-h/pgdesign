package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/smm-h/strictcli/go/strictcli"
)

// collectCommandPaths walks the live strictcli app and returns every command
// path (dot-joined for grouped subcommands), matching the key form of
// writersRegistry.
func collectCommandPaths(app *strictcli.App) map[string]bool {
	paths := make(map[string]bool)
	for name := range app.Commands() {
		paths[name] = true
	}
	var walk func(prefix string, groups map[string]*strictcli.Group)
	walk = func(prefix string, groups map[string]*strictcli.Group) {
		for gname, g := range groups {
			gp := gname
			if prefix != "" {
				gp = prefix + "." + gname
			}
			for cname := range g.Commands {
				paths[gp+"."+cname] = true
			}
			walk(gp, g.Groups)
		}
	}
	walk("", app.Groups())
	return paths
}

// TestWritersRegistry_Totality is the mechanical PRE-COMMIT for the writer
// taxonomy (roadmap 6.2): the set of live CLI command paths must equal the set of
// writersRegistry keys exactly. A new command that omits its writer-class entry
// (or a stale registry entry) turns this test red — forcing every file-writing
// command to declare its class before it can ship.
func TestWritersRegistry_Totality(t *testing.T) {
	app := buildApp()
	live := collectCommandPaths(app)

	var missing []string // live command not classified in the registry
	for p := range live {
		if _, ok := writersRegistry[p]; !ok {
			missing = append(missing, p)
		}
	}
	var stale []string // registry entry with no live command
	for p := range writersRegistry {
		if !live[p] {
			stale = append(stale, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("commands missing a writer-class entry in writersRegistry (add each to writers.go):\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("writersRegistry entries with no live command (remove each from writers.go):\n  %s",
			strings.Join(stale, "\n  "))
	}
}

// TestWritersRegistry_ClassInvariants pins the class of the commands the roadmap
// names explicitly, so a reclassification is a deliberate, reviewed edit.
func TestWritersRegistry_ClassInvariants(t *testing.T) {
	want := map[string]WriterClass{
		"build":            ClassFullRegenerator,
		"revise":           ClassFullRegenerator,
		"codegen":          ClassPartialWriter,
		"fmt":              ClassSourceEditing,
		"introspect":       ClassScaffolding,
		"testdb.init":      ClassScaffolding,
		"seed":             ClassStampedUnenforced,
		"migrate.generate": ClassAppendOnlyStore,
	}
	for cmd, cls := range want {
		if got := writersRegistry[cmd]; got != cls {
			t.Errorf("writersRegistry[%q] = %q, want %q", cmd, got, cls)
		}
	}

	// Exactly one partial writer exists (roadmap 6.2 invariant).
	var partial []string
	for cmd, cls := range writersRegistry {
		if cls == ClassPartialWriter {
			partial = append(partial, cmd)
		}
	}
	if len(partial) != 1 || partial[0] != "codegen" {
		t.Errorf("expected exactly one partial writer (codegen), got %v", partial)
	}
}
