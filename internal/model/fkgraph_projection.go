package model

import "sort"

// FKNodeProjection is one table node in an FKGraphProjection: its (schema, name)
// identity plus the constraint fan counts.
type FKNodeProjection struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	FanIn  int    `json:"fan_in"`
	FanOut int    `json:"fan_out"`
}

// FKEdgeProjection is one FK edge in an FKGraphProjection. It mirrors FKEdge,
// including the schema qualification of both endpoints and the Imported flag,
// so the projection is a faithful, self-describing snapshot.
type FKEdgeProjection struct {
	FromSchema string `json:"from_schema"`
	FromTable  string `json:"from_table"`
	FromColumn string `json:"from_column"`
	ToSchema   string `json:"to_schema"`
	ToTable    string `json:"to_table"`
	ToColumn   string `json:"to_column"`
	OnDelete   string `json:"on_delete"`
	FKName     string `json:"fk_name"`
	Imported   bool   `json:"imported"`
}

// FKGraphProjection is a deterministic, (schema, name)-keyed, JSON-able snapshot
// of an FKGraph. It is EXCLUDED from schema identity (the graph is a derived
// structure, never a declared one — FKGraph itself is `json:"-"`), but it is
// carried in the API payload so consumers can read the resolved relationship
// graph without re-deriving it. Nodes and edges are sorted, so json.Marshal of
// a projection is stable regardless of the source graph's map iteration order.
type FKGraphProjection struct {
	Nodes []FKNodeProjection `json:"nodes"`
	Edges []FKEdgeProjection `json:"edges"`
}

// Project produces the deterministic projection of the graph. Edges are read
// from Forward (the authoritative edge set — every edge appears exactly once
// there) and sorted; nodes are the union of edge endpoints, each carrying its
// FanIn/FanOut from the graph.
func (g *FKGraph) Project() FKGraphProjection {
	var edges []FKEdgeProjection
	nodeSeen := make(map[string]bool)
	var nodes []FKNodeProjection
	addNode := func(schema, name string) {
		key := TableKey(schema, name)
		if nodeSeen[key] {
			return
		}
		nodeSeen[key] = true
		nodes = append(nodes, FKNodeProjection{
			Schema: schema,
			Name:   name,
			FanIn:  g.FanIn[key],
			FanOut: g.FanOut[key],
		})
	}
	for _, bucket := range g.Forward {
		for _, e := range bucket {
			edges = append(edges, FKEdgeProjection{
				FromSchema: e.FromSchema,
				FromTable:  e.FromTable,
				FromColumn: e.FromColumn,
				ToSchema:   e.ToSchema,
				ToTable:    e.ToTable,
				ToColumn:   e.ToColumn,
				OnDelete:   e.OnDelete,
				FKName:     e.FKName,
				Imported:   e.Imported,
			})
			addNode(e.FromSchema, e.FromTable)
			addNode(e.ToSchema, e.ToTable)
		}
	}

	sort.Slice(edges, func(a, b int) bool { return lessEdgeProjection(edges[a], edges[b]) })
	sort.Slice(nodes, func(a, b int) bool {
		if nodes[a].Schema != nodes[b].Schema {
			return nodes[a].Schema < nodes[b].Schema
		}
		return nodes[a].Name < nodes[b].Name
	})

	return FKGraphProjection{Nodes: nodes, Edges: edges}
}

// lessEdgeProjection is the total ordering used to make edge serialization
// deterministic. It compares every field so identical-looking edges (same
// endpoints, different columns/constraints) still sort stably.
func lessEdgeProjection(a, b FKEdgeProjection) bool {
	for _, pair := range [][2]string{
		{a.FromSchema, b.FromSchema},
		{a.FromTable, b.FromTable},
		{a.FKName, b.FKName},
		{a.FromColumn, b.FromColumn},
		{a.ToSchema, b.ToSchema},
		{a.ToTable, b.ToTable},
		{a.ToColumn, b.ToColumn},
		{a.OnDelete, b.OnDelete},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

// FKGraphFromProjection reconstructs an FKGraph from a projection. Forward and
// Reverse are rebuilt from the edge list (keyed by TableKey); FanIn/FanOut are
// taken from the node records. Project∘FKGraphFromProjection∘Project is the
// identity on the projection (round-trip stable).
func FKGraphFromProjection(p FKGraphProjection) *FKGraph {
	g := &FKGraph{
		Forward: make(map[string][]FKEdge),
		Reverse: make(map[string][]FKEdge),
		FanIn:   make(map[string]int),
		FanOut:  make(map[string]int),
	}
	for _, n := range p.Nodes {
		key := TableKey(n.Schema, n.Name)
		if n.FanIn != 0 {
			g.FanIn[key] = n.FanIn
		}
		if n.FanOut != 0 {
			g.FanOut[key] = n.FanOut
		}
	}
	for _, e := range p.Edges {
		edge := FKEdge{
			FromSchema: e.FromSchema,
			FromTable:  e.FromTable,
			FromColumn: e.FromColumn,
			ToSchema:   e.ToSchema,
			ToTable:    e.ToTable,
			ToColumn:   e.ToColumn,
			OnDelete:   e.OnDelete,
			FKName:     e.FKName,
			Imported:   e.Imported,
		}
		fromKey := TableKey(e.FromSchema, e.FromTable)
		toKey := TableKey(e.ToSchema, e.ToTable)
		g.Forward[fromKey] = append(g.Forward[fromKey], edge)
		g.Reverse[toKey] = append(g.Reverse[toKey], edge)
	}
	return g
}
