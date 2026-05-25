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
