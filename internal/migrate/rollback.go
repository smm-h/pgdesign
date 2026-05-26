package migrate

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

// Rollback rolls back the most recently applied migration.
// Returns the version that was rolled back. lockTimeout sets the PostgreSQL
// lock_timeout (e.g. "5s"); empty defaults to "5s".
func Rollback(ctx context.Context, conn *pgx.Conn, migrationsDir string, lockTimeout string) (string, error) {
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		return "", err
	}

	// Acquire advisory lock.
	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return "", err
	}
	if !acquired {
		return "", fmt.Errorf("another migration is in progress (could not acquire advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	// Find the most recent applied version.
	applied, err := AppliedVersions(ctx, conn)
	if err != nil {
		return "", err
	}
	if len(applied) == 0 {
		return "", fmt.Errorf("no migrations to rollback")
	}
	latest := applied[len(applied)-1]

	// Load the migration file.
	path := filepath.Join(migrationsDir, latest+".toml")
	m, err := ParseMigrationFile(path)
	if err != nil {
		return "", fmt.Errorf("parse migration %s: %w", latest, err)
	}

	// Check for irreversible ops.
	if err := checkReversibility(m); err != nil {
		return "", fmt.Errorf("migration %s: %w", latest, err)
	}

	// Set lock_timeout.
	if lockTimeout == "" {
		lockTimeout = "5s"
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%s'", lockTimeout)); err != nil {
		return "", fmt.Errorf("set lock_timeout: %w", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Execute DML down ops in reverse order.
	for i := len(m.DMLOps) - 1; i >= 0; i-- {
		op := m.DMLOps[i]
		if op.Down == nil || len(op.Down.Ops) == 0 {
			continue
		}
		for _, downOp := range op.Down.Ops {
			stmt := OpToSQL(downOp)
			for _, s := range splitStatements(stmt) {
				if _, err := tx.Exec(ctx, s); err != nil {
					return "", fmt.Errorf("DML rollback op %d: %w\n  SQL: %s", i, err, s)
				}
			}
		}
	}

	// Execute DDL down ops in reverse declaration order.
	for i := len(m.DDLOps) - 1; i >= 0; i-- {
		op := m.DDLOps[i]
		if op.Down == nil || len(op.Down.Ops) == 0 {
			continue
		}
		for _, downOp := range op.Down.Ops {
			stmt := OpToSQL(downOp)
			if stmt == "" {
				continue
			}

			if IsNonTransactional(downOp) {
				if err := tx.Commit(ctx); err != nil {
					return "", fmt.Errorf("commit before non-transactional rollback op %d: %w", i, err)
				}
				for _, s := range splitStatements(stmt) {
					if _, err := conn.Exec(ctx, s); err != nil {
						return "", fmt.Errorf("non-transactional rollback op %d (%s): %w", i, downOp.Op, err)
					}
				}
				tx, err = conn.Begin(ctx)
				if err != nil {
					return "", fmt.Errorf("begin after non-transactional rollback op %d: %w", i, err)
				}
				defer tx.Rollback(ctx)
				continue
			}

			for _, s := range splitStatements(stmt) {
				if _, err := tx.Exec(ctx, s); err != nil {
					return "", fmt.Errorf("DDL rollback op %d (%s): %w\n  SQL: %s", i, downOp.Op, err, s)
				}
			}
		}
	}

	// Remove from migrations table.
	if _, err := tx.Exec(ctx,
		"DELETE FROM pgdesign_migrations WHERE version = $1", latest); err != nil {
		return "", fmt.Errorf("remove migration record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit rollback: %w", err)
	}

	return latest, nil
}

// checkReversibility verifies that all ops in the migration have reversible
// down ops. Returns an error if any op is irreversible.
func checkReversibility(m *Migration) error {
	for i, op := range m.DDLOps {
		if op.Down != nil && op.Down.Irreversible {
			return fmt.Errorf("DDL op %d (%s on %s) is irreversible", i, op.Op, opTarget(op))
		}
	}
	for i, op := range m.DMLOps {
		if op.Down != nil && op.Down.Irreversible {
			return fmt.Errorf("DML op %d (%s) is irreversible", i, op.Op)
		}
	}
	return nil
}
