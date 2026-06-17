package migrate

import (
	"fmt"
	"path/filepath"
)

// SquashResult holds the result of a squash operation.
type SquashResult struct {
	Squashed        *Migration // The combined migration
	OriginalPaths   []string   // Paths of original migration files that were squashed
	OriginalCount   int        // Number of migrations squashed
	CancelledPairs  int        // Number of inverse pairs cancelled
	MergedOps       int        // Number of ops merged (e.g., sequential type changes)
	ConsolidatedOps int        // Number of ops folded into CREATE TABLE
}

// SquashMigrations squashes all migrations in the given directory from version
// `from` to version `to` (both inclusive) into a single migration.
func SquashMigrations(dir, from, to string) (*SquashResult, error) {
	// Validate semver format.
	if _, _, _, err := semverParts(from); err != nil {
		return nil, fmt.Errorf("invalid --from version %q: %w", from, err)
	}
	if _, _, _, err := semverParts(to); err != nil {
		return nil, fmt.Errorf("invalid --to version %q: %w", to, err)
	}

	// from must be <= to.
	if compareSemver(from, to) > 0 {
		return nil, fmt.Errorf("--from %q is greater than --to %q", from, to)
	}

	// Discover all migration files.
	allMigrations, err := discoverMigrations(dir)
	if err != nil {
		return nil, fmt.Errorf("discover migrations: %w", err)
	}

	// Filter to the [from, to] range.
	var inRange []migrationFile
	for _, mf := range allMigrations {
		if compareSemver(mf.version, from) >= 0 && compareSemver(mf.version, to) <= 0 {
			inRange = append(inRange, mf)
		}
	}

	if len(inRange) == 0 {
		return nil, fmt.Errorf("no migrations found in range [%s, %s]", from, to)
	}
	if len(inRange) == 1 {
		return nil, fmt.Errorf("only one migration in range [%s, %s]; nothing to squash", from, to)
	}

	// Parse all migrations in order.
	var migrations []*Migration
	var originalPaths []string
	for _, mf := range inRange {
		m, err := ParseMigrationFile(mf.path)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", mf.path, err)
		}
		m.Version = mf.version
		migrations = append(migrations, m)
		originalPaths = append(originalPaths, mf.path)
	}

	// Concatenate all ops.
	var allDDL []DDLOp
	var allDML []DMLOp
	for _, m := range migrations {
		allDDL = append(allDDL, m.DDLOps...)
		allDML = append(allDML, m.DMLOps...)
	}

	// Optimize: cancel inverse pairs, merge sequential type changes,
	// and consolidate ops into CREATE TABLE.
	optimizedDDL, cancelledPairs, mergedOps, consolidatedOps := optimizeDDLOps(allDDL)

	// Build combined down ops.
	squashedDown := buildSquashedDown(optimizedDDL, allDML)

	// Apply down ops to the optimized DDL.
	for i := range optimizedDDL {
		if i < len(squashedDown) {
			optimizedDDL[i].Down = squashedDown[i]
		}
	}

	// Strip phase annotations: squashed output is end-state DDL.
	for i := range optimizedDDL {
		optimizedDDL[i].Phase = ""
		for j := range optimizedDDL[i].ConsolidatedOps {
			optimizedDDL[i].ConsolidatedOps[j].Phase = ""
		}
	}
	for i := range allDML {
		allDML[i].Phase = ""
	}

	squashed := &Migration{
		Version:     to,
		Description: fmt.Sprintf("Squashed from %s to %s", from, to),
		DDLOps:      optimizedDDL,
		DMLOps:      allDML,
	}

	return &SquashResult{
		Squashed:        squashed,
		OriginalPaths:   originalPaths,
		OriginalCount:   len(inRange),
		CancelledPairs:  cancelledPairs,
		MergedOps:       mergedOps,
		ConsolidatedOps: consolidatedOps,
	}, nil
}

// optimizeDDLOps cancels inverse pairs, merges sequential type changes, and
// consolidates add/FK/index/constraint ops into preceding create_table ops.
// Returns the optimized ops and counts of each optimization applied.
func optimizeDDLOps(ops []DDLOp) ([]DDLOp, int, int, int) {
	cancelledPairs := 0
	mergedOps := 0
	consolidatedCount := 0

	// Build a list tracking which ops are cancelled.
	cancelled := make([]bool, len(ops))

	// Pass 1: Cancel inverse pairs.
	// Scan for add/drop pairs on the same target. Later op cancels earlier op.
	for i := 0; i < len(ops); i++ {
		if cancelled[i] {
			continue
		}
		for j := i + 1; j < len(ops); j++ {
			if cancelled[j] {
				continue
			}
			if isInversePair(ops[i], ops[j]) {
				cancelled[i] = true
				cancelled[j] = true
				cancelledPairs++
				break
			}
		}
	}

	// Collect non-cancelled ops.
	var result []DDLOp
	for i, op := range ops {
		if !cancelled[i] {
			result = append(result, op)
		}
	}

	// Pass 2: Merge sequential alter_column_type on the same table.column.
	// Keep only the final type change.
	merged := make([]bool, len(result))
	for i := 0; i < len(result); i++ {
		if merged[i] || result[i].Op != "alter_column_type" {
			continue
		}
		for j := i + 1; j < len(result); j++ {
			if merged[j] {
				continue
			}
			if result[j].Op == "alter_column_type" &&
				result[j].Table == result[i].Table &&
				result[j].Column == result[i].Column {
				// Keep j (the later one), cancel i.
				merged[i] = true
				mergedOps++
				break
			}
		}
	}

	var final []DDLOp
	for i, op := range result {
		if !merged[i] {
			final = append(final, op)
		}
	}

	// Pass 3: Consolidate add_column, add_fk, create_index, add_unique,
	// add_check, add_exclusion into a preceding create_table on the same table.
	consolidatable := map[string]bool{
		"add_column": true, "add_fk": true, "create_index": true, "add_index": true,
		"add_unique": true, "add_check": true, "add_exclusion": true,
	}

	consolidated := make([]bool, len(final))
	for i := 0; i < len(final); i++ {
		if final[i].Op != "create_table" {
			continue
		}
		for j := i + 1; j < len(final); j++ {
			if consolidated[j] {
				continue
			}
			if !consolidatable[final[j].Op] {
				continue
			}
			if final[j].Table != final[i].Table {
				continue
			}
			final[i].ConsolidatedOps = append(final[i].ConsolidatedOps, final[j])
			consolidated[j] = true
			consolidatedCount++
		}
	}

	var afterConsolidate []DDLOp
	for i, op := range final {
		if !consolidated[i] {
			afterConsolidate = append(afterConsolidate, op)
		}
	}

	return afterConsolidate, cancelledPairs, mergedOps, consolidatedCount
}

// isInversePair returns true if op2 undoes op1.
func isInversePair(op1, op2 DDLOp) bool {
	inversePairs := [][2]string{
		{"add_column", "drop_column"},
		{"create_table", "drop_table"},
		{"create_index", "drop_index"},
		{"create_index_concurrently", "drop_index"},
		{"create_index_concurrently", "drop_index_concurrently"},
		{"add_fk", "drop_fk"},
		{"add_unique", "drop_unique"},
		{"add_check", "drop_check"},
		{"create_enum", "drop_enum"},
		{"set_not_null", "drop_not_null"},
		{"create_function", "drop_function"},
		{"create_trigger", "drop_trigger"},
	}

	for _, pair := range inversePairs {
		if op1.Op == pair[0] && op2.Op == pair[1] {
			return sameTarget(op1, op2)
		}
	}
	return false
}

// sameTarget checks if two ops target the same database object.
func sameTarget(op1, op2 DDLOp) bool {
	switch op1.Op {
	case "add_column", "drop_column":
		return op1.Table == op2.Table && op1.Column == op2.Column
	case "create_table", "drop_table":
		return op1.Table == op2.Table
	case "create_index", "create_index_concurrently", "drop_index", "drop_index_concurrently":
		return op1.Name == op2.Name
	case "add_fk", "drop_fk":
		return op1.Name == op2.Name
	case "add_unique", "drop_unique":
		return op1.Name == op2.Name
	case "add_check", "drop_check":
		return op1.Name == op2.Name
	case "create_enum", "drop_enum":
		return op1.Name == op2.Name
	case "set_not_null", "drop_not_null":
		return op1.Table == op2.Table && op1.Column == op2.Column
	case "create_function", "drop_function":
		return op1.Name == op2.Name
	case "create_trigger", "drop_trigger":
		return op1.Name == op2.Name
	default:
		return false
	}
}

// buildSquashedDown creates down ops for each DDL op in the squashed migration.
// For each op, it uses the existing down if present. If any op in the original
// set was irreversible, the entire squashed migration is marked irreversible.
func buildSquashedDown(ddlOps []DDLOp, dmlOps []DMLOp) []*DownOp {
	// Check if any original down was irreversible.
	anyIrreversible := false
	for _, op := range ddlOps {
		if op.Down != nil && op.Down.Irreversible {
			anyIrreversible = true
			break
		}
	}
	if !anyIrreversible {
		for _, op := range dmlOps {
			if op.Down != nil && op.Down.Irreversible {
				anyIrreversible = true
				break
			}
		}
	}

	downs := make([]*DownOp, len(ddlOps))
	for i, op := range ddlOps {
		if anyIrreversible {
			downs[i] = &DownOp{Irreversible: true}
		} else if op.Down != nil {
			downs[i] = op.Down
		}
	}
	return downs
}

// OutputPath returns the path for the squashed migration file.
func OutputPath(dir, toVersion string) string {
	return filepath.Join(dir, toVersion+".toml")
}
