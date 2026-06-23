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
	"time"

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

// TestConformanceJava renders the Java wrapper template, writes a Gradle
// project with a JUnit 5 test, and runs it against a real Postgres instance.
func TestConformanceJava(t *testing.T) {
	testdb.SkipIfNoPostgres(t)

	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not found in PATH")
	}
	if _, err := exec.LookPath("gradle"); err != nil {
		t.Skip("gradle not found in PATH")
	}

	fixturePath := conformanceFixturePath(t)
	baseURL := conformanceBaseURL()
	baseName := extractDBName(baseURL)

	rendered, err := testdb.RenderTemplate("java", fixturePath, baseURL, baseName)
	if err != nil {
		t.Fatalf("render Java template: %v", err)
	}

	tmpDir := t.TempDir()

	// Create directory structure.
	srcDir := filepath.Join(tmpDir, "src", "test", "java", "pgdesign")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("create src dir: %v", err)
	}

	// Write the rendered wrapper (TestDB.java).
	if err := os.WriteFile(filepath.Join(srcDir, "TestDB.java"), rendered, 0o644); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	// Write the conformance test.
	testContent := `package pgdesign;

import java.sql.Connection;
import java.sql.ResultSet;
import java.sql.Statement;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.RegisterExtension;
import static org.junit.jupiter.api.Assertions.*;

class ConformanceTest {
    @RegisterExtension
    static final TestDB db = new TestDB();

    @Test
    void tableExists() throws Exception {
        Connection conn = db.getConnection();
        try (Statement stmt = conn.createStatement()) {
            ResultSet rs = stmt.executeQuery(
                "SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'conformance_test' AND schemaname = 'public')"
            );
            assertTrue(rs.next());
            assertTrue(rs.getBoolean(1), "conformance_test table not found");
        }
    }

    @Test
    void columnCount() throws Exception {
        Connection conn = db.getConnection();
        try (Statement stmt = conn.createStatement()) {
            ResultSet rs = stmt.executeQuery(
                "SELECT count(*) FROM information_schema.columns WHERE table_name = 'conformance_test' AND table_schema = 'public'"
            );
            assertTrue(rs.next());
            assertEquals(2, rs.getInt(1), "expected 2 columns (id, name)");
        }
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "ConformanceTest.java"), []byte(testContent), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Write build.gradle.kts.
	buildGradle := `plugins { java }
repositories { mavenCentral() }
dependencies {
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.1")
    testRuntimeOnly("org.postgresql:postgresql:42.7.2")
}
tasks.test { useJUnitPlatform() }
`
	if err := os.WriteFile(filepath.Join(tmpDir, "build.gradle.kts"), []byte(buildGradle), 0o644); err != nil {
		t.Fatalf("write build.gradle.kts: %v", err)
	}

	// Write settings.gradle.kts.
	settingsGradle := `rootProject.name = "pgdesign-conformance"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.gradle.kts"), []byte(settingsGradle), 0o644); err != nil {
		t.Fatalf("write settings.gradle.kts: %v", err)
	}

	// Run gradle test.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gradle", "test", "--no-daemon")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "PGDESIGN_DB="+baseURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gradle test failed:\n%s\nerror: %v", output, err)
	}
	t.Logf("gradle test output:\n%s", output)
}

// TestConformanceKotlin renders the Kotlin wrapper template, writes a Gradle
// project with a JUnit 5 test, and runs it against a real Postgres instance.
func TestConformanceKotlin(t *testing.T) {
	testdb.SkipIfNoPostgres(t)

	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not found in PATH")
	}
	if _, err := exec.LookPath("gradle"); err != nil {
		t.Skip("gradle not found in PATH")
	}

	fixturePath := conformanceFixturePath(t)
	baseURL := conformanceBaseURL()
	baseName := extractDBName(baseURL)

	rendered, err := testdb.RenderTemplate("kotlin", fixturePath, baseURL, baseName)
	if err != nil {
		t.Fatalf("render Kotlin template: %v", err)
	}

	tmpDir := t.TempDir()

	// Create directory structure.
	srcDir := filepath.Join(tmpDir, "src", "test", "kotlin", "pgdesign")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("create src dir: %v", err)
	}

	// Write the rendered wrapper (TestDB.kt).
	if err := os.WriteFile(filepath.Join(srcDir, "TestDB.kt"), rendered, 0o644); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	// Write the conformance test.
	testContent := `package pgdesign

import org.junit.jupiter.api.Test
import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.extension.RegisterExtension

class ConformanceTest {
    companion object {
        @JvmField
        @RegisterExtension
        val db = TestDB()
    }

    @Test
    fun tableExists() {
        val conn = db.connection!!
        conn.createStatement().use { stmt ->
            val rs = stmt.executeQuery(
                "SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'conformance_test' AND schemaname = 'public')"
            )
            assertTrue(rs.next())
            assertTrue(rs.getBoolean(1), "conformance_test table not found")
        }
    }

    @Test
    fun columnCount() {
        val conn = db.connection!!
        conn.createStatement().use { stmt ->
            val rs = stmt.executeQuery(
                "SELECT count(*) FROM information_schema.columns WHERE table_name = 'conformance_test' AND table_schema = 'public'"
            )
            assertTrue(rs.next())
            assertEquals(2, rs.getInt(1), "expected 2 columns (id, name)")
        }
    }
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "ConformanceTest.kt"), []byte(testContent), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Write build.gradle.kts (with Kotlin plugin -- Gradle downloads kotlinc).
	buildGradle := `plugins { kotlin("jvm") version "2.0.0" }
repositories { mavenCentral() }
dependencies {
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.1")
    testRuntimeOnly("org.postgresql:postgresql:42.7.2")
}
tasks.test { useJUnitPlatform() }
`
	if err := os.WriteFile(filepath.Join(tmpDir, "build.gradle.kts"), []byte(buildGradle), 0o644); err != nil {
		t.Fatalf("write build.gradle.kts: %v", err)
	}

	// Write settings.gradle.kts.
	settingsGradle := `rootProject.name = "pgdesign-conformance"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.gradle.kts"), []byte(settingsGradle), 0o644); err != nil {
		t.Fatalf("write settings.gradle.kts: %v", err)
	}

	// Run gradle test.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gradle", "test", "--no-daemon")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "PGDESIGN_DB="+baseURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gradle test failed:\n%s\nerror: %v", output, err)
	}
	t.Logf("gradle test output:\n%s", output)
}

// TestConformanceZig renders the Zig wrapper template and verifies it can be
// rendered without error. Full build+run conformance requires zig and the
// pg.zig dependency to be available, which is not typical in CI.
func TestConformanceZig(t *testing.T) {
	testdb.SkipIfNoPostgres(t)

	if _, err := exec.LookPath("zig"); err != nil {
		t.Skip("zig not found in PATH")
	}

	fixturePath := conformanceFixturePath(t)
	baseURL := conformanceBaseURL()
	baseName := extractDBName(baseURL)

	// Verify the template renders without error.
	rendered, err := testdb.RenderTemplate("zig", fixturePath, baseURL, baseName)
	if err != nil {
		t.Fatalf("render Zig template: %v", err)
	}
	if len(rendered) == 0 {
		t.Fatal("rendered Zig template is empty")
	}

	// Verify key API patterns are present in the rendered output.
	output := string(rendered)
	for _, pattern := range []string{
		"pg.Conn.openAndAuthUri",
		"std.Uri.parse",
		"std.posix.clock_gettime",
		"std.crypto.random.bytes",
		"std.Io.Threaded",
	} {
		if !strings.Contains(output, pattern) {
			t.Errorf("rendered template missing expected pattern: %s", pattern)
		}
	}

	// Full build conformance requires pg.zig as a zig dependency, which
	// cannot be verified without a zig project with build.zig.zon that
	// fetches the dependency. Skip the build step.
	t.Skip("pg.zig dependency not available for build conformance (template renders correctly)")
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
