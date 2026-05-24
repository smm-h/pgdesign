package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
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

	app.Command("fmt", "Format a pgdesign schema file or directory", notImplemented,
		strictcli.WithArgs(strictcli.NewArg("path", "Path to file or directory")),
	)

	app.Command("introspect", "Introspect a live PostgreSQL database", notImplemented)

	app.Command("diff", "Diff a schema file against a live database", notImplemented,
		strictcli.WithArgs(strictcli.NewArg("file", "Path to schema file")),
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

func notImplemented(_ map[string]interface{}) int {
	fmt.Println("not implemented")
	return 0
}
