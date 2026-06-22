package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// conformanceBaseURL returns the database URL for conformance tests.
func conformanceBaseURL() string {
	if u := os.Getenv("PGDESIGN_DB"); u != "" {
		return u
	}
	return "postgres://localhost:5432/postgres?sslmode=disable"
}

// conformanceFixturePath returns the absolute path to the conformance DDL fixture.
func conformanceFixturePath(t *testing.T) string {
	t.Helper()
	// The test runs in the package directory (internal/test/).
	path, err := filepath.Abs("testdata/testdb_conformance.sql.split.json")
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture not found at %s: %v", path, err)
	}
	return path
}

// conformanceFixtureDDL reads the fixture and returns the DDL statements joined
// as a single string suitable for io.Reader consumption.
func conformanceFixtureDDL(t *testing.T) string {
	t.Helper()
	path := conformanceFixturePath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var parsed struct {
		Statements []string `json:"statements"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return strings.Join(parsed.Statements, "\n")
}

// conformanceManager creates a testdb.Manager from the base URL.
func conformanceManager(t *testing.T) *testdb.Manager {
	t.Helper()
	m, err := testdb.NewManager(conformanceBaseURL())
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	return m
}

// TestConformanceGo verifies the Go engine creates an ephemeral database with
// the conformance fixture DDL, proves the table exists, and cleans up on drop.
func TestConformanceGo(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	m := conformanceManager(t)

	ddl := strings.NewReader(conformanceFixtureDDL(t))
	db := m.SetupForTest(t, testdb.CreateOptions{DDL: ddl})

	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral db: %v", err)
	}

	// Verify the conformance_test table exists.
	var exists bool
	err = conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'conformance_test' AND schemaname = 'public')",
	).Scan(&exists)
	if err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	if !exists {
		t.Fatal("conformance_test table not found in ephemeral database")
	}

	// Verify the table has the expected columns.
	var colCount int
	err = conn.QueryRow(ctx,
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'conformance_test' AND table_schema = 'public'",
	).Scan(&colCount)
	if err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if colCount != 2 {
		t.Fatalf("expected 2 columns (id, name), got %d", colCount)
	}

	// Record the database name for post-cleanup verification.
	dbName := db.Name

	// SetupForTest registers cleanup, so after the test the DB should be dropped.
	// We verify this in TestConformanceGoCleanup below using a subtest pattern.
	_ = dbName
}

// TestConformanceIsolation proves that two ephemeral databases created from the
// same fixture do not share state.
func TestConformanceIsolation(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	m := conformanceManager(t)

	ddl1 := strings.NewReader(conformanceFixtureDDL(t))
	dbA := m.SetupForTest(t, testdb.CreateOptions{DDL: ddl1})

	ddl2 := strings.NewReader(conformanceFixtureDDL(t))
	dbB := m.SetupForTest(t, testdb.CreateOptions{DDL: ddl2})

	// Verify different database names.
	if dbA.Name == dbB.Name {
		t.Fatal("two ephemeral databases have the same name")
	}

	connA, err := dbA.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to dbA: %v", err)
	}
	connB, err := dbB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to dbB: %v", err)
	}

	// Insert a row in database A.
	_, err = connA.Exec(ctx, "INSERT INTO conformance_test (id, name) VALUES (1, 'test')")
	if err != nil {
		t.Fatalf("insert into dbA: %v", err)
	}

	// Database B should have zero rows.
	var count int
	err = connB.QueryRow(ctx, "SELECT count(*) FROM conformance_test").Scan(&count)
	if err != nil {
		t.Fatalf("count in dbB: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows in dbB, got %d (databases are not isolated)", count)
	}

	// Verify database A actually has the row.
	err = connA.QueryRow(ctx, "SELECT count(*) FROM conformance_test").Scan(&count)
	if err != nil {
		t.Fatalf("count in dbA: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row in dbA, got %d", count)
	}
}

// TestConformanceGoCleanup verifies that SetupForTest's cleanup actually drops
// the ephemeral database after the subtest exits.
func TestConformanceGoCleanup(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	m := conformanceManager(t)

	var dbName string
	t.Run("create_and_use", func(t *testing.T) {
		ddl := strings.NewReader(conformanceFixtureDDL(t))
		db := m.SetupForTest(t, testdb.CreateOptions{DDL: ddl})
		dbName = db.Name

		conn, err := db.Connect(ctx)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		var exists bool
		err = conn.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'conformance_test')",
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if !exists {
			t.Fatal("table not found")
		}
	})

	// After the subtest, the database should be gone.
	maintURL := conformanceBaseURL()
	// Connect to maintenance database to check.
	maintConn, err := pgx.Connect(ctx, swapToMaintenance(maintURL))
	if err != nil {
		t.Fatalf("connect maintenance: %v", err)
	}
	defer maintConn.Close(ctx)

	var exists bool
	err = maintConn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", dbName,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check cleanup: %v", err)
	}
	if exists {
		t.Fatalf("ephemeral database %s still exists after subtest cleanup", dbName)
	}
}

// swapToMaintenance rewrites a connection URL to target the postgres maintenance database.
func swapToMaintenance(connURL string) string {
	// Parse and swap the database name to "postgres".
	// Simple approach: use the same logic as testdb internally.
	// We reimplement here to avoid depending on unexported helpers.
	parts := strings.SplitN(connURL, "?", 2)
	base := parts[0]
	query := ""
	if len(parts) == 2 {
		query = "?" + parts[1]
	}

	// Find the last / that starts the database name.
	idx := strings.LastIndex(base, "/")
	if idx < 0 {
		return connURL
	}
	return base[:idx+1] + "postgres" + query
}

// TestConformancePython renders the Python wrapper template, writes a pytest
// test file, and runs it against a real Postgres instance.
func TestConformancePython(t *testing.T) {
	testdb.SkipIfNoPostgres(t)

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH")
	}

	// Check that psycopg is importable.
	cmd := exec.Command("python3", "-c", "import psycopg")
	if err := cmd.Run(); err != nil {
		t.Skip("psycopg not installed (python3 -c 'import psycopg' failed)")
	}

	// Check that pytest is importable.
	cmd = exec.Command("python3", "-c", "import pytest")
	if err := cmd.Run(); err != nil {
		t.Skip("pytest not installed (python3 -c 'import pytest' failed)")
	}

	fixturePath := conformanceFixturePath(t)
	baseURL := conformanceBaseURL()

	// Extract base name from URL.
	baseName := extractDBName(baseURL)

	rendered, err := testdb.RenderTemplate("python", fixturePath, baseURL, baseName)
	if err != nil {
		t.Fatalf("render Python template: %v", err)
	}

	tmpDir := t.TempDir()

	// Write the rendered wrapper.
	wrapperPath := filepath.Join(tmpDir, "pgdesign_testdb.py")
	if err := os.WriteFile(wrapperPath, rendered, 0o644); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	// Write the test file.
	testFile := filepath.Join(tmpDir, "test_conformance.py")
	testContent := `from pgdesign_testdb import pgdesign_db


def test_table_exists(pgdesign_db):
    cur = pgdesign_db.cursor()
    cur.execute(
        "SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'conformance_test' AND schemaname = 'public')"
    )
    assert cur.fetchone()[0] is True


def test_column_count(pgdesign_db):
    cur = pgdesign_db.cursor()
    cur.execute(
        "SELECT count(*) FROM information_schema.columns "
        "WHERE table_name = 'conformance_test' AND table_schema = 'public'"
    )
    assert cur.fetchone()[0] == 2
`
	if err := os.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Run pytest.
	cmd = exec.Command("python3", "-m", "pytest", "test_conformance.py", "-v")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "PGDESIGN_DB="+baseURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pytest failed:\n%s\nerror: %v", output, err)
	}
	t.Logf("pytest output:\n%s", output)
}

// TestConformanceTypeScript renders the TypeScript wrapper template, writes a
// test script, and runs it against a real Postgres instance.
func TestConformanceTypeScript(t *testing.T) {
	testdb.SkipIfNoPostgres(t)

	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not found in PATH")
	}

	fixturePath := conformanceFixturePath(t)
	baseURL := conformanceBaseURL()
	baseName := extractDBName(baseURL)

	rendered, err := testdb.RenderTemplate("ts", fixturePath, baseURL, baseName)
	if err != nil {
		t.Fatalf("render TypeScript template: %v", err)
	}

	tmpDir := t.TempDir()

	// Write the rendered wrapper.
	wrapperPath := filepath.Join(tmpDir, "pgdesign-testdb.ts")
	if err := os.WriteFile(wrapperPath, rendered, 0o644); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	// Write package.json.
	packageJSON := `{
  "name": "pgdesign-conformance-test",
  "private": true,
  "type": "module",
  "dependencies": {
    "pg": "^8.11.0",
    "@types/pg": "^8.11.0",
    "tsx": "^4.0.0",
    "typescript": "^5.0.0"
  }
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	// Write tsconfig.json.
	tsConfig := `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ES2020",
    "moduleResolution": "bundler",
    "esModuleInterop": true,
    "strict": true,
    "skipLibCheck": true
  }
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "tsconfig.json"), []byte(tsConfig), 0o644); err != nil {
		t.Fatalf("write tsconfig.json: %v", err)
	}

	// Write the test script.
	testScript := fmt.Sprintf(`import { setupTestDB } from "./pgdesign-testdb";

async function main() {
    const db = await setupTestDB();
    try {
        // Verify table exists.
        const res = await db.client.query(
            "SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'conformance_test' AND schemaname = 'public')"
        );
        if (!res.rows[0].exists) {
            throw new Error("conformance_test table not found");
        }

        // Verify column count.
        const colRes = await db.client.query(
            "SELECT count(*) FROM information_schema.columns WHERE table_name = 'conformance_test' AND table_schema = 'public'"
        );
        const colCount = parseInt(colRes.rows[0].count, 10);
        if (colCount !== 2) {
            throw new Error("expected 2 columns, got " + colCount);
        }

        console.log("PASS");
    } finally {
        await db.teardown();
    }
}

main().catch(e => { console.error(e); process.exit(1); });
`)
	testPath := filepath.Join(tmpDir, "test.ts")
	if err := os.WriteFile(testPath, []byte(testScript), 0o644); err != nil {
		t.Fatalf("write test script: %v", err)
	}

	// npm install.
	cmd := exec.Command("npm", "install", "--no-audit", "--no-fund")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "PGDESIGN_DB="+baseURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed:\n%s\nerror: %v", output, err)
	}

	// Run the test via npx tsx.
	cmd = exec.Command("npx", "tsx", "test.ts")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "PGDESIGN_DB="+baseURL)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TypeScript test failed:\n%s\nerror: %v", output, err)
	}
	if !strings.Contains(string(output), "PASS") {
		t.Fatalf("TypeScript test did not print PASS:\n%s", output)
	}
	t.Logf("TypeScript test output:\n%s", output)
}

// extractDBName extracts the database name from a PostgreSQL connection URL.
func extractDBName(connURL string) string {
	// Strip query parameters.
	base := connURL
	if idx := strings.Index(base, "?"); idx >= 0 {
		base = base[:idx]
	}
	// Find the last slash.
	idx := strings.LastIndex(base, "/")
	if idx < 0 {
		return "postgres"
	}
	name := base[idx+1:]
	if name == "" {
		return "postgres"
	}
	return name
}
