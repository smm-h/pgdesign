package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadPoolConfig(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[database]
pool_max_conns = 25
pool_min_conns = 5
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Database.PoolMaxConns != 25 {
		t.Errorf("pool_max_conns = %d, want 25", cfg.Database.PoolMaxConns)
	}
	if cfg.Database.PoolMinConns != 5 {
		t.Errorf("pool_min_conns = %d, want 5", cfg.Database.PoolMinConns)
	}
}

func TestLoadPoolConfig_Absent(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[database]
pg_version = 16
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Database.PoolMaxConns != 0 {
		t.Errorf("pool_max_conns = %d, want 0 (zero value)", cfg.Database.PoolMaxConns)
	}
	if cfg.Database.PoolMinConns != 0 {
		t.Errorf("pool_min_conns = %d, want 0 (zero value)", cfg.Database.PoolMinConns)
	}
}

func TestLoadPoolConfig_NegativeMaxConns(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[database]
pool_max_conns = -1
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative pool_max_conns")
	}
	if !strings.Contains(err.Error(), "pool_max_conns must be non-negative") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "pool_max_conns must be non-negative")
	}
}

func TestLoadPoolConfig_NegativeMinConns(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[database]
pool_min_conns = -5
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative pool_min_conns")
	}
	if !strings.Contains(err.Error(), "pool_min_conns must be non-negative") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "pool_min_conns must be non-negative")
	}
}

func TestLoadPoolConfig_MinExceedsMax(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[database]
pool_max_conns = 5
pool_min_conns = 10
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when pool_min_conns > pool_max_conns")
	}
	if !strings.Contains(err.Error(), "pool_min_conns cannot exceed pool_max_conns") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "pool_min_conns cannot exceed pool_max_conns")
	}
}

func TestLoadOutputSection(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.ddl]
format = "sql"
path = "schema/generated.sql"
idempotent = true
comments = false

[output.diagram]
format = "d2"
path = "schema/schema.d2"

[output.constants_python]
format = "codegen"
path = "src/constants.py"
lang = "python"
mode = "constants"

[output.validators_python]
format = "codegen"
path = "src/validators.py"
lang = "python"
mode = "validators"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Output) != 4 {
		t.Fatalf("expected 4 output targets, got %d", len(cfg.Output))
	}

	ddl := cfg.Output["ddl"]
	if ddl.Format != "sql" {
		t.Errorf("ddl.format = %q, want %q", ddl.Format, "sql")
	}
	if ddl.Path != "schema/generated.sql" {
		t.Errorf("ddl.path = %q, want %q", ddl.Path, "schema/generated.sql")
	}
	if !ddl.Idempotent {
		t.Error("ddl.idempotent = false, want true")
	}
	if ddl.Comments == nil || *ddl.Comments != false {
		t.Errorf("ddl.comments = %v, want false", ddl.Comments)
	}

	diagram := cfg.Output["diagram"]
	if diagram.Format != "d2" {
		t.Errorf("diagram.format = %q, want %q", diagram.Format, "d2")
	}
	if diagram.Comments != nil {
		t.Errorf("diagram.comments = %v, want nil (unset)", diagram.Comments)
	}

	cp := cfg.Output["constants_python"]
	if cp.Format != "codegen" {
		t.Errorf("constants_python.format = %q, want %q", cp.Format, "codegen")
	}
	if cp.Lang != "python" {
		t.Errorf("constants_python.lang = %q, want %q", cp.Lang, "python")
	}
	if cp.Mode != "constants" {
		t.Errorf("constants_python.mode = %q, want %q", cp.Mode, "constants")
	}

	vp := cfg.Output["validators_python"]
	if vp.Mode != "validators" {
		t.Errorf("validators_python.mode = %q, want %q", vp.Mode, "validators")
	}
}

func TestLoadOutput_MissingPath(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.bad]
format = "sql"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "output.bad: path is required") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "output.bad: path is required")
	}
}

func TestLoadOutput_InvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.bad]
format = "yaml"
path = "out.yaml"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "invalid format") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid format")
	}
}

func TestLoadOutput_CodegenWithoutLang(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.bad]
format = "codegen"
path = "out.py"
mode = "constants"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for codegen without lang")
	}
	if !strings.Contains(err.Error(), "lang is required when format is codegen") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "lang is required when format is codegen")
	}
}

func TestLoadOutput_CodegenWithoutMode(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.bad]
format = "codegen"
path = "out.py"
lang = "python"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for codegen without mode")
	}
	if !strings.Contains(err.Error(), "mode is required when format is codegen") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "mode is required when format is codegen")
	}
}

func TestLoadOutput_InvalidLang(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.bad]
format = "codegen"
path = "out.rs"
lang = "rust"
mode = "constants"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid lang")
	}
	if !strings.Contains(err.Error(), "invalid lang") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid lang")
	}
}

func TestLoadOutput_InvalidMode(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.bad]
format = "codegen"
path = "out.py"
lang = "python"
mode = "classes"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid mode")
	}
}

func TestLoadSuppressSection(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[suppress]
"users.tags.W004" = "tags column is intentionally denormalized"
"audit_log.W001" = "audit tables are expected to be large"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Suppress) != 2 {
		t.Fatalf("expected 2 suppress entries, got %d", len(cfg.Suppress))
	}
	if v := cfg.Suppress["users.tags.W004"]; v != "tags column is intentionally denormalized" {
		t.Errorf("suppress[users.tags.W004] = %q, want %q", v, "tags column is intentionally denormalized")
	}
	if v := cfg.Suppress["audit_log.W001"]; v != "audit tables are expected to be large" {
		t.Errorf("suppress[audit_log.W001] = %q, want %q", v, "audit tables are expected to be large")
	}
}

func TestLoadOutputGroups(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[project]
schemas = ["schema.toml"]

[output.core_sql]
format = "sql"
path = "core.sql"
groups = ["core", "auth"]

[output.full_sql]
format = "sql"
path = "full.sql"
`
	path := filepath.Join(tmpDir, "pgdesign.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	coreSql := cfg.Output["core_sql"]
	if len(coreSql.Groups) != 2 {
		t.Fatalf("expected 2 groups on core_sql output, got %d", len(coreSql.Groups))
	}
	if coreSql.Groups[0] != "core" || coreSql.Groups[1] != "auth" {
		t.Errorf("core_sql groups = %v, want [core auth]", coreSql.Groups)
	}

	fullSql := cfg.Output["full_sql"]
	if len(fullSql.Groups) != 0 {
		t.Errorf("expected no groups on full_sql output, got %v", fullSql.Groups)
	}
}
