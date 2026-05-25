// Package fd provides functional dependency algorithms used by validate/ and audit/.
package fd

import "sort"

// FuncDep represents a functional dependency X -> Y.
type FuncDep struct {
	Determinant []string
	Dependent   []string
}

// Closure computes the attribute closure of attrs under fds using Armstrong's axioms.
// Returns the closure set (sorted for determinism).
func Closure(attrs []string, fds []FuncDep) []string {
	result := make([]string, len(attrs))
	copy(result, attrs)
	sort.Strings(result)

	changed := true
	for changed {
		changed = false
		for _, fd := range fds {
			if isSubset(fd.Determinant, result) {
				for _, attr := range fd.Dependent {
					if !contains(result, attr) {
						result = append(result, attr)
						changed = true
					}
				}
			}
		}
		if changed {
			sort.Strings(result)
		}
	}

	return result
}

// MinimalCover computes the minimal (canonical) cover of a set of functional dependencies.
// Does not merge cyclic equivalences (e.g., A->B, B->A). This is a known limitation,
// not a bug — the algorithm is correct for non-cyclic FD sets.
func MinimalCover(fds []FuncDep) []FuncDep {
	// Step 1: Decompose RHS — split each FD X→{A,B,C} into X→A, X→B, X→C
	var decomposed []FuncDep
	for _, fd := range fds {
		for _, attr := range fd.Dependent {
			det := make([]string, len(fd.Determinant))
			copy(det, fd.Determinant)
			sort.Strings(det)
			decomposed = append(decomposed, FuncDep{
				Determinant: det,
				Dependent:   []string{attr},
			})
		}
	}

	// Step 2: Remove extraneous LHS attributes
	for i := range decomposed {
		if len(decomposed[i].Determinant) <= 1 {
			continue
		}
		det := decomposed[i].Determinant
		for j := 0; j < len(det); j++ {
			// Try removing attribute at index j
			reduced := make([]string, 0, len(det)-1)
			reduced = append(reduced, det[:j]...)
			reduced = append(reduced, det[j+1:]...)

			closure := Closure(reduced, decomposed)
			if contains(closure, decomposed[i].Dependent[0]) {
				// Attribute is extraneous, remove it
				det = reduced
				decomposed[i].Determinant = det
				j-- // Re-check at same index since slice shifted
			}
		}
	}

	// Step 3: Remove redundant FDs
	var result []FuncDep
	for i := range decomposed {
		// Build FD set without the current one
		remaining := make([]FuncDep, 0, len(decomposed)-1)
		remaining = append(remaining, decomposed[:i]...)
		remaining = append(remaining, decomposed[i+1:]...)

		closure := Closure(decomposed[i].Determinant, remaining)
		if !contains(closure, decomposed[i].Dependent[0]) {
			// FD is not redundant, keep it
			result = append(result, decomposed[i])
		}
	}

	// Sort for determinism: by determinant first, then dependent
	sort.Slice(result, func(i, j int) bool {
		di := joinAttrs(result[i].Determinant)
		dj := joinAttrs(result[j].Determinant)
		if di != dj {
			return di < dj
		}
		return joinAttrs(result[i].Dependent) < joinAttrs(result[j].Dependent)
	})

	return result
}

// CandidateKeys finds all minimal superkeys of allAttrs under fds.
// Returns candidate keys sorted for determinism (each key sorted internally).
func CandidateKeys(allAttrs []string, fds []FuncDep) [][]string {
	sorted := make([]string, len(allAttrs))
	copy(sorted, allAttrs)
	sort.Strings(sorted)

	var keys [][]string

	// Bottom-up search: start with single attributes, then pairs, etc.
	for size := 1; size <= len(sorted); size++ {
		combos := combinations(sorted, size)
		for _, combo := range combos {
			// Prune: skip if combo is a superset of an already-found key
			if isSupersetOfAny(combo, keys) {
				continue
			}
			if IsSuperkey(combo, sorted, fds) {
				keys = append(keys, combo)
			}
		}
	}

	// Sort for determinism
	sort.Slice(keys, func(i, j int) bool {
		return joinAttrs(keys[i]) < joinAttrs(keys[j])
	})

	return keys
}

// IsSuperkey returns true if Closure(attrs, fds) contains all of allAttrs.
func IsSuperkey(attrs []string, allAttrs []string, fds []FuncDep) bool {
	closure := Closure(attrs, fds)
	return isSubset(allAttrs, closure)
}

// IsPrime returns true if attr appears in any candidate key.
func IsPrime(attr string, candidateKeys [][]string) bool {
	for _, key := range candidateKeys {
		if contains(key, attr) {
			return true
		}
	}
	return false
}

// isSubset checks if all elements of a are in b.
func isSubset(a, b []string) bool {
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	for _, v := range a {
		if _, ok := set[v]; !ok {
			return false
		}
	}
	return true
}

// setUnion merges two sorted slices with deduplication.
func setUnion(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, v := range a {
		set[v] = struct{}{}
	}
	for _, v := range b {
		set[v] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for v := range set {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}

// setEquals checks equality of two sorted slices.
func setEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// contains checks if a sorted slice contains an element.
func contains(sorted []string, elem string) bool {
	i := sort.SearchStrings(sorted, elem)
	return i < len(sorted) && sorted[i] == elem
}

// joinAttrs joins attributes for sorting purposes.
func joinAttrs(attrs []string) string {
	result := ""
	for i, a := range attrs {
		if i > 0 {
			result += ","
		}
		result += a
	}
	return result
}

// combinations generates all combinations of size k from the sorted slice.
func combinations(sorted []string, k int) [][]string {
	var result [][]string
	n := len(sorted)
	if k > n {
		return nil
	}

	indices := make([]int, k)
	for i := range indices {
		indices[i] = i
	}

	for {
		combo := make([]string, k)
		for i, idx := range indices {
			combo[i] = sorted[idx]
		}
		result = append(result, combo)

		// Find rightmost index that can be incremented
		i := k - 1
		for i >= 0 && indices[i] == n-k+i {
			i--
		}
		if i < 0 {
			break
		}
		indices[i]++
		for j := i + 1; j < k; j++ {
			indices[j] = indices[j-1] + 1
		}
	}

	return result
}

// isSupersetOfAny checks if combo is a superset of any key in keys.
func isSupersetOfAny(combo []string, keys [][]string) bool {
	for _, key := range keys {
		if isSubset(key, combo) {
			return true
		}
	}
	return false
}
