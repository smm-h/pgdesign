package generate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/lib/log"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

// GenerateD2 produces D2 diagram language text from a resolved schema.
// Each table is rendered as a sql_table shape with columns listed,
// and FK relationships appear as labeled edges with the ON DELETE action.
func GenerateD2(schema *model.Schema) string {
	var sections []string

	tables := schema.TableOrder()

	// Render each table as a D2 sql_table shape.
	for _, t := range tables {
		sections = append(sections, renderD2Table(&t))
	}

	// Render FK edges after all tables.
	for _, t := range tables {
		fks := sortedFKs(t.FKs)
		for _, fk := range fks {
			sections = append(sections, renderD2Edge(&t, &fk))
		}
	}

	// Render views as rectangle shapes.
	for _, v := range schema.Views {
		sections = append(sections, renderD2View(&v))
	}

	// Render view dependency edges.
	for _, v := range schema.Views {
		for _, dep := range v.DependsOn {
			sections = append(sections, fmt.Sprintf("%s -> %s", v.Name, dep))
		}
	}

	// Render materialized views as rectangle shapes (distinct from regular views).
	for _, mv := range schema.MaterializedViews {
		sections = append(sections, renderD2MaterializedView(&mv))
	}

	// Render materialized view dependency edges.
	for _, mv := range schema.MaterializedViews {
		for _, dep := range mv.DependsOn {
			sections = append(sections, fmt.Sprintf("%s -> %s", mv.Name, dep))
		}
	}

	return strings.Join(sections, "\n") + "\n"
}

// renderD2Table produces a D2 sql_table block for a single table.
func renderD2Table(t *model.Table) string {
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteString(": {\n")
	b.WriteString("  shape: sql_table\n")

	for _, col := range t.Columns {
		b.WriteString("  ")
		b.WriteString(col.Name)
		b.WriteString(": ")
		b.WriteString(col.PGType)

		constraint := columnConstraint(t, col.Name)
		if constraint != "" {
			b.WriteString(" {constraint: ")
			b.WriteString(constraint)
			b.WriteString("}")
		}

		b.WriteString("\n")
	}

	b.WriteString("}")
	return b.String()
}

// columnConstraint returns the D2 constraint annotation for a column.
// Primary key columns get "primary_key", FK columns get "foreign_key".
// If a column is both PK and FK, "primary_key" takes precedence.
func columnConstraint(t *model.Table, colName string) string {
	for _, pk := range t.PK {
		if pk == colName {
			return "primary_key"
		}
	}
	for _, fk := range t.FKs {
		for _, c := range fk.Columns {
			if c == colName {
				return "foreign_key"
			}
		}
	}
	return ""
}

// renderD2Edge produces a D2 edge line for a foreign key relationship.
// Format: source_table.source_col -> ref_table.ref_col: ON_DELETE_ACTION
func renderD2Edge(t *model.Table, fk *model.FK) string {
	// For composite FKs, join column names. Single-column FKs are the common case.
	srcCols := strings.Join(fk.Columns, "_")
	refCols := strings.Join(fk.RefColumns, "_")

	label := fk.OnDelete
	if label == "" {
		label = "NO ACTION"
	}

	return fmt.Sprintf("%s.%s -> %s.%s: %s", t.Name, srcCols, fk.RefTable, refCols, label)
}

// renderD2View produces a D2 rectangle block for a view.
func renderD2View(v *model.View) string {
	var b strings.Builder
	b.WriteString(v.Name)
	b.WriteString(": {\n")
	b.WriteString("  shape: rectangle\n")
	fmt.Fprintf(&b, "  label: \"<<view>>\\n%s\"\n", v.Name)
	b.WriteString("  style.fill: \"#e8f4fd\"\n")
	b.WriteString("}")
	return b.String()
}

// renderD2MaterializedView produces a D2 rectangle block for a materialized view.
func renderD2MaterializedView(mv *model.MaterializedView) string {
	var b strings.Builder
	b.WriteString(mv.Name)
	b.WriteString(": {\n")
	b.WriteString("  shape: rectangle\n")
	fmt.Fprintf(&b, "  label: \"<<materialized view>>\\n%s\"\n", mv.Name)
	b.WriteString("  style.fill: \"#d4edda\"\n")
	b.WriteString("}")
	return b.String()
}

// RenderSVG compiles D2 source text and renders it to SVG bytes.
func RenderSVG(d2Source string) ([]byte, error) {
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		return nil, fmt.Errorf("d2: create text ruler: %w", err)
	}

	layoutName := "dagre"
	compileOpts := &d2lib.CompileOptions{
		Layout: &layoutName,
		LayoutResolver: func(engine string) (d2graph.LayoutGraph, error) {
			return func(ctx context.Context, g *d2graph.Graph) error {
				return d2dagrelayout.Layout(ctx, g, nil)
			}, nil
		},
		Ruler: ruler,
	}

	renderOpts := &d2svg.RenderOpts{}

	// Provide a silent logger to suppress D2's noisy warnings about missing slog.Logger.
	ctx := log.With(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	diagram, _, err := d2lib.Compile(ctx, d2Source, compileOpts, renderOpts)
	if err != nil {
		return nil, fmt.Errorf("d2: compile: %w", err)
	}

	svg, err := d2svg.Render(diagram, renderOpts)
	if err != nil {
		return nil, fmt.Errorf("d2: render SVG: %w", err)
	}

	return svg, nil
}

