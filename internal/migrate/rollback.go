package migrate

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/sqlparse"
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
			stmts, err := sqlparse.SplitStatements(stmt)
			if err != nil {
				return "", fmt.Errorf("parse DML rollback op %d: %w", i, err)
			}
			for _, s := range stmts {
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
				stmts, err := sqlparse.SplitStatements(stmt)
				if err != nil {
					return "", fmt.Errorf("parse non-transactional rollback op %d (%s): %w", i, downOp.Op, err)
				}
				for _, s := range stmts {
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

			stmts, err := sqlparse.SplitStatements(stmt)
			if err != nil {
				return "", fmt.Errorf("parse DDL rollback op %d (%s): %w", i, downOp.Op, err)
			}
			for _, s := range stmts {
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

// RollbackTo rolls back all migrations from the most recent down to (but not
// including) the target version. All intermediate migrations are pre-checked
// for reversibility before any rollback begins. Returns the list of versions
// that were successfully rolled back. On partial failure, returns both the
// rolled-back versions and the error.
func RollbackTo(ctx context.Context, conn *pgx.Conn, migrationsDir, targetVersion, lockTimeout string) ([]string, error) {
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		return nil, err
	}

	// Validate target version format.
	if _, _, _, err := semverParts(targetVersion); err != nil {
		return nil, fmt.Errorf("invalid --to version %q: %w", targetVersion, err)
	}

	// Acquire advisory lock.
	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("another migration is in progress (could not acquire advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	// Find applied versions (sorted ascending by semver).
	applied, err := AppliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}
	if len(applied) == 0 {
		return nil, fmt.Errorf("no migrations to rollback")
	}

	// Verify target version is in the applied list.
	targetFound := false
	for _, v := range applied {
		if v == targetVersion {
			targetFound = true
			break
		}
	}
	if !targetFound {
		return nil, fmt.Errorf("target version %q is not in the applied migrations", targetVersion)
	}

	// Collect versions to rollback: everything after targetVersion, in reverse order.
	var toRollback []string
	for i := len(applied) - 1; i >= 0; i-- {
		if applied[i] == targetVersion {
			break
		}
		toRollback = append(toRollback, applied[i])
	}

	if len(toRollback) == 0 {
		return nil, fmt.Errorf("version %q is already the latest applied migration; nothing to rollback", targetVersion)
	}

	// Pre-check: load and verify reversibility of ALL migrations before starting.
	migrations := make([]*Migration, len(toRollback))
	for i, version := range toRollback {
		path := filepath.Join(migrationsDir, version+".toml")
		m, err := ParseMigrationFile(path)
		if err != nil {
			return nil, fmt.Errorf("parse migration %s: %w", version, err)
		}
		if err := checkReversibility(m); err != nil {
			return nil, fmt.Errorf("cannot rollback to %s: migration %s is irreversible: %w", targetVersion, version, err)
		}
		migrations[i] = m
	}

	// Set lock_timeout.
	if lockTimeout == "" {
		lockTimeout = "5s"
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%s'", lockTimeout)); err != nil {
		return nil, fmt.Errorf("set lock_timeout: %w", err)
	}

	// Execute rollbacks in order (most recent first).
	var rolledBack []string
	for i, version := range toRollback {
		m := migrations[i]

		tx, err := conn.Begin(ctx)
		if err != nil {
			return rolledBack, fmt.Errorf("begin rollback of %s: %w", version, err)
		}

		rollbackErr := func() error {
			// Execute DML down ops in reverse order.
			for j := len(m.DMLOps) - 1; j >= 0; j-- {
				op := m.DMLOps[j]
				if op.Down == nil || len(op.Down.Ops) == 0 {
					continue
				}
				for _, downOp := range op.Down.Ops {
					stmt := OpToSQL(downOp)
					stmts, err := sqlparse.SplitStatements(stmt)
					if err != nil {
						return fmt.Errorf("parse DML rollback op %d: %w", j, err)
					}
					for _, s := range stmts {
						if _, err := tx.Exec(ctx, s); err != nil {
							return fmt.Errorf("DML rollback op %d: %w\n  SQL: %s", j, err, s)
						}
					}
				}
			}

			// Execute DDL down ops in reverse order.
			for j := len(m.DDLOps) - 1; j >= 0; j-- {
				op := m.DDLOps[j]
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
							return fmt.Errorf("commit before non-transactional rollback: %w", err)
						}
						stmts, err := sqlparse.SplitStatements(stmt)
						if err != nil {
							return fmt.Errorf("parse non-transactional rollback: %w", err)
						}
						for _, s := range stmts {
							if _, err := conn.Exec(ctx, s); err != nil {
								return fmt.Errorf("non-transactional rollback (%s): %w", downOp.Op, err)
							}
						}
						var txErr error
						tx, txErr = conn.Begin(ctx)
						if txErr != nil {
							return fmt.Errorf("begin after non-transactional rollback: %w", txErr)
						}
						continue
					}

					stmts, err := sqlparse.SplitStatements(stmt)
					if err != nil {
						return fmt.Errorf("parse DDL rollback: %w", err)
					}
					for _, s := range stmts {
						if _, err := tx.Exec(ctx, s); err != nil {
							return fmt.Errorf("DDL rollback (%s): %w\n  SQL: %s", downOp.Op, err, s)
						}
					}
				}
			}

			// Remove from tracking table.
			if _, err := tx.Exec(ctx,
				"DELETE FROM pgdesign_migrations WHERE version = $1", version); err != nil {
				return fmt.Errorf("remove migration record: %w", err)
			}

			return tx.Commit(ctx)
		}()

		if rollbackErr != nil {
			tx.Rollback(ctx)
			return rolledBack, fmt.Errorf("rollback %s: %w", version, rollbackErr)
		}

		rolledBack = append(rolledBack, version)
	}

	return rolledBack, nil
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
