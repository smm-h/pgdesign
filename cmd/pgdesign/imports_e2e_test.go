package main

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/imports"
)

// writeFile writes content to dir/name, creating parent dirs.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeFrameworkRepo creates a mini framework pgdesign project and commits it to a
// git repo tagged v1, returning the repo path. The framework provides a users
// table (id, status enum) in schema "public".
func makeFrameworkRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	writeFile(t, dir, "pgdesign.toml", `[project]
schemas = ["schema.toml"]

[database]
pg_version = 15
`)
	writeFile(t, dir, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "public"
extensions = ["pgcrypto"]

[types.user_status]
kind = "enum"
values = ["active", "banned"]
comment = "account status"

[tables.users]
comment = "framework users"

[tables.users.columns.id]
type = "id"

[tables.users.columns.status]
type = "user_status"

[tables.audit_log]
comment = "unreferenced framework table"

[tables.audit_log.columns.id]
type = "id"

[tables.audit_log.columns.note]
type = "short_text"
`)
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", "pgdesign.toml", "schema.toml")
	runGit("commit", "-q", "-m", "framework v1")
	runGit("tag", "v1")
	return dir
}

// makeConsumer creates a consumer project referencing the framework via alias.
// localUserType is the local FK column type ("ref" matches the framework id).
func makeConsumer(t *testing.T, frameworkRepo, localUserType string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "pgdesign.toml", `[project]
schemas = ["schema.toml"]

[database]
pg_version = 15

[imports.framework]
git = "`+frameworkRepo+`"
ref = "v1"
schema = "app"
`)
	writeFile(t, dir, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "public"

[tables.orders]
comment = "consumer orders"

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.user_id]
type = "`+localUserType+`"

[tables.orders.fks.fk_orders_user]
columns = ["user_id"]
ref_table = "framework:users"
ref_columns = ["id"]
on_delete = "CASCADE"
`)
	return dir
}

func TestImportsE2E_LockVendorsSurfaceAndCheckPasses(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)
	consumer := makeConsumer(t, fw, "ref")
	t.Chdir(consumer)

	app := buildApp()
	res := app.Test([]string{"import", "lock"})
	if res.ExitCode != 0 {
		t.Fatalf("import lock failed: exit %d\nstdout: %s\nstderr: %s", res.ExitCode, res.Stdout, res.Stderr)
	}

	// Lockfile exists and records the resolved commit + surface.
	if !imports.LockfileExists(consumer, "framework") {
		t.Fatal("lockfile not written")
	}
	lf, err := imports.ReadLockfile(consumer, "framework")
	if err != nil {
		t.Fatal(err)
	}
	if lf.Commit == "" {
		t.Error("lockfile missing resolved commit")
	}
	if lf.PGVersion != 15 {
		t.Errorf("lockfile pg_version = %d, want 15", lf.PGVersion)
	}
	// Surface: users table + user_status enum (2 objects); audit_log excluded.
	if len(lf.Objects) != 2 {
		t.Errorf("expected 2 surface objects (users + user_status), got %d: %+v", len(lf.Objects), lf.Objects)
	}
	for _, o := range lf.Objects {
		if o.Key == "table:app.audit_log" {
			t.Error("unreferenced audit_log vendored into surface")
		}
	}

	// Offline drift check passes against the vendored surface.
	built, _, exit := parseAndBuild(nil, []string{"."})
	if exit != 0 {
		t.Fatalf("consumer build failed: exit %d", exit)
	}
	diags := imports.Check(consumer, "framework", built)
	if diags.HasErrors() {
		t.Fatalf("expected clean offline check, got: %v", diags)
	}
}

func TestImportsE2E_UpdateRequiresExistingLock(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)
	consumer := makeConsumer(t, fw, "ref")
	t.Chdir(consumer)
	app := buildApp()

	// update before any lock -> error.
	res := app.Test([]string{"import", "update"})
	if res.ExitCode == 0 {
		t.Fatalf("import update should fail without an existing lockfile\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	}

	// lock, then update succeeds; lock again refuses.
	if r := app.Test([]string{"import", "lock"}); r.ExitCode != 0 {
		t.Fatalf("import lock failed: %s%s", r.Stdout, r.Stderr)
	}
	if r := app.Test([]string{"import", "update"}); r.ExitCode != 0 {
		t.Fatalf("import update failed: %s%s", r.Stdout, r.Stderr)
	}
	if r := app.Test([]string{"import", "lock"}); r.ExitCode == 0 {
		t.Fatalf("import lock should refuse to overwrite an existing lockfile")
	}
}

func TestImportsE2E_UnknownAliasArg(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)
	consumer := makeConsumer(t, fw, "ref")
	t.Chdir(consumer)
	app := buildApp()
	res := app.Test([]string{"import", "lock", "nosuchalias"})
	if res.ExitCode == 0 {
		t.Fatal("import lock with an undeclared alias should fail")
	}
}

func TestImportsE2E_BadRef(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)
	consumer := t.TempDir()
	writeFile(t, consumer, "pgdesign.toml", `[project]
schemas = ["schema.toml"]

[imports.framework]
git = "`+fw+`"
ref = "v-does-not-exist"
schema = "app"
`)
	writeFile(t, consumer, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "public"

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
	res := app.Test([]string{"import", "lock"})
	if res.ExitCode == 0 {
		t.Fatal("import lock should fail on a bad ref")
	}
}

func TestImportsE2E_DriftedColumnTypeDetectedOffline(t *testing.T) {
	testenv.Isolate(t)
	fw := makeFrameworkRepo(t)
	// Consumer's local user_id is "text" but the framework users.id is uuid-based
	// "id" -> junction type drift.
	consumer := makeConsumer(t, fw, "short_text")
	t.Chdir(consumer)
	app := buildApp()
	if r := app.Test([]string{"import", "lock"}); r.ExitCode != 0 {
		t.Fatalf("import lock failed: %s%s", r.Stdout, r.Stderr)
	}
	built, _, exit := parseAndBuild(nil, []string{"."})
	if exit != 0 {
		t.Fatalf("consumer build failed: exit %d", exit)
	}
	diags := imports.Check(consumer, "framework", built)
	found := false
	for _, d := range diags {
		if d.Code == "E237" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected E237 junction drift for text vs uuid, got: %v", diags)
	}
}
