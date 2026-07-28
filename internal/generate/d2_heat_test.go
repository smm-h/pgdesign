package generate

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// hubSchema is a star: three tables each reference a central hub, so the hub has
// fan-in 3 and the spokes have fan-in 0.
func hubSchema() *model.Schema {
	c := func(name string) model.Column {
		return model.Column{Name: name, PGType: typeinfo.MustParse("uuid"), NotNull: true}
	}
	tables := []model.Table{
		{Name: "hub", Schema: "app", Columns: []model.Column{c("id")}, PK: []string{"id"}},
	}
	for _, n := range []string{"t1", "t2", "t3"} {
		tables = append(tables, model.Table{
			Name: n, Schema: "app",
			Columns: []model.Column{c("id"), c("hub_id")}, PK: []string{"id"},
			FKs: []model.FK{{Name: "fk_" + n, Columns: []string{"hub_id"}, RefSchema: "app", RefTable: "hub", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
		})
	}
	s := &model.Schema{Name: "app", Tables: tables}
	s.Canonicalize()
	return s
}

func TestD2HeatMapFanIn(t *testing.T) {
	s := hubSchema()
	opts := DefaultD2Options()
	opts.HeatMap = "fan-in"
	out := GenerateD2(s, nil, opts)

	// hub has fan-in 3 -> mid-ramp blue on the border.
	hub := blockFor(out, "hub")
	if !strings.Contains(hub, `style.stroke: "`+heatColor(3)+`"`) {
		t.Errorf("hub (fan-in 3) expected stroke %s, got block:\n%s", heatColor(3), hub)
	}
	if !strings.Contains(hub, "style.stroke-width: 2") {
		t.Errorf("hub expected stroke-width 2, got:\n%s", hub)
	}
	// A spoke has fan-in 0 -> neutral grey.
	t1 := blockFor(out, "t1")
	if !strings.Contains(t1, `style.stroke: "`+heatColor(0)+`"`) {
		t.Errorf("t1 (fan-in 0) expected stroke %s, got block:\n%s", heatColor(0), t1)
	}
}

func TestD2HeatMapFanOut(t *testing.T) {
	s := hubSchema()
	opts := DefaultD2Options()
	opts.HeatMap = "fan-out"
	out := GenerateD2(s, nil, opts)

	// hub has fan-out 0.
	hub := blockFor(out, "hub")
	if !strings.Contains(hub, `style.stroke: "`+heatColor(0)+`"`) {
		t.Errorf("hub (fan-out 0) expected stroke %s, got:\n%s", heatColor(0), hub)
	}
	// A spoke has fan-out 1.
	t1 := blockFor(out, "t1")
	if !strings.Contains(t1, `style.stroke: "`+heatColor(1)+`"`) {
		t.Errorf("t1 (fan-out 1) expected stroke %s, got:\n%s", heatColor(1), t1)
	}
}

func TestD2HeatMapOffNoStroke(t *testing.T) {
	s := hubSchema()
	out := GenerateD2(s, nil, DefaultD2Options()) // HeatMap == ""
	if strings.Contains(out, "style.stroke:") {
		t.Errorf("heat map off: no stroke styling expected, got:\n%s", out)
	}
}

// TestD2InjectedStats verifies caller-provided live stats are rendered without
// the generate package ever touching a database (the stats arrive as data).
func TestD2InjectedStats(t *testing.T) {
	s := hubSchema()
	opts := DefaultD2Options()
	opts.Stats = map[string]TableStats{
		model.TableKey("app", "hub"): {RowCount: 1000, SeqScanRatio: 0.5},
	}
	out := GenerateD2(s, nil, opts)

	hub := blockFor(out, "hub")
	if !strings.Contains(hub, "rows: 1000") {
		t.Errorf("expected injected row count in hub tooltip, got:\n%s", hub)
	}
	if !strings.Contains(hub, "seq scan ratio: 0.50") {
		t.Errorf("expected injected seq scan ratio in hub tooltip, got:\n%s", hub)
	}
	// A table with no injected stats has no stats tooltip.
	t1 := blockFor(out, "t1")
	if strings.Contains(t1, "rows:") {
		t.Errorf("t1 should have no injected stats, got:\n%s", t1)
	}
	mustCompileD2(t, out)
}

// TestGenerateHasNoDBImport enforces roadmap L5: the generate package stays
// DB-free. Live stats must arrive as caller-provided data, never fetched here.
func TestGenerateHasNoDBImport(t *testing.T) {
	forbidden := []string{
		"github.com/jackc/pgx",
		"database/sql",
		"github.com/smm-h/pgdesign/internal/introspect",
		"github.com/smm-h/pgdesign/internal/serve",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.HasPrefix(p, bad) {
					t.Errorf("%s imports forbidden DB package %q (generate must stay DB-free)", name, p)
				}
			}
		}
	}
}

// blockFor returns the D2 shape block ("name: { ... }") for the given top-level
// shape id, for scoped assertions.
func blockFor(src, name string) string {
	lines := strings.Split(src, "\n")
	var b strings.Builder
	in := false
	for _, line := range lines {
		if !in && strings.HasPrefix(line, name+": {") {
			in = true
			b.WriteString(line + "\n")
			continue
		}
		if in {
			b.WriteString(line + "\n")
			if line == "}" {
				break
			}
		}
	}
	return b.String()
}
