package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
)

// writeConfigOverrideTree builds two independent config locations:
//   - walkup/: contains a pgdesign.toml (pg_version 15) discoverable by
//     FindConfig walk-up search from walkup/
//   - elsewhere/override-config.toml: a config file at a non-standard name and
//     location (pg_version 16), only reachable via the --config override
//
// Returns (walkupDir, overridePath).
func writeConfigOverrideTree(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()

	walkup := filepath.Join(base, "walkup")
	if err := os.MkdirAll(walkup, 0o755); err != nil {
		t.Fatalf("mkdir walkup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(walkup, "pgdesign.toml"), []byte("[database]\npg_version = 15\n"), 0o644); err != nil {
		t.Fatalf("write walkup pgdesign.toml: %v", err)
	}

	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("mkdir elsewhere: %v", err)
	}
	overridePath := filepath.Join(elsewhere, "override-config.toml")
	if err := os.WriteFile(overridePath, []byte("[database]\npg_version = 16\n"), 0o644); err != nil {
		t.Fatalf("write override config: %v", err)
	}

	return walkup, overridePath
}

func TestResolveConfigPath_OverrideWins(t *testing.T) {
	walkup, overridePath := writeConfigOverrideTree(t)

	got, found, err := resolveConfigPath(&overridePath, walkup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for existing override")
	}
	if got != overridePath {
		t.Errorf("expected override path %q to win, got %q", overridePath, got)
	}
}

func TestResolveConfigPath_WalkUpFallback(t *testing.T) {
	walkup, _ := writeConfigOverrideTree(t)

	got, found, err := resolveConfigPath(nil, walkup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected walk-up to find walkup/pgdesign.toml")
	}
	want := filepath.Join(walkup, "pgdesign.toml")
	if got != want {
		t.Errorf("expected walk-up path %q, got %q", want, got)
	}
}

func TestResolveConfigPath_MissingOverrideIsHardError(t *testing.T) {
	// walkup contains a findable pgdesign.toml; a missing override must NOT
	// silently fall back to it.
	walkup, _ := writeConfigOverrideTree(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.toml")

	got, found, err := resolveConfigPath(&missing, walkup)
	if err == nil {
		t.Fatalf("expected hard error for missing override, got path=%q found=%v", got, found)
	}
	if found || got != "" {
		t.Errorf("missing override must not resolve to anything, got path=%q found=%v", got, found)
	}
}

func TestResolveConfigPath_DirectoryOverrideIsHardError(t *testing.T) {
	walkup, _ := writeConfigOverrideTree(t)
	dirOverride := t.TempDir()

	got, found, err := resolveConfigPath(&dirOverride, walkup)
	if err == nil {
		t.Fatalf("expected hard error for directory override, got path=%q found=%v", got, found)
	}
}

func TestLoadProjectConfig_OverrideWins(t *testing.T) {
	walkup, overridePath := writeConfigOverrideTree(t)

	cfg, err := loadProjectConfig(&overridePath, walkup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.PGVersion != 16 {
		t.Errorf("expected override config (pg_version 16) to win over walk-up (15), got %d", cfg.Database.PGVersion)
	}
}

func TestLoadProjectConfig_WalkUpFallback(t *testing.T) {
	walkup, _ := writeConfigOverrideTree(t)

	cfg, err := loadProjectConfig(nil, walkup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.PGVersion != 15 {
		t.Errorf("expected walk-up config (pg_version 15), got %d", cfg.Database.PGVersion)
	}
}

func TestLoadProjectConfig_MissingOverrideIsHardError(t *testing.T) {
	walkup, _ := writeConfigOverrideTree(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.toml")

	cfg, err := loadProjectConfig(&missing, walkup)
	if err == nil {
		t.Fatalf("expected hard error for missing override, got cfg=%+v", cfg)
	}
}

func TestResolveSchemaPaths_OverrideAppliesToDirectorySearch(t *testing.T) {
	// A directory positional arg normally triggers FindConfig; with an
	// override, the override's schema list must be used instead.
	base := t.TempDir()
	project := filepath.Join(base, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Two schema files in the directory; the override config selects only one.
	for _, name := range []string{"a.toml", "b.toml"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("[meta]\nschema = \"x\"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	overridePath := filepath.Join(base, "alt-config.toml")
	cfgContent := fmt.Sprintf("[project]\nschemas = [%q]\n", filepath.Join(project, "a.toml"))
	if err := os.WriteFile(overridePath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write override config: %v", err)
	}

	paths, err := resolveSchemaPaths(&overridePath, []string{project})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(project, "a.toml") {
		t.Errorf("expected override config to select only a.toml, got %v", paths)
	}
}

func TestRunBuild_ConfigOverride(t *testing.T) {
	config.CodegenModes = SupportedModes()

	// Full project (schema + config) in one place...
	project := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "freshness_schema.toml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "schema.toml"), src, 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}
	cfgPath := filepath.Join(project, "pgdesign.toml")
	cfg := `[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.sql]
format = "sql"
path = "out.sql"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}

	// ...but the working directory is elsewhere, where walk-up finds nothing.
	t.Chdir(t.TempDir())

	// Without the override, build must fail: no config is discoverable.
	if code := runBuild(nil, true, true, false); code == 0 {
		t.Fatal("sanity: expected build without override to fail from a config-less cwd")
	}

	// With the override, build must succeed (dry-run exits 0 on missing files).
	if code := runBuild(&cfgPath, true, true, false); code != 0 {
		t.Errorf("expected build with --config override to succeed, got exit code %d", code)
	}
}

func TestRunBuild_MissingConfigOverrideIsHardError(t *testing.T) {
	config.CodegenModes = SupportedModes()
	missing := filepath.Join(t.TempDir(), "does-not-exist.toml")
	t.Chdir(t.TempDir())

	if code := runBuild(&missing, true, true, false); code == 0 {
		t.Error("expected build with missing --config override to fail")
	}
}
