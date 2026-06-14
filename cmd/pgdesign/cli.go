package main

import (
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

func main() {
	app := strictcli.NewApp("pgdesign", Version, "PostgreSQL schema compiler",
		strictcli.WithChecks(".strictcli/checks.toml"),
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

	app.GlobalFlag(strictcli.BoolFlag("quiet", "Suppress non-error output"))

	app.Command("generate", "Generate SQL from schema file(s) or directory", handleGenerate,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.BoolFlag("idempotent", "Add IF NOT EXISTS guards to all statements"),
			strictcli.BoolFlag("no-comments", "Exclude COMMENT ON statements from output"),
			strictcli.StringFlag("format", "Output format", strictcli.Default("sql"), strictcli.Choices("sql", "json", "d2", "svg")),
			strictcli.BoolFlag("strict-nf", "Enable strict normal form checking"),
		),
	)

	app.Command("validate", "Validate schema file(s) or directory", handleValidate,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.BoolFlag("show-suppressed", "Show suppressed diagnostics with their reasons"),
			strictcli.StringFlag("db", "PostgreSQL connection URL", strictcli.Default(nil)),
			strictcli.BoolFlag("strict-nf", "Enable strict normal form checking"),
			strictcli.BoolFlag("json", "Output as JSON"),
			strictcli.StringFlag("schema", "Schema name", strictcli.Repeatable()),
		),
	)

	app.Command("audit", "Audit schema file(s) or directory for issues", handleAudit,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.StringFlag("tables", "Limit FD discovery to specific tables", strictcli.Repeatable()),
			strictcli.FloatFlag("approximate", "Approximate FD threshold (0.0 = exact only)", strictcli.Default(0.0)),
			strictcli.StringFlag("db", "PostgreSQL connection URL", strictcli.Default(nil)),
			strictcli.BoolFlag("strict-nf", "Enable strict normal form checking"),
			strictcli.BoolFlag("json", "Output as JSON"),
			strictcli.StringFlag("schema", "Schema name", strictcli.Repeatable()),
		),
	)

	app.Command("fmt", "Format a pgdesign schema file or directory", handleFmt,
		strictcli.WithArgs(strictcli.NewArg("path", "Path to file or directory")),
		strictcli.WithFlags(
			strictcli.BoolFlag("check", "Check if file is already formatted (exit 1 if not)"),
			strictcli.StringFlag("table-order", "Table ordering: dependency or alphabetical",
				strictcli.Default("dependency"), strictcli.Choices("dependency", "alphabetical")),
			strictcli.StringFlag("column-order", "Column ordering: pk_fk_alpha, alphabetical, fk_last, or preserve",
				strictcli.Default("pk_fk_alpha"), strictcli.Choices("pk_fk_alpha", "alphabetical", "fk_last", "preserve")),
		),
	)

	app.Command("introspect", "Introspect a live PostgreSQL database", handleIntrospect,
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.StringFlag("schema", "Schema name to introspect", strictcli.Repeatable()),
			strictcli.StringFlag("output", "Output file path (default: stdout)", strictcli.Default(nil)),
		),
	)

	app.Command("diff", "Diff schema file(s) or directory against a live database", handleDiff,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.BoolFlag("json", "Output diff as JSON"),
			strictcli.StringFlag("live", "PostgreSQL connection URL for live comparison", strictcli.Default(nil)),
		),
	)

	mig := app.Group("migrate", "Database migration commands")
	mig.Command("plan", "Plan migrations from schema changes", handleMigratePlan,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)
	mig.Command("generate", "Generate migration files", handleMigrateGenerate,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.StringFlag("version", "Migration version (semver)", strictcli.Default(nil)),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)
	mig.Command("apply", "Apply pending migrations", handleMigrateApply,
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
			strictcli.BoolFlag("dry-run", "Show SQL without executing"),
			strictcli.IntFlag("timeout", "Lock timeout in seconds", strictcli.Default(30)),
		),
	)
	mig.Command("rollback", "Rollback the last migration", handleMigrateRollback,
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)
	mig.Command("status", "Show migration status", handleMigrateStatus,
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)

	app.Command("serve", "Start the pgdesign HTTP API server", handleServe,
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
			strictcli.IntFlag("port", "HTTP port to listen on", strictcli.Default(8080)),
			strictcli.StringFlag("schema", "Schema name to serve", strictcli.Repeatable()),
			strictcli.IntFlag("timeout", "Request timeout in seconds", strictcli.Default(30)),
		),
	)

	ext := app.Group("extension", "Extension management commands")
	ext.Command("discover", "Discover extensions from a live database", handleExtensionDiscover,
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL"),
		),
	)

	app.Command("codegen", "Generate application code from schema policies", handleCodegen,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL", strictcli.Default(nil)),
			strictcli.StringFlag("lang", "Target language", strictcli.Choices("python", "zig", "go")),
			strictcli.StringFlag("mode", "Codegen mode", strictcli.Default("validators"), strictcli.Choices("validators", "constants")),
			strictcli.StringFlag("output", "Output file path (default: stdout)", strictcli.Default(nil)),
		),
	)

	app.Run()
}

// loadProjectConfig attempts to load pgdesign.toml from the directory containing
// the given path (or the path itself if it's a directory). Returns a zero-valued
// config silently if no config file is found.
func loadProjectConfig(path string) *config.Config {
	dir := path
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		dir = filepath.Dir(path)
	}
	cfg, err := config.LoadOrDefault(dir)
	if err != nil {
		// Config exists but is malformed; fall back to defaults.
		return &config.Config{}
	}
	return cfg
}

// configSchemaNames derives PostgreSQL schema names from config.Project.Schemas
// by stripping the .toml extension from each file basename. Returns nil if no
// schemas are configured.
func configSchemaNames(cfg *config.Config) []string {
	if len(cfg.Project.Schemas) == 0 {
		return nil
	}
	names := make([]string, len(cfg.Project.Schemas))
	for i, s := range cfg.Project.Schemas {
		base := filepath.Base(s)
		names[i] = strings.TrimSuffix(base, ".toml")
	}
	return names
}

// extractPaths extracts the path(s) from kwargs. Handles the variadic "path"
// arg which returns []interface{}.
func extractPaths(kwargs map[string]interface{}) []string {
	raw := kwargs["path"].([]interface{})
	paths := make([]string, len(raw))
	for i, v := range raw {
		paths[i] = v.(string)
	}
	return paths
}

// resolveSchemaPaths resolves the given CLI paths into a list of .toml schema
// file paths. Handles single files, multiple files, directories (with optional
// pgdesign.toml config), and pgdesign.toml files directly.
func resolveSchemaPaths(paths []string) ([]string, error) {
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
			return resolveFromConfig(p)
		}
		return []string{p}, nil
	}

	// Directory: look for pgdesign.toml.
	configPath, hasConfig := config.FindConfig(p)
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

// resolveFromConfig loads pgdesign.toml and returns the resolved schema file paths.
func resolveFromConfig(configPath string) ([]string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if len(cfg.Project.Schemas) == 0 {
		return nil, fmt.Errorf("pgdesign.toml lists no schemas")
	}
	return cfg.SchemaFiles(filepath.Dir(configPath)), nil
}

// collectUserTypes extracts UserTypeDefs from a RawSchema's Types.
func collectUserTypes(raw *parse.RawSchema) []semtype.UserTypeDef {
	var userTypes []semtype.UserTypeDef
	for _, rt := range raw.Types {
		ut := semtype.UserTypeDef{
			Name:   rt.Name,
			Kind:   rt.Kind,
			Base:   rt.BaseType,
			Values: rt.Values,
		}
		if rt.NotNull != nil {
			ut.NotNull = rt.NotNull
		}
		if rt.Default != nil {
			v := *rt.Default
			ut.Default = &v
		}
		if rt.DefaultExpr != nil {
			ut.DefaultExpr = *rt.DefaultExpr
		}
		if rt.Check != nil {
			ut.Check = *rt.Check
		}
		if rt.Unique != nil {
			ut.Unique = *rt.Unique
		}
		if rt.Array != nil {
			ut.Array = *rt.Array
		}
		if rt.Comment != nil {
			ut.Comment = *rt.Comment
		}
		userTypes = append(userTypes, ut)
	}
	return userTypes
}

// parseAndBuild is a shared helper for commands that need a resolved schema.
// It accepts one or more paths (files or a directory) and returns the built schema.
func parseAndBuild(paths []string) (*model.Schema, int) {
	resolvedPaths, err := resolveSchemaPaths(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
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
		return nil, 1
	}

	// Print parse warnings/info but continue.
	parseWarnings := parseDiags.Warnings()
	if len(parseWarnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(parseWarnings, true))
	}

	reg := semtype.NewBuiltinRegistry()

	// Load user-defined types from all schemas into the registry.
	for _, raw := range raws {
		userTypes := collectUserTypes(raw)
		if len(userTypes) > 0 {
			loadDiags := reg.LoadUserTypes(userTypes)
			if loadDiags.HasErrors() {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(loadDiags, true))
				return nil, 1
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
		return nil, 1
	}

	warnings := buildDiags.Warnings()
	if len(warnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(warnings, true))
	}

	return schema, 0
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
			Name:      e.Name,
			Types:     e.Types,
			Opclasses: e.Opclasses,
			Functions: e.Functions,
		}
	}
	return result
}
