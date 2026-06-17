// Package fd provides functional dependency algorithms used by validate/ and audit/.
package fd

import "sort"

// FuncDep represents a functional dependency X -> Y.
type FuncDep struct {
	Determinant []string
	Dependent   []string
	Source      string // "declared", "discovered", "inferred", or "" (unspecified)
}

// String formats the FD as "{A, B} -> {C, D}".
func (f FuncDep) String() string {
	lhs := make([]string, len(f.Determinant))
	copy(lhs, f.Determinant)
	sort.Strings(lhs)
	rhs := make([]string, len(f.Dependent))
	copy(rhs, f.Dependent)
	sort.Strings(rhs)

	result := "{"
	for i, a := range lhs {
		if i > 0 {
			result += ", "
		}
		result += a
	}
	result += "} -> {"
	for i, a := range rhs {
		if i > 0 {
			result += ", "
		}
		result += a
	}
	result += "}"
	return result
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

// Component represents a relation produced by BCNF decomposition.
type Component struct {
	Name       string    // suggested table name
	Attributes []string  // columns in this component (sorted)
	FDs        []FuncDep // FDs projected onto this component
}

// BCNFDecompose decomposes a relation into BCNF components.
// Returns one Component per BCNF sub-relation, with names derived from the
// original name (e.g., "orders_1", "orders_2").
func BCNFDecompose(name string, allAttrs []string, fds []FuncDep) []Component {
	attrs := make([]string, len(allAttrs))
	copy(attrs, allAttrs)
	sort.Strings(attrs)

	mc := MinimalCover(fds)

	// Check if already in BCNF: every non-trivial FD's determinant is a superkey.
	var violating *FuncDep
	for i, fd := range mc {
		// Skip trivial FDs (dependent is subset of determinant).
		if isSubset(fd.Dependent, fd.Determinant) {
			continue
		}
		if !IsSuperkey(fd.Determinant, attrs, mc) {
			violating = &mc[i]
			break
		}
	}

	if violating == nil {
		projected := projectFDs(attrs, mc)
		return []Component{{
			Name:       name,
			Attributes: attrs,
			FDs:        projected,
		}}
	}

	// Decompose on the violating FD.
	xPlus := Closure(violating.Determinant, mc)

	// R1 = X+ (closure of the violating determinant)
	r1Attrs := make([]string, len(xPlus))
	copy(r1Attrs, xPlus)
	sort.Strings(r1Attrs)

	// R2 = X union (allAttrs minus X+)
	diff := setDifference(attrs, xPlus)
	r2Attrs := setUnion(violating.Determinant, diff)

	// Project FDs onto each sub-relation before recursing.
	r1FDs := projectFDs(r1Attrs, mc)
	r2FDs := projectFDs(r2Attrs, mc)

	left := BCNFDecompose(name, r1Attrs, r1FDs)
	right := BCNFDecompose(name, r2Attrs, r2FDs)

	components := append(left, right...)

	// Renumber if more than one component.
	if len(components) == 1 {
		components[0].Name = name
	} else {
		for i := range components {
			components[i].Name = name + "_" + itoa(i+1)
		}
	}

	return components
}

// projectFDs projects a set of FDs onto a subset of attributes.
// For each FD X->Y where X is a subset of attrs, computes closure(X) under
// the original fds, intersects with attrs, and produces X -> (intersection - X).
// The result is minimized via MinimalCover.
func projectFDs(attrs []string, fds []FuncDep) []FuncDep {
	var projected []FuncDep

	for _, fd := range fds {
		if !isSubset(fd.Determinant, attrs) {
			continue
		}
		closure := Closure(fd.Determinant, fds)
		inter := setIntersection(closure, attrs)
		rhs := setDifference(inter, fd.Determinant)
		if len(rhs) == 0 {
			continue
		}
		projected = append(projected, FuncDep{
			Determinant: fd.Determinant,
			Dependent:   rhs,
		})
	}

	return MinimalCover(projected)
}

// setDifference returns elements in a that are not in b, sorted.
func setDifference(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	var result []string
	for _, v := range a {
		if _, ok := bSet[v]; !ok {
			result = append(result, v)
		}
	}
	sort.Strings(result)
	return result
}

// setIntersection returns elements present in both a and b, sorted.
func setIntersection(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	var result []string
	for _, v := range a {
		if _, ok := bSet[v]; ok {
			result = append(result, v)
		}
	}
	sort.Strings(result)
	return result
}

// IsLosslessJoin checks whether decomposing into r1 and r2 is lossless
// under the given FDs. The decomposition is lossless if the closure of
// (r1 intersect r2) contains all of r1 or all of r2.
func IsLosslessJoin(r1, r2 []string, allAttrs []string, fds []FuncDep) bool {
	inter := setIntersection(r1, r2)
	if len(inter) == 0 {
		return false
	}
	closure := Closure(inter, fds)
	return isSubset(r1, closure) || isSubset(r2, closure)
}

// PreservesDependencies checks whether the projected FDs in the given
// components preserve all original FDs. Returns true if all are preserved,
// along with any lost FDs (with multi-attribute dependents merged).
func PreservesDependencies(original []FuncDep, components []Component) (preserved bool, lost []FuncDep) {
	// Collect all projected FDs from all components.
	var projectedAll []FuncDep
	for _, comp := range components {
		projectedAll = append(projectedAll, comp.FDs...)
	}

	// Decompose each original FD to single-attribute RHS and check.
	// Track lost FDs keyed by determinant for merging.
	type lostEntry struct {
		det  []string
		deps []string
	}
	lostMap := make(map[string]*lostEntry)

	for _, fd := range original {
		for _, attr := range fd.Dependent {
			// Check if X -> attr is preserved.
			closure := Closure(fd.Determinant, projectedAll)
			if !contains(closure, attr) {
				key := joinAttrs(fd.Determinant)
				if entry, ok := lostMap[key]; ok {
					if !contains(entry.deps, attr) {
						entry.deps = append(entry.deps, attr)
						sort.Strings(entry.deps)
					}
				} else {
					det := make([]string, len(fd.Determinant))
					copy(det, fd.Determinant)
					sort.Strings(det)
					lostMap[key] = &lostEntry{
						det:  det,
						deps: []string{attr},
					}
				}
			}
		}
	}

	if len(lostMap) == 0 {
		return true, nil
	}

	// Collect and sort lost FDs for determinism.
	lost = make([]FuncDep, 0, len(lostMap))
	for _, entry := range lostMap {
		lost = append(lost, FuncDep{
			Determinant: entry.det,
			Dependent:   entry.deps,
		})
	}
	sort.Slice(lost, func(i, j int) bool {
		di := joinAttrs(lost[i].Determinant)
		dj := joinAttrs(lost[j].Determinant)
		if di != dj {
			return di < dj
		}
		return joinAttrs(lost[i].Dependent) < joinAttrs(lost[j].Dependent)
	})

	return false, lost
}

// ArmstrongRelation generates a small counterexample table showing redundancy
// for a specific FD X->A in relation to the full attribute set. Two rows agree
// on X (and A, since X determines A) but differ on other attributes, making
// the redundancy visible. Cap at 10 rows.
func ArmstrongRelation(allAttrs []string, fds []FuncDep, violating FuncDep) []map[string]string {
	sorted := make([]string, len(allAttrs))
	copy(sorted, allAttrs)
	sort.Strings(sorted)

	// Compute what the violating determinant determines
	closure := Closure(violating.Determinant, fds)

	// Build two rows that agree on everything in X+ (the closure of the
	// determinant) and differ on everything else. This shows redundancy:
	// X values are the same, A values are the same (determined by X),
	// but non-determined attributes differ.
	row1 := make(map[string]string, len(sorted))
	row2 := make(map[string]string, len(sorted))

	for _, attr := range sorted {
		if contains(closure, attr) {
			// Attributes determined by X: same in both rows
			row1[attr] = attr + "1"
			row2[attr] = attr + "1"
		} else {
			// Attributes NOT determined by X: differ between rows
			row1[attr] = attr + "1"
			row2[attr] = attr + "2"
		}
	}

	// Add a third row with different determinant values to show
	// that the FD holds (different X -> can have different A).
	row3 := make(map[string]string, len(sorted))
	for _, attr := range sorted {
		row3[attr] = attr + "3"
	}

	return []map[string]string{row1, row2, row3}
}

// FormatRelation formats rows as a text table for diagnostic output.
func FormatRelation(allAttrs []string, rows []map[string]string) string {
	sorted := make([]string, len(allAttrs))
	copy(sorted, allAttrs)
	sort.Strings(sorted)

	// Cap at 10 rows
	truncated := false
	displayRows := rows
	if len(displayRows) > 10 {
		displayRows = displayRows[:10]
		truncated = true
	}

	// Build header
	result := "  |"
	for _, attr := range sorted {
		result += " " + attr + " |"
	}
	result += "\n"

	// Build rows
	for _, row := range displayRows {
		result += "  |"
		for _, attr := range sorted {
			val := row[attr]
			// Pad to match header column width
			width := len(attr)
			for len(val) < width {
				val += " "
			}
			result += " " + val + " |"
		}
		result += "\n"
	}

	if truncated {
		result += "  (truncated to 10 rows)\n"
	}

	return result
}

// itoa converts a small non-negative integer to its string representation
// without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
