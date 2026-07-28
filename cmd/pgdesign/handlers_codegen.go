package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
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
			strictcli.StringFlag("lang", "Target programming language for the generated code", strictcli.Choices("python", "zig", "go", "ts", "java", "kotlin")),
			strictcli.StringFlag("mode", "Code generation mode determining what code to produce", strictcli.Default("validators"), strictcli.Choices(toIfaces(SupportedModeNames())...)),
			strictcli.StringFlag("output", "Write output to a file at this path instead of stdout", strictcli.Default(nil)),
			strictcli.StringFlag("split-mode", "How to split multi-file Python DDL output: 'faceted' writes one file per object kind, 'self-contained' emits a single importable module", strictcli.Default(nil), strictcli.Choices("faceted", "self-contained")),
			strictcli.ListFlag(strictcli.TypeStr, "groups", "Restrict generation to tables in these schema groups (matches build's per-output group filtering)", strictcli.Default(nil), strictcli.Unique(true)),
			strictcli.ListFlag(strictcli.TypeStr, "source", "Restrict generation to tables from these source file basenames (matches build's per-output source filtering)", strictcli.Default(nil), strictcli.Unique(true)),
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
	groups := kwargsStrSlice(kwargs["groups"])
	source := kwargsStrSlice(kwargs["source"])

	if checkOnly && outputPath == "" {
		fmt.Fprintln(os.Stderr, "error: --check requires --output (the path to verify against)")
		return 1
	}

	// Validate the lang/mode pair and the split-mode constraint up front so the
	// error messages are codegen-specific (the planner path re-selects the
	// generator internally).
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
		// Applied here for the stdout path; the planner applies it from
		// out.SplitMode for the --output path.
		ddlGen.SplitMode = codegen.SplitMode(splitMode)
	}

	// Apply the same group/source filtering build applies per output, so the
	// two entry points can never produce two contents for one artifact.
	outputSchema := applyOutputFilters(schema, groups, source)

	// stdout mode: no file artifact, so it stays outside the planner (which is
	// path-addressed). Filtering still applies for consistency.
	if outputPath == "" {
		return runCodegenStdout(gen, outputSchema)
	}

	// --output mode: route through the shared planner so filtering, headers,
	// and owned-dir/orphan bookkeeping are byte-for-byte identical to build.
	out := config.OutputConfig[config.AbsolutePath]{
		Format:    "codegen",
		Path:      config.AbsolutePath(outputPath),
		Lang:      lang,
		Mode:      mode,
		Groups:    groups,
		Source:    source,
		SplitMode: splitMode,
	}
	// Pass the UNFILTERED schema: PlanStandaloneCodegen filters content per
	// out.Groups/out.Source internally and computes the full-project revision
	// from the unfiltered model (roadmap 4.2), so a filtered output still carries
	// the full-project stamp.
	plan, err := PlanStandaloneCodegen(schema, out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, d := range plan.Diagnostics {
		fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
	}

	if checkOnly {
		return reportFreshness(plan.Files, plan.OwnedDirs, quiet)
	}

	// Orphan detection matches build: unexpected files in an owned directory
	// are a hard error and block writing (pgdesign never deletes them).
	orphans, orphanErr := scanAllOrphans(plan.OwnedDirs)
	if orphanErr != nil {
		fmt.Fprintf(os.Stderr, "error: orphan scan: %v\n", orphanErr)
		return 1
	}
	if len(orphans) > 0 {
		fmt.Fprintln(os.Stderr, "codegen: orphan file(s) found in owned output directory:")
		for _, p := range orphans {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintln(os.Stderr, orphanExplanation)
		fmt.Fprintln(os.Stderr, "error: refusing to write any outputs while orphans exist")
		return 1
	}

	// PARTIAL-WRITER REFUSAL (roadmap 6.2): codegen --output is the one partial
	// writer. Writing a single artifact while sibling [output] artifacts sit at a
	// different revision would leave the project in a mixed-revision tree, so
	// refuse and name the stale siblings. The check reuses the same per-output
	// machinery as `check --tag revision`.
	stale, staleErr := partialWriteStaleSiblings(configOverride, paths, outputPath, schema)
	if staleErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", staleErr)
		return 1
	}
	if len(stale) > 0 {
		fmt.Fprintln(os.Stderr, "codegen --output: refusing to write; sibling outputs are at a different revision:")
		for _, s := range stale {
			fmt.Fprintf(os.Stderr, "  %s\n", s)
		}
		fmt.Fprintln(os.Stderr, "error: run `pgdesign build` to regenerate all outputs at one revision")
		return 1
	}

	outPaths := make([]string, 0, len(plan.Files))
	for p := range plan.Files {
		outPaths = append(outPaths, p)
	}
	sort.Strings(outPaths)
	if _, err := writePlanFiles(outPaths, plan.Files, quiet); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runCodegenStdout writes generator output to stdout (no --output). It applies
// the same schema (already filtered) but stays outside the path-addressed
// planner. Multi-file generators print each file with a header banner.
func runCodegenStdout(gen genkit.Generator, schema *model.Schema) int {
	if mfg, ok := gen.(genkit.MultiFileGenerator); ok {
		files, diags := mfg.GenerateFiles(schema)
		for _, d := range diags {
			fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
		}
		for relPath, data := range files {
			fmt.Printf("==> %s <==\n%s\n", relPath, data)
		}
		return 0
	}
	out, diags := gen.Generate(schema)
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
	}
	fmt.Print(string(out))
	return 0
}
