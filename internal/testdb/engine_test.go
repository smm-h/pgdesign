package testdb

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// testURL returns the database URL for tests.
func testURL() string {
	if u := os.Getenv("PGDESIGN_DB"); u != "" {
		return u
	}
	return defaultPostgresURL
}

// testManager creates a Manager for tests using the test URL.
func testManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(testURL())
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	return m
}

func TestCreateAndDrop(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	ddl := strings.NewReader("CREATE TABLE test_table (id int PRIMARY KEY);")
	db, err := m.Create(ctx, CreateOptions{DDL: ddl})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Verify database exists.
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral db: %v", err)
	}
	var exists bool
	err = conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", db.Name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check database existence: %v", err)
	}
	if !exists {
		t.Fatal("database does not exist after creation")
	}

	// Verify table was created.
	var tableExists bool
	err = conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename = 'test_table' AND schemaname = 'public')",
	).Scan(&tableExists)
	if err != nil {
		t.Fatalf("check table existence: %v", err)
	}
	if !tableExists {
		t.Fatal("test_table does not exist after DDL application")
	}
	conn.Close(ctx)

	// Drop and verify gone.
	if err := m.Drop(ctx, db); err != nil {
		t.Fatalf("drop: %v", err)
	}

	// Verify database is gone by querying pg_database from maintenance.
	mconn, err := m.connectMaintenance(ctx)
	if err != nil {
		t.Fatalf("connect maintenance: %v", err)
	}
	defer mconn.Close(ctx)

	err = mconn.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", db.Name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check database absence: %v", err)
	}
	if exists {
		t.Fatal("database still exists after drop")
	}
}

func TestApplyDDL(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	ddl := strings.NewReader(`
		CREATE TABLE users (id serial PRIMARY KEY, name text NOT NULL);
		CREATE TABLE orders (id serial PRIMARY KEY, user_id int REFERENCES users(id));
	`)
	db, err := m.Create(ctx, CreateOptions{DDL: ddl})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = m.Drop(ctx, db) }()

	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	for _, table := range []string{"users", "orders"} {
		var exists bool
		err := conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename = $1 AND schemaname = 'public')",
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s does not exist", table)
		}
	}
}

func TestApplyDDL_ExtensionError(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	ddl := strings.NewReader("CREATE EXTENSION nonexistent_extension;")
	_, err := m.Create(ctx, CreateOptions{DDL: ddl})
	if err == nil {
		t.Fatal("expected error for nonexistent extension")
	}
	if !strings.Contains(err.Error(), "nonexistent_extension") {
		t.Fatalf("error should mention extension name, got: %v", err)
	}
}

func TestDropIdempotent(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	db, err := m.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.Drop(ctx, db); err != nil {
		t.Fatalf("first drop: %v", err)
	}

	// Second drop should not error (IF EXISTS).
	if err := m.Drop(ctx, db); err != nil {
		t.Fatalf("second drop should not error: %v", err)
	}
}

func TestDropWithActiveConnection(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	db, err := m.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Open a tracked connection.
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Verify connection works.
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query on connection: %v", err)
	}

	// Drop should close the tracked connection and succeed.
	if err := m.Drop(ctx, db); err != nil {
		t.Fatalf("drop with active connection: %v", err)
	}
}

func TestSetupForTest(t *testing.T) {
	SkipIfNoPostgres(t)
	m := testManager(t)

	var dbName string
	t.Run("subtest", func(t *testing.T) {
		ddl := strings.NewReader("CREATE TABLE setup_test (id int PRIMARY KEY);")
		db := m.SetupForTest(t, CreateOptions{DDL: ddl})
		dbName = db.Name

		// Verify table exists.
		ctx := context.Background()
		conn, err := db.Connect(ctx)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		var exists bool
		err = conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename = 'setup_test' AND schemaname = 'public')",
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check table: %v", err)
		}
		if !exists {
			t.Fatal("setup_test table does not exist")
		}
		// Cleanup runs when subtest exits.
	})

	// After the subtest, the database should be gone.
	ctx := context.Background()
	mconn, err := m.connectMaintenance(ctx)
	if err != nil {
		t.Fatalf("connect maintenance: %v", err)
	}
	defer mconn.Close(ctx)

	var exists bool
	err = mconn.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check database absence: %v", err)
	}
	if exists {
		t.Fatal("database still exists after subtest cleanup")
	}
}

func TestConcurrentCreate(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	const n = 10
	dbs := make([]*EphemeralDB, n)
	errs := make([]error, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			db, err := m.Create(ctx, CreateOptions{})
			dbs[idx] = db
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// Cleanup all created databases.
	defer func() {
		for _, db := range dbs {
			if db != nil {
				_ = m.Drop(ctx, db)
			}
		}
	}()

	// Check for errors and uniqueness.
	names := make(map[string]bool)
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if names[dbs[i].Name] {
			t.Fatalf("duplicate database name: %s", dbs[i].Name)
		}
		names[dbs[i].Name] = true
	}
	if len(names) != n {
		t.Fatalf("expected %d unique names, got %d", n, len(names))
	}
}

func TestListOrphans(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	db1, err := m.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("create db1: %v", err)
	}
	db2, err := m.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("create db2: %v", err)
	}
	defer func() {
		_ = m.Drop(ctx, db1)
		_ = m.Drop(ctx, db2)
	}()

	// Wait a moment so time.Since(created) > 0.
	time.Sleep(10 * time.Millisecond)

	orphans, err := m.ListOrphans(ctx, 0)
	if err != nil {
		t.Fatalf("list orphans: %v", err)
	}

	found := make(map[string]*EphemeralDB)
	for _, o := range orphans {
		found[o.Name] = o
	}
	if found[db1.Name] == nil {
		t.Fatalf("db1 %s not found in orphans", db1.Name)
	}
	if found[db2.Name] == nil {
		t.Fatalf("db2 %s not found in orphans", db2.Name)
	}

	// Verify ActiveConnections is populated (non-nil) for each orphan.
	for _, o := range orphans {
		if o.ActiveConnections == nil {
			t.Fatalf("orphan %s has nil ActiveConnections", o.Name)
		}
		if *o.ActiveConnections < 0 {
			t.Fatalf("orphan %s has negative ActiveConnections: %d", o.Name, *o.ActiveConnections)
		}
	}
}

func TestConnAndPool(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	m := testManager(t)

	db, err := m.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = m.Drop(ctx, db) }()

	// Test Connect.
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query via conn: %v", err)
	}
	if one != 1 {
		t.Fatalf("expected 1, got %d", one)
	}

	// Test Pool.
	pool, err := db.Pool(ctx)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	var two int
	if err := pool.QueryRow(ctx, "SELECT 2").Scan(&two); err != nil {
		t.Fatalf("query via pool: %v", err)
	}
	if two != 2 {
		t.Fatalf("expected 2, got %d", two)
	}
}
