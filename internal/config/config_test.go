package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["auth.toml", "game.toml"]
migrations_dir = "migrations"

[database]
pg_version = 18

[format]
table_order = "dependency"
column_order = "pk_fk_alpha"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.Project.Schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(cfg.Project.Schemas))
	}
	if cfg.Project.Schemas[0] != "auth.toml" {
		t.Errorf("schemas[0] = %q, want %q", cfg.Project.Schemas[0], "auth.toml")
	}
	if cfg.Project.Schemas[1] != "game.toml" {
		t.Errorf("schemas[1] = %q, want %q", cfg.Project.Schemas[1], "game.toml")
	}
	if cfg.Project.MigrationsDir != "migrations" {
		t.Errorf("migrations_dir = %q, want %q", cfg.Project.MigrationsDir, "migrations")
	}
	if cfg.Database.PGVersion != 18 {
		t.Errorf("pg_version = %d, want %d", cfg.Database.PGVersion, 18)
	}
	if cfg.Format.TableOrder != "dependency" {
		t.Errorf("table_order = %q, want %q", cfg.Format.TableOrder, "dependency")
	}
	if cfg.Format.ColumnOrder != "pk_fk_alpha" {
		t.Errorf("column_order = %q, want %q", cfg.Format.ColumnOrder, "pk_fk_alpha")
	}
}

func TestSchemaFiles(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{
			Schemas: []string{"auth.toml", "game.toml"},
		},
	}

	paths := cfg.SchemaFiles("/home/user/project/schema")
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if paths[0] != "/home/user/project/schema/auth.toml" {
		t.Errorf("paths[0] = %q, want %q", paths[0], "/home/user/project/schema/auth.toml")
	}
	if paths[1] != "/home/user/project/schema/game.toml" {
		t.Errorf("paths[1] = %q, want %q", paths[1], "/home/user/project/schema/game.toml")
	}
}

func TestFindConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// No pgdesign.toml yet.
	_, found := FindConfig(tmpDir)
	if found {
		t.Error("expected no config found in empty dir")
	}

	// Create pgdesign.toml.
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte("[project]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	foundPath, found := FindConfig(tmpDir)
	if !found {
		t.Error("expected config found after creating pgdesign.toml")
	}
	if foundPath != path {
		t.Errorf("found path = %q, want %q", foundPath, path)
	}
}

func TestLoadMinimal(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Project.Schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(cfg.Project.Schemas))
	}
	// Zero-value defaults.
	if cfg.Database.PGVersion != 0 {
		t.Errorf("pg_version = %d, want 0 (zero value)", cfg.Database.PGVersion)
	}
}

func TestLoadValidateSection(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[validate]
disable = ["W001", "E201"]
naming_pattern = "camelCase"
max_columns = 50
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Validate.Disable) != 2 {
		t.Fatalf("expected 2 disabled rules, got %d", len(cfg.Validate.Disable))
	}
	if cfg.Validate.Disable[0] != "W001" {
		t.Errorf("disable[0] = %q, want %q", cfg.Validate.Disable[0], "W001")
	}
	if cfg.Validate.Disable[1] != "E201" {
		t.Errorf("disable[1] = %q, want %q", cfg.Validate.Disable[1], "E201")
	}
	if cfg.Validate.NamingPattern != "camelCase" {
		t.Errorf("naming_pattern = %q, want %q", cfg.Validate.NamingPattern, "camelCase")
	}
	if cfg.Validate.MaxColumns != 50 {
		t.Errorf("max_columns = %d, want %d", cfg.Validate.MaxColumns, 50)
	}
}

func TestLoadMigrateSection(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[migrate]
lock_timeout = "5s"
auto_concurrent_threshold = 100000
expand_contract_threshold = 1000000
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Migrate.LockTimeout != "5s" {
		t.Errorf("lock_timeout = %q, want %q", cfg.Migrate.LockTimeout, "5s")
	}
	if cfg.Migrate.AutoConcurrentThreshold != 100000 {
		t.Errorf("auto_concurrent_threshold = %d, want %d", cfg.Migrate.AutoConcurrentThreshold, 100000)
	}
	if cfg.Migrate.ExpandContractThreshold != 1000000 {
		t.Errorf("expand_contract_threshold = %d, want %d", cfg.Migrate.ExpandContractThreshold, 1000000)
	}
}

func TestLoadExtensionsSection(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[[extensions]]
name = "pgcrypto"
types = ["uuid"]
functions = ["gen_random_uuid"]

[[extensions]]
name = "btree_gin"
opclasses = ["int4_ops"]
index_methods = ["gin"]
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Extensions) != 2 {
		t.Fatalf("expected 2 extensions, got %d", len(cfg.Extensions))
	}

	ext0 := cfg.Extensions[0]
	if ext0.Name != "pgcrypto" {
		t.Errorf("extensions[0].name = %q, want %q", ext0.Name, "pgcrypto")
	}
	if len(ext0.Types) != 1 || ext0.Types[0] != "uuid" {
		t.Errorf("extensions[0].types = %v, want [uuid]", ext0.Types)
	}
	if len(ext0.Functions) != 1 || ext0.Functions[0] != "gen_random_uuid" {
		t.Errorf("extensions[0].functions = %v, want [gen_random_uuid]", ext0.Functions)
	}

	ext1 := cfg.Extensions[1]
	if ext1.Name != "btree_gin" {
		t.Errorf("extensions[1].name = %q, want %q", ext1.Name, "btree_gin")
	}
	if len(ext1.Opclasses) != 1 || ext1.Opclasses[0] != "int4_ops" {
		t.Errorf("extensions[1].opclasses = %v, want [int4_ops]", ext1.Opclasses)
	}
	if len(ext1.IndexMethods) != 1 || ext1.IndexMethods[0] != "gin" {
		t.Errorf("extensions[1].index_methods = %v, want [gin]", ext1.IndexMethods)
	}
}

func TestLoadOrDefault_NoConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := LoadOrDefault(tmpDir)
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	// Should return zero-valued config.
	if len(cfg.Project.Schemas) != 0 {
		t.Errorf("expected no schemas, got %d", len(cfg.Project.Schemas))
	}
	if cfg.Validate.MaxColumns != 0 {
		t.Errorf("expected zero max_columns, got %d", cfg.Validate.MaxColumns)
	}
}

func TestLoadOrDefault_WithConfig(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[validate]
max_columns = 42
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadOrDefault(tmpDir)
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	if cfg.Validate.MaxColumns != 42 {
		t.Errorf("max_columns = %d, want %d", cfg.Validate.MaxColumns, 42)
	}
}

func TestMergeValidateFlags(t *testing.T) {
	cfg := &Config{
		Validate: ValidateConfig{
			NamingPattern: "snake_case",
			MaxColumns:    30,
		},
	}

	// Non-zero flags override.
	cfg.MergeValidateFlags("camelCase", 50)
	if cfg.Validate.NamingPattern != "camelCase" {
		t.Errorf("naming_pattern = %q, want %q", cfg.Validate.NamingPattern, "camelCase")
	}
	if cfg.Validate.MaxColumns != 50 {
		t.Errorf("max_columns = %d, want %d", cfg.Validate.MaxColumns, 50)
	}

	// Zero-value flags do not override.
	cfg.MergeValidateFlags("", 0)
	if cfg.Validate.NamingPattern != "camelCase" {
		t.Errorf("naming_pattern should not change with empty flag, got %q", cfg.Validate.NamingPattern)
	}
	if cfg.Validate.MaxColumns != 50 {
		t.Errorf("max_columns should not change with zero flag, got %d", cfg.Validate.MaxColumns)
	}
}
