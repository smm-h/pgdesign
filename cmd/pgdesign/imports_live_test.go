package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/pgdesign/internal/testenv"
)

// liveConn connects to the configured database for DB-gated import tests and
// resets the "app" import-target schema so each test starts clean.
//
// The connection comes from testdb.RequireConn and not from a hand-rolled
// os.Getenv + t.Skip, which is what this used to be: reading the env directly
// meant an absent or unreachable database skipped even under
// PGDESIGN_REQUIRE_DB=1, so a CI lane that declared PostgreSQL must exist could
// run none of these tests and still report green.
func liveConn(t *testing.T) (*pgx.Conn, context.Context) {
	t.Helper()
	ctx := context.Background()
	conn := testdb.RequireConn(t, ctx)
	// Registered after RequireConn's own close, so it runs BEFORE it.
	t.Cleanup(func() { conn.Exec(ctx, "DROP SCHEMA IF EXISTS app CASCADE") })
	if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS app CASCADE"); err != nil {
		t.Fatalf("reset app schema: %v", err)
	}
	return conn, ctx
}

// buildConsumerWithImportedFK builds a locked consumer project whose `orders` table
// declares an imported FK to framework:users (resolved into schema "app"). The
// returned schema carries ImportedTables (app.users) and an FK with RefAlias set,
// exercising the imported-surface code paths.
func buildConsumerWithImportedFK(t *testing.T) *model.Schema {
	t.Helper()
	fw := makeFrameworkRepo(t)
	consumer := makeConsumer(t, fw, "ref")
	t.Chdir(consumer)
	app := buildApp()
	if r := app.Test([]string{"import", "lock"}); r.ExitCode != 0 {
		t.Fatalf("import lock failed: %s%s", r.Stdout, r.Stderr)
	}
	schema, _, exit := parseAndBuild(nil, []string{"."})
	if exit != 0 {
		t.Fatalf("consumer build failed: exit %d", exit)
	}
	if len(schema.ImportedTables) == 0 {
		t.Fatal("expected the consumer build to carry imported reference tables")
	}
	return schema
}

func hasCode(diags []diagnostic.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// TestVerifyLiveImports_PresentAbsentMismatch verifies the live import
// verification seam (roadmap 7.4): a present matching imported table+column passes,
// an absent table is E238, and a type mismatch on the referenced column is E239.
func TestVerifyLiveImports_PresentAbsentMismatch(t *testing.T) {
	testenv.Isolate(t)
	conn, ctx := liveConn(t)
	schema := buildConsumerWithImportedFK(t)

	// Absent: no app.users at all -> E238 (missing table).
	if _, err := conn.Exec(ctx, "CREATE SCHEMA app"); err != nil {
		t.Fatalf("create schema app: %v", err)
	}
	diags := verifyLiveImports(ctx, conn, schema)
	if !hasCode(diags, "E238") {
		t.Fatalf("expected E238 for an absent imported table, got: %v", diags)
	}

	// Present + matching: app.users(id uuid) -> passes clean.
	if _, err := conn.Exec(ctx, "CREATE TABLE app.users (id uuid PRIMARY KEY)"); err != nil {
		t.Fatalf("create app.users: %v", err)
	}
	diags = verifyLiveImports(ctx, conn, schema)
	for _, d := range diags {
		if d.Severity.String() == "error" {
			t.Fatalf("expected a clean pass for a present matching import, got error: %v", diags)
		}
	}

	// Type mismatch: recreate app.users(id text) -> E239 (present but wrong type).
	if _, err := conn.Exec(ctx, "DROP TABLE app.users"); err != nil {
		t.Fatalf("drop app.users: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE app.users (id text PRIMARY KEY)"); err != nil {
		t.Fatalf("recreate app.users text: %v", err)
	}
	diags = verifyLiveImports(ctx, conn, schema)
	if !hasCode(diags, "E239") {
		t.Fatalf("expected E239 for an imported column type mismatch (uuid vs text), got: %v", diags)
	}
}

// TestLoadImportedFKPools_LiveEndToEnd verifies tier-1 pool loading (roadmap 7.4):
// loadImportedFKPools reads real keys from the live imported table, keyed by
// "<schema>.<table>.<column>", as quoted SQL literals.
func TestLoadImportedFKPools_LiveEndToEnd(t *testing.T) {
	testenv.Isolate(t)
	conn, ctx := liveConn(t)
	schema := buildConsumerWithImportedFK(t)

	if _, err := conn.Exec(ctx, "CREATE SCHEMA app"); err != nil {
		t.Fatalf("create schema app: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE app.users (id uuid PRIMARY KEY)"); err != nil {
		t.Fatalf("create app.users: %v", err)
	}
	ids := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	}
	for _, id := range ids {
		if _, err := conn.Exec(ctx, "INSERT INTO app.users (id) VALUES ($1)", id); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	pools, err := loadImportedFKPools(ctx, conn, schema)
	if err != nil {
		t.Fatalf("loadImportedFKPools: %v", err)
	}
	pool, ok := pools["app.users.id"]
	if !ok {
		t.Fatalf("expected a pool keyed app.users.id, got keys: %v", keysOf(pools))
	}
	if len(pool) != len(ids) {
		t.Fatalf("expected %d pooled keys, got %d: %v", len(ids), len(pool), pool)
	}
	for _, id := range ids {
		if !containsLiteral(pool, "'"+id+"'") {
			t.Fatalf("expected pooled quoted literal for %s, got: %v", id, pool)
		}
	}
}

func containsLiteral(pool []string, want string) bool {
	for _, v := range pool {
		if v == want {
			return true
		}
	}
	return false
}

func keysOf(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
