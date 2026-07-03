package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/strictcli/go/strictcli"
)

type codegenHandler struct {
	DB        *string  `cli:"db" help:"PostgreSQL connection URL for the target database server"`
	Lang      string   `cli:"lang" help:"Target programming language for the generated code" choices:"python,zig,go,ts,java,kotlin"`
	Mode      string   `cli:"mode" help:"Code generation mode determining what code to produce" default:"validators" choices_from:"ModeChoices"`
	Output    *string  `cli:"output" help:"Write output to a file at this path instead of stdout"`
	SplitMode *string  `cli:"split-mode" help:"Split Python DDL output mode" choices:"faceted,self-contained"`
	Check     bool     `cli:"check" help:"Verify generated code on disk is up to date without writing anything; requires --output, exits 1 on any missing, stale, or orphan file" default:"false"`
	Paths     []string `arg:"path" help:"Path to TOML schema file(s) or directory containing them" variadic:"true"`
}

// ModeChoices supplies the valid --mode values for the choices_from tag; it is
// resolved once at registration time.
func (h *codegenHandler) ModeChoices() []string {
	return SupportedModeNames()
}

func (h *codegenHandler) Run(ctx *strictcli.Context) int {
	g := strictcli.Globals[Globals](ctx)
	return h.run(g.Config, g.Quiet)
}

// run contains the codegen logic; tests call it directly with explicit
// configOverride and quiet values instead of going through a CLI parse.
func (h *codegenHandler) run(configOverride *string, quiet bool) int {
	paths := h.Paths
	schema, typeReg, exitCode := parseAndBuild(configOverride, paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config and validate schema before generating code.
	cfg, cfgErr := loadProjectConfig(configOverride, paths[0])
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return 1
	}
	pgVersion := resolvePGVersion(0, cfg.Database.PGVersion, schema.PGVersion)
	schema.PGVersion = pgVersion

	valDiags := validateSchema(schema, typeReg, cfg, pgVersion)
	if len(valDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(valDiags, true))
	}
	if diagnostic.Diagnostics(valDiags).HasErrors() {
		fmt.Fprintln(os.Stderr, "error: schema validation failed, refusing to generate code")
		return 1
	}

	lang := h.Lang
	mode := h.Mode
	var splitMode string
	if h.SplitMode != nil {
		splitMode = *h.SplitMode
	}
	checkOnly := h.Check
	var outputPath string
	if h.Output != nil {
		outputPath = *h.Output
	}

	if checkOnly && outputPath == "" {
		fmt.Fprintln(os.Stderr, "error: --check requires --output (the path to verify against)")
		return 1
	}

	gen, err := SelectGenerator(lang, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if splitMode != "" {
		ddlGen, ok := gen.(*codegen.PythonDDLGenerator)
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --split-mode is only supported for Python DDL mode (--lang python --mode ddl)\n")
			return 1
		}
		ddlGen.SplitMode = codegen.SplitMode(splitMode)
	}

	// MultiFileGenerator: write files into output directory.
	if mfg, ok := gen.(codegen.MultiFileGenerator); ok {
		files, diags := mfg.GenerateFiles(schema)
		for _, d := range diags {
			fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
		}
		if checkOnly {
			// --check: compare each generated file against disk and orphan-scan
			// the output directory (same ownership rules as `pgdesign build`).
			// Writes nothing; exits 1 on any missing, stale, or orphan file.
			planned := make(map[string][]byte, len(files))
			owned := make(map[string]bool, len(files))
			for relPath, data := range files {
				planned[filepath.Join(outputPath, relPath)] = data
				owned[filepath.ToSlash(filepath.Clean(relPath))] = true
			}
			return reportFreshness(planned, map[string]map[string]bool{outputPath: owned}, quiet)
		}
		if outputPath == "" {
			// Without -o, print each file to stdout with a header.
			for relPath, data := range files {
				fmt.Printf("==> %s <==\n%s\n", relPath, data)
			}
			return 0
		}
		if err := os.MkdirAll(outputPath, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot create output directory: %v\n", err)
			return 1
		}
		for relPath, data := range files {
			fp := filepath.Join(outputPath, relPath)
			if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			if err := os.WriteFile(fp, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
				return 1
			}
			if !quiet {
				fmt.Fprintf(os.Stderr, "Generated %s (%d bytes)\n", fp, len(data))
			}
		}
		return 0
	}

	out, diags := gen.Generate(schema)
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
	}

	if checkOnly {
		// --check for single-file output: byte-exact comparison only. Plain
		// file paths get no directory ownership scanning (see freshness.go).
		return reportFreshness(map[string][]byte{outputPath: out}, nil, quiet)
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
			return 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "Generated %s (%d bytes)\n", outputPath, len(out))
		}
	} else {
		fmt.Print(string(out))
	}

	return 0
}
