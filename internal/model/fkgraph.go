package model

import "strings"

// TableKey is THE canonical map key for a table across the model package.
// TablesByName, the FKGraph adjacency maps (Forward/Reverse/FanIn/FanOut), the
// topological sort, and group resolution all key on it. The rule is a single
// function of (schema, name): "<schema>.<name>" when a schema is present, and
// the bare "<name>" when the schema is empty.
//
// This reconciles the two historical conventions — TablesByName's leading-dot
// ".name" form for empty schemas and the FKGraph's schema-blind bare names —
// into one rule, so a table has exactly one identity everywhere and same-named
// tables in different schemas never collide in the graph.
func TableKey(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// FKEdge represents a single foreign key relationship between two tables. Both
// endpoints carry their schema so the edge is unambiguous across schemas; the
// FromTable/ToTable fields remain the bare table names (codegen and workload
// use them for type/identifier derivation), and TableKey combines them into the
// graph's canonical (schema, name) map key.
type FKEdge struct {
	FromSchema string
	FromTable  string
	FromColumn string
	ToSchema   string
	ToTable    string
	ToColumn   string
	OnDelete   string
	FKName     string
	// Imported marks an edge whose referenced endpoint lives in an imported
	// (externally owned) schema. It is false for every edge produced today; the
	// import pipeline (roadmap phase 7) is the only writer. It is carried in the
	// graph projection payload but excluded from schema identity.
	Imported bool
}

// FKGraph is a pre-computed graph of foreign key relationships across all
// tables. Every map is keyed by TableKey(schema, name).
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
// the given direction. start is a TableKey(schema, name) key. maxDepth bounds
// the path length: when maxDepth > 0 the walk never extends a path beyond
// maxDepth edges; maxDepth <= 0 means unbounded. follow reports whether an edge
// may be traversed; firstHop is true for edges directly attached to start.
// visit is invoked at every step with the full edge path from start
// (len(path) >= 1); the slice is reused between calls, so callers must copy it
// if they retain it. Cycles are cut by never revisiting a table already on the
// current path. Exploring all simple paths is worst-case exponential, but FK
// graphs are small and sparse in practice.
func (g *FKGraph) WalkCascade(start string, dir WalkDirection, maxDepth int, follow func(edge FKEdge, firstHop bool) bool, visit func(path []FKEdge)) {
	onPath := map[string]bool{start: true}
	var path []FKEdge
	var dfs func(key string)
	dfs = func(key string) {
		if maxDepth > 0 && len(path) >= maxDepth {
			return
		}
		edges := g.Reverse[key]
		if dir == TowardReferenced {
			edges = g.Forward[key]
		}
		for _, edge := range edges {
			next := TableKey(edge.FromSchema, edge.FromTable)
			if dir == TowardReferenced {
				next = TableKey(edge.ToSchema, edge.ToTable)
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
// triggered by deleting rows from the given table. table is a
// TableKey(schema, name) key.
func (g *FKGraph) CascadeDepth(table string) int {
	maxDepth := 0
	g.WalkCascade(table, TowardReferencing, 0, followCascadeOnly, func(path []FKEdge) {
		if len(path) > maxDepth {
			maxDepth = len(path)
		}
	})
	return maxDepth
}

// CascadeBreadth returns the total count of distinct tables whose rows are
// deleted when rows are deleted from the given table (transitively, via
// CASCADE edges). Does NOT count the starting table. table is a
// TableKey(schema, name) key.
func (g *FKGraph) CascadeBreadth(table string) int {
	return len(g.CascadeChain(table))
}

// CascadeChain returns the distinct tables affected by deleting rows from the
// given table, in first-reached DFS order, as TableKey(schema, name) keys. The
// argument is likewise a TableKey key. Does NOT include the starting table.
// Returns nil if no cascade edges exist.
func (g *FKGraph) CascadeChain(table string) []string {
	seen := make(map[string]bool)
	var result []string
	g.WalkCascade(table, TowardReferencing, 0, followCascadeOnly, func(path []FKEdge) {
		edge := path[len(path)-1]
		t := TableKey(edge.FromSchema, edge.FromTable)
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

// ReferencedTableKeys returns the set of TableKey(schema, name) keys that are the
// target of at least one FK declared by an owned table. This is THE single
// union-aware orphan-detection helper (roadmap 7.3) consumed by both W002
// (internal/validate) and C103 (cmd/pgdesign) — replacing the two divergent
// raw-string scans that keyed on fk.RefSchema+"."+fk.RefTable and so could
// mismatch the TableKey convention (leading-dot vs bare for empty schemas) and
// bypassed the import union. Each FK target is resolved through TableByName (the
// union of owned and imported tables) so an imported-FK target keys correctly and
// never causes a spuriously-orphaned local table; unresolved targets fall back to
// the raw (schema, name) key so a dangling FK is still counted as an incoming
// reference (E204 reports the dangling ref separately).
func (s *Schema) ReferencedTableKeys() map[string]bool {
	referenced := make(map[string]bool)
	for _, t := range s.Tables {
		for _, fk := range t.FKs {
			refSchema := fk.RefSchema
			if refSchema == "" {
				refSchema = t.Schema
			}
			if resolved := s.TableByName(refSchema, fk.RefTable); resolved != nil {
				referenced[TableKey(resolved.Schema, resolved.Name)] = true
			} else {
				referenced[TableKey(refSchema, fk.RefTable)] = true
			}
		}
	}
	return referenced
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
		fromKey := TableKey(tbl.Schema, tbl.Name)
		for _, fk := range tbl.FKs {
			// A bare FK reference (no explicit schema) targets a table in the
			// same schema as the declaring table — the rule resolveFK encodes
			// during Build. Normalize it here so the graph is correctly keyed
			// even for schemas assembled directly from structs.
			refSchema := fk.RefSchema
			if refSchema == "" {
				refSchema = tbl.Schema
			}
			toKey := TableKey(refSchema, fk.RefTable)
			// For multi-column FKs, create one edge per column pair.
			for i := range fk.Columns {
				toCol := ""
				if i < len(fk.RefColumns) {
					toCol = fk.RefColumns[i]
				}
				edge := FKEdge{
					FromSchema: tbl.Schema,
					FromTable:  tbl.Name,
					FromColumn: fk.Columns[i],
					ToSchema:   refSchema,
					ToTable:    fk.RefTable,
					ToColumn:   toCol,
					OnDelete:   fk.OnDelete,
					FKName:     fk.Name,
					// An FK whose ref_table resolved through an import alias points
					// at a table owned by another project (roadmap 7.3, union site
					// 2). RefAlias is the provenance signal set during resolveFK; it
					// is the ONE writer of this flag, which 0.3 introduced but left
					// unset. Marking the edge keeps the imported endpoint keyed in
					// the graph (as a Reverse/Forward target) while telling the graph
					// projection and cascade walkers the node is externally owned.
					Imported: fk.RefAlias != "",
				}
				g.Forward[fromKey] = append(g.Forward[fromKey], edge)
				g.Reverse[toKey] = append(g.Reverse[toKey], edge)
			}
			// FanIn/FanOut count FK constraints, not columns.
			g.FanOut[fromKey]++
			g.FanIn[toKey]++
		}
	}
	s.FKGraph = g
}
