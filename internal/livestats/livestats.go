// Package livestats fetches live table statistics from a running PostgreSQL
// server and shapes them into the generate.D2Options.Stats contract (a map
// keyed by model.TableKey). It is the single DB-touching adapter that keeps the
// generate package DB-free: serve and the CLI build path call Fetch and inject
// the result, so heat-map / live annotations render without generate ever
// importing pgx.
package livestats

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
)

// Querier is the minimal query surface Fetch needs. Both *pgxpool.Pool and
// *pgx.Conn satisfy it, so serve (pool) and the CLI (conn) share one fetcher.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Fetch reads pg_stat_user_tables for the given schemas and returns live
// statistics keyed by model.TableKey(schema, name), ready to assign to
// generate.D2Options.Stats. RowCount comes from n_live_tup; SeqScanRatio is
// seq_scan / (seq_scan + idx_scan), or -1 when a table has never been scanned.
// Tables absent from pg_stat_user_tables (never accessed) are simply omitted;
// generate renders no annotation for a key it does not find.
func Fetch(ctx context.Context, q Querier, schemaNames []string) (map[string]generate.TableStats, error) {
	rows, err := q.Query(ctx, `
		SELECT schemaname, relname, n_live_tup, seq_scan, idx_scan
		FROM pg_stat_user_tables
		WHERE schemaname = ANY($1)
	`, schemaNames)
	if err != nil {
		return nil, fmt.Errorf("querying pg_stat_user_tables: %w", err)
	}
	defer rows.Close()

	out := make(map[string]generate.TableStats)
	for rows.Next() {
		var (
			schemaName, relName string
			nLiveTup            int64
			seqScan, idxScan    int64
		)
		if err := rows.Scan(&schemaName, &relName, &nLiveTup, &seqScan, &idxScan); err != nil {
			return nil, fmt.Errorf("scanning pg_stat_user_tables row: %w", err)
		}
		ratio := -1.0
		if total := seqScan + idxScan; total > 0 {
			ratio = float64(seqScan) / float64(total)
		}
		out[model.TableKey(schemaName, relName)] = generate.TableStats{
			RowCount:     nLiveTup,
			SeqScanRatio: ratio,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pg_stat_user_tables rows: %w", err)
	}
	return out, nil
}
