package main

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"
)

// TestImportsE2E_DeclaredButUnreferencedAliasBuildsClean verifies that declaring an
// import alias in [imports] without any FK referencing it is not an error: the
// build proceeds, contributes no imported reference tables, and emits no import
// diagnostics. (An unused declaration is a valid intermediate state — you can pin a
// dependency before wiring the first cross-project FK.)
func TestImportsE2E_DeclaredButUnreferencedAliasBuildsClean(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)

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
	// A purely local schema — nothing references framework:*.
	writeFile(t, consumer, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "public"

[tables.orders]
comment = "consumer orders, no imported FK"

[tables.orders.columns.id]
type = "id"
`)
	t.Chdir(consumer)
	app := buildApp()
	if r := app.Test([]string{"import", "lock"}); r.ExitCode != 0 {
		t.Fatalf("import lock failed: %s%s", r.Stdout, r.Stderr)
	}

	schema, _, exit := parseAndBuild(nil, []string{"."})
	if exit != 0 {
		t.Fatalf("build with a declared-but-unreferenced import alias should succeed, got exit %d", exit)
	}
	if len(schema.ImportedTables) != 0 {
		t.Fatalf("an unreferenced alias must vendor no imported tables, got %d", len(schema.ImportedTables))
	}
}

// TestImportsE2E_CheckFrameworkImportsPasses drives the imports check through the
// check-framework command (`pgdesign check --tag imports`), the CI-facing entry
// point that runs drift detection AND requirement enforcement (extension
// re-declaration + pg_version floor). A consumer that re-declares the required
// extension and targets a compatible version passes with exit 0.
func TestImportsE2E_CheckFrameworkImportsPasses(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)

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
	// Re-declares pgcrypto (the framework surface requires it) and references
	// framework:users so the surface is non-empty.
	writeFile(t, consumer, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "public"
extensions = ["pgcrypto"]

[tables.orders]
comment = "consumer orders"

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

	res := app.Test([]string{"check", "--tag", "imports"})
	if res.ExitCode != 0 {
		t.Fatalf("check --tag imports should pass for a clean locked import, got exit %d\nstdout: %s\nstderr: %s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// TestImportsE2E_CheckFrameworkImportsMissingExtension verifies the check-framework
// command FAILS when the consumer omits an extension the imported surface requires
// (E241), proving requirement enforcement runs through `check --tag imports`.
func TestImportsE2E_CheckFrameworkImportsMissingExtension(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)
	// makeConsumer does NOT declare pgcrypto, which the framework surface requires.
	consumer := makeConsumer(t, fw, "ref")
	t.Chdir(consumer)
	app := buildApp()
	if r := app.Test([]string{"import", "lock"}); r.ExitCode != 0 {
		t.Fatalf("import lock failed: %s%s", r.Stdout, r.Stderr)
	}

	res := app.Test([]string{"check", "--tag", "imports"})
	if res.ExitCode == 0 {
		t.Fatalf("check --tag imports should FAIL when a required extension is not re-declared\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	}
	out := res.Stdout + res.Stderr
	if !strings.Contains(out, "E241") {
		t.Fatalf("expected E241 (missing extension) from check --tag imports, got:\n%s", out)
	}
}
