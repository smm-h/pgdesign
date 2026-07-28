package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/imports"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/workload"
	"github.com/smm-h/strictcli/go/strictcli"
)

// pgdesignCheckContext implements strictcli.CheckContext for pgdesign checks.
type pgdesignCheckContext struct {
	root string
}

func (c *pgdesignCheckContext) ProjectRoot() string { return c.root }

// loadSchemaForCheck resolves schema paths from the project root directory,
// parses, and builds the schema. This is the shared entry point for check
// functions that need a resolved schema.
//
// Check functions receive a strictcli.CheckContext, not a *strictcli.Context:
// strictcli's built-in check command is a plain command handler (no struct
// handler dispatch), so parsed global flags — including the --project-config override
// — are not reachable here. Checks therefore always use walk-up config
// discovery from the project root, and all config-discovery calls in this file
// pass a nil override.
func loadSchemaForCheck(root string) ([]string, error) {
	configPath, hasConfig := config.FindConfig(root)
	if hasConfig {
		return resolveFromConfig(configPath)
	}
	return resolveSchemaPaths(nil, []string{root})
}

// diagDetail formats a single diagnostic as a string.
func diagDetail(d diagnostic.Diagnostic) string {
	loc := ""
	if d.Table != "" {
		loc = d.Table
	}
	if d.Column != "" {
		loc += "." + d.Column
	}
	if loc != "" {
		return fmt.Sprintf("[%s] %s: %s", d.Code, loc, d.Message)
	}
	return fmt.Sprintf("[%s] %s", d.Code, d.Message)
}

func checkValidation(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		r.Error(fmt.Sprintf("cannot resolve schema paths: %v", err))
		return r.Found("schema resolution failed")
	}

	schema, typeReg, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		r.Error("schema parse/build failed")
		return r.Found("schema parse/build failed")
	}

	// nil override: globals are not reachable in check functions (see
	// loadSchemaForCheck); with a nil override the error is always nil.
	cfg, _ := loadProjectConfig(nil, root)

	pgVersion, pgErr := requireSchemaPGVersion(schema)
	if pgErr != nil {
		return r.Skipped(pgErr.Error())
	}

	diags := validateSchema(schema, typeReg, cfg, pgVersion)

	if diagnostic.Diagnostics(diags).HasErrors() {
		for _, d := range diags {
			if d.Severity == diagnostic.Error {
				r.Error(diagDetail(d))
			} else {
				r.Warn(diagDetail(d))
			}
		}
		return r.Found("validation errors found")
	}

	// The whole-model revision is the content identity of the validated model
	// (registry-present, since it is TOML-built — L7). Surface it on the check
	// outcome so a passing validate names the exact revision it validated.
	revStr := ""
	if rv, rvErr := rev.Compute(schema, rev.RegistryPresent); rvErr == nil {
		revStr = " (revision " + rv.String() + ")"
	}

	warnings := diagnostic.Diagnostics(diags).Warnings()
	if len(warnings) > 0 {
		for _, w := range warnings {
			r.Warn(diagDetail(w))
		}
		return r.Found(fmt.Sprintf("%d validation warning(s)%s", len(warnings), revStr))
	}

	return r.Passed("all validation checks passed" + revStr)
}

// resolveCheckDBURL resolves the database URL for a check via the framework's
// connection-env capability (PGDESIGN_DB), with the project config as the
// documented last layer (resolution order cli > env > config; checks carry no
// CLI flag, so it is env > config). Under --hermetic the framework resolves the
// connection env as absent and the config layer is skipped too, so DB-backed
// checks skip VISIBLY. Returns (url, hermetic): hermetic is true when
// --hermetic suppressed the connection env, so the caller can name the skip.
//
// The framework's ConnectionEnvReader returns (value, present) where
// present==false covers BOTH --hermetic suppression and a genuinely-unset env;
// IsHermetic() (strictcli >= 0.27.0) distinguishes the two. Under --hermetic the
// config layer is skipped entirely so DB-backed checks skip VISIBLY instead of
// connecting to a config URL.
func resolveCheckDBURL[P config.PathKind](ctx strictcli.CheckContext, cfg *config.Config[P]) (url string, hermetic bool) {
	if reader, ok := ctx.(strictcli.ConnectionEnvReader); ok {
		if v, present := reader.ConnectionEnvValue("PGDESIGN_DB"); present {
			return v, false
		}
		if reader.IsHermetic() {
			// Connection env suppressed by --hermetic: never fall through to the
			// config URL, which would connect and break the hermetic guarantee.
			return "", true
		}
	}
	if cfg.Database.URL != "" {
		return cfg.Database.URL, false
	}
	return "", false
}

// nfAuditPure runs the normal-form audit over the schema's DECLARED functional
// dependencies only — no live FD discovery, no database. It is the PURE blocking
// core (roadmap 6.1): the split of the old checkNF that both revise's pure tier
// (via pureNFGate) and the `nf` check framework wrapper (checkNF) share. It
// returns only the NF-violation diagnostics (codes in nfViolationCodes), so a
// caller can surface them or promote+block on them uniformly.
//
// This preserves generate's --strict-nf gate behavior exactly (audit.Audit over
// the built model, then promote NF violations), extracted so revise reuses the
// identical rule rather than reimplementing it.
func nfAuditPure(schema *model.Schema) []diagnostic.Diagnostic {
	diags := audit.Audit(schema)
	var nfDiags []diagnostic.Diagnostic
	for _, d := range diags {
		if nfViolationCodes[d.Code] {
			nfDiags = append(nfDiags, d)
		}
	}
	return nfDiags
}

// pureNFGate runs the pure NF audit and reports whether it BLOCKS. Per roadmap
// 6.1's [%%] policy ("pure analyses BLOCK in revise's pure tier"), every
// normal-form violation (1NF..BCNF) is promoted to Error severity and blocks;
// the returned diagnostics carry that promotion so the caller renders them as
// errors. It is DB-free and used by revise's pure tier.
func pureNFGate(schema *model.Schema) (diags []diagnostic.Diagnostic, blocks bool) {
	promoted := promoteNFViolations(nfAuditPure(schema))
	return promoted, diagnostic.Diagnostics(promoted).HasErrors()
}

// discoverFDsInto fills in discovered functional dependencies for tables that
// have no declared dependencies, mutating schema in place. It is the DB-tier
// half of the checkNF split: live FD discovery over a connected database. A
// per-table discovery failure is skipped (non-fatal) — discovery augments, it
// never blocks. Shared by the `nf` check wrapper and revise's DB tier.
func discoverFDsInto(ctx context.Context, conn *pgx.Conn, schema *model.Schema) {
	for i := range schema.Tables {
		tbl := &schema.Tables[i]
		if len(tbl.Dependencies) > 0 {
			continue
		}
		schemaName := tbl.Schema
		if schemaName == "" {
			schemaName = "public"
		}
		fds, _, discErr := discover.Discover(conn, schemaName, tbl.Name, discover.Options{})
		if discErr != nil {
			// Discovery failure for one table is not fatal; skip it.
			continue
		}
		if len(fds) > 0 {
			for j := range fds {
				fds[j].Source = "discovered"
			}
			schema.Tables[i].Dependencies = fds
		}
	}
}

// checkNF is the DB-tier FD-discovery WRAPPER around the pure NF core
// (nfAuditPure) for the check framework. When a database URL resolves it
// discovers FDs for undeclared tables first, then runs the same pure audit;
// under --hermetic (or with no URL) it runs the pure audit alone — no connection
// is attempted. The check never blocks (warn reporter): it surfaces NF
// violations as warnings. revise's pure tier calls pureNFGate directly instead,
// where the same violations BLOCK.
func checkNF(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return r.Skipped(fmt.Sprintf("cannot resolve schema paths: %v", err))
	}

	schema, _, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		return r.Skipped("schema parse/build failed")
	}

	// nil override: globals are not reachable in check functions (see
	// loadSchemaForCheck); with a nil override the error is always nil.
	cfg, _ := loadProjectConfig(nil, root)
	// Under --hermetic dbURL is "" (connection env suppressed), so FD discovery
	// is skipped and only the pure NF audit runs — no connection is attempted.
	dbURL, _ := resolveCheckDBURL(ctx, cfg)

	// If a DB URL is available, discover FDs for tables without declared dependencies.
	if dbURL != "" {
		bgCtx := context.Background()
		conn, connErr := pgx.Connect(bgCtx, dbURL)
		if connErr != nil {
			return r.Skipped(fmt.Sprintf("cannot connect to database: %v", connErr))
		}
		defer conn.Close(bgCtx)
		discoverFDsInto(bgCtx, conn, schema)
	}

	nfDiags := nfAuditPure(schema)
	if len(nfDiags) > 0 {
		for _, d := range nfDiags {
			r.Warn(diagDetail(d))
		}
		return r.Found(fmt.Sprintf("%d normal form violation(s)", len(nfDiags)))
	}

	return r.Passed("no normal form violations")
}

// analyzeCoverage checks constraint completeness and returns diagnostics with codes C100-C104.
func analyzeCoverage(schema *model.Schema) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	for _, table := range schema.Tables {
		// C100: Table without check constraints
		if len(table.Checks) == 0 && len(table.Columns) > 2 && !table.AppendOnly {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C100",
				Table:    table.Name,
				Message:  "table has no check constraints",
			})
		}

		// C101: FK columns without index
		for _, fk := range table.FKs {
			if !table.HasIndexCovering(fk.Columns) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Warning,
					Code:     "C101",
					Table:    table.Name,
					Message:  fmt.Sprintf("foreign key %q on columns [%s] has no covering index", fk.Name, strings.Join(fk.Columns, ", ")),
				})
			}
		}

		// C104: Missing index for FK join pattern
		for _, fk := range table.FKs {
			refTable := schema.TableByName(fk.RefSchema, fk.RefTable)
			if refTable == nil {
				continue
			}
			for _, col := range refTable.Columns {
				isFilter := col.Name == "status" || col.Name == "type" || col.Name == "kind" || col.Name == "category" ||
					strings.HasSuffix(col.Name, "_at") || strings.HasSuffix(col.Name, "_date")
				if !isFilter {
					continue
				}
				suggested := make([]string, len(fk.Columns)+1)
				copy(suggested, fk.Columns)
				suggested[len(fk.Columns)] = col.Name
				if !table.HasIndexCovering(suggested) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Info,
						Code:     "C104",
						Table:    table.Name,
						Message:  fmt.Sprintf("consider index on [%s] for filtered joins on %q", strings.Join(suggested, ", "), fk.RefTable),
					})
				}
			}
		}
	}

	// C102: Unused enum type
	for _, enum := range schema.Enums {
		used := false
		for _, table := range schema.Tables {
			for _, col := range table.Columns {
				if col.PGType.Base == enum.Name {
					used = true
					break
				}
			}
			if used {
				break
			}
		}
		if !used {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C102",
				Message:  fmt.Sprintf("enum type %q is not referenced by any column", enum.Name),
			})
		}
	}

	// C103: Orphan table. Uses the ONE shared union-aware orphan helper (roadmap
	// 7.3, union site 6) — the same referenced-set W002 consumes — so imported-FK
	// targets key correctly and a table referenced only by an imported schema is
	// never spuriously flagged.
	referenced := schema.ReferencedTableKeys()
	for _, table := range schema.Tables {
		if len(table.Columns) <= 2 {
			continue
		}
		hasOutgoingFK := len(table.FKs) > 0
		referencedByOther := referenced[model.TableKey(table.Schema, table.Name)]
		if !hasOutgoingFK && !referencedByOther {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C103",
				Table:    table.Name,
				Message:  "table has no foreign key relationships (orphan)",
			})
		}
	}

	return diags
}

// designCodes are the validation diagnostic codes for schema design checks.
var designCodes = map[string]bool{
	"W013": true, // CASCADE depth
	"W014": true, // CASCADE breadth
	"W015": true, // Mixed ON DELETE
	"I001": true, // Natural key candidate
	"W016": true, // PK subsumes UNIQUE
	"W017": true, // Redundant IS NOT NULL CHECK
	"W018": true, // Domain CHECK duplicate
	"W019": true, // Range subsumption
	"I002": true, // Dead column
	"I003": true, // Row size TOAST threshold
	"W021": true, // Row size exceeds page
	"I004": true, // Column reordering savings
	"W027": true, // SM unreachable state
	"W028": true, // SM dead-end state
	"E223": true, // SM requires column missing
	"E224": true, // SM default mismatch
	"E226": true, // SM reserved trigger prefix
	"E228": true, // Cascade writes into append-only table
}

func checkDesign(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return r.Skipped(fmt.Sprintf("cannot resolve schema paths: %v", err))
	}

	schema, typeReg, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		return r.Skipped("schema parse/build failed")
	}

	// nil override: globals are not reachable in check functions (see
	// loadSchemaForCheck); with a nil override the error is always nil.
	cfg, _ := loadProjectConfig(nil, root)

	pgVersion, pgErr := requireSchemaPGVersion(schema)
	if pgErr != nil {
		return r.Skipped(pgErr.Error())
	}

	diags := validateSchema(schema, typeReg, cfg, pgVersion)

	// Filter to design-related codes only.
	var designDiags []diagnostic.Diagnostic
	for _, d := range diags {
		if designCodes[d.Code] {
			designDiags = append(designDiags, d)
		}
	}

	if len(designDiags) == 0 {
		return r.Passed("no design issues found")
	}

	for _, d := range designDiags {
		r.Warn(diagDetail(d))
	}
	return r.Found(fmt.Sprintf("%d design issue(s) found", len(designDiags)))
}

func checkCoverage(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return r.Skipped(fmt.Sprintf("cannot resolve schema paths: %v", err))
	}

	schema, _, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		return r.Skipped("schema parse/build failed")
	}

	allDiags := analyzeCoverage(schema)

	if len(allDiags) > 0 {
		for _, d := range allDiags {
			r.Warn(diagDetail(d))
		}
		return r.Found(fmt.Sprintf("%d coverage issue(s) found", len(allDiags)))
	}

	return r.Passed("all coverage checks passed")
}

// pureStructuralChecks runs the schema-only (DB-free) workload tier: structural
// index recommendations plus low-selectivity and excessive-index detection. It
// is the pure core shared by the `structural` check and revise's pure tier. All
// findings are Warning/Info severity — advisory, never blocking.
func pureStructuralChecks(schema *model.Schema) []diagnostic.Diagnostic {
	var allDiags []diagnostic.Diagnostic
	allDiags = append(allDiags, workload.StructuralRecommendations(schema)...)
	allDiags = append(allDiags, workload.DetectLowSelectivityIndexes(schema)...)
	allDiags = append(allDiags, workload.DetectExcessiveIndexes(schema)...)
	return allDiags
}

func checkStructural(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()
	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return r.Skipped(fmt.Sprintf("cannot resolve schema paths: %v", err))
	}
	schema, _, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		return r.Skipped("schema parse/build failed")
	}

	allDiags := pureStructuralChecks(schema)

	if len(allDiags) == 0 {
		return r.Passed("no structural issues found")
	}
	for _, d := range allDiags {
		r.Warn(diagDetail(d))
	}
	return r.Found(fmt.Sprintf("%d structural issue(s) found", len(allDiags)))
}

func checkWorkload(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()
	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return r.Skipped(fmt.Sprintf("cannot resolve schema paths: %v", err))
	}
	schema, _, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		return r.Skipped("schema parse/build failed")
	}

	// nil override: globals are not reachable in check functions (see
	// loadSchemaForCheck); with a nil override the error is always nil.
	cfg, _ := loadProjectConfig(nil, root)
	dbURL, hermetic := resolveCheckDBURL(ctx, cfg)
	if hermetic {
		return r.Skipped("hermetic mode: database checks suppressed (PGDESIGN_DB not consulted under --hermetic)")
	}
	if dbURL == "" {
		return r.Skipped("no database URL configured (set database.url in pgdesign.toml or PGDESIGN_DB env)")
	}

	bgCtx := context.Background()
	conn, connErr := pgx.Connect(bgCtx, dbURL)
	if connErr != nil {
		return r.Skipped(fmt.Sprintf("cannot connect to database: %v", connErr))
	}
	defer conn.Close(bgCtx)

	var allDiags []diagnostic.Diagnostic

	// pg_stat_statements analysis + N+1 detection
	stmtStats, stmtErr := workload.QueryStatements(bgCtx, conn, 100)
	if stmtErr == nil && schema.FKGraph != nil {
		allDiags = append(allDiags, workload.DetectNPlusOne(schema.FKGraph, stmtStats)...)
	}

	// Sequential scan analysis
	schemaNames := modelSchemaNames(schema)
	scanStats, scanErr := workload.QueryTableScanStats(bgCtx, conn, schemaNames)
	if scanErr == nil {
		allDiags = append(allDiags, workload.DetectSeqScanHeavy(scanStats)...)
	}

	if len(allDiags) == 0 {
		return r.Passed("no workload issues found")
	}
	for _, d := range allDiags {
		r.Warn(diagDetail(d))
	}
	return r.Found(fmt.Sprintf("%d workload issue(s) found", len(allDiags)))
}

// checkImports is the OFFLINE import drift check (roadmap 7.2). For every
// declared alias that has a committed lockfile it re-derives the vendored surface
// and reports, at error severity: vendor/lockfile integrity, semantic drift
// (column level, via N), and reference drift (FKs naming imported columns the
// surface lacks or whose junction type drifted). It never touches the remote.
func checkImports(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	configPath, found := config.FindConfig(root)
	if !found {
		return r.Skipped("no pgdesign.toml found")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		r.Error(fmt.Sprintf("cannot load config: %v", err))
		return r.Found("config loading failed")
	}
	if len(cfg.Imports) == 0 {
		return r.Skipped("no [imports] declared")
	}
	projectRoot := filepath.Dir(configPath)

	declared := make([]string, 0, len(cfg.Imports))
	for a := range cfg.Imports {
		declared = append(declared, a)
	}
	aliases := imports.ImportAliases(projectRoot, declared)
	if len(aliases) == 0 {
		return r.Skipped("no locked imports (run `pgdesign import lock`)")
	}

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		r.Error(fmt.Sprintf("cannot resolve schema paths: %v", err))
		return r.Found("schema resolution failed")
	}
	consumer, _, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		r.Error("schema parse/build failed")
		return r.Found("schema parse/build failed")
	}

	total := 0
	for _, alias := range aliases {
		diags := imports.Check(projectRoot, alias, consumer)
		for _, d := range diags {
			r.Error(diagDetail(d))
			total++
		}
	}
	// Extension/pg_version re-declaration enforcement (roadmap 7.3): the consumer
	// must re-declare every extension the imported surface requires and target a
	// pg_version >= the imported floor.
	for _, d := range imports.CheckRequirements(projectRoot, declared, consumer) {
		r.Error(diagDetail(d))
		total++
	}
	if total > 0 {
		return r.Found(fmt.Sprintf("%d import issue(s) across %d locked alias(es)", total, len(aliases)))
	}
	return r.Passed(fmt.Sprintf("all %d locked import(s) match the vendored surface", len(aliases)))
}

func checkBuild(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
	root := ctx.ProjectRoot()

	configPath, found := config.FindConfig(root)
	if !found {
		return r.Skipped("no pgdesign.toml found")
	}

	cfg, err := config.LoadAndResolve(configPath)
	if err != nil {
		r.Error(fmt.Sprintf("cannot load config: %v", err))
		return r.Found("config loading failed")
	}

	if len(cfg.Output) == 0 {
		return r.Skipped("no [output] section in pgdesign.toml")
	}

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		r.Error(fmt.Sprintf("cannot resolve schema paths: %v", err))
		return r.Found("schema resolution failed")
	}

	schema, typeReg, exitCode := parseAndBuild(nil, paths)
	if exitCode != 0 {
		r.Error("schema parse/build failed")
		return r.Found("schema parse/build failed")
	}

	_, pgErr := requireSchemaPGVersion(schema)
	if pgErr != nil {
		return r.Skipped(pgErr.Error())
	}

	plan, planErr := Plan(schema, cfg, typeReg)
	if planErr != nil {
		r.Error(fmt.Sprintf("plan failed: %v", planErr))
		return r.Found("plan generation failed")
	}

	// Sort paths for deterministic detail ordering.
	planned := make([]string, 0, len(plan.Files))
	for p := range plan.Files {
		planned = append(planned, p)
	}
	sort.Strings(planned)

	var staleFiles, missingFiles []string
	for _, p := range planned {
		existing, err := os.ReadFile(p)
		if err != nil {
			missingFiles = append(missingFiles, p)
		} else if !bytes.Equal(existing, plan.Files[p]) {
			staleFiles = append(staleFiles, p)
		}
	}

	// Orphan detection: files on disk inside owned multi-file codegen output
	// directories that the current configuration does not produce. Hard error.
	orphans, orphanErr := scanAllOrphans(plan.OwnedDirs)
	if orphanErr != nil {
		r.Error(fmt.Sprintf("orphan scan failed: %v", orphanErr))
		return r.Found("orphan scan failed")
	}

	if len(staleFiles) == 0 && len(missingFiles) == 0 && len(orphans) == 0 {
		return r.Passed("all build outputs are fresh")
	}

	for _, p := range missingFiles {
		r.Error(fmt.Sprintf("[missing] %s", p))
	}
	for _, p := range staleFiles {
		r.Error(fmt.Sprintf("[stale] %s", p))
	}
	for _, p := range orphans {
		r.Error(fmt.Sprintf("[orphan] %s", p))
	}
	if len(orphans) > 0 {
		r.Error("orphans: " + orphanExplanation)
	}

	return r.Found(fmt.Sprintf("working tree is not a fixed point of pgdesign build: %d file(s) would change, %d file(s) missing, %d orphan file(s)", len(staleFiles), len(missingFiles), len(orphans)))
}
