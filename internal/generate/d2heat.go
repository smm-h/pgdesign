package generate

import "github.com/smm-h/pgdesign/internal/model"

// heatColor maps a graph degree (fan-in or fan-out) to a fixed, colorblind-safe
// stroke color (ColorBrewer single-hue "Blues" sequential ramp, documented
// colorblind- and print-safe). The ramp is applied to the table BORDER, never
// the fill: a d2 sql_table's fill only tints the header, so a stroke is the
// honest whole-shape signal. Degree 0 (an isolated table) is a neutral grey so
// it reads as "no relationships" rather than the low end of the ramp.
func heatColor(degree int) string {
	switch {
	case degree <= 0:
		return "#bdbdbd"
	case degree == 1:
		return "#bdd7e7"
	case degree <= 3:
		return "#6baed6"
	case degree <= 6:
		return "#3182bd"
	default:
		return "#08519c"
	}
}

// tableHeatColor returns the heat stroke color for a table, or "" when the heat
// map is off or the FK graph is unavailable. fan-in counts incoming FK
// constraints (how many tables reference this one); fan-out counts outgoing.
func tableHeatColor(schema *model.Schema, t *model.Table, opts D2Options) string {
	if opts.HeatMap == "" || schema.FKGraph == nil {
		return ""
	}
	key := model.TableKey(t.Schema, t.Name)
	var degree int
	switch opts.HeatMap {
	case "fan-in":
		degree = schema.FKGraph.FanIn[key]
	case "fan-out":
		degree = schema.FKGraph.FanOut[key]
	}
	return heatColor(degree)
}

// tableStats returns the caller-provided live statistics for a table, or nil.
func tableStats(t *model.Table, opts D2Options) *TableStats {
	if opts.Stats == nil {
		return nil
	}
	if st, ok := opts.Stats[model.TableKey(t.Schema, t.Name)]; ok {
		return &st
	}
	return nil
}
