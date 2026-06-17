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

// CascadeDepth returns the max depth following ON DELETE CASCADE edges from the
// given table. Uses DFS with a visited set to handle cycles defensively.
func (g *FKGraph) CascadeDepth(table string) int {
	visited := make(map[string]bool)
	return g.cascadeDepthDFS(table, visited)
}

func (g *FKGraph) cascadeDepthDFS(table string, visited map[string]bool) int {
	visited[table] = true
	maxDepth := 0
	for _, edge := range g.Forward[table] {
		if !strings.EqualFold(edge.OnDelete, "CASCADE") {
			continue
		}
		if visited[edge.ToTable] {
			continue
		}
		depth := 1 + g.cascadeDepthDFS(edge.ToTable, visited)
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	visited[table] = false
	return maxDepth
}

// CascadeBreadth returns the total count of distinct tables reachable via
// CASCADE edges starting from the given table. Uses BFS. Does NOT count the
// starting table.
func (g *FKGraph) CascadeBreadth(table string) int {
	return len(g.CascadeChain(table))
}

// CascadeChain returns an ordered list of tables in the cascade path (BFS order).
// Does NOT include the starting table. Returns nil if no cascade edges exist.
func (g *FKGraph) CascadeChain(table string) []string {
	visited := map[string]bool{table: true}
	queue := []string{table}
	var result []string

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range g.Forward[current] {
			if !strings.EqualFold(edge.OnDelete, "CASCADE") {
				continue
			}
			if visited[edge.ToTable] {
				continue
			}
			visited[edge.ToTable] = true
			result = append(result, edge.ToTable)
			queue = append(queue, edge.ToTable)
		}
	}

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
