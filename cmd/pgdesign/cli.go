package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/imports"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/strictcli/go/strictcli"
)

//go:embed .strictcli/checks.toml
var checksToml []byte

func main() {
	buildApp().Run()
}

// buildApp constructs and fully registers the pgdesign CLI app. It is extracted
// from main so tests can construct the app (which exercises strictcli's
// registration-time enforcement, e.g. the connection-URL binding check) and
// invoke commands via App.Test without a process boundary.
func buildApp() *strictcli.App {
	// Initialize codegen mode registry for config validation.
	config.CodegenModes = SupportedModes()

	app := strictcli.NewApp("pgdesign", Version, "PostgreSQL schema compiler",
		strictcli.WithChecksEmbed(checksToml),
		// PGDESIGN_DB is the single connection env for every database-backed
		// command and check. Every --db / --live flag binds to it via
		// ConnectionURLFlag (registration-time enforcement prevents an unbound
		// DB-URL flag). It is hermetic-suppressed: under --hermetic, DB work
		// resolves as absent and skips visibly instead of connecting.
		strictcli.WithConnectionEnv("PGDESIGN_DB", "PostgreSQL connection URL for database-backed commands and checks"),
	)

	app.SetCheckContext(func() strictcli.CheckContext {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		return &pgdesignCheckContext{root: cwd}
	})

	app.RegisterErrorCheck("validation", checkValidation)
	app.RegisterWarnCheck("nf", checkNF)
	app.RegisterWarnCheck("coverage", checkCoverage)
	app.RegisterWarnCheck("design", checkDesign)
	app.RegisterWarnCheck("structural", checkStructural)
	app.RegisterWarnCheck("workload", checkWorkload)
	app.RegisterErrorCheck("build", checkBuild)
	app.RegisterErrorCheck("revision", checkRevision)
	app.RegisterErrorCheck("imports", checkImports)

	registerGlobals(app)

	registerGenerateCmd(app)
	registerFmtCmd(app)
	registerIntrospectCmd(app)
	registerDiffCmd(app)

	mig := app.Group("migrate", "Database migration planning, generation, and execution")
	registerMigratePlanCmd(mig)
	registerMigrateGenerateCmd(mig)
	registerMigrateApplyCmd(mig)
	registerMigrateRollbackCmd(mig)
	registerMigrateStatusCmd(mig)
	registerMigrateSquashCmd(mig)
	registerMigrateRebaseCmd(mig)
	registerMigrateTestCmd(mig)
	registerMigrateBaselineCmd(mig)
	registerMigrateUpgradeCmd(mig)

	registerImportCmds(app)

	registerSeedCmd(app)
	registerServeCmd(app)
	registerCodegenCmd(app)
	registerBuildCmd(app)
	registerReviseCmd(app)
	registerStatsCmd(app)

	tdb := app.Group("testdb", "Manage ephemeral test databases for schema testing")
	registerTestdbSetupCmd(tdb)
	registerTestdbTeardownCmd(tdb)
	registerTestdbGCCmd(tdb)
	registerTestdbInitCmd(tdb)

	return app
}

// resolveConfigPath returns the pgdesign.toml path to use for config discovery.
// When override is non-nil (the --project-config global flag), the file must exist and
// be a regular file — a missing or unusable override is a hard error, never a
// silent fall back to directory search. When override is nil, it walks up from
// startDir via config.FindConfig.
func resolveConfigPath(override *string, startDir string) (string, bool, error) {
	if override != nil {
		info, err := os.Stat(*override)
		if err != nil {
			return "", false, fmt.Errorf("--project-config %q: %w", *override, err)
		}
		if info.IsDir() {
			return "", false, fmt.Errorf("--project-config %q is a directory, expected a pgdesign.toml file", *override)
		}
		return *override, true, nil
	}
	path, found := config.FindConfig(startDir)
	return path, found, nil
}

// loadProjectConfig loads the project's pgdesign.toml. When configOverride
// (the --project-config global flag) is non-nil, that exact file is loaded and any
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
			return nil, fmt.Errorf("--project-config %q: %w", *configOverride, err)
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

// modelSchemaNames returns the distinct PostgreSQL namespace names the built
// model's objects live in, sorted, defaulting to ["public"] when nothing carries
// an explicit schema. These are the namespaces to introspect when diffing the
// model against a live database.
//
// It replaces the earlier configSchemaNames, which derived namespaces from the
// config's schema FILE basenames — a bug: a multi-file project split across
// trace.toml/dispatch.toml/auth.toml whose tables all live in `public` was
// introspected against nonexistent namespaces (trace/dispatch/auth), yielding
// empty introspection and total false drift. Namespaces come from the tables'
// `schema=` attribute, never from filenames.
func modelSchemaNames(schema *model.Schema) []string {
	seen := make(map[string]bool)
	var names []string
	add := func(s string) {
		if s == "" {
			s = "public"
		}
		if !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	for i := range schema.Tables {
		add(schema.Tables[i].Schema)
	}
	for i := range schema.Views {
		add(schema.Views[i].Schema)
	}
	for i := range schema.MaterializedViews {
		add(schema.MaterializedViews[i].Schema)
	}
	for i := range schema.Sequences {
		add(schema.Sequences[i].Schema)
	}
	if len(names) == 0 {
		return []string{"public"}
	}
	sort.Strings(names)
	return names
}

// resolveSchemaPaths resolves the given CLI paths into a list of .toml schema
// file paths. Handles single files, multiple files, directories (with optional
// pgdesign.toml config), and pgdesign.toml files directly. configOverride (the
// --project-config global flag) replaces the walk-up config search for directory paths;
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
			// A positional config file and a --project-config override are two explicit
			// config sources; silently preferring one would hide the conflict.
			if configOverride != nil && !samePath(*configOverride, p) {
				return nil, fmt.Errorf("conflicting config sources: positional %q and --project-config %q", p, *configOverride)
			}
			return resolveFromConfig(p)
		}
		return []string{p}, nil
	}

	// Directory: look for pgdesign.toml (or use the --project-config override).
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
// schema. configOverride (the --project-config global flag) replaces the walk-up
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

	buildOpts := []model.BuildOption{model.WithImports(importAliasSchemas(cfg))}

	// Load the vendored import surface (imports/<alias>/) as REFERENCE tables so
	// imported-FK targets resolve through the union (roadmap 7.3). Aliases without
	// a committed lockfile are skipped — the unresolved FK then surfaces as a normal
	// diagnostic rather than being silently satisfied. A corrupt/undecodable
	// vendored surface is a hard error.
	if cfg != nil && len(cfg.Imports) > 0 {
		if configPath, found, _ := resolveConfigPath(configOverride, filepath.Dir(resolvedPaths[0])); found {
			projectRoot := filepath.Dir(configPath)
			declared := make([]string, 0, len(cfg.Imports))
			for a := range cfg.Imports {
				declared = append(declared, a)
			}
			surface, err := imports.LoadAllSurfaces(projectRoot, declared)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return nil, nil, 1
			}
			if len(surface.Tables) > 0 {
				buildOpts = append(buildOpts, model.WithImportedTables(surface.Tables))
			}
		}
	}

	if len(raws) == 1 {
		schema, buildDiags = model.Build(raws[0], reg, buildOpts...)
	} else {
		schema, buildDiags = model.BuildMulti(raws, reg, buildOpts...)
	}

	if buildDiags.HasErrors() {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(buildDiags, true))
		return nil, nil, 1
	}

	warnings := buildDiags.Warnings()
	if len(warnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(warnings, true))
	}

	// Resolve the config and toml PG-version tiers into schema.PGVersion here,
	// at the shared build entry point. Build() sets the toml tier; the config
	// tier (pgdesign.toml [database].pg_version) wins over it. The live tier is
	// applied later, only where a database connection is available, via
	// applyLivePGVersion.
	schema.PGVersion = resolvePGVersion(0, cfg.Database.PGVersion, schema.PGVersion)

	return schema, reg, 0
}

// nfViolationCodes are the audit diagnostic codes for normal form violations.
// These are the codes the --strict-nf gate (and revise's pure NF core) promote to
// error severity and BLOCK on. BCNF (W103) is included: it is a normal-form
// violation like the others, so "strict normal form" must reject it too — and
// roadmap 6.1 requires a BCNF violation to block revise's pure tier.
var nfViolationCodes = map[string]bool{
	"W100": true, // 1NF
	"W101": true, // 2NF
	"W102": true, // 3NF
	"W103": true, // BCNF
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

// importAliasSchemas projects the project's [imports] declarations into the
// alias -> target-PG-schema map model.WithImports consumes, so `alias:table` FK
// references resolve at build time (roadmap 7.1). A nil config yields nil.
func importAliasSchemas[P config.PathKind](cfg *config.Config[P]) map[string]string {
	if cfg == nil || len(cfg.Imports) == 0 {
		return nil
	}
	m := make(map[string]string, len(cfg.Imports))
	for alias, d := range cfg.Imports {
		m[alias] = d.Schema
	}
	return m
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
