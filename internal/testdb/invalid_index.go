package testdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CreateInvalidIndex deterministically leaves an INVALID index
// (pg_index.indisvalid = false) named indexName on table(column), modeling the
// catalog state after an interrupted CREATE INDEX CONCURRENTLY — with NO backend
// kill, no SIGKILL, no faulttest machinery.
//
// The technique is unique-CIC-over-duplicate-data: table(column) must already
// contain duplicate values, and CREATE UNIQUE INDEX CONCURRENTLY builds the index
// in two phases; the second (validation) phase detects the duplicate and fails,
// but Postgres leaves the half-built index in the catalog marked invalid. This is
// exactly the recoverable state the create-index resume protocol must handle
// (pg_index.indisvalid check + DROP-rebuild, roadmap L8).
//
// conn must be in autocommit mode (CONCURRENTLY cannot run inside a transaction).
// The expected duplicate-key failure is swallowed; any OTHER error (including the
// build unexpectedly succeeding, or the index ending up valid) is returned so a
// mis-set-up fixture fails loudly rather than silently.
func CreateInvalidIndex(ctx context.Context, conn *pgx.Conn, indexName, table, column string) error {
	stmt := fmt.Sprintf(
		"CREATE UNIQUE INDEX CONCURRENTLY %s ON %s (%s)",
		pgx.Identifier{indexName}.Sanitize(),
		table, // caller-supplied, already qualified/quoted as needed
		pgx.Identifier{column}.Sanitize(),
	)
	_, err := conn.Exec(ctx, stmt)
	if err == nil {
		return fmt.Errorf("testdb: CreateInvalidIndex: unique CIC over %s(%s) succeeded — the column must contain duplicate values for the build to fail and leave an invalid index", table, column)
	}
	// The duplicate-key failure is expected. Confirm the invalid index is present.
	var valid bool
	qErr := conn.QueryRow(ctx,
		"SELECT i.indisvalid FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid WHERE c.relname = $1",
		indexName).Scan(&valid)
	if qErr == pgx.ErrNoRows {
		return fmt.Errorf("testdb: CreateInvalidIndex: CIC failed (%v) but left no index %q — expected an invalid leftover", err, indexName)
	}
	if qErr != nil {
		return fmt.Errorf("testdb: CreateInvalidIndex: probe indisvalid for %q: %w", indexName, qErr)
	}
	if valid {
		return fmt.Errorf("testdb: CreateInvalidIndex: index %q is valid — expected invalid (CIC error was: %v)", indexName, err)
	}
	return nil
}
