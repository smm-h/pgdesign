package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

// SquashResult holds the result of a legacy-mode (semver-TOML) squash.
//
// The op-list OPTIMIZER (inverse-pair cancellation, sequential type merging, and
// CREATE TABLE folding) was RETIRED in roadmap 5.3: squash is now defined as
// ORDERED CONCATENATION, never a rewriting system (the roadmap: "today's
// optimizeDDLOps and its tests ... RETIRE with it as superseded dead code"). The
// legacy path keeps only the minimal concatenation + phase-strip + down-build
// mechanics pre-upgrade projects still need. Chain-mode consolidation lives in
// squash_chain.go.
type SquashResult struct {
	Squashed      *Migration // The combined migration
	OriginalPaths []string   // Paths of original migration files that were squashed
	OriginalCount int        // Number of migrations squashed
}

// SquashMigrations squashes all LEGACY (semver-TOML) migrations in the given
// directory from version `from` to version `to` (both inclusive) into a single
// migration. Chain-mode projects use SquashChain (squash_chain.go); this path is
// guarded off for them.
//
// It is a MANDATORY-DB operation: the caller must supply a live connection so
// the M200 applied-version safety check can run. Squashing a range that
// contains applied migrations would desynchronize the LEGACY tracking table, so
// the operation refuses. This blocks offline squash even of never-applied ranges
// (a deliberate stopgap for the legacy path; chain mode replaces the M200 refusal
// with the consolidation model — originals archive intact and mid-range databases
// resume via the path-finder — so applied state is irrelevant there).
func SquashMigrations(ctx context.Context, conn *pgx.Conn, dir, from, to string) (*SquashResult, error) {
	if err := guardChainMode(dir, "squash", "5.3"); err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("migrate squash requires a database connection (--db) for the M200 applied-version safety check; offline squash is not permitted, even for never-applied ranges")
	}

	// M200: refuse to squash a range that contains any applied migration.
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		return nil, err
	}
	applied, err := AppliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}
	var appliedInRange []string
	for _, v := range applied {
		if InSemverRange(v, from, to) {
			appliedInRange = append(appliedInRange, v)
		}
	}
	if len(appliedInRange) > 0 {
		return nil, fmt.Errorf("M200: cannot squash: %d migration(s) in range [%s, %s] have been applied: %v; squashing would desynchronize the tracking table", len(appliedInRange), from, to, appliedInRange)
	}

	return squashFiles(dir, from, to)
}

// squashFiles performs the pure file-level squash mechanics with no database
// interaction. SquashMigrations wraps it with the M200 safety check.
func squashFiles(dir, from, to string) (*SquashResult, error) {
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

	// Concatenate all ops in order. The op-list optimizer (cancellation / merging
	// / CREATE TABLE folding) was retired in roadmap 5.3 — squash is ORDERED
	// CONCATENATION, never a rewriting system. DML/RawSQL ops are preserved
	// verbatim by construction (concatenation never drops or folds).
	var allDDL []DDLOp
	var allDML []DMLOp
	for _, m := range migrations {
		allDDL = append(allDDL, m.DDLOps...)
		allDML = append(allDML, m.DMLOps...)
	}

	// Build combined down ops (reversibility propagates: any irreversible member
	// makes the whole squash irreversible).
	squashedDown := buildSquashedDown(allDDL, allDML)
	for i := range allDDL {
		if i < len(squashedDown) {
			allDDL[i].Down = squashedDown[i]
		}
	}

	// Strip phase annotations: squashed output is end-state DDL. ConsolidatedOps
	// carried on a create_table (populated by generate for the TOML round-trip,
	// unrelated to the retired squash optimizer) get their phases stripped too.
	for i := range allDDL {
		allDDL[i].Phase = ""
		for j := range allDDL[i].ConsolidatedOps {
			allDDL[i].ConsolidatedOps[j].Phase = ""
		}
	}
	for i := range allDML {
		allDML[i].Phase = ""
	}

	squashed := &Migration{
		Version:     to,
		Description: fmt.Sprintf("Squashed from %s to %s", from, to),
		DDLOps:      allDDL,
		DMLOps:      allDML,
	}

	return &SquashResult{
		Squashed:      squashed,
		OriginalPaths: originalPaths,
		OriginalCount: len(inRange),
	}, nil
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

// LegacyArchiveDir is the sibling directory under a legacy (semver-TOML)
// migrations dir into which squash-superseded originals retire. It mirrors
// chain mode's migrations/archive/ (chainArchiveDir).
const LegacyArchiveDir = "archive"

// ArchiveLegacyOriginals retires the given legacy (semver-TOML) migration files
// INTACT into a sibling migrations/archive/ directory via a pure-Go file move.
// This honors the same "retire originals, never destroy" contract chain-mode
// squash keeps (moveEdgeToArchive), but shells out to NOTHING: it must run on CI
// runners and consumer machines that have no developer-only file-archival tool on
// PATH. Returns the destination paths in the order given.
func ArchiveLegacyOriginals(dir string, paths []string) ([]string, error) {
	archiveDir := filepath.Join(dir, LegacyArchiveDir)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, err
	}
	archived := make([]string, 0, len(paths))
	for _, p := range paths {
		name := filepath.Base(p)
		dst := filepath.Join(archiveDir, name)
		if err := os.Rename(p, dst); err != nil {
			return nil, fmt.Errorf("archive %s: %w", name, err)
		}
		archived = append(archived, dst)
	}
	return archived, nil
}
