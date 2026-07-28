package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateSquashCLI_RequiresDB is the first CLI-level test for `migrate
// squash`. It verifies the 0.6(d) safety stopgap: --db is mandatory, so
// invoking squash without it hard-errors before touching any files. This
// blocks offline squash even of never-applied ranges.
func TestMigrateSquashCLI_RequiresDB(t *testing.T) {
	root := projectRoot(t)

	// Two minimal migration files so the range is non-trivial; the command
	// must reject BEFORE reading them because --db is absent.
	dir := t.TempDir()
	m1 := `version = "0.1.0"
description = "first"

[[ddl]]
op = "create_table"
table = "public.users"
comment = "Users"
pk = ["id"]
`
	m2 := `version = "0.2.0"
description = "second"

[[ddl]]
op = "add_column"
table = "public.users"
column = "email"
type = "text"
`
	if err := os.WriteFile(filepath.Join(dir, "0.1.0.toml"), []byte(m1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0.2.0.toml"), []byte(m2), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "./cmd/pgdesign/", "migrate", "squash",
		"--from", "0.1.0", "--to", "0.2.0", "--dir", dir)
	cmd.Dir = root
	// The --db flag has a PGDESIGN_DB env fallback (ConnectionURLFlag). This
	// test asserts the WITHOUT-a-database behavior, so the env var must be
	// stripped from the subprocess — otherwise CI (which sets PGDESIGN_DB)
	// would supply a connection and the command would proceed past the guard.
	env := os.Environ()
	filtered := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "PGDESIGN_DB=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	cmd.Env = filtered
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --db is omitted, got success:\n%s", out)
	}
	got := string(out)
	if !strings.Contains(got, "--db") || !strings.Contains(got, "required") {
		t.Errorf("expected an error indicating --db is required, got:\n%s", got)
	}
}
