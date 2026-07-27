package migrate

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

const migrationsTableDDL = `CREATE TABLE IF NOT EXISTS pgdesign_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now(),
    checksum text NOT NULL,
    description text
);`

// EnsureMigrationsTable creates the pgdesign_migrations table if it doesn't exist.
func EnsureMigrationsTable(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, migrationsTableDDL)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	return nil
}

// AppliedVersions returns all applied migration versions, sorted by semver.
func AppliedVersions(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM pgdesign_migrations")
	if err != nil {
		return nil, fmt.Errorf("query applied versions: %w", err)
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate versions: %w", err)
	}

	sort.Slice(versions, func(i, j int) bool {
		return compareSemver(versions[i], versions[j]) < 0
	})
	return versions, nil
}

// RecordMigration inserts a migration record into the tracking table.
func RecordMigration(ctx context.Context, conn *pgx.Conn, version, checksum, description string) error {
	_, err := conn.Exec(ctx,
		"INSERT INTO pgdesign_migrations (version, checksum, description) VALUES ($1, $2, $3)",
		version, checksum, description)
	if err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	return nil
}

// AcquireAdvisoryLock acquires a session-level advisory lock for migrations.
// Returns true if the lock was acquired, false if another migration is in progress.
func AcquireAdvisoryLock(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var acquired bool
	err := conn.QueryRow(ctx,
		"SELECT pg_try_advisory_lock(hashtext('pgdesign_migrate'))").Scan(&acquired)
	if err != nil {
		return false, fmt.Errorf("acquire advisory lock: %w", err)
	}
	return acquired, nil
}

// ReleaseAdvisoryLock releases the session-level advisory lock for migrations.
func ReleaseAdvisoryLock(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx,
		"SELECT pg_advisory_unlock(hashtext('pgdesign_migrate'))")
	if err != nil {
		return fmt.Errorf("release advisory lock: %w", err)
	}
	return nil
}
