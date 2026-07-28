package model

import "github.com/smm-h/pgdesign/internal/graph"

// topoSort performs topological sort on tables using Kahn's algorithm.
// It uses FK references to build the dependency graph: if table A has an FK
// referencing table B, then B must come before A.
// Tables are identified by schema-qualified names to support multi-schema sorts.
// Ties (tables with no dependency relation) and cycle members are broken
// alphabetically via TopoSortStable, so ordering is independent of the input's
// origin (TOML declaration order vs introspect ORDER BY).
// Returns sorted tables and any cycle groups (sets of mutually-referencing tables).
func topoSort(tables []Table) (sorted []Table, cycles [][]string) {
	getName := func(t Table) string {
		return TableKey(t.Schema, t.Name)
	}
	getDeps := func(t Table) []string {
		var deps []string
		for _, fk := range t.FKs {
			deps = append(deps, TableKey(fk.RefSchema, fk.RefTable))
		}
		return deps
	}
	// FAIL-SAFE BY ACCIDENT (roadmap 7.3): an imported-FK dep names a table that
	// lives in ImportedTables, never in `tables`. TopoSortStable IGNORES deps that
	// reference names outside the item set (documented on graph.TopoSort), so an
	// imported dependency is silently dropped from the ordering — which is exactly
	// correct: imported tables are not ordered or emitted here, they already exist
	// in the framework's schema. This site needs no union wiring; the drop is the
	// right behavior, pinned so a future "resolve all deps" refactor does not
	// accidentally start requiring imported tables to be present.
	sorted, cycleParts := graph.TopoSortStable(tables, getName, getDeps)
	// Convert cycle groups from [][]Table to [][]string (just names).
	for _, group := range cycleParts {
		var names []string
		for _, t := range group {
			names = append(names, t.Name)
		}
		cycles = append(cycles, names)
	}
	return sorted, cycles
}
