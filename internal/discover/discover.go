// Package discover provides automatic functional dependency discovery from live
// PostgreSQL data using the TANE algorithm (Huhtala et al., 1999).
package discover

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/fd"
)

// Options controls FD discovery behavior.
type Options struct {
	SampleSize           int     // default 5000
	MaxColumns           int     // default 20
	ApproximateThreshold float64 // default 0.0 (exact only)
}

// defaults fills zero-valued fields with sensible defaults.
func (o Options) defaults() Options {
	if o.SampleSize <= 0 {
		o.SampleSize = 5000
	}
	if o.MaxColumns <= 0 {
		o.MaxColumns = 20
	}
	return o
}

// Discover connects to a PostgreSQL database, samples rows from the given table,
// and runs the TANE algorithm to find functional dependencies that hold in the
// sample. Discovered FDs are empirical (data-based), not semantic declarations.
func Discover(conn *pgx.Conn, schemaName, tableName string, opts Options) ([]fd.FuncDep, []diagnostic.Diagnostic, error) {
	opts = opts.defaults()
	ctx := context.Background()

	// Step 1: Query column names.
	columns, err := queryColumnNames(ctx, conn, schemaName, tableName)
	if err != nil {
		return nil, nil, fmt.Errorf("discover: query columns for %s.%s: %w", schemaName, tableName, err)
	}

	if len(columns) == 0 {
		return nil, nil, nil
	}

	// Step 2: Check column count limit.
	if len(columns) > opts.MaxColumns {
		diag := diagnostic.Diagnostic{
			Severity: diagnostic.Info,
			Table:    tableName,
			Message: fmt.Sprintf(
				"Table has %d columns (limit: %d), skipping FD discovery. Declare FDs manually or select a column subset.",
				len(columns), opts.MaxColumns,
			),
		}
		return nil, []diagnostic.Diagnostic{diag}, nil
	}

	// Step 3: Sample data.
	data, err := sampleData(ctx, conn, schemaName, tableName, columns, opts.SampleSize)
	if err != nil {
		return nil, nil, fmt.Errorf("discover: sample data for %s.%s: %w", schemaName, tableName, err)
	}

	if len(data) == 0 {
		return nil, nil, nil
	}

	// Step 4-5: Run TANE.
	fds := tane(columns, data, opts.ApproximateThreshold)

	return fds, nil, nil
}

// queryColumnNames returns the column names of a table in attnum order.
func queryColumnNames(ctx context.Context, conn *pgx.Conn, schema, table string) ([]string, error) {
	qualifiedName := schema + "." + table
	rows, err := conn.Query(ctx, `
		SELECT attname
		FROM pg_attribute
		WHERE attrelid = $1::regclass
		  AND attnum > 0
		  AND NOT attisdropped
		ORDER BY attnum
	`, qualifiedName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// sampleData fetches a random sample of rows, returning each row as a slice of
// string values (NULL represented as the empty string with a special sentinel).
func sampleData(ctx context.Context, conn *pgx.Conn, schema, table string, columns []string, limit int) ([][]string, error) {
	// Build quoted column list for safety.
	quotedCols := make([]string, len(columns))
	for i, c := range columns {
		quotedCols[i] = pgQuoteIdent(c)
	}
	colList := strings.Join(quotedCols, ", ")

	query := fmt.Sprintf(
		"SELECT %s FROM %s.%s ORDER BY RANDOM() LIMIT %d",
		colList,
		pgQuoteIdent(schema),
		pgQuoteIdent(table),
		limit,
	)

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data [][]string
	for rows.Next() {
		vals := make([]interface{}, len(columns))
		ptrs := make([]interface{}, len(columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make([]string, len(columns))
		for i, v := range vals {
			if v == nil {
				// Use a sentinel that cannot collide with real data.
				row[i] = "\x00NULL\x00"
			} else {
				row[i] = fmt.Sprintf("%v", v)
			}
		}
		data = append(data, row)
	}
	return data, rows.Err()
}

// pgQuoteIdent quotes a PostgreSQL identifier.
func pgQuoteIdent(name string) string {
	// Double any embedded double-quotes, then wrap in double-quotes.
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// --- TANE algorithm ---

// partition represents an equivalence partition: groups of row indices that share
// the same value(s) for some attribute set. Only non-singleton classes are stored
// (singletons don't affect FD checking and waste memory).
type partition struct {
	classes [][]int
	// numRows is the total number of rows (including singletons not stored in classes).
	numRows int
}

// numClasses returns the total number of equivalence classes, including
// implicit singleton classes.
func (p *partition) numClasses() int {
	nonSingletonRows := 0
	for _, c := range p.classes {
		nonSingletonRows += len(c)
	}
	return len(p.classes) + (p.numRows - nonSingletonRows)
}

// buildPartition builds the initial partition for a single column.
func buildPartition(data [][]string, colIdx int) *partition {
	groups := make(map[string][]int)
	for rowIdx, row := range data {
		key := row[colIdx]
		groups[key] = append(groups[key], rowIdx)
	}

	p := &partition{numRows: len(data)}
	for _, g := range groups {
		if len(g) > 1 {
			p.classes = append(p.classes, g)
		}
	}
	return p
}

// partitionProduct computes the product (intersection) of two partitions.
// The result contains only equivalence classes of size > 1.
func partitionProduct(p1, p2 *partition) *partition {
	// Build a lookup from row index to class index for p2.
	rowToClass := make(map[int]int)
	for classIdx, class := range p2.classes {
		for _, rowIdx := range class {
			rowToClass[rowIdx] = classIdx
		}
	}

	result := &partition{numRows: p1.numRows}

	for _, class := range p1.classes {
		// Split this class by p2's classes.
		subgroups := make(map[int][]int)
		// Rows not in any p2 non-singleton class get a unique negative key.
		singletonCounter := -1
		for _, rowIdx := range class {
			if classIdx, ok := rowToClass[rowIdx]; ok {
				subgroups[classIdx] = append(subgroups[classIdx], rowIdx)
			} else {
				// This row is in a singleton class in p2, so it forms its own group.
				subgroups[singletonCounter] = append(subgroups[singletonCounter], rowIdx)
				singletonCounter--
			}
		}
		for _, sg := range subgroups {
			if len(sg) > 1 {
				result.classes = append(result.classes, sg)
			}
		}
	}

	return result
}

// attrSet is a sorted slice of column indices used as a key in the lattice.
type attrSet []int

func (a attrSet) key() string {
	var b strings.Builder
	for i, idx := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", idx)
	}
	return b.String()
}

func attrSetWithout(s attrSet, elem int) attrSet {
	result := make(attrSet, 0, len(s)-1)
	for _, v := range s {
		if v != elem {
			result = append(result, v)
		}
	}
	return result
}

func attrSetUnion(a, b attrSet) attrSet {
	seen := make(map[int]bool, len(a)+len(b))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		seen[v] = true
	}
	result := make(attrSet, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	sort.Ints(result)
	return result
}

func attrSetContains(s attrSet, elem int) bool {
	for _, v := range s {
		if v == elem {
			return true
		}
	}
	return false
}

// tane runs the TANE algorithm on in-memory data and returns discovered FDs.
func tane(columns []string, data [][]string, approxThreshold float64) []fd.FuncDep {
	numCols := len(columns)
	numRows := len(data)

	// Cache of partitions keyed by attribute set.
	partitions := make(map[string]*partition)

	// Build level-1 partitions (single columns).
	for i := 0; i < numCols; i++ {
		p := buildPartition(data, i)
		partitions[attrSet{i}.key()] = p
	}

	// getPartition retrieves or computes the partition for an attribute set.
	var getPartition func(s attrSet) *partition
	getPartition = func(s attrSet) *partition {
		k := s.key()
		if p, ok := partitions[k]; ok {
			return p
		}
		if len(s) < 2 {
			return &partition{numRows: numRows}
		}
		prefix := make(attrSet, len(s)-1)
		copy(prefix, s[:len(s)-1])
		last := attrSet{s[len(s)-1]}

		pPrefix := getPartition(prefix)
		pLast := getPartition(last)
		result := partitionProduct(pPrefix, pLast)
		partitions[k] = result
		return result
	}

	var discovered []fd.FuncDep

	// RHS candidates for each attribute set X: attributes A where X -> A might hold.
	type candidateInfo struct {
		rhs map[int]bool
	}

	// Level 1 setup.
	currentLevel := make(map[string]attrSet)
	rhsCandidates := make(map[string]*candidateInfo)
	for i := 0; i < numCols; i++ {
		s := attrSet{i}
		k := s.key()
		currentLevel[k] = s
		ci := &candidateInfo{rhs: make(map[int]bool)}
		for j := 0; j < numCols; j++ {
			ci.rhs[j] = true
		}
		rhsCandidates[k] = ci
	}

	// Pruned sets: superkeys whose supersets need not be explored.
	pruned := make(map[string]bool)

	// Level-1 special case: single-attribute superkeys directly determine all
	// other columns, but the standard TANE check (X\{A} -> A) cannot test this
	// because X\{A} is empty. Handle it explicitly.
	for _, x := range currentLevel {
		if len(x) != 1 {
			continue
		}
		col := x[0]
		pX := getPartition(x)
		if pX.numClasses() != numRows {
			continue
		}
		// Column col is a superkey: it determines every other column.
		for j := 0; j < numCols; j++ {
			if j == col {
				continue
			}
			discovered = append(discovered, fd.FuncDep{
				Determinant: []string{columns[col]},
				Dependent:   []string{columns[j]},
			})
		}
		// Clear RHS candidates and mark as pruned.
		if ci, ok := rhsCandidates[x.key()]; ok {
			ci.rhs = make(map[int]bool)
		}
		pruned[x.key()] = true
	}

	for level := 2; level <= numCols; level++ {
		// Generate next level candidates (apriori-gen) from currentLevel.
		nextLevel := make(map[string]attrSet)

		var sets []attrSet
		for _, s := range currentLevel {
			if !pruned[s.key()] {
				sets = append(sets, s)
			}
		}

		sort.Slice(sets, func(i, j int) bool {
			return sets[i].key() < sets[j].key()
		})

		for i := 0; i < len(sets); i++ {
			for j := i + 1; j < len(sets); j++ {
				if level > 2 && !sharesPrefix(sets[i], sets[j], level-2) {
					continue
				}
				candidate := attrSetUnion(sets[i], sets[j])
				if len(candidate) != level {
					continue
				}

				ck := candidate.key()
				if _, exists := nextLevel[ck]; exists {
					continue
				}

				// Verify all (level-1)-sized subsets are in current level.
				allSubsetsPresent := true
				for k := 0; k < len(candidate); k++ {
					sub := attrSetWithout(candidate, candidate[k])
					if _, ok := currentLevel[sub.key()]; !ok {
						allSubsetsPresent = false
						break
					}
				}
				if !allSubsetsPresent {
					continue
				}

				nextLevel[ck] = candidate

				// Compute RHS candidates: intersection of RHS of all subsets.
				newRHS := make(map[int]bool)
				first := true
				for k := 0; k < len(candidate); k++ {
					sub := attrSetWithout(candidate, candidate[k])
					subCI, ok := rhsCandidates[sub.key()]
					if !ok {
						newRHS = nil
						break
					}
					if first {
						for attr := range subCI.rhs {
							newRHS[attr] = true
						}
						first = false
					} else {
						for attr := range newRHS {
							if !subCI.rhs[attr] {
								delete(newRHS, attr)
							}
						}
					}
				}
				if len(newRHS) > 0 {
					rhsCandidates[ck] = &candidateInfo{rhs: newRHS}
				}
			}
		}

		if len(nextLevel) == 0 {
			break
		}

		// Check FDs at this level.
		for _, x := range nextLevel {
			xKey := x.key()
			ci, ok := rhsCandidates[xKey]
			if !ok {
				continue
			}

			for _, a := range x {
				if !ci.rhs[a] {
					continue
				}

				xMinusA := attrSetWithout(x, a)
				if len(xMinusA) == 0 {
					continue
				}

				pXMinusA := getPartition(xMinusA)
				pX := getPartition(x)

				if fdHolds(pXMinusA, pX, approxThreshold, numRows) {
					det := make([]string, len(xMinusA))
					for i, idx := range xMinusA {
						det[i] = columns[idx]
					}
					discovered = append(discovered, fd.FuncDep{
						Determinant: det,
						Dependent:   []string{columns[a]},
					})
					delete(ci.rhs, a)
				}
			}

			// Superkey pruning.
			pX := getPartition(x)
			if pX.numClasses() == numRows {
				pruned[xKey] = true
			}
		}

		currentLevel = nextLevel
	}

	return discovered
}

// fdHolds checks whether adding the extra attribute to the partition doesn't
// split any equivalence class. For exact FDs (threshold=0), this means
// |p1| == |pCombined|. For approximate FDs, the error ratio must be below threshold.
func fdHolds(pDeterminant, pCombined *partition, threshold float64, numRows int) bool {
	detClasses := pDeterminant.numClasses()
	combClasses := pCombined.numClasses()

	if threshold == 0.0 {
		return detClasses == combClasses
	}

	// Approximate: error = (|pCombined| - |pDeterminant|) / numRows
	if numRows == 0 {
		return true
	}
	errorRatio := float64(combClasses-detClasses) / float64(numRows)
	return errorRatio <= threshold
}

// sharesPrefix checks if two sorted attribute sets share the first n elements.
func sharesPrefix(a, b attrSet, n int) bool {
	if len(a) < n || len(b) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
