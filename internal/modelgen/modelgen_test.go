package modelgen

import (
	"reflect"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/validate"
	"pgregory.net/rapid"
)

// buildAndValidate is the oracle: it runs a generated model through the real
// parse/model pipeline (BuildMulti, which also Canonicalizes) and then through
// validate with BOTH the extension registry and the type registry populated.
// Several E-codes self-disable on nil registries, so a registry-less oracle
// would be silently weaker — the roadmap's validate exception warns about
// exactly that distortion, so this honors it literally.
//
// It returns any Build errors and any validate ERROR diagnostics (warnings are
// tolerated per the fragment).
func buildAndValidate(raws []*parse.RawSchema) (buildErrs diagnostic.Diagnostics, validateErrs []diagnostic.Diagnostic) {
	typeReg := semtype.NewBuiltinRegistry()
	// Increment A generates no user types, but load them anyway so the oracle
	// stays correct as later increments add type closures.
	for _, raw := range raws {
		if uts := parse.CollectUserTypes(raw); len(uts) > 0 {
			if d := typeReg.LoadUserTypes(uts); d.HasErrors() {
				buildErrs = append(buildErrs, d.Errors()...)
			}
		}
	}
	if buildErrs.HasErrors() {
		return buildErrs, nil
	}

	schema, diags := model.BuildMulti(raws, typeReg)
	if diags.HasErrors() {
		return diags.Errors(), nil
	}

	valCfg := &validate.Config{
		NamingPattern: "snake_case",
		Extensions:    schema.Extensions,
		ExtRegistry:   extregistry.NewBuiltinRegistry(),
		TypeRegistry:  typeReg,
	}
	active, _ := validate.Validate(schema, valCfg)
	for _, d := range active {
		if d.Severity == diagnostic.Error {
			validateErrs = append(validateErrs, d)
		}
	}
	return nil, validateErrs
}

// TestOracle_GeneratedModelsAreValid is the L9 oracle property: every generated
// model Builds + Canonicalizes cleanly AND passes validate with zero errors,
// at all sizes the default config produces. Runs in normal CI (-short safe,
// bounded by rapid's default iteration count).
func TestOracle_GeneratedModelsAreValid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		raws := Draw(rt, DefaultConfig())

		buildErrs, validateErrs := buildAndValidate(raws)
		for _, d := range buildErrs {
			rt.Errorf("Build error: %s %s (table=%s col=%s)", d.Code, d.Message, d.Table, d.Column)
		}
		for _, d := range validateErrs {
			rt.Errorf("validate error: %s %s (table=%s col=%s)", d.Code, d.Message, d.Table, d.Column)
		}
		if len(buildErrs) > 0 || len(validateErrs) > 0 {
			rt.FailNow()
		}
	})
}

// TestOracle_GeneratedPairsAreValid is the pair-generator oracle: for every drawn
// (a, b), BOTH models Build + Canonicalize + validate cleanly. This guarantees
// L10's round-trip inputs are always valid on both endpoints (the derivation must
// never mint an invalid post-state).
func TestOracle_GeneratedPairsAreValid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a, b := DrawPair(rt, DefaultConfig())
		for label, raws := range map[string][]*parse.RawSchema{"a": a, "b": b} {
			buildErrs, validateErrs := buildAndValidate(raws)
			for _, d := range buildErrs {
				rt.Errorf("pair %s Build error: %s %s (table=%s col=%s)", label, d.Code, d.Message, d.Table, d.Column)
			}
			for _, d := range validateErrs {
				rt.Errorf("pair %s validate error: %s %s (table=%s col=%s)", label, d.Code, d.Message, d.Table, d.Column)
			}
			if len(buildErrs) > 0 || len(validateErrs) > 0 {
				rt.FailNow()
			}
		}
	})
}

// TestOracle_SingleColumnFragment exercises the smallest fragment (one schema,
// one table, no extra columns) to pin that the minimal shape — a bare
// surrogate-PK table — is itself valid.
func TestOracle_SingleColumnFragment(t *testing.T) {
	cfg := Config{
		MinSchemas: 1, MaxSchemas: 1,
		MinTables: 1, MaxTables: 1,
		MinExtraColumns: 0, MaxExtraColumns: 0,
		PGVersion: 16,
	}
	rapid.Check(t, func(rt *rapid.T) {
		raws := Draw(rt, cfg)
		if len(raws) != 1 {
			rt.Fatalf("expected 1 schema, got %d", len(raws))
		}
		if len(raws[0].Tables) != 1 {
			rt.Fatalf("expected 1 table, got %d", len(raws[0].Tables))
		}
		if got := len(raws[0].Tables[0].Columns); got != 1 {
			rt.Fatalf("expected 1 column (the PK), got %d", got)
		}
		buildErrs, validateErrs := buildAndValidate(raws)
		if len(buildErrs) > 0 || len(validateErrs) > 0 {
			rt.Fatalf("minimal fragment invalid: build=%v validate=%v", buildErrs, validateErrs)
		}
	})
}

// TestFragmentRestrictionsHonored verifies the config bounds are respected: the
// generator never exceeds the configured schema/table/column counts.
func TestFragmentRestrictionsHonored(t *testing.T) {
	cfg := Config{
		MinSchemas: 2, MaxSchemas: 2,
		MinTables: 1, MaxTables: 3,
		MinExtraColumns: 1, MaxExtraColumns: 2,
		PGVersion: 15,
	}
	rapid.Check(t, func(rt *rapid.T) {
		raws := Draw(rt, cfg)
		if len(raws) != 2 {
			rt.Fatalf("schema count %d out of range [2,2]", len(raws))
		}
		for _, raw := range raws {
			if raw.Meta.Version != 15 {
				rt.Fatalf("pg_version = %d, want 15", raw.Meta.Version)
			}
			if n := len(raw.Tables); n < 1 || n > 3 {
				rt.Fatalf("table count %d out of range [1,3]", n)
			}
			for _, tbl := range raw.Tables {
				// One PK column plus [1,2] extras => [2,3] total.
				if n := len(tbl.Columns); n < 2 || n > 3 {
					rt.Fatalf("column count %d out of range [2,3]", n)
				}
			}
		}
	})
}

// TestGroupsGeneratedAndValid pins the Groups increment: with MinGroups>=1 the
// model always carries groups, every group is non-empty, and every referenced
// table exists — so the model still Builds + validates cleanly (resolveGroups
// rejects unknown table references with E227).
func TestGroupsGeneratedAndValid(t *testing.T) {
	cfg := Config{
		MinSchemas: 1, MaxSchemas: 2,
		MinTables: 1, MaxTables: 3,
		MinExtraColumns: 0, MaxExtraColumns: 2,
		PGVersion: 16,
		MinGroups: 1, MaxGroups: 3,
	}
	rapid.Check(t, func(rt *rapid.T) {
		raws := Draw(rt, cfg)
		known := make(map[string]bool)
		for _, raw := range raws {
			for _, tbl := range raw.Tables {
				known[tbl.Name] = true
			}
		}
		var groupCount int
		for _, raw := range raws {
			for name, members := range raw.Groups {
				groupCount++
				if len(members) == 0 {
					rt.Fatalf("group %q is empty", name)
				}
				for _, m := range members {
					if !known[m] {
						rt.Fatalf("group %q references unknown table %q", name, m)
					}
				}
			}
		}
		if groupCount == 0 {
			rt.Fatal("MinGroups>=1 but no groups generated")
		}
		buildErrs, validateErrs := buildAndValidate(raws)
		if len(buildErrs) > 0 || len(validateErrs) > 0 {
			rt.Fatalf("model with groups invalid: build=%v validate=%v", buildErrs, validateErrs)
		}
	})
}

// TestDeterministicUnderSeed pins that generation is deterministic: the same
// rapid seed yields byte-identical models. rapid's integrated shrinking relies
// on this, and it is a stated 1.6 verification obligation.
func TestDeterministicUnderSeed(t *testing.T) {
	gen := Generator(DefaultConfig())
	for seed := 1; seed <= 5; seed++ {
		a := gen.Example(seed)
		b := gen.Example(seed)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("seed %d: generation not deterministic", seed)
		}
	}
}
