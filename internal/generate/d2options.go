package generate

import "fmt"

// TableStats is caller-provided live statistics for one table, keyed into
// D2Options.Stats by model.TableKey(schema, name). The generate package never
// fetches these itself — it stays DB-free (roadmap L5: no DB import in
// generate). serve and the CLI fetch them from a live database and inject them
// here so heat-map/live annotations render without generate ever touching pgx.
type TableStats struct {
	// RowCount is the (approximate) live row count. Negative means unknown.
	RowCount int64
	// SeqScanRatio is seq_scan / (seq_scan + idx_scan) when known; negative
	// means unknown. Rendered as a note when the heat map is active.
	SeqScanRatio float64
}

// D2Options controls D2 diagram enrichment, filtering, cardinality, heat maps,
// layout, and rendering. This is deliberately plain presentation configuration
// outside the algebra: only its inputs (the canonical model, the FK graph, the
// imported reference shapes) are law-governed.
//
// Construct with DefaultD2Options and override individual fields; the zero value
// disables every enrichment layer and is NOT the intended default.
type D2Options struct {
	// --- Rendering / layout (9.1) ---

	// Layout selects the layout engine: "dagre" (default) or "elk". "tala" is
	// rejected by Validate — it is not in the OSS d2 library.
	Layout string
	// Theme is the d2 theme id passed to the SVG renderer; 0 = library default.
	Theme int
	// Direction is the top-level d2 layout direction emitted into the diagram
	// source: "down" (default), "right", "left", or "up".
	Direction string

	// --- Enrichment layers (9.2), all default-on, individually disableable ---

	// IndexMarkers annotates indexed / unique columns.
	IndexMarkers bool
	// Nullable annotates nullable columns.
	Nullable bool
	// Comments renders table comments as tooltips.
	Comments bool
	// Checks renders CHECK constraints as notes.
	Checks bool
	// RLSMarkers renders RLS / append-only markers.
	RLSMarkers bool
	// Enums renders enum types as rectangles with their value lists.
	Enums bool

	// --- Filtering (9.3) ---

	// Include is a list of glob patterns matched against bare table names; empty
	// means all tables. A table is kept when it matches any Include pattern (or
	// Include is empty) and no Exclude pattern.
	Include []string
	// Exclude is a list of glob patterns; a matching table is dropped.
	Exclude []string
	// IncludeDependencies, when > 0, also pulls in FK dependencies of the
	// surviving tables within this depth via the depth-bounded FK walker.
	IncludeDependencies int
	// Summary renders names + edges only (no columns).
	Summary bool

	// --- Cardinality (9.4) ---

	// Cardinality renders FK edges with native crow's-foot arrowheads.
	Cardinality bool

	// --- Heat map + live stats (9.5) ---

	// HeatMap colors table borders by graph degree: "" (off), "fan-in", or
	// "fan-out". The scale is a fixed, colorblind-safe, stroke-based ramp.
	HeatMap string
	// Stats holds caller-provided live statistics keyed by model.TableKey.
	Stats map[string]TableStats
}

// DefaultD2Options returns the intended defaults: dagre layout, downward
// direction, every enrichment layer and cardinality enabled, no filtering, and
// no heat map.
func DefaultD2Options() D2Options {
	return D2Options{
		Layout:       "dagre",
		Direction:    "down",
		IndexMarkers: true,
		Nullable:     true,
		Comments:     true,
		Checks:       true,
		RLSMarkers:   true,
		Enums:        true,
		Cardinality:  true,
	}
}

// Validate reports a hard error for any out-of-range option. TALA is rejected
// with a specific message because it is a common request that the OSS library
// cannot satisfy.
func (o D2Options) Validate() error {
	switch o.Layout {
	case "", "dagre", "elk":
	case "tala":
		return fmt.Errorf("d2 layout %q is not available in the OSS d2 library (use dagre or elk)", o.Layout)
	default:
		return fmt.Errorf("d2 layout %q is invalid (must be dagre or elk)", o.Layout)
	}
	switch o.Direction {
	case "", "down", "right", "left", "up":
	default:
		return fmt.Errorf("d2 direction %q is invalid (must be down, right, left, or up)", o.Direction)
	}
	switch o.HeatMap {
	case "", "fan-in", "fan-out":
	default:
		return fmt.Errorf("d2 heat_map %q is invalid (must be fan-in or fan-out)", o.HeatMap)
	}
	if o.IncludeDependencies < 0 {
		return fmt.Errorf("d2 include_dependencies must be >= 0, got %d", o.IncludeDependencies)
	}
	return nil
}
