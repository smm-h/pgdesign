package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/format"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/validate"
	"github.com/smm-h/strictcli/go/strictcli"
)

func main() {
	app := strictcli.NewApp("pgdesign", Version, "PostgreSQL schema compiler")

	app.GlobalFlag(strictcli.BoolFlag("quiet", "Suppress non-error output"))
	app.GlobalFlag(strictcli.StringFlag("db", "PostgreSQL connection URL", strictcli.Default(nil)))
	app.GlobalFlag(strictcli.BoolFlag("strict-nf", "Enable strict normal form checking"))

	app.Command("generate", "Generate SQL from a pgdesign schema file", handleGenerate,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
		strictcli.WithFlags(
			strictcli.BoolFlag("idempotent", "Add IF NOT EXISTS guards to all statements"),
			strictcli.BoolFlag("no-comments", "Exclude COMMENT ON statements from output"),
			strictcli.StringFlag("format", "Output format", strictcli.Default("sql"), strictcli.Choices("sql", "json", "d2", "svg")),
		),
	)

	app.Command("validate", "Validate a pgdesign schema file", handleValidate,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
	)

	app.Command("audit", "Audit a pgdesign schema file for issues", handleAudit,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
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

	app.Command("diff", "Diff a schema file against a live database", handleDiff,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
		strictcli.WithFlags(
			strictcli.BoolFlag("json", "Output diff as JSON"),
		),
	)

	mig := app.Group("migrate", "Database migration commands")
	mig.Command("plan", "Plan migrations from schema changes", handleMigratePlan,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
	)
	mig.Command("generate", "Generate migration files", handleMigrateGenerate,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
		strictcli.WithFlags(
			strictcli.StringFlag("version", "Migration version (semver)", strictcli.Default(nil)),
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
		),
	)
	mig.Command("apply", "Apply pending migrations", handleMigrateApply,
		strictcli.WithFlags(
			strictcli.StringFlag("dir", "Migrations directory", strictcli.Default("migrations")),
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

	app.Command("serve", "Start the pgdesign language server", notImplemented)

	app.Run()
}

func handleGenerate(kwargs map[string]interface{}) int {
	filePath := kwargs["file"].(string)

	raw, parseDiags := parse.File(filePath)
	if raw == nil {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(parseDiags, true))
		return 1
	}

	reg := semtype.NewBuiltinRegistry()

	// Load user-defined types from the schema into the registry.
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
	if len(userTypes) > 0 {
		loadDiags := reg.LoadUserTypes(userTypes)
		if loadDiags.HasErrors() {
			fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(loadDiags, true))
			return 1
		}
	}

	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(buildDiags, true))
		return 1
	}

	// Print warnings to stderr but continue.
	warnings := buildDiags.Warnings()
	if len(warnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(warnings, true))
	}

	opts := generate.Options{
		Idempotent:      kwargs["idempotent"].(bool),
		IncludeComments: !kwargs["no_comments"].(bool),
		Format:          kwargs["format"].(string),
		PGVersion:       schema.PGVersion,
	}

	out := generate.Generate(schema, opts)
	fmt.Print(out)
	return 0
}

func handleValidate(kwargs map[string]interface{}) int {
	filePath := kwargs["file"].(string)
	schema, exitCode := parseAndBuild(filePath)
	if exitCode != 0 {
		return exitCode
	}

	config := &validate.Config{
		NamingPattern: "snake_case",
		MaxColumns:    30,
		Extensions:    schema.Extensions,
		ExtRegistry:   extregistry.NewBuiltinRegistry(),
	}

	diags := validate.Validate(schema, config)
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}
	return 0
}

func handleAudit(kwargs map[string]interface{}) int {
	filePath := kwargs["file"].(string)
	schema, exitCode := parseAndBuild(filePath)
	if exitCode != 0 {
		return exitCode
	}

	diags := audit.Audit(schema)
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}
	return 0
}

// parseAndBuild is a shared helper for commands that need a resolved schema.
func parseAndBuild(filePath string) (*model.Schema, int) {
	raw, parseDiags := parse.File(filePath)
	if raw == nil {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(parseDiags, true))
		return nil, 1
	}

	reg := semtype.NewBuiltinRegistry()

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
	if len(userTypes) > 0 {
		loadDiags := reg.LoadUserTypes(userTypes)
		if loadDiags.HasErrors() {
			fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(loadDiags, true))
			return nil, 1
		}
	}

	schema, buildDiags := model.Build(raw, reg)
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
	filePath := kwargs["path"].(string)

	input, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read file: %v\n", err)
		return 1
	}

	config := &format.Config{
		TableOrder:  kwargs["table_order"].(string),
		ColumnOrder: kwargs["column_order"].(string),
	}

	formatted, err := format.Format(input, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if kwargs["check"].(bool) {
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

func handleIntrospect(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for introspect")
		return 1
	}

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
		schemaNames = []string{"public"}
	}

	schema, diags, err := introspect.Introspect(dbURL, schemaNames)
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
	filePath := kwargs["file"].(string)
	schema, exitCode := parseAndBuild(filePath)
	if exitCode != 0 {
		return exitCode
	}

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		// No --db: just validate the TOML (parse+build already succeeded).
		if !kwargs["quiet"].(bool) {
			fmt.Println("Schema is valid. Use --db to diff against a live database.")
		}
		return 0
	}

	// Introspect the live database.
	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	}

	actual, diags, err := introspect.Introspect(dbURL, schemaNames)
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
	filePath := kwargs["file"].(string)
	schema, exitCode := parseAndBuild(filePath)
	if exitCode != 0 {
		return exitCode
	}

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate plan")
		return 1
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	}

	actual, diags, err := introspect.Introspect(dbURL, schemaNames)
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

	m, migDiags := migrate.GenerateMigration(d, schema, "0.0.0")

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
	filePath := kwargs["file"].(string)
	schema, exitCode := parseAndBuild(filePath)
	if exitCode != 0 {
		return exitCode
	}

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

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	}

	actual, diags, err := introspect.Introspect(dbURL, schemaNames)
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

	m, migDiags := migrate.GenerateMigration(d, schema, version)

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

	dir := kwargs["dir"].(string)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	applied, err := migrate.Apply(ctx, conn, dir)
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

func handleMigrateRollback(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate rollback")
		return 1
	}

	dir := kwargs["dir"].(string)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	version, err := migrate.Rollback(ctx, conn, dir)
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

	dir := kwargs["dir"].(string)

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

func notImplemented(_ map[string]interface{}) int {
	fmt.Println("not implemented")
	return 0
}
