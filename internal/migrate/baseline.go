package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Baseline marks a database as being at a specific migration version without
// actually applying any migrations. This is used when adopting pgdesign
// migrations for an existing database whose schema was created by other means.
//
// All discovered migration files with version <= targetVersion are recorded as
// baseline-applied. This ensures that a subsequent Apply sees them as already
// applied and skips them.
//
// Additive idempotency: re-running baseline records any versions that are
// discovered but not yet recorded (versions <= target). A conflict is reported
// only when a previously recorded version is absent from the discovered set
// (true divergence -- the migration file was deleted).
//
// Out-of-order guard: if a discovered migration file has a version < the
// maximum already-applied version and is not yet recorded, this indicates a
// migration file was added after later versions were applied. This is a hard
// error requiring explicit adoption.
func Baseline(ctx context.Context, conn *pgx.Conn, migrationsDir string, targetVersion string, description string) error {
	// Acquire advisory lock (same pattern as Apply).
	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("another migration is in progress (could not acquire advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		return err
	}

	// Discover migration files.
	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		return fmt.Errorf("discover migrations: %w", err)
	}

	// Filter to versions <= targetVersion.
	var toRecord []migrationFile
	for _, mf := range migrations {
		if compareSemver(mf.version, targetVersion) <= 0 {
			toRecord = append(toRecord, mf)
		}
	}

	if len(toRecord) == 0 {
		return fmt.Errorf("no migration files found with version <= %s in %s", targetVersion, migrationsDir)
	}

	// Validate that the target version actually exists in the discovered set.
	targetFound := false
	for _, mf := range toRecord {
		if mf.version == targetVersion {
			targetFound = true
			break
		}
	}
	if !targetFound {
		return fmt.Errorf("target version %s not found in migrations directory %s", targetVersion, migrationsDir)
	}

	// Check for existing records.
	existing, err := AppliedVersions(ctx, conn)
	if err != nil {
		return err
	}
	existingSet := make(map[string]bool, len(existing))
	for _, v := range existing {
		existingSet[v] = true
	}

	// Build the set of versions we want to record.
	toRecordSet := make(map[string]bool, len(toRecord))
	for _, mf := range toRecord {
		toRecordSet[mf.version] = true
	}

	// Divergence check: any recorded version absent from discovered set is
	// true divergence (migration file was deleted).
	for _, v := range existing {
		if compareSemver(v, targetVersion) <= 0 && !toRecordSet[v] {
			return fmt.Errorf("baseline divergence: version %s is recorded in the database but no corresponding migration file exists in %s; this may indicate a deleted migration file", v, migrationsDir)
		}
	}

	// Find maximum already-applied version for out-of-order guard.
	var maxApplied string
	for _, v := range existing {
		if maxApplied == "" || compareSemver(v, maxApplied) > 0 {
			maxApplied = v
		}
	}

	// Out-of-order guard: a discovered file with version < max-applied that
	// is NOT already recorded means it was added after later versions were
	// applied. This is a hard error.
	if maxApplied != "" {
		for _, mf := range toRecord {
			if compareSemver(mf.version, maxApplied) < 0 && !existingSet[mf.version] {
				return fmt.Errorf("out-of-order migration detected: version %s was discovered but version %s is already applied; this migration file appears to have been added after later versions were applied -- use 'migrate baseline --adopt' to explicitly confirm adoption of out-of-order migrations", mf.version, maxApplied)
			}
		}
	}

	// Record all versions that are not yet recorded.
	for _, mf := range toRecord {
		if existingSet[mf.version] {
			continue // Already recorded: skip (additive idempotency).
		}
		if err := RecordMigration(ctx, conn, mf.version, "baseline", description); err != nil {
			return err
		}
	}

	return nil
}
