package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerGenerateCmd(app *strictcli.App) {
	app.Command("generate", "Generate SQL DDL from TOML schema file(s) or directory",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			cfgOverride := kwargsConfigOverride(kwargs)

			paths := kwargsStrSlice(kwargs["path"])
			schema, typeReg, exitCode := parseAndBuild(cfgOverride, paths)
			if exitCode != 0 {
				return strictcli.Exit(exitCode)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, paths[0])
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			strictNF := kwargs["strict_nf"].(bool)
			if strictNF {
				diags := audit.Audit(schema)
				diags = promoteNFViolations(diags)
				if len(diags) > 0 {
					fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
				}
				if diagnostic.Diagnostics(diags).HasErrors() {
					fmt.Fprintln(os.Stderr, "error: --strict-nf: normal form violations found, refusing to generate DDL")
					return strictcli.Exit(1)
				}
			}

			pgVersion, err := requireSchemaPGVersion(schema)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			valDiags := validateSchema(schema, typeReg, cfg, pgVersion)
			if len(valDiags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(valDiags, true))
			}
			if diagnostic.Diagnostics(valDiags).HasErrors() {
				fmt.Fprintln(os.Stderr, "error: schema validation failed, refusing to generate")
				return strictcli.Exit(1)
			}

			extReg := extregistry.NewBuiltinRegistry()
			extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))

			opts := generate.Options{
				Idempotent:      kwargs["idempotent"].(bool),
				IncludeComments: kwargs["comments"].(bool),
				Format:          kwargs["format"].(string),
				TypeRegistry:    typeReg,
				ExtRegistry:     extReg,
				// The generate command operates on a TOML-built model, which
				// carries type information: registry-present class (L7).
				ModelClass: rev.RegistryPresent,
			}

			out, genDiags, genErr := generate.Generate(schema, opts)
			if genErr != nil {
				fmt.Fprintf(os.Stderr, "generate: %v\n", genErr)
				return strictcli.Exit(1)
			}
			if len(genDiags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(genDiags, true))
			}
			fmt.Print(out)
			return strictcli.Exit(0)
		},
		strictcli.WithEffect(strictcli.EffectReadOnly),
		strictcli.WithFlags(
			strictcli.BoolFlag("idempotent", "Add IF NOT EXISTS guards to all generated DDL statements", strictcli.Default(false)),
			strictcli.BoolFlag("comments", "Include COMMENT ON statements in the generated output", strictcli.Default(true)),
			strictcli.StringFlag("format", "Output format for the generated schema representation", strictcli.Default("sql"), strictcli.Choices("sql", "json", "d2", "svg", "doc", "graphql")),
			strictcli.BoolFlag("strict-nf", "Promote normal form violations to errors instead of warnings", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory containing them", strictcli.Variadic()),
		),
	)
}
