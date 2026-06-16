package model

import "github.com/smm-h/pgdesign/internal/graph"

// qualifiedName returns "schema.table" for use as a unique key in topo sort.
// Falls back to just the table name if schema is empty.
func qualifiedName(schema, table string) string {
	if schema == "" {
		return table
	}
	return schema + "." + table
}

// topoSort performs topological sort on tables using Kahn's algorithm.
// It uses FK references to build the dependency graph: if table A has an FK
// referencing table B, then B must come before A.
// Tables are identified by schema-qualified names to support multi-schema sorts.
// Returns sorted tables and any cycle groups (sets of mutually-referencing tables).
func topoSort(tables []Table) (sorted []Table, cycles [][]string) {
	getName := func(t Table) string {
		return qualifiedName(t.Schema, t.Name)
	}
	getDeps := func(t Table) []string {
		var deps []string
		for _, fk := range t.FKs {
			deps = append(deps, qualifiedName(fk.RefSchema, fk.RefTable))
		}
		return deps
	}
	sorted, cycleParts := graph.TopoSort(tables, getName, getDeps)
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
