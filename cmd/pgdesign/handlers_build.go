package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/livestats"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/pkg/genkit"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerBuildCmd(app *strictcli.App) {
	app.Command("build", "Generate all configured outputs from pgdesign.toml",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			return strictcli.Exit(runBuild(kwargsConfigOverride(kwargs), ctx.Quiet(), ctx.DryRun(), optBool(kwargs["auto_commit"], true), kwargsDBURL(kwargs)))
		},
		strictcli.WithEffect(strictcli.EffectMutating),
		strictcli.WithFlags(
			// build is mutating, so --auto-commit declares Optional() rather
			// than Default(true) (contract §27.1) and names its fallback in its
			// own help text. Omitting it still commits; --no-auto-commit still
			// leaves the outputs in the working tree.
			strictcli.BoolFlag("auto-commit", "Automatically git commit the generated output files after a successful build; omitted means they are committed, and --no-auto-commit leaves them in the working tree", strictcli.Optional()),
			strictcli.StringFlag("db", "PostgreSQL connection URL; required only when a [output.<name>.d2] sets live_stats=true", strictcli.Optional(), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
		),
	)
}

// runBuild is the typed entry point for the build command; tests call it
// directly to exercise the build flow without a CLI parse. configOverride is
// the --project-config global flag: when set, it names the exact pgdesign.toml to use
// instead of the walk-up search.
func runBuild(configOverride *string, quiet, dryRun, autoCommit bool, dbURL string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	configPath, found, err := resolveConfigPath(configOverride, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
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

	// live_stats is an explicit opt-in with no implicit DB dependency: build only
	// touches a database when some [output.<name>.d2] sets live_stats=true. When
	// it does, a database is mandatory — an absent --db/PGDESIGN_DB is a hard
	// error naming the requirement, never a silent fallback to no stats.
	needLive := configNeedsLiveStats(cfg)
	if needLive && dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: an [output.*.d2] sets live_stats=true, which requires a live database; pass --db or set PGDESIGN_DB")
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

	schema, typeReg, exitCode := parseAndBuild(configOverride, schemaPaths)
	if exitCode != 0 {
		return exitCode
	}

	pgVersion, err := requireSchemaPGVersion(schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

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
	plan, planErr := Plan(schema, cfg, typeReg)
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

	// Orphan detection: files on disk inside owned multi-file codegen output
	// directories that the current configuration does not produce.
	orphans, orphanErr := scanAllOrphans(plan.OwnedDirs)
	if orphanErr != nil {
		fmt.Fprintf(os.Stderr, "build: orphan scan: %v\n", orphanErr)
		return 1
	}

	if dryRun {
		return handleBuildDryRun(paths, plan, orphans, quiet)
	}

	// Orphans are a hard error and block the build BEFORE anything is written:
	// with unexpected files inside an owned output directory the desired tree
	// state is ambiguous, so the consumer must resolve the orphans first.
	// pgdesign never deletes the orphans itself.
	if len(orphans) > 0 {
		fmt.Fprintln(os.Stderr, "build: orphan file(s) found in owned output directories:")
		for _, p := range orphans {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintln(os.Stderr, orphanExplanation)
		fmt.Fprintln(os.Stderr, "error: refusing to write any outputs while orphans exist")
		return 1
	}

	// Fetch live table statistics once when any output opts in via live_stats.
	// The DB requirement was already enforced above, so needLive implies dbURL.
	var liveStats map[string]generate.TableStats
	if needLive {
		ls, code := fetchBuildLiveStats(dbURL, schema)
		if code != 0 {
			return code
		}
		liveStats = ls
	}

	// Handle non-deterministic outputs excluded from Plan: SVG (unstable layout)
	// and any live_stats d2 (time-varying row counts). Both are generated and
	// written here, carrying the fetched live stats when configured.
	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))
	svgFiles, svgExit := handleBuildLiveOutputs(cfg, schema, typeReg, extReg, liveStats, quiet)
	if svgExit != 0 {
		return svgExit
	}

	// Write all planned files to disk.
	writtenFiles, err := writePlanFiles(paths, plan.Files, quiet)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		return 1
	}
	writtenFiles = append(writtenFiles, svgFiles...)

	if autoCommit && len(writtenFiles) > 0 {
		// Commit failure is a HARD ERROR (roadmap 6.1): warn-and-continue would
		// leave regenerated outputs on disk but uncommitted, silently diverging
		// the repo from the revision they claim.
		if err := safegitCommit("pgdesign build: regenerate outputs", writtenFiles); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	// Print the whole-model revision: the content identity of the model these
	// outputs were generated from. build operates on a TOML-built model, so it
	// is registry-present (L7).
	if r, err := rev.Compute(schema, rev.RegistryPresent); err == nil && !quiet {
		fmt.Fprintf(os.Stderr, "revision %s\n", r)
	}

	return 0
}

// writePlanFiles writes each planned file (in the given path order) to disk,
// creating parent directories as needed. It is the single multi-file write path
// shared by `build` and the standalone `codegen` command. Returns the list of
// written paths (for downstream auto-commit).
func writePlanFiles(paths []string, files map[string][]byte, quiet bool) ([]string, error) {
	var written []string
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(p, files[p], 0o644); err != nil {
			return written, err
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", p, len(files[p]))
		}
		written = append(written, p)
	}
	return written, nil
}

// handleBuildDryRun compares planned files against disk and reports per-file
// status. Missing and stale files keep exit code 0 (previewing what a build
// would write is the point of --dry-run), but orphans exit 1: they are a hard
// error that would also block the real build.
func handleBuildDryRun(paths []string, plan *PlanResult, orphans []string, quiet bool) int {
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
	for _, p := range orphans {
		fmt.Fprintf(os.Stderr, "[orphan]  %s\n", p)
	}
	if len(orphans) > 0 {
		fmt.Fprintln(os.Stderr, orphanExplanation)
	}
	fmt.Fprintf(os.Stderr, "\n%d file(s): %d missing, %d stale, %d fresh; %d orphan(s)\n", missing+stale+fresh, missing, stale, fresh, len(orphans))
	if len(orphans) > 0 {
		return 1
	}
	return 0
}

// configNeedsLiveStats reports whether any [output.<name>.d2] subsection opts
// into live statistics. It is the single predicate that decides whether build
// touches a database at all — no database is opened unless this returns true.
func configNeedsLiveStats(cfg *config.ResolvedConfig) bool {
	for _, out := range cfg.Output {
		if out.D2 != nil && out.D2.LiveStats {
			return true
		}
	}
	return false
}

// schemaNamesOf returns the distinct PostgreSQL schema names the model's tables
// live in, defaulting to "public" when none are present. It scopes the
// pg_stat_user_tables query to exactly the served schemas.
func schemaNamesOf(schema *model.Schema) []string {
	seen := map[string]bool{}
	var names []string
	for _, t := range schema.Tables {
		s := t.Schema
		if s == "" {
			s = "public"
		}
		if !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		names = []string{"public"}
	}
	return names
}

// fetchBuildLiveStats opens a short-lived connection and reads live table
// statistics for the model's schemas. It is only ever called when some output
// opted into live_stats and a database URL was supplied.
func fetchBuildLiveStats(dbURL string, schema *model.Schema) (map[string]generate.TableStats, int) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build: connect for live_stats: %v\n", err)
		return nil, 1
	}
	defer conn.Close(ctx)
	stats, err := livestats.Fetch(ctx, conn, schemaNamesOf(schema))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build: fetch live_stats: %v\n", err)
		return nil, 1
	}
	return stats, 0
}

// handleBuildLiveOutputs generates the non-deterministic outputs excluded from
// Plan: SVG (unstable layout coordinates) and any d2 output with live_stats=true
// (time-varying row counts). Both are written during non-dry-run builds. When an
// output opts into live_stats, the caller-fetched liveStats map is injected so
// the diagram carries the live annotations.
func handleBuildLiveOutputs(cfg *config.ResolvedConfig, schema *model.Schema, typeReg *semtype.Registry, extReg *extregistry.Registry, liveStats map[string]generate.TableStats, quiet bool) ([]string, int) {
	// Stamp the provenance banner (d2 outputs carry it, matching Plan's d2 path).
	// Reset after so no later generator call inherits it.
	if projectRev, err := rev.Compute(schema, rev.RegistryPresent); err == nil {
		if serr := genkit.SetRevision(projectRev.String()); serr == nil {
			defer genkit.SetRevision("")
		}
	}

	var written []string
	for name, out := range cfg.Output {
		isSVG := out.Format == "svg"
		isLiveD2 := out.Format == "d2" && out.D2 != nil && out.D2.LiveStats
		if !isSVG && !isLiveD2 {
			continue
		}

		outputSchema := applyOutputFilters(schema, out.Groups, out.Source)

		d2opts := d2OptionsFromConfig(out.D2, liveStats)
		result, genDiags, err := generate.Generate(outputSchema, generate.Options{
			Format:       out.Format,
			TypeRegistry: typeReg,
			ExtRegistry:  extReg,
			ModelClass:   rev.RegistryPresent,
			D2:           &d2opts,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "build: output %q: %v\n", name, err)
			return nil, 1
		}
		if len(genDiags) > 0 {
			fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(genDiags, true))
		}

		// d2 text carries the hash-comment provenance banner (as Plan does);
		// svg is raw XML written verbatim.
		content := []byte(result)
		if out.Format == "d2" {
			content = []byte(genkit.Header(genkit.CommentHash) + "\n" + result)
		}

		outPath := string(out.Path)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "build: output %q: %v\n", name, err)
			return nil, 1
		}
		if err := os.WriteFile(outPath, content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "build: output %q: %v\n", name, err)
			return nil, 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", outPath, len(content))
		}
		written = append(written, outPath)
	}
	return written, 0
}

// selectCodegenGenerator selects the appropriate genkit.Generator for the given
// lang and mode. Returns the generator and true on success, or prints an error
// and returns false if the combination is unsupported.
func selectCodegenGenerator(outputName, lang, mode string) (genkit.Generator, bool) {
	gen, err := SelectGenerator(lang, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: output %q: %v\n", outputName, err)
		return nil, false
	}
	return gen, true
}

// codegenHeader returns the generated-file provenance header for the given
// language, routed through the single genkit stamp writer. An empty string is
// returned for languages with no known comment prefix.
func codegenHeader(lang string) string {
	switch lang {
	case "python":
		return genkit.Header(genkit.CommentHash) + "\n"
	case "go", "zig", "ts", "java", "kotlin":
		return genkit.Header(genkit.CommentSlash) + "\n"
	default:
		return ""
	}
}

// hasCommentHeader reports whether the generated output already carries the
// canonical provenance banner (under any comment prefix, including --),
// indicating the generator manages its own header.
func hasCommentHeader(data []byte) bool {
	return genkit.HasStamp(data)
}
