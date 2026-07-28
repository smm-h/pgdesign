package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/workload"
	"github.com/smm-h/strictcli/go/strictcli"
)

// Exit codes for `revise`. A hard pure-tier failure aborts before any commit; a
// DB-tier skip is distinct because the pure tier (outputs + migration) is ALREADY
// committed — the caller must be able to tell "nothing shipped" from "pure work
// shipped, live analyses did not run".
const (
	reviseExitOK          = 0 // pure tier + DB tier both completed
	reviseExitPureFailure = 1 // pure tier failed; nothing committed
	reviseExitDBSkipped   = 2 // pure tier committed; DB tier skipped/unreachable
)

func registerReviseCmd(app *strictcli.App) {
	app.Command("revise", "Regenerate all outputs, chain the migration, and commit — the one-command project revision. Runs the PURE tier (build outputs, chain-mode migration, blocking normal-form and structural checks) and commits it, then runs the non-retroactive DB tier (live FD discovery, pg_stat workload).",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			return strictcli.Exit(runRevise(kwargsConfigOverride(kwargs), kwargsQuiet(kwargs), kwargsOptString(kwargs, "dir"), kwargsDBURL(kwargs)))
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the non-retroactive DB tier (live FD discovery, pg_stat workload). When absent, the DB tier is skipped and revise exits non-zero after committing the pure tier.", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing the chain-format migrations project (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
		),
	)
}

// runRevise is the typed entry point for `revise`; tests call it directly. It is
// the L5+L6 one-command revision (roadmap 6.1):
//
//	PURE TIER (all BLOCKING, committed atomically-in-two-commits):
//	  1. parse + build + validate the schema (blocks on errors)
//	  2. pure checks: NF audit core (BCNF and lower BLOCK) + structural workload
//	     (advisory warnings) — the checkNF split makes "pure tier" true
//	  3. build planner (Plan) → outputs in memory, orphan-checked
//	  4. chain-mode generation (GenerateEdge from head — pure, no DB)
//	  5. commit 1 = pure outputs; commit 2 = migration + chain + store
//	     (ONE shared commit helper; commit failure is a HARD ERROR)
//
//	DB TIER (phase-2 env; NON-RETROACTIVE): live FD discovery + pg_stat workload
//	+ the 7.4 live-import-verification seam. Unreachable/unconfigured → the pure
//	tier stays committed and revise exits non-zero naming the skipped tier.
//
// The funnels are strictly SEQUENTIAL: Plan is the only genkit stamp funnel
// revise runs, and genkit.SetRevision now mechanically asserts non-reentrancy
// (roadmap 4.2 obligation), so an accidental overlap is a hard error, not silent
// stamp corruption.
func runRevise(configOverride *string, quiet bool, dirFlag *string, dbURL string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return reviseExitPureFailure
	}

	configPath, found, err := resolveConfigPath(configOverride, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return reviseExitPureFailure
	}
	if !found {
		fmt.Fprintln(os.Stderr, "error: pgdesign.toml not found in current directory or any ancestor")
		return reviseExitPureFailure
	}

	cfg, err := config.LoadAndResolve(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return reviseExitPureFailure
	}
	if len(cfg.Output) == 0 {
		fmt.Fprintln(os.Stderr, "error: no [output] section in pgdesign.toml")
		return reviseExitPureFailure
	}

	projectRoot := filepath.Dir(configPath)

	// Resolve schema paths (config schemas, else directory search).
	var schemaPaths []string
	if len(cfg.Project.Schemas) > 0 {
		schemaPaths = cfg.SchemaFiles()
	} else {
		schemaPaths, err = resolveFromConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return reviseExitPureFailure
		}
	}

	schema, typeReg, exitCode := parseAndBuild(configOverride, schemaPaths)
	if exitCode != 0 {
		return reviseExitPureFailure
	}

	pgVersion, err := requireSchemaPGVersion(schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return reviseExitPureFailure
	}

	// ---- PURE TIER ----

	// Validate the schema first: Plan and chain generation both require a valid
	// model, and validation errors block the whole pure tier.
	valDiags := validateSchema(schema, typeReg, cfg, pgVersion)
	if len(valDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(valDiags, true))
	}
	if diagnostic.Diagnostics(valDiags).HasErrors() {
		fmt.Fprintln(os.Stderr, "error: schema validation failed, refusing to revise")
		return reviseExitPureFailure
	}

	// Pure checks. NF violations (1NF..BCNF) BLOCK the pure tier (roadmap 6.1
	// [%%]: pure analyses that can block must block). Structural workload
	// findings are advisory (surfaced, non-blocking).
	nfDiags, nfBlocks := pureNFGate(schema)
	if len(nfDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(nfDiags, true))
	}
	if nfBlocks {
		fmt.Fprintln(os.Stderr, "error: normal form violations found, refusing to revise")
		return reviseExitPureFailure
	}
	if structDiags := pureStructuralChecks(schema); len(structDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(structDiags, true))
	}

	// Migrations directory + legacy-project guard. revise is chain-mode only: an
	// absent/empty migrations dir becomes genesis (chain created below), but a
	// legacy semver-TOML project is a hard error pointing at `migrate upgrade`.
	dir := resolveMigrationsDir(dirFlag, string(cfg.Project.MigrationsDir))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(projectRoot, dir)
	}
	legacy, err := isLegacyMigrationsDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: inspect migrations dir: %v\n", err)
		return reviseExitPureFailure
	}
	if legacy {
		fmt.Fprintf(os.Stderr, "error: %q is a legacy semver-migration project, not a chain-format project; revise operates only on chain-format projects. Run `pgdesign migrate upgrade` to convert it first.\n", dir)
		return reviseExitPureFailure
	}

	// Build planner: generate all outputs in memory (the genkit stamp funnel).
	plan, planErr := Plan(schema, cfg, typeReg)
	if planErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", planErr)
		return reviseExitPureFailure
	}
	if len(plan.Diagnostics) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(plan.Diagnostics, true))
	}

	paths := make([]string, 0, len(plan.Files))
	for p := range plan.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// Orphan detection: like build, a hard error BEFORE anything is written.
	orphans, orphanErr := scanAllOrphans(plan.OwnedDirs)
	if orphanErr != nil {
		fmt.Fprintf(os.Stderr, "error: orphan scan: %v\n", orphanErr)
		return reviseExitPureFailure
	}
	if len(orphans) > 0 {
		fmt.Fprintln(os.Stderr, "revise: orphan file(s) found in owned output directories:")
		for _, p := range orphans {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintln(os.Stderr, orphanExplanation)
		fmt.Fprintln(os.Stderr, "error: refusing to revise while orphans exist")
		return reviseExitPureFailure
	}

	// Chain-mode generation (pure). This writes the edge + objects + to-revision
	// manifest to disk. It runs BEFORE build outputs are written so a fork or
	// rename-gate failure aborts the pure tier without leaving build outputs
	// dangling. A genesis edge is produced for a fresh/empty project.
	chainRes, chainErr := generateChainEdge(schema, dir, configRenameSpec(cfg))
	if len(chainRes.Diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(chainRes.Diags, true))
	}
	if chainErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", chainErr)
		return reviseExitPureFailure
	}

	// Write build outputs, plus SVGs (excluded from Plan for non-determinism).
	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))
	writtenFiles, err := writePlanFiles(paths, plan.Files, quiet)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return reviseExitPureFailure
	}
	svgFiles, svgExit := handleBuildSVG(cfg, schema, typeReg, extReg, pgVersion, quiet)
	if svgExit != 0 {
		return reviseExitPureFailure
	}
	writtenFiles = append(writtenFiles, svgFiles...)

	// COMMIT 1: pure outputs. Commit failure is a HARD ERROR.
	if err := safegitCommit("pgdesign revise: regenerate outputs", writtenFiles); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return reviseExitPureFailure
	}

	// COMMIT 2: migration + chain + store. Skipped only when the diff produced no
	// edge (nothing changed in the schema since the head).
	if !chainRes.NoChanges {
		migPaths := chainStorePaths(dir)
		msg := "pgdesign revise: chain migration"
		if chainRes.EdgeName != "" {
			msg = "pgdesign revise: chain migration " + chainRes.EdgeName
		}
		if err := safegitCommit(msg, migPaths); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return reviseExitPureFailure
		}
	}

	if !quiet {
		if r, rErr := rev.Compute(schema, rev.RegistryPresent); rErr == nil {
			fmt.Fprintf(os.Stderr, "revision %s\n", r)
		}
		if chainRes.NoChanges {
			fmt.Fprintln(os.Stderr, "revise: no schema change since chain head; outputs regenerated, no migration edge written")
		} else {
			fmt.Fprintf(os.Stderr, "revise: wrote migration edge %s\n", chainRes.EdgeName)
		}
	}

	// ---- DB TIER (non-retroactive) ----
	return runReviseDBTier(schema, dbURL, quiet)
}

// chainStorePaths returns the three on-disk store directories a chain edge
// touches (chain edges, object store, revision manifests), used as the file set
// for the migration commit. They are content-addressed and append-only, so
// committing the directories captures exactly the new edge's artifacts (already
// committed, unchanged siblings are commit no-ops).
func chainStorePaths(dir string) []string {
	return []string{
		filepath.Join(dir, "chain"),
		filepath.Join(dir, "objects"),
		filepath.Join(dir, "revisions"),
	}
}

// isLegacyMigrationsDir reports whether dir is a legacy semver-TOML migrations
// project: it exists, is NOT chain-format, and holds at least one .toml migration
// file. A missing or empty dir is genesis (not legacy); a chain-format dir is not
// legacy. revise refuses legacy projects, pointing at `migrate upgrade`.
func isLegacyMigrationsDir(dir string) (bool, error) {
	if migrate.IsChainMode(dir) {
		return false, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			return true, nil
		}
	}
	return false, nil
}

// runReviseDBTier runs the non-retroactive DB tier: live FD discovery, pg_stat
// workload analysis, and the 7.4 live-import-verification seam. It runs AFTER the
// pure tier is committed, so its findings are advisory — they inform the NEXT
// revise; the committed migration stands (roadmap 6.1: NON-RETROACTIVE).
//
// When no database is configured or it is unreachable, the DB tier is SKIPPED and
// revise exits non-zero (reviseExitDBSkipped) naming the skipped tier — the pure
// tier remains committed. This is the fail-loud posture: a configured-but-broken
// database never silently degrades to "success".
func runReviseDBTier(schema *model.Schema, dbURL string, quiet bool) int {
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "revise: DB tier SKIPPED — no database configured (set database.url in pgdesign.toml or PGDESIGN_DB). "+
			"The pure tier (outputs + migration) is committed; live analyses (FD discovery, pg_stat workload) did not run.")
		return reviseExitDBSkipped
	}

	ctx := context.Background()
	conn, connErr := pgx.Connect(ctx, dbURL)
	if connErr != nil {
		fmt.Fprintf(os.Stderr, "revise: DB tier SKIPPED — cannot connect to database: %v. "+
			"The pure tier is committed; the committed migration stands, and the next revise incorporates any fixes.\n", connErr)
		return reviseExitDBSkipped
	}
	defer conn.Close(ctx)

	var dbDiags []diagnostic.Diagnostic

	// Live FD discovery (advisory): discover FDs for tables without declared
	// dependencies, then re-audit. Any resulting NF finding is surfaced for the
	// next revise — it does NOT retroactively block the committed migration.
	discoverFDsInto(ctx, conn, schema)
	dbDiags = append(dbDiags, nfAuditPure(schema)...)

	// Live pg_stat workload analysis (advisory): N+1 via call ratios, seq-scan
	// heavy tables. Missing pg_stat_statements is not fatal — the analysis simply
	// contributes nothing.
	if stmtStats, stmtErr := workload.QueryStatements(ctx, conn, 100); stmtErr == nil && schema.FKGraph != nil {
		dbDiags = append(dbDiags, workload.DetectNPlusOne(schema.FKGraph, stmtStats)...)
	}
	scanStats, scanErr := workload.QueryTableScanStats(ctx, conn, modelSchemaNames(schema))
	if scanErr == nil {
		dbDiags = append(dbDiags, workload.DetectSeqScanHeavy(scanStats)...)
	}

	// Live import verification seam (roadmap 7.4). Imports are phase 7; this slot
	// is where 7.4 wires the 5.7 predicate executor to verify imported-surface
	// facts against the live database. It contributes nothing today.
	dbDiags = append(dbDiags, verifyLiveImports(ctx, conn, schema)...)

	if len(dbDiags) > 0 && !quiet {
		fmt.Fprintln(os.Stderr, "revise: DB tier findings (advisory — addressed by the next revise):")
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(dbDiags, true))
	}
	return reviseExitOK
}

// verifyLiveImports is the roadmap-7.4 seam for live import verification. Imports
// are a phase-7 feature; until 7.4 wires the 5.7 predicate executor here to check
// imported-surface facts against the live database, it returns no diagnostics.
// The slot exists so 6.1's DB tier already names the boundary and 7.4 fills one
// function rather than re-threading the tier.
func verifyLiveImports(_ context.Context, _ *pgx.Conn, _ *model.Schema) []diagnostic.Diagnostic {
	return nil
}
