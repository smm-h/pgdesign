package generate

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/model"
)

// junctionEdge describes the M:N relationship a strict junction table collapses
// to: an edge between the two tables it links.
type junctionEdge struct {
	fromID string
	toID   string
	label  string
}

// detectJunctions returns, keyed by model.TableKey, the M:N edge each strict
// junction table collapses to (9.4). A strict junction has exactly two FKs
// whose columns together form the whole primary key, and NO columns outside
// that key. A junction table carrying any extra column is deliberately NOT
// collapsed — it is a first-class entity, not a pure link.
func detectJunctions(schema *model.Schema) map[string]junctionEdge {
	out := make(map[string]junctionEdge)
	for i := range schema.Tables {
		t := &schema.Tables[i]
		if je, ok := strictJunction(t); ok {
			out[model.TableKey(t.Schema, t.Name)] = je
		}
	}
	return out
}

// strictJunction reports whether t is a strict junction table and, if so, the
// M:N edge it collapses to.
func strictJunction(t *model.Table) (junctionEdge, bool) {
	if len(t.FKs) != 2 || len(t.PK) == 0 {
		return junctionEdge{}, false
	}
	pk := sliceSet(t.PK)

	fkCols := make(map[string]bool)
	for _, fk := range t.FKs {
		for _, c := range fk.Columns {
			fkCols[c] = true
		}
	}
	// The two FKs together must be exactly the primary key.
	if !setEqual(fkCols, pk) {
		return junctionEdge{}, false
	}
	// No column may fall outside the primary key.
	for _, col := range t.Columns {
		if !pk[col.Name] {
			return junctionEdge{}, false
		}
	}
	return junctionEdge{
		fromID: fkEndpointID(&t.FKs[0]),
		toID:   fkEndpointID(&t.FKs[1]),
		label:  t.Name,
	}, true
}

// fkEndpointID returns the D2 shape id of an FK's referenced table: the
// schema-qualified reference-shape id for imported targets, else the bare table
// name (matching renderD2Table's shape id).
func fkEndpointID(fk *model.FK) string {
	if fk.RefAlias != "" {
		return fk.RefSchema + "." + fk.RefTable
	}
	return fk.RefTable
}

// renderMNEdge renders a collapsed junction as a single M:N edge with crow's-foot
// "many" arrowheads on both ends.
func renderMNEdge(je junctionEdge) string {
	return fmt.Sprintf("%s -> %s: %s {\n  source-arrowhead: {shape: cf-many}\n  target-arrowhead: {shape: cf-many}\n}",
		je.fromID, je.toID, je.label)
}

// fkColumnsUnique reports whether the given FK columns form a superkey of the
// referencing table — i.e. they equal the primary key, a UNIQUE constraint, or
// a unique index. When true the relationship is 1:1 (at most one child row per
// parent); otherwise it is the 1:N default.
func fkColumnsUnique(t *model.Table, cols []string) bool {
	target := sliceSet(cols)
	if len(t.PK) > 0 && setEqual(sliceSet(t.PK), target) {
		return true
	}
	for _, u := range t.Uniques {
		if setEqual(sliceSet(u.Columns), target) {
			return true
		}
	}
	for _, idx := range t.Indexes {
		if idx.Unique && setEqual(sliceSet(idx.Columns), target) {
			return true
		}
	}
	return false
}

func sliceSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func setEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
