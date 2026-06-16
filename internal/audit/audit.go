// Package audit provides normal form analysis for pgdesign schemas.
// It detects 1NF, 2NF, 3NF, and BCNF violations using declared functional dependencies
// and suggests decompositions via Bernstein's synthesis algorithm.
package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
)

// Audit analyzes a schema for normal form violations and returns diagnostics.
func Audit(schema *model.Schema) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for i := range schema.Tables {
		tbl := &schema.Tables[i]
		diags = append(diags, auditTable(tbl)...)
	}
	return diags
}

func auditTable(tbl *model.Table) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	// Check for implied FDs from PK/UNIQUE that are not declared
	diags = append(diags, inferFDs(tbl)...)

	if len(tbl.Dependencies) == 0 {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Info,
			Table:    tbl.Name,
			Message:  "No functional dependencies declared. NF audit skipped.",
		})
		return diags
	}

	diags = append(diags, check1NF(tbl)...)
	diags = append(diags, check2NF(tbl)...)

	threeNFDiags, hasViolation := check3NF(tbl)
	diags = append(diags, threeNFDiags...)

	if hasViolation {
		diags = append(diags, suggestDecomposition(tbl)...)
	}

	bcnfDiags, _ := checkBCNF(tbl)
	diags = append(diags, bcnfDiags...)

	return diags
}

// check1NF applies heuristics to detect potential 1NF violations.
func check1NF(tbl *model.Table) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, col := range tbl.Columns {
		if col.PGType != "jsonb" {
			continue
		}
		name := strings.ToLower(col.Name)
		isRepeating := strings.HasSuffix(name, "s") ||
			strings.Contains(name, "list") ||
			strings.Contains(name, "items") ||
			strings.Contains(name, "tags") ||
			strings.Contains(name, "values")
		hasArrayDefault := col.Default != nil && *col.Default == "'[]'::jsonb"

		if isRepeating || hasArrayDefault {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "W100",
				Table:    tbl.Name,
				Column:   col.Name,
				Message:  "Column may violate 1NF (contains repeating group). Consider a separate table.",
			})
		}
	}
	return diags
}

// check2NF detects partial dependencies in tables with composite candidate keys.
func check2NF(tbl *model.Table) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	candidateKeys := tbl.CandidateKeys()

	// Only check tables that have at least one composite candidate key
	hasComposite := false
	for _, key := range candidateKeys {
		if len(key) > 1 {
			hasComposite = true
			break
		}
	}
	if !hasComposite {
		return nil
	}

	allAttrs := columnNames(tbl)

	for _, attr := range allAttrs {
		if fd.IsPrime(attr, candidateKeys) {
			continue
		}
		// attr is non-prime; check for partial dependency on any candidate key
		for _, key := range candidateKeys {
			if len(key) <= 1 {
				continue
			}
			subsets := properSubsets(key)
			for _, subset := range subsets {
				closure := fd.Closure(subset, tbl.Dependencies)
				if containsStr(closure, attr) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Warning,
						Code:     "W101",
						Table:    tbl.Name,
						Column:   attr,
						Message: fmt.Sprintf(
							"2NF violation: column '%s' partially depends on '%s' (subset of key '%s'). Consider extracting to a separate table.",
							attr,
							formatAttrs(subset),
							formatAttrs(key),
						),
					})
					// Report only one partial dependency per attribute
					goto nextAttr
				}
			}
		}
	nextAttr:
	}

	// Also check for 2NF using PK if it's composite and not already a candidate key
	_ = allAttrs
	return diags
}

// check3NF detects transitive dependencies. Returns diagnostics and whether any violation was found.
func check3NF(tbl *model.Table) ([]diagnostic.Diagnostic, bool) {
	var diags []diagnostic.Diagnostic
	hasViolation := false

	candidateKeys := tbl.CandidateKeys()
	allAttrs := columnNames(tbl)

	// Decompose all dependencies to single-attribute RHS for checking
	for _, dep := range tbl.Dependencies {
		for _, attr := range dep.Dependent {
			// Check: is X a superkey?
			if fd.IsSuperkey(dep.Determinant, allAttrs, tbl.Dependencies) {
				continue
			}
			// Check: is A prime?
			if fd.IsPrime(attr, candidateKeys) {
				continue
			}
			// Violation
			hasViolation = true
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "W102",
				Table:    tbl.Name,
				Column:   attr,
				Message: fmt.Sprintf(
					"3NF violation: '%s' -> '%s' where %s is not a superkey and %s is not prime. Transitive dependency.",
					formatAttrs(dep.Determinant),
					attr,
					formatAttrs(dep.Determinant),
					attr,
				),
			})
		}
	}

	return diags, hasViolation
}

// checkBCNF detects Boyce-Codd Normal Form violations. Returns diagnostics and whether any violation was found.
func checkBCNF(tbl *model.Table) ([]diagnostic.Diagnostic, bool) {
	var diags []diagnostic.Diagnostic
	hasViolation := false

	allAttrs := columnNames(tbl)

	// Decompose all dependencies to single-attribute RHS for checking
	for _, dep := range tbl.Dependencies {
		for _, attr := range dep.Dependent {
			// Check: is X a superkey?
			if fd.IsSuperkey(dep.Determinant, allAttrs, tbl.Dependencies) {
				continue
			}
			// BCNF violation: determinant is not a superkey (no prime exception)
			hasViolation = true
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "W103",
				Table:    tbl.Name,
				Column:   attr,
				Message: fmt.Sprintf(
					"BCNF violation: '%s' -> '%s' where %s is not a superkey.",
					formatAttrs(dep.Determinant),
					attr,
					formatAttrs(dep.Determinant),
				),
			})
		}
	}

	return diags, hasViolation
}

// suggestDecomposition applies Bernstein's synthesis to suggest a decomposition.
func suggestDecomposition(tbl *model.Table) []diagnostic.Diagnostic {
	minCover := fd.MinimalCover(tbl.Dependencies)

	// Group FDs by determinant
	type group struct {
		determinant []string
		dependents  []string
	}
	groups := make(map[string]*group)
	var order []string // preserve insertion order for determinism

	for _, f := range minCover {
		key := formatAttrs(f.Determinant)
		if _, ok := groups[key]; !ok {
			groups[key] = &group{determinant: f.Determinant}
			order = append(order, key)
		}
		groups[key].dependents = append(groups[key].dependents, f.Dependent...)
	}

	candidateKeys := tbl.CandidateKeys()

	// Build suggested tables
	var suggestions []string
	containsKey := false

	for _, key := range order {
		g := groups[key]
		cols := make([]string, 0, len(g.determinant)+len(g.dependents))
		cols = append(cols, g.determinant...)
		for _, d := range g.dependents {
			if !containsStr(cols, d) {
				cols = append(cols, d)
			}
		}
		sort.Strings(cols)

		tableName := strings.Join(g.determinant, "_")
		suggestions = append(suggestions, fmt.Sprintf("%s(%s)", tableName, strings.Join(cols, ", ")))

		// Check if this table contains a candidate key of the original
		for _, ck := range candidateKeys {
			if isSubsetOf(ck, cols) {
				containsKey = true
			}
		}
	}

	// If no suggested table contains a candidate key, add one
	if !containsKey && len(candidateKeys) > 0 {
		ck := candidateKeys[0]
		sorted := make([]string, len(ck))
		copy(sorted, ck)
		sort.Strings(sorted)
		tableName := strings.Join(sorted, "_")
		suggestions = append(suggestions, fmt.Sprintf("%s(%s)", tableName, strings.Join(sorted, ", ")))
	}

	suggestion := "Suggested decomposition: " + strings.Join(suggestions, "; ")

	return []diagnostic.Diagnostic{{
		Severity:   diagnostic.Info,
		Table:      tbl.Name,
		Message:    "Decomposition suggestion (Bernstein's synthesis).",
		Suggestion: suggestion,
	}}
}

// columnNames returns all column names from a table.
func columnNames(tbl *model.Table) []string {
	names := make([]string, len(tbl.Columns))
	for i, c := range tbl.Columns {
		names[i] = c.Name
	}
	return names
}

// properSubsets returns all proper subsets of a set with size > 0 and size < len(set).
func properSubsets(set []string) [][]string {
	n := len(set)
	if n <= 1 {
		return nil
	}
	var result [][]string
	// Generate all subsets of size 1 to n-1
	for size := 1; size < n; size++ {
		result = append(result, combinationsOf(set, size)...)
	}
	return result
}

// combinationsOf generates all combinations of a given size from the slice.
func combinationsOf(set []string, size int) [][]string {
	if size > len(set) {
		return nil
	}
	var result [][]string
	indices := make([]int, size)
	for i := range indices {
		indices[i] = i
	}
	for {
		combo := make([]string, size)
		for i, idx := range indices {
			combo[i] = set[idx]
		}
		result = append(result, combo)

		i := size - 1
		for i >= 0 && indices[i] == len(set)-size+i {
			i--
		}
		if i < 0 {
			break
		}
		indices[i]++
		for j := i + 1; j < size; j++ {
			indices[j] = indices[j-1] + 1
		}
	}
	return result
}

// containsStr checks if a slice contains a string.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// isSubsetOf checks if all elements of a are in b.
func isSubsetOf(a, b []string) bool {
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

// formatAttrs formats a list of attributes as a comma-separated string.
func formatAttrs(attrs []string) string {
	sorted := make([]string, len(attrs))
	copy(sorted, attrs)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}
