package migrate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// Apply discovers pending migrations in migrationsDir, applies them in semver
// order, and returns the list of applied versions. lockTimeout sets the
// PostgreSQL lock_timeout for each migration (e.g. "5s"); empty defaults to "5s".
func Apply(ctx context.Context, conn *pgx.Conn, migrationsDir string, lockTimeout string) ([]string, error) {
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		return nil, err
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

	// Discover migration files.
	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("discover migrations: %w", err)
	}

	// Get already-applied versions.
	applied, err := AppliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	// Determine pending migrations.
	var pending []migrationFile
	for _, mf := range migrations {
		if !appliedSet[mf.version] {
			pending = append(pending, mf)
		}
	}

	if len(pending) == 0 {
		return nil, nil
	}

	var appliedVersions []string
	for _, mf := range pending {
		if err := applyOne(ctx, conn, mf, lockTimeout); err != nil {
			return appliedVersions, fmt.Errorf("migration %s: %w", mf.version, err)
		}
		appliedVersions = append(appliedVersions, mf.version)
	}

	return appliedVersions, nil
}

type migrationFile struct {
	version  string
	path     string
	checksum string
}

// discoverMigrations finds all *.toml files in the migrations directory,
// parses their version from the filename, and sorts by semver.
func discoverMigrations(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var files []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		version := strings.TrimSuffix(e.Name(), ".toml")
		if _, _, _, err := semverParts(version); err != nil {
			continue // Skip non-semver files.
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(data))

		files = append(files, migrationFile{
			version:  version,
			path:     path,
			checksum: checksum,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return compareSemver(files[i].version, files[j].version) < 0
	})

	return files, nil
}

// applyOne applies a single migration within a transaction, handling
// non-transactional ops by committing/re-opening transactions as needed.
func applyOne(ctx context.Context, conn *pgx.Conn, mf migrationFile, lockTimeout string) error {
	m, err := ParseMigrationFile(mf.path)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	// Set lock_timeout for this session (persists across transactions).
	if lockTimeout == "" {
		lockTimeout = "5s"
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%s'", lockTimeout)); err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}

	if HasPhases(m) {
		if err := applyPhased(ctx, conn, m, mf); err != nil {
			return err
		}
		return nil
	}

	// Original flat execution path (no phases).
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) // No-op if already committed.

	for i, op := range m.DDLOps {
		sqlStmt := OpToSQL(op)
		if sqlStmt == "" {
			continue
		}

		if IsNonTransactional(op) {
			// Commit current transaction, execute outside, start new one.
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit before non-transactional op %d (%s): %w", i, op.Op, err)
			}

			// Execute each statement in the multi-statement string separately.
			stmts, err := sqlparse.SplitStatements(sqlStmt)
			if err != nil {
				return fmt.Errorf("parse non-transactional op %d (%s): %w", i, op.Op, err)
			}
			for _, stmt := range stmts {
				if _, err := conn.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("non-transactional op %d (%s): %w", i, op.Op, err)
				}
			}

			tx, err = conn.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin after non-transactional op %d: %w", i, err)
			}
			defer tx.Rollback(ctx)
			continue
		}

		// Execute each statement separately within the transaction.
		stmts, err := sqlparse.SplitStatements(sqlStmt)
		if err != nil {
			return fmt.Errorf("parse DDL op %d (%s): %w", i, op.Op, err)
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("DDL op %d (%s): %w\n  SQL: %s", i, op.Op, err, stmt)
			}
		}
	}

	// Execute DML ops.
	for i, op := range m.DMLOps {
		if op.SQL == "" {
			continue
		}
		if _, err := tx.Exec(ctx, op.SQL); err != nil {
			return fmt.Errorf("DML op %d (%s): %w\n  SQL: %s", i, op.Op, err, op.SQL)
		}
	}

	// Record in migrations table.
	if _, err := tx.Exec(ctx,
		"INSERT INTO pgdesign_migrations (version, checksum, description) VALUES ($1, $2, $3)",
		mf.version, mf.checksum, m.Description); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// applyPhased executes a migration phase-by-phase: expand, migrate, contract.
// Each phase runs in its own transaction (with non-transactional ops handled
// the same way as the flat path). The migration is recorded in a final
// transaction after all phases complete.
func applyPhased(ctx context.Context, conn *pgx.Conn, m *Migration, mf migrationFile) error {
	phases := []string{PhaseExpand, PhaseMigrate, PhaseContract}
	for _, phase := range phases {
		if err := applyPhaseOps(ctx, conn, m, phase); err != nil {
			return fmt.Errorf("phase %s: %w", phase, err)
		}
	}
	// Record migration in its own transaction.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin (record): %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		"INSERT INTO pgdesign_migrations (version, checksum, description) VALUES ($1, $2, $3)",
		mf.version, mf.checksum, m.Description); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return tx.Commit(ctx)
}

// applyPhaseOps executes all DDL and DML ops belonging to a single phase
// within a transaction, handling non-transactional ops by committing and
// re-opening the transaction as needed.
func applyPhaseOps(ctx context.Context, conn *pgx.Conn, m *Migration, phase string) error {
	// Collect DDL ops for this phase.
	var ddlOps []DDLOp
	for _, op := range m.DDLOps {
		if op.Phase == phase {
			ddlOps = append(ddlOps, op)
		}
	}
	// Collect DML ops for this phase.
	var dmlOps []DMLOp
	for _, op := range m.DMLOps {
		if op.Phase == phase {
			dmlOps = append(dmlOps, op)
		}
	}

	if len(ddlOps) == 0 && len(dmlOps) == 0 {
		return nil
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for i, op := range ddlOps {
		sqlStmt := OpToSQL(op)
		if sqlStmt == "" {
			continue
		}

		if IsNonTransactional(op) {
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit before non-transactional op %d (%s): %w", i, op.Op, err)
			}
			stmts, err := sqlparse.SplitStatements(sqlStmt)
			if err != nil {
				return fmt.Errorf("parse non-transactional op %d (%s): %w", i, op.Op, err)
			}
			for _, stmt := range stmts {
				if _, err := conn.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("non-transactional op %d (%s): %w", i, op.Op, err)
				}
			}
			tx, err = conn.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin after non-transactional op %d: %w", i, err)
			}
			defer tx.Rollback(ctx)
			continue
		}

		stmts, err := sqlparse.SplitStatements(sqlStmt)
		if err != nil {
			return fmt.Errorf("parse DDL op %d (%s): %w", i, op.Op, err)
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("DDL op %d (%s): %w\n  SQL: %s", i, op.Op, err, stmt)
			}
		}
	}

	for i, op := range dmlOps {
		if op.SQL == "" {
			continue
		}
		if _, err := tx.Exec(ctx, op.SQL); err != nil {
			return fmt.Errorf("DML op %d (%s): %w\n  SQL: %s", i, op.Op, err, op.SQL)
		}
	}

	return tx.Commit(ctx)
}
