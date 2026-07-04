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
// Idempotent: if a record with the same version already exists, returns nil.
// If a record with a different version exists, returns an error.
func Baseline(ctx context.Context, conn *pgx.Conn, version string, description string) error {
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

	// Check for existing records.
	existing, err := AppliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	for _, v := range existing {
		if v == version {
			// Same version already recorded: idempotent success.
			return nil
		}
	}

	if len(existing) > 0 {
		return fmt.Errorf("baseline conflict: database already has migration records %v; cannot baseline to %s", existing, version)
	}

	// Record baseline.
	if err := RecordMigration(ctx, conn, version, "baseline", description); err != nil {
		return err
	}

	return nil
}
