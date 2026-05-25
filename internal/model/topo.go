package model

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
	tableByQName := make(map[string]*Table, len(tables))
	for i := range tables {
		qn := qualifiedName(tables[i].Schema, tables[i].Name)
		tableByQName[qn] = &tables[i]
	}

	// Build adjacency: inDegree counts how many FKs point into this table from others.
	// edges[A] = [B] means A depends on B (A has FK referencing B), so B must come first.
	inDegree := make(map[string]int, len(tables))
	dependsOn := make(map[string][]string, len(tables))

	for _, t := range tables {
		qn := qualifiedName(t.Schema, t.Name)
		if _, ok := inDegree[qn]; !ok {
			inDegree[qn] = 0
		}
		for _, fk := range t.FKs {
			refQN := qualifiedName(fk.RefSchema, fk.RefTable)
			// Self-references don't create ordering constraints.
			if refQN == qn {
				continue
			}
			// Only add dependency if the referenced table is in our set.
			// Cross-schema FKs to external schemas (not in this build) are
			// not ordering constraints.
			if _, exists := tableByQName[refQN]; !exists {
				continue
			}
			dependsOn[qn] = append(dependsOn[qn], refQN)
			inDegree[qn]++
		}
	}

	// Kahn's algorithm: start with zero-in-degree nodes.
	var queue []string
	for _, t := range tables {
		qn := qualifiedName(t.Schema, t.Name)
		if inDegree[qn] == 0 {
			queue = append(queue, qn)
		}
	}

	visited := make(map[string]bool, len(tables))
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		visited[name] = true
		if t, ok := tableByQName[name]; ok {
			sorted = append(sorted, *t)
		}

		// For each table that depends on this one, decrement its in-degree.
		for _, t := range tables {
			qn := qualifiedName(t.Schema, t.Name)
			if visited[qn] {
				continue
			}
			for _, dep := range dependsOn[qn] {
				if dep == name {
					inDegree[qn]--
				}
			}
			if inDegree[qn] == 0 && !visited[qn] {
				// Check it's not already in the queue.
				if !inQueue(queue, qn) {
					queue = append(queue, qn)
				}
			}
		}
	}

	// Remaining nodes are in cycles.
	if len(sorted) < len(tables) {
		var cycleGroup []string
		for _, t := range tables {
			qn := qualifiedName(t.Schema, t.Name)
			if !visited[qn] {
				cycleGroup = append(cycleGroup, t.Name)
				sorted = append(sorted, *tableByQName[qn])
			}
		}
		if len(cycleGroup) > 0 {
			cycles = append(cycles, cycleGroup)
		}
	}

	return sorted, cycles
}

func inQueue(queue []string, name string) bool {
	for _, q := range queue {
		if q == name {
			return true
		}
	}
	return false
}
