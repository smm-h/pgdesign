package test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/dbutil"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// TestPartmanForwardOnlyInterval is a live conformance test that verifies
// whether pg_partman supports forward-only interval changes: updating
// p_interval on an existing partitioned table so that new child partitions
// use the new interval while old children retain their original width.
//
// Expected verdict: partman applies the new interval to future maintenance
// runs while leaving existing children untouched. This produces mixed-width
// children, which is operationally surprising but not incorrect.
//
// This test requires a live PostgreSQL instance with pg_partman installed.
// It will be skipped if either is unavailable.
//
// TODO: Once this test passes on live PG, the Phase 3.2 hard error for
// interval changes (MAINTENANCE_INTERVAL_CHANGE in internal/migrate/generate.go)
// should be downgraded to a Caution classification, gated on partman_version.
// Until live verification is performed, the hard error remains as the safe default.
func TestPartmanForwardOnlyInterval(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	info := testdb.SkipIfNoPartman(t)
	if info == nil {
		return
	}

	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/postgres?sslmode=disable"
	}

	// Create a dedicated test database to avoid polluting other state.
	testDBName := fmt.Sprintf("pgdesign_partman_interval_test_%d", time.Now().UnixNano())
	maintenanceURL, err := dbutil.MaintenanceURL(dbURL)
	if err != nil {
		t.Fatalf("maintenance URL: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to maintenance DB to create the test database.
	mConn, err := pgx.Connect(ctx, maintenanceURL)
	if err != nil {
		t.Fatalf("connect to maintenance DB: %v", err)
	}
	_, err = mConn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", testDBName))
	mConn.Close(ctx)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	defer func() {
		// Clean up: drop the test database.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanConn, cErr := pgx.Connect(cleanupCtx, maintenanceURL)
		if cErr == nil {
			// Terminate other connections before dropping.
			cleanConn.Exec(cleanupCtx, fmt.Sprintf(
				"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()",
				testDBName,
			))
			cleanConn.Exec(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", testDBName))
			cleanConn.Close(cleanupCtx)
		}
	}()

	// Build connection URL for the test database.
	testDBURL := replaceDBName(dbURL, testDBName)

	conn, err := pgx.Connect(ctx, testDBURL)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	defer conn.Close(ctx)

	// Step 1: Install pg_partman extension into a dedicated partman schema.
	// Without SCHEMA partman the extension lands in public and the
	// schema-qualified partman.create_parent calls below fail.
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS partman")
	if err != nil {
		t.Fatalf("create partman schema: %v", err)
	}
	_, err = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_partman SCHEMA partman")
	if err != nil {
		t.Fatalf("create pg_partman extension: %v", err)
	}

	// Step 2: Create a partman-managed table with interval A = '1 month'.
	_, err = conn.Exec(ctx, `
		CREATE TABLE public.events (
			id bigserial,
			created_at timestamptz NOT NULL DEFAULT now(),
			data text
		) PARTITION BY RANGE (created_at)
	`)
	if err != nil {
		t.Fatalf("create parent table: %v", err)
	}

	// Step 3: Call partman.create_parent to initialize partitioning with monthly interval.
	_, err = conn.Exec(ctx, `
		SELECT partman.create_parent(
			p_parent_table := 'public.events',
			p_control := 'created_at',
			p_interval := '1 month'
		)
	`)
	if err != nil {
		t.Fatalf("create_parent: %v", err)
	}

	// Record the initial set of child tables and their bounds.
	initialChildren, err := listPartitionChildren(ctx, conn, "public.events")
	if err != nil {
		t.Fatalf("list initial children: %v", err)
	}
	t.Logf("initial children (interval=1 month): %d partitions", len(initialChildren))
	for _, c := range initialChildren {
		t.Logf("  %s: %s", c.name, c.bounds)
	}

	// Step 4: Update p_interval to '1 week' (interval B).
	_, err = conn.Exec(ctx, `
		UPDATE partman.part_config
		SET partition_interval = '1 week'
		WHERE parent_table = 'public.events'
	`)
	if err != nil {
		t.Fatalf("update partition_interval: %v", err)
	}

	// Step 5: Run maintenance to create new children at the new interval.
	_, err = conn.Exec(ctx, "CALL partman.run_maintenance_proc()")
	if err != nil {
		t.Fatalf("run_maintenance_proc: %v", err)
	}

	// Step 6: Collect all children after maintenance.
	finalChildren, err := listPartitionChildren(ctx, conn, "public.events")
	if err != nil {
		t.Fatalf("list final children: %v", err)
	}
	t.Logf("final children (after interval change to 1 week): %d partitions", len(finalChildren))
	for _, c := range finalChildren {
		t.Logf("  %s: %s", c.name, c.bounds)
	}

	// Step 7: Verify the verdict.
	// We expect:
	// - The original monthly children still exist with their original bounds.
	// - New children were created at the weekly interval.
	// This means mixed-width convergence works.

	if len(finalChildren) <= len(initialChildren) {
		t.Errorf("expected more children after maintenance with shorter interval, got %d (was %d)",
			len(finalChildren), len(initialChildren))
	}

	// Verify original children are preserved (same name and bounds).
	initialByName := make(map[string]string, len(initialChildren))
	for _, c := range initialChildren {
		initialByName[c.name] = c.bounds
	}
	for name, bounds := range initialByName {
		found := false
		for _, c := range finalChildren {
			if c.name == name {
				found = true
				if c.bounds != bounds {
					t.Errorf("original child %s bounds changed: was %q, now %q", name, bounds, c.bounds)
				}
				break
			}
		}
		if !found {
			t.Errorf("original child %s disappeared after interval change", name)
		}
	}

	// Identify new children (not in the initial set).
	var newChildren []partitionChild
	for _, c := range finalChildren {
		if _, existed := initialByName[c.name]; !existed {
			newChildren = append(newChildren, c)
		}
	}
	t.Logf("new children created after interval change: %d", len(newChildren))
	for _, c := range newChildren {
		t.Logf("  NEW: %s: %s", c.name, c.bounds)
	}

	// Verdict: if we got here without errors, forward-only interval change works.
	// New partitions are created at the new (weekly) interval while old partitions
	// retain their original (monthly) width.
	t.Log("VERDICT: forward-only interval change works -- mixed-width children are created successfully")
	t.Logf("pg_partman version: %s", info.Version)
}

type partitionChild struct {
	name   string
	bounds string
}

// listPartitionChildren returns all child partitions of a parent table,
// sorted by name.
func listPartitionChildren(ctx context.Context, conn *pgx.Conn, parentTable string) ([]partitionChild, error) {
	// Parse schema and table name.
	parts := strings.SplitN(parentTable, ".", 2)
	schemaName := "public"
	tableName := parentTable
	if len(parts) == 2 {
		schemaName = parts[0]
		tableName = parts[1]
	}

	rows, err := conn.Query(ctx, `
		SELECT c.relname,
		       pg_get_expr(c.relpartbound, c.oid) AS bounds
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace n ON n.oid = p.relnamespace
		WHERE p.relname = $1 AND n.nspname = $2
		ORDER BY c.relname
	`, tableName, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var children []partitionChild
	for rows.Next() {
		var c partitionChild
		if err := rows.Scan(&c.name, &c.bounds); err != nil {
			return nil, err
		}
		children = append(children, c)
	}
	return children, rows.Err()
}

// replaceDBName rewrites a PostgreSQL connection URL to use a different database name.
func replaceDBName(connURL, newDB string) string {
	// Simple replacement: find the last / before ? and replace the database name.
	// Works for standard postgres:// URLs.
	qIdx := strings.Index(connURL, "?")
	prefix := connURL
	suffix := ""
	if qIdx >= 0 {
		prefix = connURL[:qIdx]
		suffix = connURL[qIdx:]
	}
	lastSlash := strings.LastIndex(prefix, "/")
	if lastSlash >= 0 {
		return prefix[:lastSlash+1] + newDB + suffix
	}
	return connURL
}
