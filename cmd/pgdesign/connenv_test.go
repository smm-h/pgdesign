package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
)

// TestAppConstructsWithBoundDBFlags is the MECHANICAL "no DB-URL flag registers
// unbound" assertion. strictcli hard-ERRORS at registration when a
// ConnectionURLFlag binds to an undeclared env or a URL-class flag is unbound,
// so a successful construction of the fully-registered app proves every --db
// and --live flag is bound to the declared PGDESIGN_DB connection env. If any
// binding were missing (or the WithConnectionEnv declaration removed), buildApp
// would panic here.
func TestAppConstructsWithBoundDBFlags(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("buildApp panicked (an unbound or misbound DB-URL flag): %v", r)
		}
	}()
	if app := buildApp(); app == nil {
		t.Fatal("buildApp returned nil")
	}
}

// TestDBFlagsAdvertiseConnectionEnv confirms each DB command surfaces the
// PGDESIGN_DB env binding in its help (the framework prints [env: PGDESIGN_DB]
// for a ConnectionURLFlag). This is a second, output-level witness that the
// bindings exist across the command surface.
func TestDBFlagsAdvertiseConnectionEnv(t *testing.T) {
	app := buildApp()
	cmds := [][]string{
		{"introspect", "--help"},
		{"stats", "--help"},
		{"serve", "--help"},
		{"codegen", "--help"},
		{"seed", "--help"},
		{"diff", "--help"},
		{"migrate", "plan", "--help"},
		{"migrate", "generate", "--help"},
		{"migrate", "apply", "--help"},
		{"testdb", "setup", "--help"},
	}
	for _, argv := range cmds {
		res := app.Test(argv)
		if !strings.Contains(res.Stdout, "PGDESIGN_DB") {
			t.Errorf("%v help does not advertise PGDESIGN_DB binding:\n%s", argv, res.Stdout)
		}
	}
}

// TestEnvOnlyInvocation proves a DB command resolves its URL from PGDESIGN_DB
// when no --db flag is passed: with the env set to an unreachable URL, the
// command must proceed to a CONNECTION failure, never the "--db is required"
// handler error (which would mean the env fallback did not resolve).
func TestEnvOnlyInvocation(t *testing.T) {
	t.Setenv("PGDESIGN_DB", "postgres://u:p@127.0.0.1:1/envonly_db")
	app := buildApp()
	res := app.Test([]string{"stats"})
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit connecting to an unreachable env URL, got 0\n%s", res.Stdout)
	}
	if strings.Contains(res.Stderr, "--db is required") {
		t.Fatalf("env fallback did not resolve: got \"--db is required\" despite PGDESIGN_DB set\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "envonly_db") && !strings.Contains(res.Stderr, "connect") {
		t.Fatalf("expected a connection error referencing the env URL, got:\n%s", res.Stderr)
	}
}

// TestPrecedenceCLIOverEnv proves the CLI flag wins over the env value: with
// both set to distinct unreachable URLs, the command must dial the CLI target.
func TestPrecedenceCLIOverEnv(t *testing.T) {
	t.Setenv("PGDESIGN_DB", "postgres://u:p@127.0.0.1:1/env_layer_db")
	app := buildApp()
	res := app.Test([]string{"stats", "--db", "postgres://u:p@127.0.0.1:1/cli_layer_db"})
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0\n%s", res.Stdout)
	}
	if strings.Contains(res.Stderr, "env_layer_db") {
		t.Fatalf("env value was used despite an explicit --db (precedence violated)\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "cli_layer_db") {
		t.Fatalf("expected the CLI --db value to be dialed, got:\n%s", res.Stderr)
	}
}

// fakeConnReader is a CheckContext that also implements ConnectionEnvReader,
// returning a fixed (value, present) for PGDESIGN_DB.
type fakeConnReader struct {
	value   string
	present bool
}

func (f fakeConnReader) ProjectRoot() string { return "." }
func (f fakeConnReader) ConnectionEnvValue(envVar string) (string, bool) {
	if envVar != "PGDESIGN_DB" {
		return "", false
	}
	return f.value, f.present
}

// TestResolveCheckDBURL pins the check-side resolution layering (env > config)
// and the hermetic detection.
func TestResolveCheckDBURL(t *testing.T) {
	withCfg := &config.Config[config.AbsolutePath]{}
	withCfg.Database.URL = "postgres://config/url"
	empty := &config.Config[config.AbsolutePath]{}

	t.Run("env layer wins over config", func(t *testing.T) {
		ctx := fakeConnReader{value: "postgres://env/url", present: true}
		url, herm := resolveCheckDBURL(ctx, withCfg)
		if herm || url != "postgres://env/url" {
			t.Fatalf("got (%q, %v), want (env url, false)", url, herm)
		}
	})

	t.Run("hermetic: env set but suppressed => skip, config ignored", func(t *testing.T) {
		t.Setenv("PGDESIGN_DB", "postgres://suppressed/url")
		ctx := fakeConnReader{present: false} // framework hid it under --hermetic
		url, herm := resolveCheckDBURL(ctx, withCfg)
		if !herm {
			t.Fatalf("expected hermetic=true, got (%q, %v)", url, herm)
		}
		if url != "" {
			t.Fatalf("hermetic must not fall back to config, got url %q", url)
		}
	})

	t.Run("env unset, config layer used", func(t *testing.T) {
		os.Unsetenv("PGDESIGN_DB")
		ctx := fakeConnReader{present: false}
		url, herm := resolveCheckDBURL(ctx, withCfg)
		if herm || url != "postgres://config/url" {
			t.Fatalf("got (%q, %v), want (config url, false)", url, herm)
		}
	})

	t.Run("nothing configured => empty, not hermetic", func(t *testing.T) {
		os.Unsetenv("PGDESIGN_DB")
		ctx := fakeConnReader{present: false}
		url, herm := resolveCheckDBURL(ctx, empty)
		if herm || url != "" {
			t.Fatalf("got (%q, %v), want (\"\", false)", url, herm)
		}
	})
}

// TestNoRawGetenvInCmd enforces roadmap 2.2's grep obligation: no raw
// os.Getenv for the DB URL remains in cmd/ (the connection-env framework and
// resolveCheckDBURL replace it). Test files and the testdb harness are excepted.
func TestNoRawGetenvInCmd(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "handlers_testdb") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "os.Getenv") {
			t.Errorf("%s contains a raw os.Getenv; DB URLs must resolve via the connection-env framework", name)
		}
	}
}

// TestHermeticCheckSkipsVisibly is the end-to-end witness that a DB-backed
// check (workload) SKIPS VISIBLY under --hermetic, naming hermetic in the
// outcome, instead of attempting to connect. PGDESIGN_DB is set to prove the
// skip is due to hermetic suppression, not a missing URL.
func TestHermeticCheckSkipsVisibly(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "freshness_schema.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PGDESIGN_DB", "postgres://u:p@127.0.0.1:1/should_not_connect")

	app := buildApp()
	res := app.Test([]string{"--hermetic", "check", "--tag", "workload", "--verbose"})
	out := res.Stdout + res.Stderr
	if !strings.Contains(strings.ToLower(out), "hermetic") {
		t.Fatalf("hermetic check-run did not name hermetic in a visible skip:\n%s", out)
	}
	if strings.Contains(out, "should_not_connect") {
		t.Fatalf("hermetic run attempted a connection to the suppressed URL:\n%s", out)
	}
}
