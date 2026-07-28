package main

import (
	"strings"
	"testing"
)

// TestImportsE2E_OwnedImportedTableCollision verifies that an owned table whose
// (schema, name) collides with a vendored imported reference table is a hard error
// naming BOTH sources (E244), replacing the earlier silent owned-wins shadowing.
func TestImportsE2E_OwnedImportedTableCollision(t *testing.T) {
	fw := makeFrameworkRepo(t)

	// Consumer lives in the SAME target schema ("app") the framework's `users`
	// table is imported into, and declares its OWN `users` table there — a direct
	// (app, users) collision between the owned table and the imported reference.
	consumer := t.TempDir()
	writeFile(t, consumer, "pgdesign.toml", `[project]
schemas = ["schema.toml"]

[database]
pg_version = 15

[imports.framework]
git = "`+fw+`"
ref = "v1"
schema = "app"
`)
	writeFile(t, consumer, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "app"

[tables.users]
comment = "consumer's own users table — collides with the imported one"

[tables.users.columns.id]
type = "id"

[tables.orders]
comment = "references the framework users, which vendors it into the surface"

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.user_id]
type = "ref"

[tables.orders.fks.fk_orders_user]
columns = ["user_id"]
ref_table = "framework:users"
ref_columns = ["id"]
on_delete = "CASCADE"
`)
	t.Chdir(consumer)
	app := buildApp()

	if r := app.Test([]string{"import", "lock"}); r.ExitCode != 0 {
		t.Fatalf("import lock failed: %s%s", r.Stdout, r.Stderr)
	}

	// A build path (generate) must reject the collision with E244.
	res := app.Test([]string{"generate", "."})
	if res.ExitCode == 0 {
		t.Fatalf("expected owned/imported table collision to fail the build\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "E244") {
		t.Fatalf("expected E244 in diagnostics, got stderr: %s", res.Stderr)
	}
	// Both sources must be named: the imported schema and the local table.
	if !strings.Contains(res.Stderr, "app") || !strings.Contains(res.Stderr, "users") {
		t.Fatalf("expected the diagnostic to name both sources, got: %s", res.Stderr)
	}
}
