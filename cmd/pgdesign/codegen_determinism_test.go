package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// loadDeterminismSchema parses and builds the representative determinism
// fixture (enums, CHECK constraints, a scalar domain, a state machine, FKs,
// uniques, and indexes).
func loadDeterminismSchema(t *testing.T) *model.Schema {
	t.Helper()
	raw, diags := parse.File(filepath.Join("testdata", "determinism_schema.toml"))
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	reg := semtype.NewBuiltinRegistry()
	if userTypes := parse.CollectUserTypes(raw); len(userTypes) > 0 {
		if loadDiags := reg.LoadUserTypes(userTypes); loadDiags.HasErrors() {
			t.Fatalf("user type load errors: %v", loadDiags)
		}
	}
	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}

	// Guard against fixture rot: the contract is only meaningful if the schema
	// actually exercises the map-backed extraction paths (multiple enum
	// columns and multiple CHECK columns on one table, plus a state machine).
	if len(schema.Enums) < 3 {
		t.Fatalf("fixture must declare at least 3 enums, got %d", len(schema.Enums))
	}
	if len(schema.StateMachineTransitions) < 1 {
		t.Fatalf("fixture must declare a state machine type")
	}
	exercised := false
	for _, tbl := range schema.Tables {
		cs := codegen.ExtractConstraints(tbl, *schema)
		if len(cs.EnumFields) >= 3 && len(cs.CheckExprs) >= 3 {
			exercised = true
			break
		}
	}
	if !exercised {
		t.Fatal("fixture must have a table with at least 3 enum columns and 3 CHECK-constrained columns")
	}
	return schema
}

// TestCodegenDeterminismContract is the permanent byte-stability contract for
// every codegen (mode, lang) combination in the registry: generating the same
// schema repeatedly must produce byte-identical output. New generators added
// to SupportedModes are covered automatically.
func TestCodegenDeterminismContract(t *testing.T) {
	const runs = 5
	schema := loadDeterminismSchema(t)

	modes := SupportedModes()
	modeNames := make([]string, 0, len(modes))
	for m := range modes {
		modeNames = append(modeNames, m)
	}
	sort.Strings(modeNames)

	for _, mode := range modeNames {
		langs := append([]string(nil), modes[mode]...)
		sort.Strings(langs)
		for _, lang := range langs {
			t.Run(mode+"/"+lang, func(t *testing.T) {
				// Single-file output contract. A fresh generator per run
				// mirrors separate CLI invocations.
				var first []byte
				for i := 0; i < runs; i++ {
					gen, err := SelectGenerator(lang, mode)
					if err != nil {
						t.Fatalf("SelectGenerator(%s, %s): %v", lang, mode, err)
					}
					out, diags := gen.Generate(schema)
					failOnErrorDiags(t, i, diags)
					if i == 0 {
						first = out
						continue
					}
					if !bytes.Equal(out, first) {
						t.Fatalf("run %d produced different single-file output than run 0:\n%s",
							i, firstDivergence(first, out))
					}
				}

				// Multi-file output contract: compare the full file maps.
				probe, err := SelectGenerator(lang, mode)
				if err != nil {
					t.Fatalf("SelectGenerator(%s, %s): %v", lang, mode, err)
				}
				if _, ok := probe.(codegen.MultiFileGenerator); !ok {
					return
				}
				var firstFiles map[string][]byte
				for i := 0; i < runs; i++ {
					gen, err := SelectGenerator(lang, mode)
					if err != nil {
						t.Fatalf("SelectGenerator(%s, %s): %v", lang, mode, err)
					}
					files, diags := gen.(codegen.MultiFileGenerator).GenerateFiles(schema)
					failOnErrorDiags(t, i, diags)
					if i == 0 {
						firstFiles = files
						continue
					}
					compareFileMaps(t, i, firstFiles, files)
				}
			})
		}
	}
}

// failOnErrorDiags fails the test if any diagnostic has Error severity.
func failOnErrorDiags(t *testing.T, run int, diags []diagnostic.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			t.Fatalf("run %d: unexpected error diagnostic: %s %s", run, d.Code, d.Message)
		}
	}
}

// compareFileMaps fails the test if the two generated file maps differ in
// file names or file contents.
func compareFileMaps(t *testing.T, run int, want, got map[string][]byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("run %d produced %d files, run 0 produced %d files", run, len(got), len(want))
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		gotData, ok := got[name]
		if !ok {
			t.Fatalf("run %d is missing file %q present in run 0", run, name)
		}
		if !bytes.Equal(gotData, want[name]) {
			t.Fatalf("run %d produced different contents for file %q than run 0:\n%s",
				run, name, firstDivergence(want[name], gotData))
		}
	}
}

// firstDivergence returns a short description of the first byte position
// where two outputs diverge, with surrounding context from both.
func firstDivergence(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	pos := n
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			pos = i
			break
		}
	}
	start := pos - 80
	if start < 0 {
		start = 0
	}
	endA := pos + 80
	if endA > len(a) {
		endA = len(a)
	}
	endB := pos + 80
	if endB > len(b) {
		endB = len(b)
	}
	return fmt.Sprintf("first divergence at byte %d\n--- run 0 ---\n...%s...\n--- run N ---\n...%s...",
		pos, a[start:endA], b[start:endB])
}
