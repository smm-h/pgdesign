package model

import "sort"

// sortedByName returns a copy of items sorted alphabetically by the key
// returned from name. The input slice is never mutated.
func sortedByName[T any](items []T, name func(T) string) []T {
	result := make([]T, len(items))
	copy(result, items)
	sort.Slice(result, func(i, j int) bool {
		return name(result[i]) < name(result[j])
	})
	return result
}

// SortedFKs returns FKs sorted alphabetically by name.
func SortedFKs(fks []FK) []FK {
	return sortedByName(fks, func(fk FK) string { return fk.Name })
}

// SortedUniques returns unique constraints sorted alphabetically by name.
func SortedUniques(uqs []UniqueConstraint) []UniqueConstraint {
	return sortedByName(uqs, func(uq UniqueConstraint) string { return uq.Name })
}

// SortedChecks returns check constraints sorted alphabetically by name.
func SortedChecks(cks []CheckConstraint) []CheckConstraint {
	return sortedByName(cks, func(ck CheckConstraint) string { return ck.Name })
}

// SortedExclusions returns exclusion constraints sorted alphabetically by name.
func SortedExclusions(excls []ExclusionConstraint) []ExclusionConstraint {
	return sortedByName(excls, func(ex ExclusionConstraint) string { return ex.Name })
}

// SortedIndexes returns indexes sorted alphabetically by name.
func SortedIndexes(idxs []Index) []Index {
	return sortedByName(idxs, func(idx Index) string { return idx.Name })
}

// SortedPolicies returns policies sorted alphabetically by name.
func SortedPolicies(pols []Policy) []Policy {
	return sortedByName(pols, func(p Policy) string { return p.Name })
}

// SortedTriggers returns triggers sorted alphabetically by name.
func SortedTriggers(trigs []Trigger) []Trigger {
	return sortedByName(trigs, func(tr Trigger) string { return tr.Name })
}
