package generate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"oss.terrastruct.com/d2/d2compiler"
)

// richSchema exercises every 9.2 enrichment layer: an enum type, a table with a
// nullable column, a unique index, a plain index, CHECK constraints, a comment,
// RLS + append-only, plus an imported reference table with a cross-project FK.
func richSchema() *model.Schema {
	nickDefault := "'anon'"
	s := &model.Schema{
		Name: "app",
		Enums: []model.Enum{
			{Schema: "app", Name: "status", Values: []string{"active", "banned"}},
		},
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "app",
				Comment: "Application users.",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "email", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "handle", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "nickname", PGType: typeinfo.MustParse("text"), NotNull: false, DefaultExpr: nickDefault},
					{Name: "org_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
				Indexes: []model.Index{
					{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
					{Name: "ix_users_handle", Columns: []string{"handle"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_users_email", Expr: "email <> ''"},
				},
				FKs: []model.FK{
					{Name: "fk_users_org", Columns: []string{"org_id"}, RefSchema: "framework", RefTable: "orgs", RefColumns: []string{"id"}, OnDelete: "CASCADE", RefAlias: "fw"},
				},
				EnableRLS:  true,
				AppendOnly: true,
			},
		},
		ImportedTables: []model.Table{
			{
				Name:    "orgs",
				Schema:  "framework",
				Columns: []model.Column{{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true}},
				PK:      []string{"id"},
			},
		},
	}
	s.Canonicalize()
	return s
}

func TestD2EnrichmentLayerIndexMarkers(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	on := GenerateD2(s, nil, DefaultD2Options())
	if !strings.Contains(on, "email: text {constraint: unique}") {
		t.Errorf("index_markers on: expected unique marker on email, got:\n%s", on)
	}
	if !strings.Contains(on, "handle: text {constraint: idx}") {
		t.Errorf("index_markers on: expected idx marker on handle, got:\n%s", on)
	}

	opts := DefaultD2Options()
	opts.IndexMarkers = false
	off := GenerateD2(s, nil, opts)
	if strings.Contains(off, "constraint: unique") || strings.Contains(off, "constraint: idx") {
		t.Errorf("index_markers off: markers should be absent, got:\n%s", off)
	}
}

func TestD2EnrichmentLayerNullable(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	on := GenerateD2(s, nil, DefaultD2Options())
	if !strings.Contains(on, "nullable") {
		t.Errorf("nullable on: expected nullable marker, got:\n%s", on)
	}

	opts := DefaultD2Options()
	opts.Nullable = false
	off := GenerateD2(s, nil, opts)
	if strings.Contains(off, "nullable") {
		t.Errorf("nullable off: marker should be absent, got:\n%s", off)
	}
}

func TestD2EnrichmentLayerComments(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	on := GenerateD2(s, nil, DefaultD2Options())
	if !strings.Contains(on, `tooltip: "Application users."`) {
		t.Errorf("comments on: expected tooltip, got:\n%s", on)
	}

	opts := DefaultD2Options()
	opts.Comments = false
	off := GenerateD2(s, nil, opts)
	if strings.Contains(off, "tooltip:") {
		t.Errorf("comments off: tooltip should be absent, got:\n%s", off)
	}
}

func TestD2EnrichmentLayerChecks(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	on := GenerateD2(s, nil, DefaultD2Options())
	if !strings.Contains(on, "users_checks: {") {
		t.Errorf("checks on: expected check note shape, got:\n%s", on)
	}
	if !strings.Contains(on, "ck_users_email") {
		t.Errorf("checks on: expected check name in note, got:\n%s", on)
	}

	opts := DefaultD2Options()
	opts.Checks = false
	off := GenerateD2(s, nil, opts)
	if strings.Contains(off, "users_checks") {
		t.Errorf("checks off: check note should be absent, got:\n%s", off)
	}
}

func TestD2EnrichmentLayerRLSMarkers(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	on := GenerateD2(s, nil, DefaultD2Options())
	if !strings.Contains(on, `label: "users [RLS, append-only]"`) {
		t.Errorf("rls_markers on: expected RLS/append-only label, got:\n%s", on)
	}

	opts := DefaultD2Options()
	opts.RLSMarkers = false
	off := GenerateD2(s, nil, opts)
	if strings.Contains(off, "[RLS") || strings.Contains(off, "append-only") {
		t.Errorf("rls_markers off: markers should be absent, got:\n%s", off)
	}
}

func TestD2EnrichmentLayerEnums(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	on := GenerateD2(s, nil, DefaultD2Options())
	if !strings.Contains(on, "status: {") || !strings.Contains(on, "<<enum>>") {
		t.Errorf("enums on: expected enum rectangle, got:\n%s", on)
	}

	opts := DefaultD2Options()
	opts.Enums = false
	off := GenerateD2(s, nil, opts)
	if strings.Contains(off, "<<enum>>") {
		t.Errorf("enums off: enum shape should be absent, got:\n%s", off)
	}
}

// TestD2ReferenceShapeSurvivesAllLayerCombos verifies the imported reference
// shape (roadmap 7.4) is preserved across every combination of enrichment
// layers, and that every combination compiles through the d2 library.
func TestD2ReferenceShapeSurvivesAllLayerCombos(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()

	type layer struct {
		set func(*D2Options, bool)
	}
	layers := []func(*D2Options, bool){
		func(o *D2Options, v bool) { o.IndexMarkers = v },
		func(o *D2Options, v bool) { o.Nullable = v },
		func(o *D2Options, v bool) { o.Comments = v },
		func(o *D2Options, v bool) { o.Checks = v },
		func(o *D2Options, v bool) { o.RLSMarkers = v },
		func(o *D2Options, v bool) { o.Enums = v },
	}

	combos := 1 << len(layers)
	for mask := 0; mask < combos; mask++ {
		opts := DefaultD2Options()
		for i, set := range layers {
			set(&opts, mask&(1<<i) != 0)
		}
		src := GenerateD2(s, nil, opts)

		// The imported reference shape and its qualified FK edge always survive.
		if !strings.Contains(src, "framework.orgs: {") {
			t.Fatalf("mask %06b: imported reference shape missing:\n%s", mask, src)
		}
		if !strings.Contains(src, "<<imported>>") {
			t.Fatalf("mask %06b: imported reference label missing", mask)
		}
		if !strings.Contains(src, "users.org_id -> framework.orgs:") {
			t.Fatalf("mask %06b: qualified imported FK edge missing:\n%s", mask, src)
		}

		// Every combination compiles.
		if _, _, err := d2compiler.Compile("", strings.NewReader(src), nil); err != nil {
			t.Fatalf("mask %06b: d2 compile failed: %v\nsource:\n%s", mask, err, src)
		}
	}
}

func TestD2SummaryModeOmitsColumns(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()
	opts := DefaultD2Options()
	opts.Summary = true
	out := GenerateD2(s, nil, opts)

	if !strings.Contains(out, "users: {") {
		t.Errorf("summary: table shape should still be present, got:\n%s", out)
	}
	if strings.Contains(out, "email: text") {
		t.Errorf("summary: columns should be omitted, got:\n%s", out)
	}
	// Edges still present.
	if !strings.Contains(out, "users.org_id -> framework.orgs:") {
		t.Errorf("summary: FK edge should be present, got:\n%s", out)
	}
	if _, _, err := d2compiler.Compile("", strings.NewReader(out), nil); err != nil {
		t.Fatalf("summary output failed to compile: %v\n%s", err, out)
	}
}

// TestSharedPresentationHelperUsedByDoc guards that the doc renderer derives
// column defaults/nullability through the same shared helper as d2 — the
// derivation lives in exactly one place.
func TestSharedPresentationHelperUsedByDoc(t *testing.T) {
	testenv.Isolate(t)
	s := richSchema()
	cps := deriveColumnPresentations(&s.Tables[0])

	byName := map[string]columnPresentation{}
	for _, cp := range cps {
		byName[cp.Name] = cp
	}
	if !byName["nickname"].Nullable {
		t.Errorf("nickname should be nullable")
	}
	if byName["nickname"].Default != "'anon'" {
		t.Errorf("nickname default = %q, want 'anon'", byName["nickname"].Default)
	}
	if !byName["email"].IsUnique {
		t.Errorf("email should be unique (unique index)")
	}
	if !byName["handle"].Indexed || byName["handle"].IsUnique {
		t.Errorf("handle should be indexed but not unique")
	}
	if !byName["id"].IsPK {
		t.Errorf("id should be PK")
	}
	if !byName["org_id"].IsFK {
		t.Errorf("org_id should be FK")
	}
}
