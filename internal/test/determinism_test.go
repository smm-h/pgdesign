package test

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// buildDeterminismSchema parses and builds one of the determinism fixtures.
// Each call reparses and rebuilds from scratch so that Go's randomized map
// iteration order is re-rolled every time — the exact condition Canonicalize
// must neutralize.
func buildDeterminismSchema(t *testing.T, name string) *model.Schema {
	t.Helper()
	path := filepath.Join("testdata", "determinism", name)
	raw, diags := parse.File(path)
	if raw == nil {
		t.Fatalf("parse %s returned nil: %v", name, diags)
	}
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("parse error in %s: %s", name, d.Message)
		}
	}
	reg := semtype.NewBuiltinRegistry()
	if userTypes := parse.CollectUserTypes(raw); len(userTypes) > 0 {
		if loadDiags := reg.LoadUserTypes(userTypes); loadDiags.HasErrors() {
			t.Fatalf("user type errors in %s: %v", name, loadDiags)
		}
	}
	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors in %s: %v", name, buildDiags)
	}
	return schema
}

func mustGen(t *testing.T, schema *model.Schema, format string) string {
	t.Helper()
	// The json format emits the canonical whole-model envelope, which requires
	// a model class; these fixtures are TOML-built (registry-present, L7).
	out, _, err := generate.Generate(schema, generate.Options{IncludeComments: true, Format: format, ModelClass: rev.RegistryPresent})
	if err != nil {
		t.Fatalf("generate %s: %v", format, err)
	}
	return out
}

// TestDeterminism_MultiIteration rebuilds the same fixture many times and
// asserts that both the DDL and JSON renderings are byte-identical across every
// iteration. Before Canonicalize, resolveTable ranged Go maps and the emitters
// leaked that nondeterministic order, so this test would flake (RED). After
// Canonicalize it is stable (GREEN). The fixture carries >= 2 entries in every
// map-sourced collection so ordering is actually observable.
func TestDeterminism_MultiIteration(t *testing.T) {
	testenv.Isolate(t)
	const iterations = 50

	var firstSQL, firstJSON string
	for i := 0; i < iterations; i++ {
		schema := buildDeterminismSchema(t, "canonical.toml")
		gotSQL := mustGen(t, schema, "sql")
		gotJSON := mustGen(t, schema, "json")
		if i == 0 {
			firstSQL, firstJSON = gotSQL, gotJSON
			continue
		}
		if gotSQL != firstSQL {
			t.Fatalf("DDL not deterministic on iteration %d:\n--- first ---\n%s\n--- iter %d ---\n%s", i, firstSQL, i, gotSQL)
		}
		if gotJSON != firstJSON {
			t.Fatalf("JSON not deterministic on iteration %d:\n--- first ---\n%s\n--- iter %d ---\n%s", i, firstJSON, i, gotJSON)
		}
	}

	// The rendered schema must itself be canonical.
	assertCanonical(t, buildDeterminismSchema(t, "canonical.toml"))
}

// TestCanonicality_ShuffledDeclarationOrder proves the output is CANONICAL, not
// merely repeatable: two fixtures declaring the same schema with the map-sourced
// collections (checks, indexes, uniques, policies, fks) in different textual
// order — but with identical column and enum-value order — must produce
// byte-identical DDL and JSON.
func TestCanonicality_ShuffledDeclarationOrder(t *testing.T) {
	testenv.Isolate(t)
	canonical := buildDeterminismSchema(t, "canonical.toml")
	shuffled := buildDeterminismSchema(t, "shuffled.toml")

	if a, b := mustGen(t, canonical, "sql"), mustGen(t, shuffled, "sql"); a != b {
		t.Errorf("shuffled declaration order changed DDL:\n--- canonical ---\n%s\n--- shuffled ---\n%s", a, b)
	}

	// JSON carries the originating filename in source_file; normalize it away
	// before comparing so only structural ordering is under test.
	canonJSON := strings.ReplaceAll(mustGen(t, canonical, "json"), "canonical.toml", "FIXTURE")
	shufJSON := strings.ReplaceAll(mustGen(t, shuffled, "json"), "shuffled.toml", "FIXTURE")
	if canonJSON != shufJSON {
		t.Errorf("shuffled declaration order changed JSON:\n--- canonical ---\n%s\n--- shuffled ---\n%s", canonJSON, shufJSON)
	}
}

// TestConstraintAutoNames_StableUnderReordering pins the reorder-renames-
// constraints hazard: auto-generated constraint/index names are content-derived
// (build.go constraintName), never positional, so reordering the FK
// declarations must not change the emitted auto-FK index names.
func TestConstraintAutoNames_StableUnderReordering(t *testing.T) {
	testenv.Isolate(t)
	canonSQL := mustGen(t, buildDeterminismSchema(t, "canonical.toml"), "sql")
	shufSQL := mustGen(t, buildDeterminismSchema(t, "shuffled.toml"), "sql")

	// Both FK columns lack an explicit covering index, so enrich() materializes
	// content-named auto-FK indexes for each.
	for _, autoName := range []string{"idx_orders_user_id", "idx_orders_product_id"} {
		if !strings.Contains(canonSQL, autoName) {
			t.Errorf("canonical output missing content-derived auto index %q", autoName)
		}
		if !strings.Contains(shufSQL, autoName) {
			t.Errorf("shuffled output missing content-derived auto index %q (auto names must be positional-independent)", autoName)
		}
	}
}

// TestViewReferencesView_DependencyOrdered asserts a view that references
// another view is emitted after its dependency, regardless of declaration order.
func TestViewReferencesView_DependencyOrdered(t *testing.T) {
	testenv.Isolate(t)
	for _, fixture := range []string{"canonical.toml", "shuffled.toml"} {
		sql := mustGen(t, buildDeterminismSchema(t, fixture), "sql")
		base := strings.Index(sql, "CREATE VIEW shop.active_orders AS")
		dependent := strings.Index(sql, "CREATE VIEW shop.active_orders_by_user AS")
		if base < 0 || dependent < 0 {
			t.Fatalf("%s: expected both views in output:\n%s", fixture, sql)
		}
		if base > dependent {
			t.Errorf("%s: active_orders (pos %d) must precede active_orders_by_user (pos %d)", fixture, base, dependent)
		}
	}
}

// assertCanonical verifies the Canonicalize postcondition on a schema:
// per-table collections, top-level type collections, and Extensions are in
// ascending name order; derived structures are populated; and Canonicalize is
// idempotent. Introspected schemas reach this state via the Canonicalize call
// at the end of Introspect.
func assertCanonical(t *testing.T, s *model.Schema) {
	t.Helper()

	assertSortedByName(t, "Extensions", s.Extensions, func(e string) string { return e })
	assertSortedByName(t, "Enums", s.Enums, func(e model.Enum) string { return e.Name })
	assertSortedByName(t, "Domains", s.Domains, func(d model.Domain) string { return d.Name })
	assertSortedByName(t, "CompositeTypes", s.CompositeTypes, func(c model.CompositeType) string { return c.Name })
	assertSortedByName(t, "Sequences", s.Sequences, func(sq model.Sequence) string { return sq.Name })

	for i := range s.Tables {
		tbl := &s.Tables[i]
		assertSortedByName(t, tbl.Name+".FKs", tbl.FKs, func(f model.FK) string { return f.Name })
		assertSortedByName(t, tbl.Name+".Indexes", tbl.Indexes, func(x model.Index) string { return x.Name })
		assertSortedByName(t, tbl.Name+".Uniques", tbl.Uniques, func(u model.UniqueConstraint) string { return u.Name })
		assertSortedByName(t, tbl.Name+".Checks", tbl.Checks, func(c model.CheckConstraint) string { return c.Name })
		assertSortedByName(t, tbl.Name+".Exclusions", tbl.Exclusions, func(e model.ExclusionConstraint) string { return e.Name })
		assertSortedByName(t, tbl.Name+".Policies", tbl.Policies, func(p model.Policy) string { return p.Name })
		assertSortedByName(t, tbl.Name+".Triggers", tbl.Triggers, func(tr model.Trigger) string { return tr.Name })
	}
	for i := range s.MaterializedViews {
		mv := &s.MaterializedViews[i]
		assertSortedByName(t, mv.Name+".Indexes", mv.Indexes, func(x model.Index) string { return x.Name })
	}

	if s.FKGraph == nil {
		t.Error("FKGraph is nil after Canonicalize")
	}
	if s.TablesByName == nil {
		t.Error("TablesByName is nil after Canonicalize")
	}

	// Idempotence: canonicalizing an already-canonical schema is a no-op on the
	// rendered bytes.
	before := mustGen(t, s, "sql")
	s.Canonicalize()
	if after := mustGen(t, s, "sql"); after != before {
		t.Errorf("Canonicalize is not idempotent:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// assertSortedByName fails if items are not in ascending order by name(item).
func assertSortedByName[T any](t *testing.T, label string, items []T, name func(T) string) {
	t.Helper()
	for i := 1; i < len(items); i++ {
		if name(items[i-1]) > name(items[i]) {
			t.Errorf("%s not canonically ordered: %q before %q", label, name(items[i-1]), name(items[i]))
		}
	}
}
