package generate

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func smallSchema() *model.Schema {
	s := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:   "posts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
					{Name: "author_id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "fk_posts_author", Columns: []string{"author_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
	s.Canonicalize()
	return s
}

func TestD2OptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*D2Options)
		wantErr bool
	}{
		{"defaults ok", func(o *D2Options) {}, false},
		{"elk ok", func(o *D2Options) { o.Layout = "elk" }, false},
		{"tala rejected", func(o *D2Options) { o.Layout = "tala" }, true},
		{"unknown layout rejected", func(o *D2Options) { o.Layout = "cose" }, true},
		{"bad direction", func(o *D2Options) { o.Direction = "sideways" }, true},
		{"bad heat map", func(o *D2Options) { o.HeatMap = "rainbow" }, true},
		{"negative depth", func(o *D2Options) { o.IncludeDependencies = -1 }, true},
		{"heat fan-in ok", func(o *D2Options) { o.HeatMap = "fan-in" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultD2Options()
			tc.mutate(&opts)
			err := opts.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestD2OptionsValidateTALAMessage(t *testing.T) {
	opts := DefaultD2Options()
	opts.Layout = "tala"
	err := opts.Validate()
	if err == nil || !strings.Contains(err.Error(), "OSS") {
		t.Fatalf("expected TALA-specific OSS message, got: %v", err)
	}
}

func TestGenerateD2Direction(t *testing.T) {
	s := smallSchema()

	// Default: down.
	out := GenerateD2(s, nil, DefaultD2Options())
	if !strings.HasPrefix(out, "direction: down\n") {
		t.Errorf("expected default direction: down at top, got:\n%s", out)
	}

	// Custom direction.
	opts := DefaultD2Options()
	opts.Direction = "right"
	out = GenerateD2(s, nil, opts)
	if !strings.HasPrefix(out, "direction: right\n") {
		t.Errorf("expected direction: right, got:\n%s", out)
	}

	// Empty direction emits no line.
	opts.Direction = ""
	out = GenerateD2(s, nil, opts)
	if strings.Contains(out, "direction:") {
		t.Errorf("expected no direction line when empty, got:\n%s", out)
	}
}

// TestRenderSVGDagreAndELK is the elk "golden": SVG rendering is
// non-deterministic (layout coordinates vary), so this is a functional golden —
// both layout engines must compile the diagram to a non-empty <svg>. ELK ships
// in the OSS d2 library (via an embedded JS runtime), so it is exercised
// directly rather than skipped.
func TestRenderSVGDagreAndELK(t *testing.T) {
	s := smallSchema()
	src := GenerateD2(s, nil, DefaultD2Options())

	for _, layout := range []string{"dagre", "elk"} {
		t.Run(layout, func(t *testing.T) {
			opts := DefaultD2Options()
			opts.Layout = layout
			svg, err := RenderSVG(src, opts)
			if err != nil {
				t.Fatalf("RenderSVG(%s) failed: %v", layout, err)
			}
			if !strings.Contains(string(svg), "<svg") {
				t.Fatalf("RenderSVG(%s) produced no <svg>", layout)
			}
		})
	}
}

func TestRenderSVGThemeID(t *testing.T) {
	s := smallSchema()
	src := GenerateD2(s, nil, DefaultD2Options())
	opts := DefaultD2Options()
	opts.Theme = 200 // a valid d2 theme id
	svg, err := RenderSVG(src, opts)
	if err != nil {
		t.Fatalf("RenderSVG with theme failed: %v", err)
	}
	if !strings.Contains(string(svg), "<svg") {
		t.Fatalf("themed render produced no <svg>")
	}
}
