package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/pkg/genkit"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerCodegenCmd(app *strictcli.App) {
	app.Command("codegen", "Generate type-safe application code from schema definitions",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)
			return strictcli.Exit(runCodegen(cfgOverride, quiet, kwargs))
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil)),
			strictcli.StringFlag("lang", "Target programming language for the generated code", strictcli.Choices("python", "zig", "go", "ts", "java", "kotlin")),
			strictcli.StringFlag("mode", "Code generation mode determining what code to produce", strictcli.Default("validators"), strictcli.Choices(toIfaces(SupportedModeNames())...)),
			strictcli.StringFlag("output", "Write output to a file at this path instead of stdout", strictcli.Default(nil)),
			strictcli.StringFlag("split-mode", "Split Python DDL output mode", strictcli.Default(nil), strictcli.Choices("faceted", "self-contained")),
			strictcli.BoolFlag("check", "Verify generated code on disk is up to date without writing anything; requires --output, exits 1 on any missing, stale, or orphan file", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory containing them", strictcli.Variadic()),
		),
	)
}

// runCodegen contains the codegen logic; tests call it directly.
func runCodegen(configOverride *string, quiet bool, kwargs map[string]interface{}) int {
	paths := kwargsStrSlice(kwargs["path"])
	schema, typeReg, exitCode := parseAndBuild(configOverride, paths)
	if exitCode != 0 {
		return exitCode
	}

	cfg, cfgErr := loadProjectConfig(configOverride, paths[0])
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return 1
	}
	pgVersion := schema.PGVersion

	valDiags := validateSchema(schema, typeReg, cfg, pgVersion)
	if len(valDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(valDiags, true))
	}
	if diagnostic.Diagnostics(valDiags).HasErrors() {
		fmt.Fprintln(os.Stderr, "error: schema validation failed, refusing to generate code")
		return 1
	}

	lang := kwargs["lang"].(string)
	mode := kwargs["mode"].(string)
	splitMode := ""
	if v := kwargsOptString(kwargs, "split_mode"); v != nil {
		splitMode = *v
	}
	checkOnly := kwargs["check"].(bool)
	outputPath := ""
	if v := kwargsOptString(kwargs, "output"); v != nil {
		outputPath = *v
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
	if mfg, ok := gen.(genkit.MultiFileGenerator); ok {
		files, diags := mfg.GenerateFiles(schema)
		for _, d := range diags {
			fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
		}
		if checkOnly {
			planned := make(map[string][]byte, len(files))
			owned := make(map[string]bool, len(files))
			for relPath, data := range files {
				planned[filepath.Join(outputPath, relPath)] = data
				owned[filepath.ToSlash(filepath.Clean(relPath))] = true
			}
			return reportFreshness(planned, map[string]map[string]bool{outputPath: owned}, quiet)
		}
		if outputPath == "" {
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
