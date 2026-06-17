package workload

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// TableScanStats holds scan statistics for a single table from pg_stat_user_tables.
type TableScanStats struct {
	Schema  string
	Table   string
	SeqScan int64
	IdxScan int64
}

// QueryTableScanStats queries pg_stat_user_tables for scan statistics.
func QueryTableScanStats(ctx context.Context, conn *pgx.Conn, schemaNames []string) ([]TableScanStats, error) {
	rows, err := conn.Query(ctx, `
		SELECT schemaname, relname, seq_scan, idx_scan
		FROM pg_stat_user_tables
		WHERE schemaname = ANY($1)
		ORDER BY schemaname, relname
	`, schemaNames)
	if err != nil {
		return nil, fmt.Errorf("querying pg_stat_user_tables: %w", err)
	}
	defer rows.Close()

	var result []TableScanStats
	for rows.Next() {
		var s TableScanStats
		if err := rows.Scan(&s.Schema, &s.Table, &s.SeqScan, &s.IdxScan); err != nil {
			return nil, fmt.Errorf("scanning pg_stat_user_tables row: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pg_stat_user_tables rows: %w", err)
	}
	return result, nil
}

// DetectSeqScanHeavy flags tables where seq_scan > 10 * idx_scan (sequential-scan-heavy).
func DetectSeqScanHeavy(scanStats []TableScanStats) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, stat := range scanStats {
		if stat.SeqScan > 0 && stat.SeqScan > 10*stat.IdxScan {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W026",
				Table:      stat.Table,
				Message:    fmt.Sprintf("Sequential scan heavy: %d seq scans vs %d index scans", stat.SeqScan, stat.IdxScan),
				Suggestion: "Review query patterns and consider adding indexes",
			})
		}
	}
	return diags
}

// DetectLowSelectivityIndexes flags boolean columns that have dedicated indexes.
// Boolean columns have only two values, making btree indexes ineffective.
func DetectLowSelectivityIndexes(schema *model.Schema) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, table := range schema.Tables {
		for _, col := range table.Columns {
			if col.PGType != "boolean" {
				continue
			}
			for _, idx := range table.Indexes {
				if len(idx.Columns) == 1 && idx.Columns[0] == col.Name {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Info,
						Code:       "I006",
						Table:      table.Name,
						Column:     col.Name,
						Message:    fmt.Sprintf("Boolean column %q has a dedicated index (low selectivity)", col.Name),
						Suggestion: "Boolean indexes are rarely useful; consider a partial index or removing the index",
					})
					break
				}
			}
		}
	}
	return diags
}

// DetectExcessiveIndexes flags tables with 10 or more indexes (write overhead).
func DetectExcessiveIndexes(schema *model.Schema) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, table := range schema.Tables {
		if len(table.Indexes) >= 10 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Info,
				Code:       "I007",
				Table:      table.Name,
				Message:    fmt.Sprintf("Table has %d indexes (may impact write performance)", len(table.Indexes)),
				Suggestion: "Review index usage and consider removing unused or redundant indexes",
			})
		}
	}
	return diags
}
