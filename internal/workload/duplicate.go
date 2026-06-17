package workload

// IndexInfo contains minimal index information for duplicate detection.
type IndexInfo struct {
	Schema  string
	Table   string
	Name    string
	Columns []string
}

// DuplicateIndex describes a pair where one index is a leading-column prefix of another.
type DuplicateIndex struct {
	Schema        string `json:"schema"`
	Table         string `json:"table"`
	Index         string `json:"index"`
	SupersetIndex string `json:"superset_index"`
}

// FindDuplicateIndexes detects indexes where one is a leading-column prefix of another.
// Only strict prefixes count (same columns is not a duplicate).
func FindDuplicateIndexes(indexes []IndexInfo) []DuplicateIndex {
	// Group indexes by (schema, table) to avoid cross-table comparisons.
	type key struct{ schema, table string }
	groups := make(map[key][]IndexInfo)
	for _, idx := range indexes {
		k := key{idx.Schema, idx.Table}
		groups[k] = append(groups[k], idx)
	}

	var duplicates []DuplicateIndex
	for _, group := range groups {
		for i := range group {
			for j := range group {
				if i == j {
					continue
				}
				a, b := group[i], group[j]
				if len(a.Columns) < len(b.Columns) && isPrefix(a.Columns, b.Columns) {
					duplicates = append(duplicates, DuplicateIndex{
						Schema:        a.Schema,
						Table:         a.Table,
						Index:         a.Name,
						SupersetIndex: b.Name,
					})
				}
			}
		}
	}
	return duplicates
}

// isPrefix reports whether a is a leading-column prefix of b.
func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
