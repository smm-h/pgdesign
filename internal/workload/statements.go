package workload

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// StatementStats holds a row from pg_stat_statements with parsed table references.
type StatementStats struct {
	QueryID       int64
	Query         string
	Calls         int64
	TotalExecTime float64  // milliseconds
	MeanExecTime  float64  // milliseconds
	Rows          int64
	Tables        []string // extracted table references
}

// QueryStatements queries pg_stat_statements for the top-N DML statements by
// total execution time and extracts table references from each normalized query.
// Only SELECT, INSERT, UPDATE, DELETE statements are returned; utility statements
// are excluded because their normalization is PG 16+ only.
func QueryStatements(ctx context.Context, conn *pgx.Conn, limit int) ([]StatementStats, error) {
	var exists bool
	err := conn.QueryRow(ctx, "SELECT true FROM pg_extension WHERE extname = 'pg_stat_statements'").Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("pg_stat_statements extension is not installed")
	}

	rows, err := conn.Query(ctx, `
		SELECT queryid, query, calls, total_exec_time, mean_exec_time, rows
		FROM pg_stat_statements
		WHERE query !~* '^\s*(CREATE|ALTER|DROP|GRANT|REVOKE|SET|RESET|BEGIN|COMMIT|ROLLBACK|VACUUM|ANALYZE|COPY|COMMENT|EXPLAIN)'
		ORDER BY total_exec_time DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying pg_stat_statements: %w", err)
	}
	defer rows.Close()

	var result []StatementStats
	for rows.Next() {
		var s StatementStats
		if err := rows.Scan(&s.QueryID, &s.Query, &s.Calls, &s.TotalExecTime, &s.MeanExecTime, &s.Rows); err != nil {
			return nil, fmt.Errorf("scanning pg_stat_statements row: %w", err)
		}
		tables, parseErr := sqlparse.ExtractTableRefs(s.Query)
		if parseErr == nil {
			s.Tables = tables
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pg_stat_statements rows: %w", err)
	}
	return result, nil
}
