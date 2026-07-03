package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/strictcli/go/strictcli"
)

//go:embed .strictcli/checks.toml
var checksToml []byte

func main() {
	// Initialize codegen mode registry for config validation.
	config.CodegenModes = SupportedModes()

	app := strictcli.NewApp("pgdesign", Version, "PostgreSQL schema compiler",
		strictcli.WithChecksEmbed(checksToml),
	)

	app.SetCheckContext(func() strictcli.CheckContext {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		return &pgdesignCheckContext{root: cwd}
	})

	app.RegisterCheck("validation", checkValidation)
	app.RegisterCheck("nf", checkNF)
	app.RegisterCheck("coverage", checkCoverage)
	app.RegisterCheck("design", checkDesign)
	app.RegisterCheck("structural", checkStructural)
	app.RegisterCheck("workload", checkWorkload)
	app.RegisterCheck("build", checkBuild)

	strictcli.RegisterGlobals[Globals](app)

	app.RegisterHandler("generate", "Generate SQL DDL from TOML schema file(s) or directory", func() strictcli.Handler {
		return &generateHandler{}
	})

	app.RegisterHandler("fmt", "Format a pgdesign TOML schema file or directory in place", func() strictcli.Handler {
		return &fmtHandler{}
	})

	app.RegisterHandler("introspect", "Introspect a live PostgreSQL database into TOML schema", func() strictcli.Handler {
		return &introspectHandler{}
	})

	app.RegisterHandler("diff", "Compare schema file(s) or directory against another target", func() strictcli.Handler {
		return &diffHandler{}
	})

	mig := app.Group("migrate", "Database migration planning, generation, and execution")
	mig.RegisterHandler("plan", "Plan migrations by diffing the TOML schema against a live database without writing any files. Shows which tables, columns, indexes, and constraints would change, along with risk levels and required lock types for each operation. Useful for previewing changes before generating migration files.", func() strictcli.Handler {
		return &migratePlanHandler{}
	})
	mig.RegisterHandler("generate", "Generate versioned migration files by comparing the TOML schema against a live database. Produces up and down SQL files with risk annotations, safety linting, and expand-migrate-contract phase classification. Volatile defaults and operations on large tables are automatically detected and handled safely.", func() strictcli.Handler {
		return &migrateGenerateHandler{}
	})
	mig.RegisterHandler("apply", "Apply all pending migrations to the target database in order. Each migration runs inside its own transaction with advisory locking to prevent concurrent execution. Non-transactional operations like CREATE INDEX CONCURRENTLY execute outside transactions automatically. Use --dry-run to preview the SQL without executing.", func() strictcli.Handler {
		return &migrateApplyHandler{}
	})
	mig.RegisterHandler("rollback", "Rollback applied database migrations to a specified target version. Executes down migration SQL in reverse application order with advisory locking. Multi-step rollbacks verify reversibility of all steps before starting. The target version is exclusive, meaning that version stays applied after rollback completes.", func() strictcli.Handler {
		return &migrateRollbackHandler{}
	})
	mig.RegisterHandler("status", "Show which migrations have been applied to the target database and which are still pending. Reads the migration tracking table and compares it with the migrations directory to display version numbers, applied timestamps, and current execution status for each migration file.", func() strictcli.Handler {
		return &migrateStatusHandler{}
	})
	mig.RegisterHandler("squash", "Consolidate a range of sequential migration files into a single optimized migration. Recognizes 12 types of inverse operation pairs for cancellation, merges sequential type changes, and folds column additions into CREATE TABLE statements where possible. The original migration files are replaced with one combined migration file.", func() strictcli.Handler {
		return &migrateSquashHandler{}
	})
	mig.RegisterHandler("test", "Test migrations by applying them against a staging database to verify correctness before production deployment. With --shadow mode, replays all migrations into a fresh database and diffs the result against the TOML schema to catch drift between migration files and schema definitions.", func() strictcli.Handler {
		return &migrateTestHandler{}
	})

	app.RegisterHandler("seed", "Generate type-aware test data for all schema tables", func() strictcli.Handler {
		return &seedHandler{}
	})

	app.RegisterHandler("serve", "Start the pgdesign HTTP API server and web interface", func() strictcli.Handler {
		return &serveHandler{}
	})

	app.RegisterHandler("codegen", "Generate type-safe application code from schema definitions", func() strictcli.Handler {
		return &codegenHandler{}
	})

	app.RegisterHandler("build", "Generate all configured outputs from pgdesign.toml", func() strictcli.Handler {
		return &buildHandler{}
	})

	app.RegisterHandler("stats", "Analyze database statistics, index usage, and health", func() strictcli.Handler {
		return &statsHandler{}
	})

	tdb := app.Group("testdb", "Manage ephemeral test databases for schema testing")
	tdb.RegisterHandler("setup", "Create an ephemeral test database on the PostgreSQL server and apply the specified DDL schema to it. The database is created with a unique name containing a timestamp and random suffix to allow parallel test execution. Returns the connection URL for the new database.", func() strictcli.Handler {
		return &testdbSetupHandler{}
	})
	tdb.RegisterHandler("teardown", "Drop an ephemeral test database that was previously created by testdb setup. Terminates any remaining connections to the database before dropping it. Should be called in test cleanup to prevent orphaned databases from accumulating on the PostgreSQL server over time.", func() strictcli.Handler {
		return &testdbTeardownHandler{}
	})
	tdb.RegisterHandler("gc", "Drop orphaned test databases that were not properly torn down after test runs. Scans the PostgreSQL server for databases matching the pgdesign test naming pattern and removes those older than the specified duration. Useful for cleaning up after interrupted or failed test runs in CI and local development.", func() strictcli.Handler {
		return &testdbGCHandler{}
	})
	tdb.RegisterHandler("init", "Generate test database wrapper code for consumer projects that need to run integration tests against a pgdesign-managed schema. Produces language-specific helper modules with setup and teardown functions that create ephemeral databases, apply DDL, and clean up automatically after each test run.", func() strictcli.Handler {
		return &testdbInitHandler{}
	})

	app.Run()
}

// configOverride returns the --config global flag value from the CLI context.
func configOverride(ctx *strictcli.Context) *string {
	return strictcli.Globals[Globals](ctx).Config
}

// resolveConfigPath returns the pgdesign.toml path to use for config discovery.
// When override is non-nil (the --config global flag), the file must exist and
// be a regular file — a missing or unusable override is a hard error, never a
// silent fall back to directory search. When override is nil, it walks up from
// startDir via config.FindConfig.
func resolveConfigPath(override *string, startDir string) (string, bool, error) {
	if override != nil {
		info, err := os.Stat(*override)
		if err != nil {
			return "", false, fmt.Errorf("--config %q: %w", *override, err)
		}
		if info.IsDir() {
			return "", false, fmt.Errorf("--config %q is a directory, expected a pgdesign.toml file", *override)
		}
		return *override, true, nil
	}
	path, found := config.FindConfig(startDir)
	return path, found, nil
}

// loadProjectConfig loads the project's pgdesign.toml. When configOverride
// (the --config global flag) is non-nil, that exact file is loaded and any
// failure (missing or malformed) is a hard error. Without an override, the
// config is discovered from the directory containing path (or path itself if
// it's a directory): a missing config yields a zero-valued config, and a
// malformed one falls back to defaults (pre-existing lenient behavior).
func loadProjectConfig(configOverride *string, path string) (*config.RawConfig, error) {
	if configOverride != nil {
		configPath, _, err := resolveConfigPath(configOverride, "")
		if err != nil {
			return nil, err
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return nil, fmt.Errorf("--config %q: %w", *configOverride, err)
		}
		return cfg, nil
	}
	dir := path
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		dir = filepath.Dir(path)
	}
	cfg, err := config.LoadOrDefault(dir)
	if err != nil {
		// Config exists but is malformed; fall back to defaults.
		return &config.RawConfig{}, nil
	}
	return cfg, nil
}

// configSchemaNames derives PostgreSQL schema names from config.Project.Schemas
// by stripping the .toml extension from each file basename. Returns nil if no
// schemas are configured.
func configSchemaNames[P config.PathKind](cfg *config.Config[P]) []string {
	if len(cfg.Project.Schemas) == 0 {
		return nil
	}
	names := make([]string, len(cfg.Project.Schemas))
	for i, s := range cfg.Project.Schemas {
		base := filepath.Base(string(s))
		names[i] = strings.TrimSuffix(base, ".toml")
	}
	return names
}

// resolveSchemaPaths resolves the given CLI paths into a list of .toml schema
// file paths. Handles single files, multiple files, directories (with optional
// pgdesign.toml config), and pgdesign.toml files directly. configOverride (the
// --config global flag) replaces the walk-up config search for directory paths;
// explicit schema file paths are unaffected.
func resolveSchemaPaths(configOverride *string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one path is required")
	}

	// Multiple paths: each must be a file.
	if len(paths) > 1 {
		for _, p := range paths {
			info, err := os.Stat(p)
			if err != nil {
				return nil, fmt.Errorf("cannot stat %q: %w", p, err)
			}
			if info.IsDir() {
				return nil, fmt.Errorf("when passing multiple paths, each must be a file, not a directory: %q", p)
			}
		}
		return paths, nil
	}

	// Single path.
	p := paths[0]
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("cannot stat %q: %w", p, err)
	}

	if !info.IsDir() {
		// Single file. Check if it's pgdesign.toml itself.
		if filepath.Base(p) == "pgdesign.toml" {
			// A positional config file and a --config override are two explicit
			// config sources; silently preferring one would hide the conflict.
			if configOverride != nil && !samePath(*configOverride, p) {
				return nil, fmt.Errorf("conflicting config sources: positional %q and --config %q", p, *configOverride)
			}
			return resolveFromConfig(p)
		}
		return []string{p}, nil
	}

	// Directory: look for pgdesign.toml (or use the --config override).
	configPath, hasConfig, err := resolveConfigPath(configOverride, p)
	if err != nil {
		return nil, err
	}
	if hasConfig {
		return resolveFromConfig(configPath)
	}

	// No config: find all .toml files in the directory (Dir handles exclusion).
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("cannot read directory %q: %w", p, err)
	}
	var filePaths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".toml") && name != "pgdesign.toml" {
			filePaths = append(filePaths, filepath.Join(p, name))
		}
	}
	if len(filePaths) == 0 {
		return nil, fmt.Errorf("no .toml schema files found in %q", p)
	}
	return filePaths, nil
}

// samePath reports whether two paths refer to the same file after absolute
// path normalization.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return absA == absB
}

// resolveFromConfig loads pgdesign.toml and returns the resolved schema file paths.
func resolveFromConfig(configPath string) ([]string, error) {
	resolved, err := config.LoadAndResolve(configPath)
	if err != nil {
		return nil, err
	}
	if len(resolved.Project.Schemas) == 0 {
		return nil, fmt.Errorf("pgdesign.toml lists no schemas")
	}
	return resolved.SchemaFiles(), nil
}

// parseAndBuild is a shared helper for commands that need a resolved schema.
// It accepts one or more paths (files or a directory) and returns the built
// schema. configOverride (the --config global flag) replaces the walk-up
// config search for both schema path resolution and project config loading.
func parseAndBuild(configOverride *string, paths []string) (*model.Schema, *semtype.Registry, int) {
	resolvedPaths, err := resolveSchemaPaths(configOverride, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, nil, 1
	}

	var raws []*parse.RawSchema
	var parseDiags diagnostic.Diagnostics

	if len(resolvedPaths) == 1 {
		raw, diags := parse.File(resolvedPaths[0])
		parseDiags = diags
		if raw != nil {
			raws = append(raws, raw)
		}
	} else {
		schemas, diags := parse.Files(resolvedPaths)
		parseDiags = diags
		raws = schemas
	}

	if len(raws) == 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(parseDiags, true))
		return nil, nil, 1
	}

	// Print parse warnings/info but continue.
	parseWarnings := parseDiags.Warnings()
	if len(parseWarnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(parseWarnings, true))
	}

	reg := semtype.NewBuiltinRegistry()

	// Register extension-provided types so they pass the base type allowlist.
	cfg, cfgErr := loadProjectConfig(configOverride, resolvedPaths[0])
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return nil, nil, 1
	}
	for _, ext := range cfg.Extensions {
		reg.AddExtensionTypes(ext.Types)
	}

	// Load user-defined types from all schemas into the registry.
	for _, raw := range raws {
		userTypes := parse.CollectUserTypes(raw)
		if len(userTypes) > 0 {
			loadDiags := reg.LoadUserTypes(userTypes)
			if loadDiags.HasErrors() {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(loadDiags, true))
				return nil, nil, 1
			}
		}
	}

	var schema *model.Schema
	var buildDiags diagnostic.Diagnostics

	if len(raws) == 1 {
		schema, buildDiags = model.Build(raws[0], reg)
	} else {
		schema, buildDiags = model.BuildMulti(raws, reg)
	}

	if buildDiags.HasErrors() {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(buildDiags, true))
		return nil, nil, 1
	}

	warnings := buildDiags.Warnings()
	if len(warnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(warnings, true))
	}

	return schema, reg, 0
}

// nfViolationCodes are the audit diagnostic codes for normal form violations.
var nfViolationCodes = map[string]bool{
	"W100": true, // 1NF
	"W101": true, // 2NF
	"W102": true, // 3NF
}

// promoteNFViolations returns a copy of diags where NF violation warnings
// (codes W100, W101, W102) are promoted to Error severity.
func promoteNFViolations(diags []diagnostic.Diagnostic) []diagnostic.Diagnostic {
	result := make([]diagnostic.Diagnostic, len(diags))
	copy(result, diags)
	for i := range result {
		if result[i].Severity == diagnostic.Warning && nfViolationCodes[result[i].Code] {
			result[i].Severity = diagnostic.Error
		}
	}
	return result
}

// configToUserExtensions converts config.ExtensionConfig entries to
// extregistry.UserExtension entries for registry loading.
func configToUserExtensions(exts []config.ExtensionConfig) []extregistry.UserExtension {
	result := make([]extregistry.UserExtension, len(exts))
	for i, e := range exts {
		result[i] = extregistry.UserExtension{
			Name:         e.Name,
			Types:        e.Types,
			Opclasses:    e.Opclasses,
			Functions:    e.Functions,
			IndexMethods: e.IndexMethods,
		}
	}
	return result
}
