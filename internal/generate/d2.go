package generate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2layouts/d2elklayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/lib/log"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

// GenerateD2 produces D2 diagram language text from a resolved schema.
// Each table is rendered as a sql_table shape with columns listed,
// and FK relationships appear as labeled edges with the ON DELETE action.
// When reg is non-nil, state machine types are rendered as state diagrams.
// opts controls layout direction, enrichment layers, filtering, cardinality,
// and heat maps; pass DefaultD2Options for the intended defaults.
func GenerateD2(schema *model.Schema, reg *semtype.Registry, opts D2Options) string {
	var sections []string

	// Top-level layout direction (9.1). d2's own default is "down"; emitting it
	// explicitly keeps the diagram source self-describing.
	if opts.Direction != "" {
		sections = append(sections, fmt.Sprintf("direction: %s", opts.Direction))
	}

	tables := schema.TableOrder()

	// Render enum types as rectangles with their value lists (9.2 enrichment).
	if opts.Enums {
		for i := range schema.Enums {
			sections = append(sections, renderD2Enum(&schema.Enums[i]))
		}
	}

	// Render each table as a D2 sql_table shape.
	for i := range tables {
		sections = append(sections, renderD2Table(&tables[i], opts))
		if note := renderD2CheckNote(&tables[i], opts); note != "" {
			sections = append(sections, note)
		}
	}

	// Render imported tables as minimal REFERENCE shapes (roadmap 7.3/7.4, union
	// site 4). They are owned by another project, so they get a distinct,
	// schema-qualified reference shape rather than a full sql_table — this is the
	// first-class reference shape class phase 9 preserves. They are nested under a
	// container named for their target schema, so the shape id is schema-qualified
	// and FK edges can point at it unambiguously.
	for i := range schema.ImportedTables {
		sections = append(sections, renderD2ImportedRef(&schema.ImportedTables[i]))
	}

	// Render FK edges after all tables.
	for _, t := range tables {
		for _, fk := range t.FKs {
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

	// Render state machine state diagrams.
	if reg != nil {
		for _, td := range reg.StateMachineTypes() {
			sections = append(sections, renderD2StateMachine(td))
		}
	}

	return strings.Join(sections, "\n") + "\n"
}

// renderD2StateMachine produces a D2 container with oval state nodes and
// directed transition edges for a state machine type.
func renderD2StateMachine(td *semtype.TypeDef) string {
	var b strings.Builder
	b.WriteString(td.Name)
	b.WriteString(": {\n")
	fmt.Fprintf(&b, "  label: \"<<state machine>>\\n%s\"\n", td.Name)

	// Render each state as an oval.
	for _, s := range td.States {
		b.WriteString("  ")
		b.WriteString(s.Name)
		b.WriteString(": {\n")
		b.WriteString("    shape: oval\n")
		if s.Name == td.InitialState {
			b.WriteString("    style.bold: true\n")
		}
		if s.Terminal {
			b.WriteString("    style.stroke-width: 3\n")
		}
		b.WriteString("  }\n")
	}

	// Render transition edges.
	for _, tr := range td.Transitions {
		for _, from := range tr.From {
			fmt.Fprintf(&b, "  %s -> %s: %s\n", from, tr.To, tr.Name)
		}
	}

	b.WriteString("}")
	return b.String()
}

// renderD2Table produces a D2 sql_table block for a single table. The
// enrichment layers in opts (index/unique markers, nullable indicator, comment
// tooltip, RLS/append-only markers) are conditionally applied; all default on.
// In summary mode the columns are omitted entirely (names + edges only).
func renderD2Table(t *model.Table, opts D2Options) string {
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteString(": {\n")
	b.WriteString("  shape: sql_table\n")

	// RLS / append-only markers ride on the header label so the id stays the
	// bare table name (FK edges reference the id). Only emitted when the marker
	// layer is on and the table actually carries such a property, so plain
	// tables keep their default header.
	if opts.RLSMarkers {
		var marks []string
		if t.EnableRLS {
			if t.ForceRLS {
				marks = append(marks, "RLS forced")
			} else {
				marks = append(marks, "RLS")
			}
		}
		if t.AppendOnly {
			marks = append(marks, "append-only")
		}
		if len(marks) > 0 {
			fmt.Fprintf(&b, "  label: %q\n", t.Name+" ["+strings.Join(marks, ", ")+"]")
		}
	}

	// Table comment as a tooltip.
	if opts.Comments && t.Comment != "" {
		fmt.Fprintf(&b, "  tooltip: %q\n", oneLine(t.Comment))
	}

	if !opts.Summary {
		for _, cp := range deriveColumnPresentations(t) {
			b.WriteString("  ")
			b.WriteString(cp.Name)
			b.WriteString(": ")
			b.WriteString(cp.Type)

			if cs := columnConstraints(cp, opts); len(cs) > 0 {
				if len(cs) == 1 {
					fmt.Fprintf(&b, " {constraint: %s}", cs[0])
				} else {
					fmt.Fprintf(&b, " {constraint: [%s]}", strings.Join(cs, "; "))
				}
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("}")
	return b.String()
}

// columnConstraints returns the ordered D2 constraint annotations for a column.
// primary_key/foreign_key/unique are d2-native (rendered PK/FK/UNQ); "idx" and
// "nullable" are plain-string markers (rendered verbatim) gated by their layer
// toggles. PK takes precedence over unique/idx (a PK column is not also tagged).
func columnConstraints(cp columnPresentation, opts D2Options) []string {
	var cs []string
	if cp.IsPK {
		cs = append(cs, "primary_key")
	}
	if cp.IsFK {
		cs = append(cs, "foreign_key")
	}
	if opts.IndexMarkers && !cp.IsPK {
		if cp.IsUnique {
			cs = append(cs, "unique")
		} else if cp.Indexed {
			cs = append(cs, "idx")
		}
	}
	if opts.Nullable && cp.Nullable {
		cs = append(cs, "nullable")
	}
	return cs
}

// renderD2CheckNote produces a companion "note" shape listing a table's CHECK
// constraints, attached by a dashed undirected connection. Returns "" when the
// checks layer is off or the table has no checks. d2 sql_table columns cannot
// carry children, so checks live in an adjacent page shape rather than inline.
func renderD2CheckNote(t *model.Table, opts D2Options) string {
	if !opts.Checks || len(t.Checks) == 0 {
		return ""
	}
	var lines []string
	for _, ck := range t.Checks {
		if ck.Name != "" {
			lines = append(lines, ck.Name+": "+oneLine(ck.Expr))
		} else {
			lines = append(lines, oneLine(ck.Expr))
		}
	}
	noteID := t.Name + "_checks"
	var b strings.Builder
	fmt.Fprintf(&b, "%s: {\n", noteID)
	b.WriteString("  shape: page\n")
	fmt.Fprintf(&b, "  label: %q\n", "CHECK\n"+strings.Join(lines, "\n"))
	b.WriteString("  style.fill: \"#fff8dc\"\n")
	b.WriteString("  style.stroke-dash: 3\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "%s -- %s: {style.stroke-dash: 3}", t.Name, noteID)
	return b.String()
}

// renderD2Enum produces a D2 rectangle block for an enum type, listing its
// values (9.2 enrichment).
func renderD2Enum(e *model.Enum) string {
	var b strings.Builder
	b.WriteString(e.Name)
	b.WriteString(": {\n")
	b.WriteString("  shape: rectangle\n")
	fmt.Fprintf(&b, "  label: %q\n", "<<enum>>\n"+e.Name+"\n"+strings.Join(e.Values, "\n"))
	b.WriteString("  style.fill: \"#f3e8fd\"\n")
	b.WriteString("}")
	return b.String()
}

// oneLine collapses newlines and escapes nothing else; d2 %q-quoted strings
// handle quote/backslash escaping, but literal newlines would break a
// single-line label, so they are replaced with spaces.
func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
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

	// Imported FK targets (roadmap 7.3, union site 4): point at the
	// schema-qualified reference shape (container.shape), NOT a bare table name.
	// The previous emitter dropped fk.RefSchema entirely, so a cross-project edge
	// would dangle or collide with a same-named local table. The reference shape is
	// minimal (no columns), so the edge connects to the shape itself.
	if fk.RefAlias != "" {
		return fmt.Sprintf("%s.%s -> %s.%s: %s", t.Name, srcCols, fk.RefSchema, fk.RefTable, label)
	}

	return fmt.Sprintf("%s.%s -> %s.%s: %s", t.Name, srcCols, fk.RefTable, refCols, label)
}

// renderD2ImportedRef renders an imported table as a minimal, schema-qualified
// reference shape. It is nested under a container named for the table's target
// schema, so its D2 id is "<schema>.<name>" — the qualification FK edges use to
// target it. It carries only a label and a distinct dashed style; the columns are
// deliberately omitted (imported tables are references, not this project's DDL).
func renderD2ImportedRef(t *model.Table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s.%s: {\n", t.Schema, t.Name)
	fmt.Fprintf(&b, "  shape: page\n")
	fmt.Fprintf(&b, "  label: \"<<imported>>\\n%s.%s\"\n", t.Schema, t.Name)
	b.WriteString("  style.fill: \"#eeeeee\"\n")
	b.WriteString("  style.stroke-dash: 3\n")
	b.WriteString("}")
	return b.String()
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

// RenderSVG compiles D2 source text and renders it to SVG bytes. opts selects
// the layout engine (dagre or elk — TALA is not in the OSS library) and the
// SVG theme id; direction is a source-level concern already emitted by
// GenerateD2, so it is not re-applied here.
func RenderSVG(d2Source string, opts D2Options) ([]byte, error) {
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		return nil, fmt.Errorf("d2: create text ruler: %w", err)
	}

	layoutName := opts.Layout
	if layoutName == "" {
		layoutName = "dagre"
	}
	var layoutFn func(context.Context, *d2graph.Graph) error
	switch layoutName {
	case "elk":
		layoutFn = func(ctx context.Context, g *d2graph.Graph) error {
			return d2elklayout.Layout(ctx, g, nil)
		}
	default: // dagre
		layoutName = "dagre"
		layoutFn = func(ctx context.Context, g *d2graph.Graph) error {
			return d2dagrelayout.Layout(ctx, g, nil)
		}
	}
	compileOpts := &d2lib.CompileOptions{
		Layout: &layoutName,
		LayoutResolver: func(engine string) (d2graph.LayoutGraph, error) {
			return layoutFn, nil
		},
		Ruler: ruler,
	}

	renderOpts := &d2svg.RenderOpts{}
	if opts.Theme != 0 {
		themeID := int64(opts.Theme)
		renderOpts.ThemeID = &themeID
	}

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
