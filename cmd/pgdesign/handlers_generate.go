package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/strictcli/go/strictcli"
)

type generateHandler struct {
	Idempotent bool     `cli:"idempotent" help:"Add IF NOT EXISTS guards to all generated DDL statements" default:"false"`
	Comments   bool     `cli:"comments" help:"Include COMMENT ON statements in the generated output" default:"true"`
	Format     string   `cli:"format" help:"Output format for the generated schema representation" default:"sql" choices:"sql,json,d2,svg,doc,graphql"`
	StrictNF   bool     `cli:"strict-nf" help:"Promote normal form violations to errors instead of warnings" default:"false"`
	Paths      []string `arg:"path" help:"Path to TOML schema file(s) or directory containing them" variadic:"true"`
}

func (h *generateHandler) Run(ctx *strictcli.Context) int {
	cfgOverride := configOverride(ctx)

	paths := h.Paths
	schema, typeReg, exitCode := parseAndBuild(cfgOverride, paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for PGVersion fallback.
	cfg, cfgErr := loadProjectConfig(cfgOverride, paths[0])
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return 1
	}

	if h.StrictNF {
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

	pgVersion, err := requirePGVersion(0, cfg.Database.PGVersion, schema.PGVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	schema.PGVersion = pgVersion

	// Validate schema before generating output.
	valDiags := validateSchema(schema, typeReg, cfg, pgVersion)
	if len(valDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(valDiags, true))
	}
	if diagnostic.Diagnostics(valDiags).HasErrors() {
		fmt.Fprintln(os.Stderr, "error: schema validation failed, refusing to generate")
		return 1
	}

	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))

	opts := generate.Options{
		Idempotent:      h.Idempotent,
		IncludeComments: h.Comments,
		Format:          h.Format,
		PGVersion:       pgVersion,
		TypeRegistry:    typeReg,
		ExtRegistry:     extReg,
	}

	out, genDiags, err := generate.Generate(schema, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		return 1
	}
	if len(genDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(genDiags, true))
	}
	fmt.Print(out)
	return 0
}
