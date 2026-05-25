package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/format"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/introspect"
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
			strictcli.StringFlag("format", "Output format", strictcli.Default("sql"), strictcli.Choices("sql", "json", "d2")),
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

	migrate := app.Group("migrate", "Database migration commands")
	migrate.Command("plan", "Plan migrations from schema changes", notImplemented)
	migrate.Command("generate", "Generate migration files", notImplemented)
	migrate.Command("apply", "Apply pending migrations", notImplemented)
	migrate.Command("rollback", "Rollback the last migration", notImplemented)
	migrate.Command("status", "Show migration status", notImplemented)

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

func notImplemented(_ map[string]interface{}) int {
	fmt.Println("not implemented")
	return 0
}
