package model

import "strings"

// FKEdge represents a single foreign key relationship between two tables.
type FKEdge struct {
	FromTable  string
	FromColumn string
	ToTable    string
	ToColumn   string
	OnDelete   string
	FKName     string
}

// FKGraph is a pre-computed graph of foreign key relationships across all tables.
type FKGraph struct {
	Forward map[string][]FKEdge // table -> tables it references
	Reverse map[string][]FKEdge // table -> tables that reference it
	FanIn   map[string]int      // table -> count of incoming FK constraints
	FanOut  map[string]int      // table -> count of outgoing FK constraints
}

// WalkDirection selects which way WalkCascade traverses FK edges.
type WalkDirection int

const (
	// TowardReferencing follows Reverse edges: from a referenced table into
	// the tables whose FKs point at it. This is the direction ON DELETE
	// actions propagate at runtime (deleting a referenced row mutates rows in
	// the referencing tables).
	TowardReferencing WalkDirection = iota
	// TowardReferenced follows Forward edges: from a referencing table out to
	// the tables it references (toward the potential delete origins whose
	// DELETE would write into the start table).
	TowardReferenced
)

// WalkCascade explores every simple path out of start, following FK edges in
// the given direction. follow reports whether an edge may be traversed;
// firstHop is true for edges directly attached to start. visit is invoked at
// every step with the full edge path from start (len(path) >= 1); the slice
// is reused between calls, so callers must copy it if they retain it. Cycles
// are cut by never revisiting a table already on the current path. Exploring
// all simple paths is worst-case exponential, but FK graphs are small and
// sparse in practice.
func (g *FKGraph) WalkCascade(start string, dir WalkDirection, follow func(edge FKEdge, firstHop bool) bool, visit func(path []FKEdge)) {
	onPath := map[string]bool{start: true}
	var path []FKEdge
	var dfs func(table string)
	dfs = func(table string) {
		edges := g.Reverse[table]
		if dir == TowardReferenced {
			edges = g.Forward[table]
		}
		for _, edge := range edges {
			next := edge.FromTable
			if dir == TowardReferenced {
				next = edge.ToTable
			}
			if onPath[next] {
				continue
			}
			if !follow(edge, len(path) == 0) {
				continue
			}
			path = append(path, edge)
			onPath[next] = true
			visit(path)
			dfs(next)
			delete(onPath, next)
			path = path[:len(path)-1]
		}
	}
	dfs(start)
}

// followCascadeOnly traverses only ON DELETE CASCADE edges: deletes are the
// only action that propagates deletion to further tables.
func followCascadeOnly(edge FKEdge, _ bool) bool {
	return strings.EqualFold(edge.OnDelete, "CASCADE")
}

// CascadeDepth returns the length of the longest ON DELETE CASCADE chain
// triggered by deleting rows from the given table.
func (g *FKGraph) CascadeDepth(table string) int {
	maxDepth := 0
	g.WalkCascade(table, TowardReferencing, followCascadeOnly, func(path []FKEdge) {
		if len(path) > maxDepth {
			maxDepth = len(path)
		}
	})
	return maxDepth
}

// CascadeBreadth returns the total count of distinct tables whose rows are
// deleted when rows are deleted from the given table (transitively, via
// CASCADE edges). Does NOT count the starting table.
func (g *FKGraph) CascadeBreadth(table string) int {
	return len(g.CascadeChain(table))
}

// CascadeChain returns the distinct tables affected by deleting rows from the
// given table, in first-reached DFS order. Does NOT include the starting
// table. Returns nil if no cascade edges exist.
func (g *FKGraph) CascadeChain(table string) []string {
	seen := make(map[string]bool)
	var result []string
	g.WalkCascade(table, TowardReferencing, followCascadeOnly, func(path []FKEdge) {
		t := path[len(path)-1].FromTable
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	})
	if len(result) == 0 {
		return nil
	}
	return result
}

// BuildFKGraph constructs the FK graph from all tables. Safe to call multiple
// times; rebuilds each time. Called automatically by Build() and BuildMulti().
func (s *Schema) BuildFKGraph() {
	g := &FKGraph{
		Forward: make(map[string][]FKEdge),
		Reverse: make(map[string][]FKEdge),
		FanIn:   make(map[string]int),
		FanOut:  make(map[string]int),
	}
	for _, tbl := range s.Tables {
		for _, fk := range tbl.FKs {
			// For multi-column FKs, create one edge per column pair.
			for i := range fk.Columns {
				toCol := ""
				if i < len(fk.RefColumns) {
					toCol = fk.RefColumns[i]
				}
				edge := FKEdge{
					FromTable:  tbl.Name,
					FromColumn: fk.Columns[i],
					ToTable:    fk.RefTable,
					ToColumn:   toCol,
					OnDelete:   fk.OnDelete,
					FKName:     fk.Name,
				}
				g.Forward[tbl.Name] = append(g.Forward[tbl.Name], edge)
				g.Reverse[fk.RefTable] = append(g.Reverse[fk.RefTable], edge)
			}
			// FanIn/FanOut count FK constraints, not columns.
			g.FanOut[tbl.Name]++
			g.FanIn[fk.RefTable]++
		}
	}
	s.FKGraph = g
}
