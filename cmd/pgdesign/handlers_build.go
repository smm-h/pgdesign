package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
)

func handleBuild(kwargs map[string]interface{}) int {
	quiet := kwargs["quiet"].(bool)
	dryRun := kwargs["dry_run"].(bool)
	noCommit := kwargs["no_commit"].(bool)

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	configPath, found := config.FindConfig(cwd)
	if !found {
		fmt.Fprintln(os.Stderr, "error: pgdesign.toml not found in current directory or any ancestor")
		return 1
	}

	cfg, err := config.LoadAndResolve(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if len(cfg.Output) == 0 {
		fmt.Fprintln(os.Stderr, "error: no [output] section in pgdesign.toml")
		return 1
	}

	// Resolve schema paths.
	var schemaPaths []string
	if len(cfg.Project.Schemas) > 0 {
		schemaPaths = cfg.SchemaFiles()
	} else {
		schemaPaths, err = resolveFromConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	schema, typeReg, exitCode := parseAndBuild(schemaPaths)
	if exitCode != 0 {
		return exitCode
	}

	pgVersion, err := requirePGVersion(0, cfg.Database.PGVersion, schema.PGVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	schema.PGVersion = pgVersion

	// Validate schema before generating outputs.
	valDiags := validateSchema(schema, typeReg, cfg, pgVersion)
	if len(valDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(valDiags, true))
	}
	if diagnostic.Diagnostics(valDiags).HasErrors() {
		fmt.Fprintln(os.Stderr, "error: schema validation failed, refusing to build")
		return 1
	}

	// Generate all outputs in memory.
	plan, planErr := Plan(schema, cfg, typeReg, pgVersion)
	if planErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", planErr)
		return 1
	}

	// Print any diagnostics collected during planning.
	if len(plan.Diagnostics) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(plan.Diagnostics, true))
	}

	// Sort file paths for deterministic output ordering.
	paths := make([]string, 0, len(plan.Files))
	for p := range plan.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	if dryRun {
		return handleBuildDryRun(paths, plan, quiet)
	}

	// Handle SVG outputs that were excluded from Plan (non-deterministic rendering).
	svgFiles, svgExit := handleBuildSVG(cfg, schema, typeReg, pgVersion, quiet)
	if svgExit != 0 {
		return svgExit
	}

	// Write all planned files to disk.
	var writtenFiles []string
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n", err)
			return 1
		}
		if err := os.WriteFile(p, plan.Files[p], 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n", err)
			return 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", p, len(plan.Files[p]))
		}
		writtenFiles = append(writtenFiles, p)
	}
	writtenFiles = append(writtenFiles, svgFiles...)

	if !noCommit && len(writtenFiles) > 0 {
		args := []string{"commit", "-m", "pgdesign build: regenerate outputs", "--"}
		args = append(args, writtenFiles...)
		cmd := exec.Command("safegit", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: safegit commit failed: %v\n", err)
		}
	}

	return 0
}

// handleBuildDryRun compares planned files against disk and reports per-file status.
func handleBuildDryRun(paths []string, plan *PlanResult, quiet bool) int {
	var missing, stale, fresh int
	for _, p := range paths {
		existing, err := os.ReadFile(p)
		if err != nil {
			// File does not exist on disk.
			fmt.Fprintf(os.Stderr, "[missing] %s\n", p)
			missing++
		} else if !bytes.Equal(existing, plan.Files[p]) {
			fmt.Fprintf(os.Stderr, "[stale]   %s\n", p)
			stale++
		} else {
			if !quiet {
				fmt.Fprintf(os.Stderr, "[fresh]   %s\n", p)
			}
			fresh++
		}
	}
	fmt.Fprintf(os.Stderr, "\n%d file(s): %d missing, %d stale, %d fresh\n", missing+stale+fresh, missing, stale, fresh)
	return 0
}

// handleBuildSVG generates SVG outputs that are excluded from Plan due to
// non-deterministic d2 rendering. These are still written during non-dry-run builds.
func handleBuildSVG(cfg *config.ResolvedConfig, schema *model.Schema, typeReg *semtype.Registry, pgVersion int, quiet bool) ([]string, int) {
	var written []string
	for name, out := range cfg.Output {
		if out.Format != "svg" {
			continue
		}

		outputSchema := schema
		if len(out.Groups) > 0 {
			outputSchema = schema.FilterByGroups(out.Groups)
		}

		result, genDiags, err := generate.Generate(outputSchema, generate.Options{
			Format:       "svg",
			PGVersion:    pgVersion,
			TypeRegistry: typeReg,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "build: output %q: %v\n", name, err)
			return nil, 1
		}
		if len(genDiags) > 0 {
			fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(genDiags, true))
		}

		outPath := string(out.Path)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "build: output %q: %v\n", name, err)
			return nil, 1
		}
		if err := os.WriteFile(outPath, []byte(result), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "build: output %q: %v\n", name, err)
			return nil, 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", outPath, len(result))
		}
		written = append(written, outPath)
	}
	return written, 0
}

// selectCodegenGenerator selects the appropriate codegen.Generator for the given
// lang and mode. Returns the generator and true on success, or prints an error
// and returns false if the combination is unsupported.
func selectCodegenGenerator(outputName, lang, mode string) (codegen.Generator, bool) {
	gen, err := SelectGenerator(lang, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: output %q: %v\n", outputName, err)
		return nil, false
	}
	return gen, true
}

// codegenHeader returns the generated-file header comment for the given language.
func codegenHeader(lang string) string {
	switch lang {
	case "python":
		return "# Generated by pgdesign -- do not edit\n\n"
	case "go", "zig", "ts", "java", "kotlin":
		return "// Generated by pgdesign -- do not edit\n\n"
	default:
		return ""
	}
}

// hasCommentHeader reports whether the generated output already starts with
// a comment marker (// or #), indicating the generator manages its own header.
func hasCommentHeader(data []byte) bool {
	s := strings.TrimLeft(string(data), " \t\n\r")
	return strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#")
}
