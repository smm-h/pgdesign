package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// IndexInfo contains the minimal index information needed for duplicate detection.
type IndexInfo struct {
	Schema  string
	Table   string
	Name    string
	Columns []string
}

// DuplicateIndex describes a pair of indexes where one is a prefix of the other.
type DuplicateIndex struct {
	Schema        string `json:"schema"`
	Table         string `json:"table"`
	Index         string `json:"index"`
	SupersetIndex string `json:"superset_index"`
}

// isPrefix returns true if a is a leading prefix of b.
func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FindDuplicateIndexes detects indexes where one is a leading-column prefix of another.
// Only strict prefixes count (same columns is not a duplicate).
func FindDuplicateIndexes(indexes []IndexInfo) []DuplicateIndex {
	type key struct{ schema, table string }
	groups := make(map[key][]IndexInfo)
	for _, idx := range indexes {
		k := key{idx.Schema, idx.Table}
		groups[k] = append(groups[k], idx)
	}

	var result []DuplicateIndex
	for _, idxs := range groups {
		for i, a := range idxs {
			for j, b := range idxs {
				if i == j {
					continue
				}
				if isPrefix(a.Columns, b.Columns) && len(a.Columns) < len(b.Columns) {
					result = append(result, DuplicateIndex{
						Schema:        a.Schema,
						Table:         a.Table,
						Index:         a.Name,
						SupersetIndex: b.Name,
					})
				}
			}
		}
	}
	return result
}

// formatNumber formats an integer with comma separators.
func formatNumber(n int64) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

// formatRelativeTime formats a time as relative to now (e.g., "2h ago", "3d ago").
func formatRelativeTime(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

type statsOutput struct {
	Database         string             `json:"database"`
	CacheHitRatio    float64            `json:"cache_hit_ratio"`
	Tables           []tableStats       `json:"tables"`
	UnusedIndexes    []unusedIndex      `json:"unused_indexes"`
	VacuumCandidates []vacuumCandidate  `json:"vacuum_candidates"`
	DuplicateIndexes []DuplicateIndex   `json:"duplicate_indexes"`
}

type tableStats struct {
	Schema       string     `json:"schema"`
	Name         string     `json:"name"`
	LiveTuples   int64      `json:"live_tuples"`
	DeadTuples   int64      `json:"dead_tuples"`
	SeqScans     int64      `json:"seq_scans"`
	LastVacuum   *time.Time `json:"last_vacuum"`
	VacuumNeeded bool       `json:"vacuum_needed"`
}

type unusedIndex struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Index  string `json:"index"`
	Scans  int64  `json:"scans"`
}

type vacuumCandidate struct {
	Schema     string  `json:"schema"`
	Table      string  `json:"table"`
	LiveTuples int64   `json:"live_tuples"`
	DeadTuples int64   `json:"dead_tuples"`
	DeadRatio  float64 `json:"dead_ratio"`
}

func handleStats(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for stats")
		return 1
	}

	jsonOutput := kwargs["json"].(bool)

	var schemaNames []string
	if raw, ok := kwargs["schema"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				schemaNames = append(schemaNames, s)
			}
		}
	}
	if len(schemaNames) == 0 {
		schemaNames = []string{"public"}
	}

	// path is optional variadic -- nil when not provided
	var paths []string
	if raw, ok := kwargs["path"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				paths = append(paths, s)
			}
		}
	}
	_ = paths // reserved for future cross-reference with schema files

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	var dbName string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		fmt.Fprintf(os.Stderr, "error: query database name: %v\n", err)
		return 1
	}

	rows, err := conn.Query(ctx,
		`SELECT schemaname, relname, n_live_tup, n_dead_tup, seq_scan, last_vacuum, last_autovacuum
		 FROM pg_stat_user_tables
		 WHERE schemaname = ANY($1)
		 ORDER BY schemaname, relname`, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: query table stats: %v\n", err)
		return 1
	}

	type tableRow struct {
		schema         string
		name           string
		liveTuples     int64
		deadTuples     int64
		seqScans       int64
		lastVacuum     *time.Time
		lastAutovacuum *time.Time
	}
	var tables []tableRow
	for rows.Next() {
		var r tableRow
		if err := rows.Scan(&r.schema, &r.name, &r.liveTuples, &r.deadTuples, &r.seqScans, &r.lastVacuum, &r.lastAutovacuum); err != nil {
			rows.Close()
			fmt.Fprintf(os.Stderr, "error: scan table stats: %v\n", err)
			return 1
		}
		tables = append(tables, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: iterate table stats: %v\n", err)
		return 1
	}

	rows, err = conn.Query(ctx,
		`SELECT schemaname, relname, indexrelname, idx_scan
		 FROM pg_stat_user_indexes
		 WHERE schemaname = ANY($1)
		 ORDER BY schemaname, indexrelname`, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: query index stats: %v\n", err)
		return 1
	}
	type indexRow struct {
		schema    string
		table     string
		indexName string
		scans     int64
	}
	var indexes []indexRow
	for rows.Next() {
		var r indexRow
		if err := rows.Scan(&r.schema, &r.table, &r.indexName, &r.scans); err != nil {
			rows.Close()
			fmt.Fprintf(os.Stderr, "error: scan index stats: %v\n", err)
			return 1
		}
		indexes = append(indexes, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: iterate index stats: %v\n", err)
		return 1
	}

	var blksHit, blksRead int64
	if err := conn.QueryRow(ctx,
		"SELECT blks_hit, blks_read FROM pg_stat_database WHERE datname = current_database()").
		Scan(&blksHit, &blksRead); err != nil {
		fmt.Fprintf(os.Stderr, "error: query cache stats: %v\n", err)
		return 1
	}
	var cacheHitRatio float64
	if blksHit+blksRead > 0 {
		cacheHitRatio = float64(blksHit) / float64(blksHit+blksRead)
	}

	indexColRows, err := conn.Query(ctx,
		`SELECT n.nspname, t.relname, i.relname AS indexname,
		        array_agg(a.attname ORDER BY k.ord)
		 FROM pg_index x
		 JOIN pg_class i ON i.oid = x.indexrelid
		 JOIN pg_class t ON t.oid = x.indrelid
		 JOIN pg_namespace n ON n.oid = t.relnamespace
		 JOIN LATERAL unnest(x.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
		 JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		 WHERE n.nspname = ANY($1)
		 GROUP BY n.nspname, t.relname, i.relname
		 ORDER BY n.nspname, t.relname, i.relname`, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: query index columns: %v\n", err)
		return 1
	}
	var indexInfos []IndexInfo
	for indexColRows.Next() {
		var info IndexInfo
		var columns []string
		if err := indexColRows.Scan(&info.Schema, &info.Table, &info.Name, &columns); err != nil {
			indexColRows.Close()
			fmt.Fprintf(os.Stderr, "error: scan index columns: %v\n", err)
			return 1
		}
		info.Columns = columns
		indexInfos = append(indexInfos, info)
	}
	indexColRows.Close()
	if err := indexColRows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: iterate index columns: %v\n", err)
		return 1
	}

	// Analyze: unused indexes
	var unused []unusedIndex
	for _, idx := range indexes {
		if idx.scans == 0 {
			unused = append(unused, unusedIndex{
				Schema: idx.schema,
				Table:  idx.table,
				Index:  idx.indexName,
				Scans:  idx.scans,
			})
		}
	}

	// Analyze: vacuum candidates (dead ratio > 10%)
	var vacuumCands []vacuumCandidate
	for _, t := range tables {
		total := t.liveTuples + t.deadTuples
		if total > 0 {
			ratio := float64(t.deadTuples) / float64(total)
			if ratio > 0.1 {
				vacuumCands = append(vacuumCands, vacuumCandidate{
					Schema:     t.schema,
					Table:      t.name,
					LiveTuples: t.liveTuples,
					DeadTuples: t.deadTuples,
					DeadRatio:  math.Round(ratio*1000) / 1000,
				})
			}
		}
	}

	// Analyze: duplicate indexes
	duplicates := FindDuplicateIndexes(indexInfos)

	// Build table stats for output
	var tableOutput []tableStats
	for _, t := range tables {
		var lastVac *time.Time
		if t.lastVacuum != nil && t.lastAutovacuum != nil {
			if t.lastVacuum.After(*t.lastAutovacuum) {
				lastVac = t.lastVacuum
			} else {
				lastVac = t.lastAutovacuum
			}
		} else if t.lastVacuum != nil {
			lastVac = t.lastVacuum
		} else if t.lastAutovacuum != nil {
			lastVac = t.lastAutovacuum
		}

		total := t.liveTuples + t.deadTuples
		vacuumNeeded := false
		if total > 0 {
			vacuumNeeded = float64(t.deadTuples)/float64(total) > 0.1
		}

		tableOutput = append(tableOutput, tableStats{
			Schema:       t.schema,
			Name:         t.name,
			LiveTuples:   t.liveTuples,
			DeadTuples:   t.deadTuples,
			SeqScans:     t.seqScans,
			LastVacuum:   lastVac,
			VacuumNeeded: vacuumNeeded,
		})
	}

	// Ensure slices are never nil for JSON output
	if tableOutput == nil {
		tableOutput = []tableStats{}
	}
	if unused == nil {
		unused = []unusedIndex{}
	}
	if vacuumCands == nil {
		vacuumCands = []vacuumCandidate{}
	}
	if duplicates == nil {
		duplicates = []DuplicateIndex{}
	}

	// Output
	if jsonOutput {
		out := statsOutput{
			Database:         dbName,
			CacheHitRatio:    math.Round(cacheHitRatio*10000) / 10000,
			Tables:           tableOutput,
			UnusedIndexes:    unused,
			VacuumCandidates: vacuumCands,
			DuplicateIndexes: duplicates,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "error: encode JSON: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Printf("Database: %s\n", dbName)
	fmt.Printf("Cache hit ratio: %.1f%%\n", cacheHitRatio*100)
	fmt.Println()

	if len(tableOutput) > 0 {
		fmt.Println("Tables:")
		maxName := 0
		for _, t := range tableOutput {
			name := t.Name
			if t.Schema != "public" {
				name = t.Schema + "." + t.Name
			}
			if len(name) > maxName {
				maxName = len(name)
			}
		}
		for _, t := range tableOutput {
			name := t.Name
			if t.Schema != "public" {
				name = t.Schema + "." + t.Name
			}
			vacInfo := ""
			if t.VacuumNeeded {
				vacInfo = "VACUUM RECOMMENDED"
			} else if t.LastVacuum != nil {
				vacInfo = "last vacuum: " + formatRelativeTime(*t.LastVacuum)
			} else {
				vacInfo = "last vacuum: never"
			}
			fmt.Printf("  %-*s  %10s rows  %8s dead tuples    %s\n",
				maxName, name,
				formatNumber(t.LiveTuples),
				formatNumber(t.DeadTuples),
				vacInfo)
		}
		fmt.Println()
	}

	if len(unused) > 0 {
		fmt.Println("Unused Indexes:")
		for _, idx := range unused {
			fmt.Printf("  %s on %s (%s scans)\n", idx.Index, idx.Table, formatNumber(idx.Scans))
		}
		fmt.Println()
	}

	if len(duplicates) > 0 {
		fmt.Println("Duplicate Indexes:")
		for _, d := range duplicates {
			fmt.Printf("  %s is a prefix of %s\n", d.Index, d.SupersetIndex)
		}
		fmt.Println()
	}

	if len(vacuumCands) > 0 {
		fmt.Println("Vacuum Candidates:")
		for _, v := range vacuumCands {
			name := v.Table
			if v.Schema != "public" {
				name = v.Schema + "." + v.Table
			}
			fmt.Printf("  %s  %.1f%% dead tuples (%s dead / %s total)\n",
				name, v.DeadRatio*100,
				formatNumber(v.DeadTuples),
				formatNumber(v.LiveTuples+v.DeadTuples))
		}
		fmt.Println()
	}

	return 0
}
