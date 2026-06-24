// Package graph provides generic topological sorting and cycle detection algorithms used by model, generate, and format for dependency ordering.
package graph

// TopoSort performs a topological sort using Kahn's algorithm.
// getName extracts a unique string key from each item.
// getDeps returns the names of items that the given item depends on.
// Dependencies that reference names not in the items set are ignored.
// Self-dependencies are ignored.
// Returns sorted items and any cycle groups (items involved in cycles,
// still appended to sorted for completeness).
func TopoSort[T any](items []T, getName func(T) string, getDeps func(T) []string) (sorted []T, cycles [][]T) {
	n := len(items)
	if n == 0 {
		return nil, nil
	}

	// Build name -> index map.
	nameToIdx := make(map[string]int, n)
	for i, item := range items {
		nameToIdx[getName(item)] = i
	}

	// Build in-degree counts and dependents adjacency list.
	// dependents[i] = list of indices that depend on items[i].
	inDegree := make([]int, n)
	dependents := make([][]int, n)
	for i, item := range items {
		name := getName(item)
		for _, dep := range getDeps(item) {
			// Skip self-dependencies.
			if dep == name {
				continue
			}
			// Skip deps not in the items set.
			depIdx, ok := nameToIdx[dep]
			if !ok {
				continue
			}
			inDegree[i]++
			dependents[depIdx] = append(dependents[depIdx], i)
		}
	}

	// Initialize queue with all zero-in-degree indices in input order.
	var queue []int
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	// Process queue.
	emitted := make([]bool, n)
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		emitted[idx] = true
		sorted = append(sorted, items[idx])
		for _, depIdx := range dependents[idx] {
			inDegree[depIdx]--
			if inDegree[depIdx] == 0 {
				queue = append(queue, depIdx)
			}
		}
	}

	// Collect remaining items (cycle members) in input order.
	if len(sorted) < n {
		var cycleGroup []T
		for i, item := range items {
			if !emitted[i] {
				cycleGroup = append(cycleGroup, item)
				sorted = append(sorted, item)
			}
		}
		cycles = append(cycles, cycleGroup)
	}

	return sorted, cycles
}
