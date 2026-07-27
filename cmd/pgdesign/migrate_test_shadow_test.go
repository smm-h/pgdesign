package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// cmdEphemeralDB returns a fresh ephemeral database, skipping cleanly when no
// PostgreSQL server is reachable.
func cmdEphemeralDB(t *testing.T) *testdb.EphemeralDB {
	t.Helper()
	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/pgdesign?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	probe, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Skipf("no database available: %v", err)
	}
	probe.Close(ctx)
	mgr, err := testdb.NewManager(dbURL)
	if err != nil {
		t.Skipf("no database manager: %v", err)
	}
	return mgr.SetupForTest(t, testdb.CreateOptions{})
}

const shadowGuardSchema = `format_version = 1
[tables.items]
comment = "items"

[tables.items.columns.id]
type = "id"

[tables.items.columns.name]
type = "short_text"
`

// TestMigrateTestShadowGuardsPreUpgrade: `migrate test --shadow` against a
// PRE-UPGRADE database (legacy pgdesign_migrations, no chain_position) must be
// refused by the pre-upgrade guard BEFORE any shadow work — it previously
// returned before the guard and proceeded to create a shadow database.
func TestMigrateTestShadowGuardsPreUpgrade(t *testing.T) {
	ephDB := cmdEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// Make the database look pre-upgrade: legacy tracking table, no chain position.
	if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.toml")
	if err := os.WriteFile(schemaPath, []byte(shadowGuardSchema), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stderr so we can assert the guard (not some later drift) fired.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	migrationsDir := filepath.Join(dir, "migrations")
	dirFlag := migrationsDir
	code := runMigrateTestShadow(ephDB.URL, &dirFlag, 30, []string{schemaPath}, nil, true)

	w.Close()
	os.Stderr = origStderr
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	stderr := string(buf[:n])

	if code == 0 {
		t.Fatalf("shadow test against a pre-upgrade DB must fail, got exit 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "migrate upgrade") {
		t.Errorf("expected the pre-upgrade guard (naming `migrate upgrade`) to fire, got stderr=%q", stderr)
	}

	// The guard fires before any shadow database is created: none should leak.
	var leaked int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname LIKE 'pgdesign_shadow_%'").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Errorf("guard should fire before CREATE DATABASE; found %d leaked shadow database(s)", leaked)
	}
}
