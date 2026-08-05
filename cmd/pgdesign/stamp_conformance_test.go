package main

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"math/rand"
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/seed"
	"github.com/smm-h/pgdesign/pkg/genkit"
)

// TestEveryGeneratorOutputCarriesStamp is the positive provenance invariant for
// roadmap 0.1: every generator output must begin with the canonical stamp, as
// recognized by genkit's own parser. This runs every (mode, lang) in the
// registry over the determinism fixture and asserts genkit.ParseStamp accepts
// the head of every produced file (single-file and each file of multi-file
// generators). New generators added to SupportedModes are covered
// automatically.
func TestEveryGeneratorOutputCarriesStamp(t *testing.T) {
	testenv.Isolate(t)
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
				gen, err := SelectGenerator(lang, mode)
				if err != nil {
					t.Fatalf("SelectGenerator(%s, %s): %v", lang, mode, err)
				}

				if mfg, ok := gen.(genkit.MultiFileGenerator); ok {
					files, _ := mfg.GenerateFiles(schema)
					if len(files) == 0 {
						t.Fatalf("%s/%s produced no files", mode, lang)
					}
					names := make([]string, 0, len(files))
					for n := range files {
						names = append(names, n)
					}
					sort.Strings(names)
					for _, n := range names {
						if !genkit.HasStamp(files[n]) {
							t.Errorf("%s/%s file %q does not begin with the canonical stamp:\n%.120s",
								mode, lang, n, files[n])
						}
					}
					return
				}

				out, _ := gen.Generate(schema)
				if !genkit.HasStamp(out) {
					t.Errorf("%s/%s output does not begin with the canonical stamp:\n%.120s",
						mode, lang, out)
				}
			})
		}
	}
}

// TestSeedOutputCarriesStamp covers the seed generator, which stamps its output
// through the same genkit writer with a free-text info line. Both the populated
// path and the empty (rowsPerTable <= 0) path must carry the stamp.
func TestSeedOutputCarriesStamp(t *testing.T) {
	testenv.Isolate(t)
	schema := loadDeterminismSchema(t)
	rng := rand.New(rand.NewSource(1))

	populated, diags := seed.Generate(schema, 2, rng, &seed.SeedConfig{})
	if diags.HasErrors() {
		t.Fatalf("seed generate errors: %v", diags)
	}
	if !genkit.HasStamp([]byte(populated)) {
		t.Errorf("populated seed output lacks the canonical stamp:\n%.120s", populated)
	}

	empty, _ := seed.Generate(schema, 0, rng, &seed.SeedConfig{})
	if !genkit.HasStamp([]byte(empty)) {
		t.Errorf("empty seed output lacks the canonical stamp:\n%.120s", empty)
	}
}
