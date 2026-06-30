package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/generate"
)

func handleGenerate(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, typeReg, exitCode := parseAndBuild(paths)
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

	opts := generate.Options{
		Idempotent:      kwargs["idempotent"].(bool),
		IncludeComments: !kwargs["no_comments"].(bool),
		Format:          kwargs["format"].(string),
		PGVersion:       pgVersion,
		TypeRegistry:    typeReg,
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
