package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TableStats maps table names to estimated row counts from pg_stat_user_tables.
type TableStats map[string]int64

// QueryTableStats queries pg_stat_user_tables for row estimates in the given
// schema. Returns a map of table name to n_live_tup. Called once per migration
// generation, not per op.
func QueryTableStats(ctx context.Context, conn *pgx.Conn, schemaName string) (TableStats, error) {
	rows, err := conn.Query(ctx, `
		SELECT relname, n_live_tup
		FROM pg_stat_user_tables
		WHERE schemaname = $1
	`, schemaName)
	if err != nil {
		return nil, fmt.Errorf("query table stats: %w", err)
	}
	defer rows.Close()

	stats := make(TableStats)
	for rows.Next() {
		var name string
		var nLiveTup int64
		if err := rows.Scan(&name, &nLiveTup); err != nil {
			return nil, fmt.Errorf("scan table stats: %w", err)
		}
		stats[name] = nLiveTup
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate table stats: %w", err)
	}
	return stats, nil
}

// lookupRows returns the estimated row count for a schema-qualified table name,
// or 0 if stats are nil or the table is not found.
func lookupRows(stats TableStats, qualifiedName string) int64 {
	if stats == nil {
		return 0
	}
	// Try the qualified name first, then the unqualified name.
	if rows, ok := stats[qualifiedName]; ok {
		return rows
	}
	_, name := splitQualifiedName(qualifiedName)
	if rows, ok := stats[name]; ok {
		return rows
	}
	return 0
}
