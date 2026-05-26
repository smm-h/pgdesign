package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/format"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/serve"
	"github.com/smm-h/pgdesign/internal/validate"
	"github.com/smm-h/strictcli/go/strictcli"
	"golang.org/x/sync/errgroup"
)

func main() {
	app := strictcli.NewApp("pgdesign", Version, "PostgreSQL schema compiler")

	app.GlobalFlag(strictcli.BoolFlag("quiet", "Suppress non-error output"))
	app.GlobalFlag(strictcli.StringFlag("db", "PostgreSQL connection URL", strictcli.Default(nil)))
	app.GlobalFlag(strictcli.BoolFlag("strict-nf", "Enable strict normal form checking"))

	app.Command("generate", "Generate SQL from schema file(s) or directory", handleGenerate,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.BoolFlag("idempotent", "Add IF NOT EXISTS guards to all statements"),
			strictcli.BoolFlag("no-comments", "Exclude COMMENT ON statements from output"),
			strictcli.StringFlag("format", "Output format", strictcli.Default("sql"), strictcli.Choices("sql", "json", "d2", "svg")),
		),
	)

	app.Command("validate", "Validate schema file(s) or directory", handleValidate,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
	)

	app.Command("audit", "Audit schema file(s) or directory for issues", handleAudit,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.StringFlag("tables", "Limit FD discovery to specific tables", strictcli.Repeatable()),
			strictcli.FloatFlag("approximate", "Approximate FD threshold (0.0 = exact only)", strictcli.Default(0.0)),
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
			strictcli.StringFlag("schema", "Schema name to introspect", strictcli.Repeatable()),
			strictcli.StringFlag("output", "Output file path (default: stdout)", strictcli.Default(nil)),
		),
	)

	app.Command("diff", "Diff schema file(s) or directory against a live database", handleDiff,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.BoolFlag("json", "Output diff as JSON"),
		),
	)

	mig := app.Group("migrate", "Database migration commands")
	mig.Command("plan", "Plan migrations from schema changes", handleMigratePlan,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
	)
	mig.Command("generate", "Generate migration files", handleMigrateGenerate,
		strictcli.WithArgs(strictcli.NewArg("path", "Path(s) to schema file(s) or directory", strictcli.Variadic())),
		strictcli.WithFlags(
			strictcli.StringFlag("version", "Migration version (semver)", strictcli.Default(nil)),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)
	mig.Command("apply", "Apply pending migrations", handleMigrateApply,
		strictcli.WithFlags(
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
			strictcli.BoolFlag("dry-run", "Show SQL without executing"),
		),
	)
	mig.Command("rollback", "Rollback the last migration", handleMigrateRollback,
		strictcli.WithFlags(
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)
	mig.Command("status", "Show migration status", handleMigrateStatus,
		strictcli.WithFlags(
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)

	app.Command("serve", "Start the pgdesign HTTP API server", handleServe,
		strictcli.WithFlags(
			strictcli.IntFlag("port", "HTTP port to listen on", strictcli.Default(8080)),
			strictcli.StringFlag("schema", "Schema name to serve", strictcli.Repeatable()),
		),
	)

	ext := app.Group("extension", "Extension management commands")
	ext.Command("discover", "Discover extensions from a live database", handleExtensionDiscover)

	app.Run()
}

func handleGenerate(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for PGVersion fallback.
	cfg := loadProjectConfig(paths[0])

	if kwargs["strict_nf"].(bool) {
		diags := audit.Audit(schema)
		diags = promoteNFViolations(diags)
		if len(diags) > 0 {
			fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
		}
		if diagnostic.Diagnostics(diags).HasErrors() {
			fmt.Fprintln(os.Stderr, "error: --strict-nf: normal form violations found, refusing to generate DDL")
			return 1
		}
	}

	// Use config PGVersion as fallback when schema doesn't specify one.
	pgVersion := schema.PGVersion
	if pgVersion == 0 && cfg.Database.PGVersion != 0 {
		pgVersion = cfg.Database.PGVersion
	}

	opts := generate.Options{
		Idempotent:      kwargs["idempotent"].(bool),
		IncludeComments: !kwargs["no_comments"].(bool),
		Format:          kwargs["format"].(string),
		PGVersion:       pgVersion,
	}

	out := generate.Generate(schema, opts)
	fmt.Print(out)
	return 0
}

func handleValidate(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Try to load project config from the directory of the first path argument.
	cfg := loadProjectConfig(paths[0])

	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))

	valCfg := &validate.Config{
		NamingPattern: cfg.Validate.NamingPattern,
		MaxColumns:    cfg.Validate.MaxColumns,
		Disabled:      cfg.Validate.Disable,
		Extensions:    schema.Extensions,
		ExtRegistry:   extReg,
	}

	diags := validate.Validate(schema, valCfg)
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}
	return 0
}

func handleAudit(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	var allDiags []diagnostic.Diagnostic

	// When --db is provided, discover FDs from live data for tables without declared FDs.
	if dbURL, ok := kwargs["db"].(string); ok && dbURL != "" {
		approxThreshold := kwargs["approximate"].(float64)
		opts := discover.Options{
			ApproximateThreshold: approxThreshold,
		}

		// Collect --tables filter values.
		var tableFilter map[string]bool
		if raw, ok := kwargs["tables"].([]interface{}); ok && len(raw) > 0 {
			tableFilter = make(map[string]bool, len(raw))
			for _, v := range raw {
				if s, ok := v.(string); ok {
					tableFilter[s] = true
				}
			}
		}

		// Build list of table indices eligible for discovery.
		var eligible []int
		for i := range schema.Tables {
			tbl := &schema.Tables[i]
			if len(tbl.Dependencies) > 0 {
				continue
			}
			if tableFilter != nil && !tableFilter[tbl.Name] {
				continue
			}
			eligible = append(eligible, i)
		}

		if len(eligible) > 0 {
			ctx := context.Background()

			// Use a connection pool for parallel discovery. Each goroutine
			// acquires its own connection since pgx.Conn is not concurrency-safe.
			pool, err := pgxpool.New(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect for FD discovery: %v\n", err)
				return 1
			}
			defer pool.Close()

			// Per-table results collected under a mutex.
			type tableResult struct {
				tableIdx int
				diags    []diagnostic.Diagnostic
			}
			var (
				mu      sync.Mutex
				results []tableResult
			)

			g, gctx := errgroup.WithContext(ctx)
			g.SetLimit(runtime.GOMAXPROCS(0))

			for _, idx := range eligible {
				idx := idx
				g.Go(func() error {
					tbl := &schema.Tables[idx]
					schemaName := tbl.Schema
					if schemaName == "" {
						schemaName = "public"
					}

					conn, err := pool.Acquire(gctx)
					if err != nil {
						mu.Lock()
						results = append(results, tableResult{
							tableIdx: idx,
							diags: []diagnostic.Diagnostic{{
								Severity: diagnostic.Warning,
								Table:    tbl.Name,
								Message:  fmt.Sprintf("FD discovery failed: %v", err),
							}},
						})
						mu.Unlock()
						return nil
					}
					defer conn.Release()

					fds, discDiags, err := discover.Discover(conn.Conn(), schemaName, tbl.Name, opts)

					var diags []diagnostic.Diagnostic
					diags = append(diags, discDiags...)
					if err != nil {
						diags = append(diags, diagnostic.Diagnostic{
							Severity: diagnostic.Warning,
							Table:    tbl.Name,
							Message:  fmt.Sprintf("FD discovery failed: %v", err),
						})
					} else if len(fds) > 0 {
						diags = append(diags, diagnostic.Diagnostic{
							Severity: diagnostic.Info,
							Table:    tbl.Name,
							Message:  fmt.Sprintf("Discovered %d FD(s) from data sample.", len(fds)),
						})
					}

					mu.Lock()
					results = append(results, tableResult{
						tableIdx: idx,
						diags:    diags,
					})
					// Write discovered FDs back to the table (safe: each goroutine
					// writes to a distinct table index).
					if err == nil && len(fds) > 0 {
						schema.Tables[idx].Dependencies = fds
					}
					mu.Unlock()

					return nil
				})
			}

			// errgroup goroutines never return errors (handled inline), but
			// Wait() still collects goroutine panics.
			_ = g.Wait()

			// Merge diagnostics in table order for deterministic output.
			for _, idx := range eligible {
				for _, r := range results {
					if r.tableIdx == idx {
						allDiags = append(allDiags, r.diags...)
						break
					}
				}
			}
		}
	}

	allDiags = append(allDiags, audit.Audit(schema)...)
	if kwargs["strict_nf"].(bool) {
		allDiags = promoteNFViolations(allDiags)
	}
	if len(allDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(allDiags, true))
	}
	if diagnostic.Diagnostics(allDiags).HasErrors() {
		return 1
	}
	return 0
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
			ut.Default = *rt.Default
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

func handleFmt(kwargs map[string]interface{}) int {
	target := kwargs["path"].(string)

	// Load config for format defaults.
	cfg := loadProjectConfig(target)

	// CLI flags override config; config overrides strictcli defaults.
	tableOrder := kwargs["table_order"].(string)
	if tableOrder == "dependency" && cfg.Format.TableOrder != "" {
		tableOrder = cfg.Format.TableOrder
	}
	columnOrder := kwargs["column_order"].(string)
	if columnOrder == "pk_fk_alpha" && cfg.Format.ColumnOrder != "" {
		columnOrder = cfg.Format.ColumnOrder
	}

	fmtConfig := &format.Config{
		TableOrder:  tableOrder,
		ColumnOrder: columnOrder,
	}

	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot stat %q: %v\n", target, err)
		return 1
	}

	if info.IsDir() {
		return fmtDir(target, fmtConfig, kwargs["check"].(bool))
	}
	return fmtFile(target, fmtConfig, kwargs["check"].(bool))
}

func fmtFile(filePath string, cfg *format.Config, checkOnly bool) int {
	input, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read file: %v\n", err)
		return 1
	}

	formatted, err := format.Format(input, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if checkOnly {
		if bytes.Equal(input, formatted) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "%s: not formatted\n", filePath)
		return 1
	}

	if err := os.WriteFile(filePath, formatted, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot write file: %v\n", err)
		return 1
	}
	return 0
}

func fmtDir(dirPath string, cfg *format.Config, checkOnly bool) int {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read directory: %v\n", err)
		return 1
	}

	exitCode := 0
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") || name == "pgdesign.toml" {
			continue
		}
		found = true
		code := fmtFile(filepath.Join(dirPath, name), cfg, checkOnly)
		if code != 0 {
			exitCode = code
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "error: no .toml schema files found in %q\n", dirPath)
		return 1
	}
	return exitCode
}

func handleIntrospect(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for introspect")
		return 1
	}

	// Load config for default schema names.
	cfg := loadProjectConfig(".")

	// Collect schema names from repeatable --schema flag.
	var schemaNames []string
	if raw, ok := kwargs["schema"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				schemaNames = append(schemaNames, s)
			}
		}
	}
	if len(schemaNames) == 0 {
		schemaNames = configSchemaNames(cfg)
	}
	if len(schemaNames) == 0 {
		schemaNames = []string{"public"}
	}

	ctx := context.Background()
	schema, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Print diagnostics (warnings/info) to stderr.
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	data, err := introspect.Export(schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: export failed: %v\n", err)
		return 1
	}

	// Write to file or stdout.
	if outputPath, ok := kwargs["output"].(string); ok && outputPath != "" {
		if err := os.WriteFile(outputPath, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
			return 1
		}
	} else {
		fmt.Print(string(data))
	}

	return 0
}

func handleDiff(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for default schema names.
	cfg := loadProjectConfig(paths[0])

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		// No --db: just validate the TOML (parse+build already succeeded).
		if !kwargs["quiet"].(bool) {
			fmt.Println("Schema is valid. Use --db to diff against a live database.")
		}
		return 0
	}

	// Introspect the live database. Use schema name from parsed schema first,
	// then fall back to config-derived schema names, then "public".
	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	d := diff.Diff(schema, actual)

	if kwargs["json"].(bool) {
		fmt.Println(diff.FormatJSON(d))
		return 0
	}

	fmt.Print(diff.FormatTerminal(d))
	if d.IsEmpty() {
		return 0
	}
	return 0
}

func handleMigratePlan(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for schema name defaults.
	cfg := loadProjectConfig(paths[0])

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate plan")
		return 1
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	d := diff.Diff(schema, actual)
	if d.IsEmpty() {
		if !kwargs["quiet"].(bool) {
			fmt.Println("No changes detected. Schema is up to date.")
		}
		return 0
	}

	// Query table stats for row estimates.
	var tableStats migrate.TableStats
	statsConn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot connect for table stats: %v\n", err)
	} else {
		for _, sn := range schemaNames {
			stats, err := migrate.QueryTableStats(ctx, statsConn, sn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot query table stats for %s: %v\n", sn, err)
			} else {
				if tableStats == nil {
					tableStats = stats
				} else {
					for k, v := range stats {
						tableStats[k] = v
					}
				}
			}
		}
		statsConn.Close(ctx)
	}

	m, migDiags := migrate.GenerateMigration(d, schema, "0.0.0", tableStats, cfg.Migrate.AutoConcurrentThreshold)

	// Print the plan.
	fmt.Println("Migration plan:")
	fmt.Printf("  Description: %s\n", m.Description)
	fmt.Println()

	for i, op := range m.DDLOps {
		sqlStmt := migrate.OpToSQL(op)
		fmt.Printf("  %d. [%s] %s\n", i+1, op.Op, opSummary(op))
		fmt.Printf("     SQL: %s\n", sqlStmt)
		if op.Down != nil {
			if op.Down.Irreversible {
				fmt.Println("     Down: IRREVERSIBLE")
			} else {
				fmt.Println("     Down: reversible")
			}
		}
		fmt.Println()
	}

	for i, op := range m.DMLOps {
		fmt.Printf("  DML %d. [%s]\n", i+1, op.Op)
		fmt.Printf("     SQL: %s\n", op.SQL)
		fmt.Println()
	}

	if len(migDiags) > 0 {
		fmt.Println("Diagnostics:")
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(migDiags, true))
	}

	return 0
}

func handleMigrateGenerate(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for migrations dir and schema name defaults.
	cfg := loadProjectConfig(paths[0])

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate generate")
		return 1
	}

	version, _ := kwargs["version"].(string)
	if version == "" {
		fmt.Fprintln(os.Stderr, "error: --version is required for migrate generate")
		return 1
	}

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = cfg.Project.MigrationsDir
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	d := diff.Diff(schema, actual)
	if d.IsEmpty() {
		fmt.Println("No changes detected. Nothing to generate.")
		return 0
	}

	// Query table stats for row estimates.
	var tableStats migrate.TableStats
	statsConn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot connect for table stats: %v\n", err)
	} else {
		for _, sn := range schemaNames {
			stats, err := migrate.QueryTableStats(ctx, statsConn, sn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot query table stats for %s: %v\n", sn, err)
			} else {
				if tableStats == nil {
					tableStats = stats
				} else {
					for k, v := range stats {
						tableStats[k] = v
					}
				}
			}
		}
		statsConn.Close(ctx)
	}

	m, migDiags := migrate.GenerateMigration(d, schema, version, tableStats, cfg.Migrate.AutoConcurrentThreshold)

	if len(migDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(migDiags, true))
	}

	// Ensure migrations directory exists.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: create migrations dir: %v\n", err)
		return 1
	}

	path := filepath.Join(dir, version+".toml")
	if err := migrate.WriteMigrationFile(path, m); err != nil {
		fmt.Fprintf(os.Stderr, "error: write migration: %v\n", err)
		return 1
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Generated migration: %s\n", path)
		fmt.Printf("  Description: %s\n", m.Description)
		fmt.Printf("  DDL ops: %d\n", len(m.DDLOps))
		fmt.Printf("  DML ops: %d\n", len(m.DMLOps))
	}

	return 0
}

func handleMigrateApply(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate apply")
		return 1
	}

	// Load config for migrations dir and lock timeout.
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = cfg.Project.MigrationsDir
	}

	dryRun := kwargs["dry_run"].(bool)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	if dryRun {
		return handleMigrateApplyDryRun(ctx, conn, dir, kwargs["quiet"].(bool))
	}

	lockTimeout := cfg.Migrate.LockTimeout

	applied, err := migrate.Apply(ctx, conn, dir, lockTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "Applied before failure: %v\n", applied)
		}
		return 1
	}

	if len(applied) == 0 {
		if !kwargs["quiet"].(bool) {
			fmt.Println("No pending migrations.")
		}
		return 0
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Applied %d migration(s):\n", len(applied))
		for _, v := range applied {
			fmt.Printf("  - %s\n", v)
		}
	}
	return 0
}

// handleMigrateApplyDryRun shows the SQL that would be executed without
// actually applying any migrations.
func handleMigrateApplyDryRun(ctx context.Context, conn *pgx.Conn, dir string, quiet bool) int {
	if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	applied, err := migrate.AppliedVersions(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	// Discover migration files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", err)
		return 1
	}

	type pendingMigration struct {
		version string
		path    string
	}
	var pending []pendingMigration
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		version := e.Name()[:len(e.Name())-5]
		if appliedSet[version] {
			continue
		}
		pending = append(pending, pendingMigration{
			version: version,
			path:    filepath.Join(dir, e.Name()),
		})
	}

	if len(pending) == 0 {
		if !quiet {
			fmt.Println("No pending migrations.")
		}
		return 0
	}

	for i, pm := range pending {
		m, err := migrate.ParseMigrationFile(pm.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: parse %s: %v\n", pm.path, err)
			return 1
		}

		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("-- Migration: %s\n", pm.version)
		if m.Description != "" {
			fmt.Printf("-- %s\n", m.Description)
		}

		for _, op := range m.DDLOps {
			sqlStmt := migrate.OpToSQL(op)
			if sqlStmt != "" {
				fmt.Println(sqlStmt)
			}
		}

		for _, op := range m.DMLOps {
			if op.SQL != "" {
				fmt.Println(op.SQL)
			}
		}
	}

	return 0
}

func handleMigrateRollback(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate rollback")
		return 1
	}

	// Load config for migrations dir and lock timeout.
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = cfg.Project.MigrationsDir
	}

	lockTimeout := cfg.Migrate.LockTimeout

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	version, err := migrate.Rollback(ctx, conn, dir, lockTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Rolled back: %s\n", version)
	}
	return 0
}

func handleMigrateStatus(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate status")
		return 1
	}

	// Load config for migrations dir.
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = cfg.Project.MigrationsDir
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	applied, err := migrate.AppliedVersions(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	// Discover migration files.
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", err)
		return 1
	}

	var allVersions []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		v := e.Name()[:len(e.Name())-5] // strip .toml
		allVersions = append(allVersions, v)
	}

	fmt.Printf("Applied migrations: %d\n", len(applied))
	for _, v := range applied {
		fmt.Printf("  [applied] %s\n", v)
	}

	pendingCount := 0
	for _, v := range allVersions {
		if !appliedSet[v] {
			fmt.Printf("  [pending] %s\n", v)
			pendingCount++
		}
	}

	if pendingCount == 0 && len(applied) > 0 {
		fmt.Println("All migrations applied.")
	} else if pendingCount > 0 {
		fmt.Printf("\n%d pending migration(s).\n", pendingCount)
	} else if len(applied) == 0 {
		fmt.Println("No migrations found or applied.")
	}

	return 0
}

func opSummary(op migrate.DDLOp) string {
	target := op.Table
	if op.Column != "" {
		target += "." + op.Column
	}
	if target == "" {
		target = op.Name
	}
	return target
}

func handleServe(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for serve")
		return 1
	}

	// Load config for default schema names.
	cfg := loadProjectConfig(".")

	port := kwargs["port"].(int)

	// Collect schema names from repeatable --schema flag.
	var schemaNames []string
	if raw, ok := kwargs["schema"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				schemaNames = append(schemaNames, s)
			}
		}
	}
	if len(schemaNames) == 0 {
		schemaNames = configSchemaNames(cfg)
	}
	if len(schemaNames) == 0 {
		schemaNames = []string{"public"}
	}

	srv, err := serve.New(dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer srv.Close()

	addr := fmt.Sprintf(":%d", port)
	if !kwargs["quiet"].(bool) {
		fmt.Printf("pgdesign serving on http://localhost:%d\n", port)
	}
	if err := srv.ListenAndServe(addr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
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

func handleExtensionDiscover(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for extension discover")
		return 1
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	// Query installed extensions, excluding plpgsql (always present).
	rows, err := conn.Query(ctx,
		"SELECT extname FROM pg_extension WHERE extname != 'plpgsql' ORDER BY extname")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: query extensions: %v\n", err)
		return 1
	}
	defer rows.Close()

	var extNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			fmt.Fprintf(os.Stderr, "error: scan extension: %v\n", err)
			return 1
		}
		extNames = append(extNames, name)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: iterate extensions: %v\n", err)
		return 1
	}

	if len(extNames) == 0 {
		if !kwargs["quiet"].(bool) {
			fmt.Println("# No extensions found (excluding plpgsql).")
		}
		return 0
	}

	for i, extName := range extNames {
		types, err := queryExtensionDeps(ctx, conn, extName,
			"SELECT t.typname FROM pg_type t JOIN pg_depend d ON d.objid = t.oid "+
				"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY t.typname")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: query types for %s: %v\n", extName, err)
			return 1
		}

		functions, err := queryExtensionDeps(ctx, conn, extName,
			"SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.objid = p.oid "+
				"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY p.proname")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: query functions for %s: %v\n", extName, err)
			return 1
		}

		opclasses, err := queryExtensionDeps(ctx, conn, extName,
			"SELECT o.opcname FROM pg_opclass o JOIN pg_depend d ON d.objid = o.oid "+
				"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY o.opcname")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: query opclasses for %s: %v\n", extName, err)
			return 1
		}

		if i > 0 {
			fmt.Println()
		}
		fmt.Println("[[extensions]]")
		fmt.Printf("name = %q\n", extName)
		if len(types) > 0 {
			fmt.Printf("types = [%s]\n", quotedList(types))
		}
		if len(opclasses) > 0 {
			fmt.Printf("opclasses = [%s]\n", quotedList(opclasses))
		}
		if len(functions) > 0 {
			fmt.Printf("functions = [%s]\n", quotedList(functions))
		}
	}

	return 0
}

// queryExtensionDeps runs a query that returns a single text column of names
// dependent on the given extension.
func queryExtensionDeps(ctx context.Context, conn *pgx.Conn, extName, query string) ([]string, error) {
	rows, err := conn.Query(ctx, query, extName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// quotedList formats a string slice as a TOML inline array body: "a", "b", "c".
func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

func notImplemented(_ map[string]interface{}) int {
	fmt.Println("not implemented")
	return 0
}
