package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/dbutil"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/migrate"
)

func handleMigratePlan(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, _, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for schema name defaults.
	cfg := loadProjectConfig(paths[0])

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate plan")
		return 1
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	d := diff.Diff(schema, actual)
	if d.IsEmpty() {
		if !kwargs["quiet"].(bool) {
			fmt.Println("No changes detected. Schema is up to date.")
		}
		return 0
	}

	// Query table stats for row estimates.
	var tableStats migrate.TableStats
	statsConn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot connect for table stats: %v\n", err)
	} else {
		for _, sn := range schemaNames {
			stats, err := migrate.QueryTableStats(ctx, statsConn, sn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot query table stats for %s: %v\n", sn, err)
			} else {
				if tableStats == nil {
					tableStats = stats
				} else {
					for k, v := range stats {
						tableStats[k] = v
					}
				}
			}
		}
		statsConn.Close(ctx)
	}

	// Resolve PGVersion: live (from introspect) > config > TOML schema.
	pgVersion, pgErr := requirePGVersion(actual.PGVersion, cfg.Database.PGVersion, schema.PGVersion)
	if pgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", pgErr)
		return 1
	}
	schema.PGVersion = pgVersion

	m, migDiags := migrate.GenerateMigration(d, schema, "0.0.0", tableStats, cfg.Migrate.AutoConcurrentThreshold, cfg.Migrate.ExpandContractThreshold, extregistry.NewBuiltinRegistry())

	// Print the plan.
	fmt.Println("Migration plan:")
	fmt.Printf("  Description: %s\n", m.Description)
	fmt.Println()

	if migrate.HasPhases(m) {
		ddlIdx := 0
		dmlIdx := 0
		for _, phase := range []string{migrate.PhaseExpand, migrate.PhaseMigrate, migrate.PhaseContract} {
			// Check if this phase has any ops.
			hasOps := false
			for _, op := range m.DDLOps {
				if op.Phase == phase {
					hasOps = true
					break
				}
			}
			if !hasOps {
				for _, op := range m.DMLOps {
					if op.Phase == phase {
						hasOps = true
						break
					}
				}
			}
			if !hasOps {
				continue
			}

			fmt.Printf("  -- Phase: %s --\n", phase)
			for _, op := range m.DDLOps {
				if op.Phase != phase {
					continue
				}
				ddlIdx++
				sqlStmt := migrate.OpToSQL(op)
				fmt.Printf("  %d. [%s] %s\n", ddlIdx, op.Op, opSummary(op))
				fmt.Printf("     SQL: %s\n", sqlStmt)
				if op.Down != nil {
					if op.Down.Irreversible {
						fmt.Println("     Down: IRREVERSIBLE")
					} else {
						fmt.Println("     Down: reversible")
					}
				}
				fmt.Println()
			}
			for _, op := range m.DMLOps {
				if op.Phase != phase {
					continue
				}
				dmlIdx++
				fmt.Printf("  DML %d. [%s]\n", dmlIdx, op.Op)
				fmt.Printf("     SQL: %s\n", op.SQL)
				fmt.Println()
			}
		}
	} else {
		for i, op := range m.DDLOps {
			sqlStmt := migrate.OpToSQL(op)
			fmt.Printf("  %d. [%s] %s\n", i+1, op.Op, opSummary(op))
			fmt.Printf("     SQL: %s\n", sqlStmt)
			if op.Down != nil {
				if op.Down.Irreversible {
					fmt.Println("     Down: IRREVERSIBLE")
				} else {
					fmt.Println("     Down: reversible")
				}
			}
			fmt.Println()
		}

		for i, op := range m.DMLOps {
			fmt.Printf("  DML %d. [%s]\n", i+1, op.Op)
			fmt.Printf("     SQL: %s\n", op.SQL)
			fmt.Println()
		}
	}

	if len(migDiags) > 0 {
		fmt.Println("Diagnostics:")
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(migDiags, true))
	}

	return 0
}

func handleMigrateGenerate(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, _, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for migrations dir and schema name defaults.
	cfg := loadProjectConfig(paths[0])

	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate generate")
		return 1
	}

	version, _ := kwargs["version"].(string)
	if version == "" {
		fmt.Fprintln(os.Stderr, "error: --version is required for migrate generate")
		return 1
	}

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	d := diff.Diff(schema, actual)
	if d.IsEmpty() {
		fmt.Println("No changes detected. Nothing to generate.")
		return 0
	}

	// Query table stats for row estimates.
	var tableStats migrate.TableStats
	statsConn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot connect for table stats: %v\n", err)
	} else {
		for _, sn := range schemaNames {
			stats, err := migrate.QueryTableStats(ctx, statsConn, sn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot query table stats for %s: %v\n", sn, err)
			} else {
				if tableStats == nil {
					tableStats = stats
				} else {
					for k, v := range stats {
						tableStats[k] = v
					}
				}
			}
		}
		statsConn.Close(ctx)
	}

	// Resolve PGVersion: live (from introspect) > config > TOML schema.
	pgVersion, pgErr := requirePGVersion(actual.PGVersion, cfg.Database.PGVersion, schema.PGVersion)
	if pgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", pgErr)
		return 1
	}
	schema.PGVersion = pgVersion

	m, migDiags := migrate.GenerateMigration(d, schema, version, tableStats, cfg.Migrate.AutoConcurrentThreshold, cfg.Migrate.ExpandContractThreshold, extregistry.NewBuiltinRegistry())

	if len(migDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(migDiags, true))
	}

	// Ensure migrations directory exists.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: create migrations dir: %v\n", err)
		return 1
	}

	path := filepath.Join(dir, version+".toml")
	if err := migrate.WriteMigrationFile(path, m); err != nil {
		fmt.Fprintf(os.Stderr, "error: write migration: %v\n", err)
		return 1
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Generated migration: %s\n", path)
		fmt.Printf("  Description: %s\n", m.Description)
		fmt.Printf("  DDL ops: %d\n", len(m.DDLOps))
		fmt.Printf("  DML ops: %d\n", len(m.DMLOps))
	}

	return 0
}

func handleMigrateApply(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate apply")
		return 1
	}

	// Load config for migrations dir and lock timeout.
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	dryRun := kwargs["dry_run"].(bool)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	if dryRun {
		return handleMigrateApplyDryRun(ctx, conn, dir, kwargs["quiet"].(bool))
	}

	lockTimeout := cfg.Migrate.LockTimeout

	applied, err := migrate.Apply(ctx, conn, dir, lockTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "Applied before failure: %v\n", applied)
		}
		return 1
	}

	if len(applied) == 0 {
		if !kwargs["quiet"].(bool) {
			fmt.Println("No pending migrations.")
		}
		return 0
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Applied %d migration(s):\n", len(applied))
		for _, v := range applied {
			fmt.Printf("  - %s\n", v)
		}
	}
	return 0
}

// handleMigrateApplyDryRun shows the SQL that would be executed without
// actually applying any migrations.
func handleMigrateApplyDryRun(ctx context.Context, conn *pgx.Conn, dir string, quiet bool) int {
	if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	applied, err := migrate.AppliedVersions(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	// Discover migration files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", err)
		return 1
	}

	type pendingMigration struct {
		version string
		path    string
	}
	var pending []pendingMigration
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		version := e.Name()[:len(e.Name())-5]
		if appliedSet[version] {
			continue
		}
		pending = append(pending, pendingMigration{
			version: version,
			path:    filepath.Join(dir, e.Name()),
		})
	}

	if len(pending) == 0 {
		if !quiet {
			fmt.Println("No pending migrations.")
		}
		return 0
	}

	for i, pm := range pending {
		m, err := migrate.ParseMigrationFile(pm.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: parse %s: %v\n", pm.path, err)
			return 1
		}

		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("-- Migration: %s\n", pm.version)
		if m.Description != "" {
			fmt.Printf("-- %s\n", m.Description)
		}

		for _, op := range m.DDLOps {
			sqlStmt := migrate.OpToSQL(op)
			if sqlStmt != "" {
				fmt.Println(sqlStmt)
			}
		}

		for _, op := range m.DMLOps {
			if op.SQL != "" {
				fmt.Println(op.SQL)
			}
		}
	}

	return 0
}

func handleMigrateRollback(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate rollback")
		return 1
	}

	// Load config for migrations dir and lock timeout.
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	lockTimeout := cfg.Migrate.LockTimeout

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	toVersion, _ := kwargs["to"].(string)
	if toVersion != "" {
		// Multi-step rollback to a target version.
		rolledBack, err := migrate.RollbackTo(ctx, conn, dir, toVersion, lockTimeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			if len(rolledBack) > 0 {
				fmt.Fprintf(os.Stderr, "Rolled back before failure: %v\n", rolledBack)
			}
			return 1
		}
		if !kwargs["quiet"].(bool) {
			fmt.Printf("Rolled back %d migration(s) to %s:\n", len(rolledBack), toVersion)
			for _, v := range rolledBack {
				fmt.Printf("  - %s\n", v)
			}
		}
		return 0
	}

	// Single-step rollback (existing behavior).
	version, err := migrate.Rollback(ctx, conn, dir, lockTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Rolled back: %s\n", version)
	}
	return 0
}

func handleMigrateStatus(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate status")
		return 1
	}

	// Load config for migrations dir.
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	applied, err := migrate.AppliedVersions(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	// Discover migration files.
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", err)
		return 1
	}

	var allVersions []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		v := e.Name()[:len(e.Name())-5] // strip .toml
		allVersions = append(allVersions, v)
	}

	fmt.Printf("Applied migrations: %d\n", len(applied))
	for _, v := range applied {
		fmt.Printf("  [applied] %s\n", v)
	}

	pendingCount := 0
	for _, v := range allVersions {
		if !appliedSet[v] {
			fmt.Printf("  [pending] %s\n", v)
			pendingCount++
		}
	}

	if pendingCount == 0 && len(applied) > 0 {
		fmt.Println("All migrations applied.")
	} else if pendingCount > 0 {
		fmt.Printf("\n%d pending migration(s).\n", pendingCount)
	} else if len(applied) == 0 {
		fmt.Println("No migrations found or applied.")
	}

	return 0
}

func opSummary(op migrate.DDLOp) string {
	target := op.Table
	if op.Column != "" {
		target += "." + op.Column
	}
	if target == "" {
		target = op.Name
	}
	return target
}

func handleMigrateSquash(kwargs map[string]interface{}) int {
	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	from, _ := kwargs["from"].(string)
	if from == "" {
		fmt.Fprintln(os.Stderr, "error: --from is required")
		return 1
	}
	to, _ := kwargs["to"].(string)
	if to == "" {
		fmt.Fprintln(os.Stderr, "error: --to is required")
		return 1
	}

	dbURL, _ := kwargs["db"].(string)
	if dbURL != "" {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: connect for safety check: %v\n", err)
			return 1
		}
		defer conn.Close(ctx)

		if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}

		applied, err := migrate.AppliedVersions(ctx, conn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}

		// Check if any migration in the [from, to] range has been applied.
		var appliedInRange []string
		for _, v := range applied {
			if migrate.InSemverRange(v, from, to) {
				appliedInRange = append(appliedInRange, v)
			}
		}

		if len(appliedInRange) > 0 {
			diags := []diagnostic.Diagnostic{{
				Severity: diagnostic.Error,
				Code:     "M200",
				Message:  fmt.Sprintf("cannot squash: %d migration(s) in range [%s, %s] have been applied: %v; squashing would desynchronize the tracking table", len(appliedInRange), from, to, appliedInRange),
			}}
			fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
			return 1
		}
	}

	result, err := migrate.SquashMigrations(dir, from, to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Write squashed migration to a temp file first, since the output path
	// ({to}.toml) is one of the original files being archived.
	outputPath := migrate.OutputPath(dir, to)
	tmpPath := outputPath + ".squash-tmp"
	if err := migrate.WriteMigrationFile(tmpPath, result.Squashed); err != nil {
		fmt.Fprintf(os.Stderr, "error: write squashed migration: %v\n", err)
		return 1
	}

	// Archive original migration files with saferm.
	args := []string{"delete", "--description", fmt.Sprintf("Squashed into %s (from %s to %s)", to, from, to)}
	args = append(args, result.OriginalPaths...)
	cmd := exec.Command("saferm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Clean up temp file on failure.
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "error: archive originals: %v\n", err)
		return 1
	}

	// Move temp file to final output path.
	if err := os.Rename(tmpPath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: rename squashed migration: %v\n", err)
		return 1
	}

	if !kwargs["quiet"].(bool) {
		fmt.Printf("Squashed %d migrations into %s\n", result.OriginalCount, outputPath)
		fmt.Printf("  Description: %s\n", result.Squashed.Description)
		fmt.Printf("  DDL ops: %d\n", len(result.Squashed.DDLOps))
		fmt.Printf("  DML ops: %d\n", len(result.Squashed.DMLOps))
		if result.CancelledPairs > 0 {
			fmt.Printf("  Cancelled inverse pairs: %d\n", result.CancelledPairs)
		}
		if result.MergedOps > 0 {
			fmt.Printf("  Merged ops: %d\n", result.MergedOps)
		}
		if result.ConsolidatedOps > 0 {
			fmt.Printf("  Consolidated into CREATE TABLE: %d\n", result.ConsolidatedOps)
		}
	}

	return 0
}

func handleMigrateTest(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for migrate test")
		return 1
	}

	shadow, _ := kwargs["shadow"].(bool)
	if shadow {
		return handleMigrateTestShadow(kwargs)
	}

	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	timeout := kwargs["timeout"].(int)
	quiet := kwargs["quiet"].(bool)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	applied, err := migrate.AppliedVersions(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}

	// Discover migration files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", err)
		return 1
	}

	type pendingMigration struct {
		version string
		path    string
	}
	var pending []pendingMigration
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		version := e.Name()[:len(e.Name())-5]
		if appliedSet[version] {
			continue
		}
		pending = append(pending, pendingMigration{
			version: version,
			path:    filepath.Join(dir, e.Name()),
		})
	}

	if len(pending) == 0 {
		if !quiet {
			fmt.Println("No pending migrations to test.")
		}
		return 0
	}

	if !quiet {
		fmt.Printf("Testing %d pending migration(s)...\n", len(pending))
	}

	totalStart := time.Now()

	tx, err := conn.Begin(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: begin transaction: %v\n", err)
		return 1
	}
	defer tx.Rollback(ctx)

	failed := false
	skippedNonTx := 0

	for _, pm := range pending {
		start := time.Now()

		m, err := migrate.ParseMigrationFile(pm.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [fail] %s: parse error: %v\n", pm.version, err)
			failed = true
			break
		}

		migFailed := false
		for _, op := range m.DDLOps {
			if migrate.IsNonTransactional(op) {
				skippedNonTx++
				if !quiet {
					fmt.Printf("  [skip] Non-transactional op (would run outside transaction): %s\n", op.Op)
				}
				continue
			}

			sqlStmt := migrate.OpToSQL(op)
			if sqlStmt == "" {
				continue
			}

			stmts, err := sqlparse.SplitStatements(sqlStmt)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [fail] %s: parse error: %v\n", pm.version, err)
				migFailed = true
				break
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					fmt.Fprintf(os.Stderr, "  [fail] %s: %v\n    SQL: %s\n", pm.version, err, stmt)
					migFailed = true
					break
				}
			}
			if migFailed {
				break
			}
		}

		if !migFailed {
			for _, op := range m.DMLOps {
				if op.SQL == "" {
					continue
				}
				if _, err := tx.Exec(ctx, op.SQL); err != nil {
					fmt.Fprintf(os.Stderr, "  [fail] %s: DML error: %v\n    SQL: %s\n", pm.version, err, op.SQL)
					migFailed = true
					break
				}
			}
		}

		elapsed := time.Since(start)

		if migFailed {
			failed = true
			break
		}

		if !quiet {
			fmt.Printf("  [pass] %s (%s)\n", pm.version, elapsed.Round(time.Millisecond))
		}
	}

	// Roll back explicitly (deferred rollback also covers this).
	tx.Rollback(ctx)

	totalElapsed := time.Since(totalStart)

	if !quiet {
		fmt.Println()
		if failed {
			fmt.Println("Result: FAIL")
		} else {
			fmt.Println("Result: PASS")
		}
		fmt.Printf("Migrations tested: %d\n", len(pending))
		fmt.Printf("Total time: %s\n", totalElapsed.Round(time.Millisecond))
		if skippedNonTx > 0 {
			fmt.Printf("Skipped non-transactional ops: %d\n", skippedNonTx)
		}
	}

	if failed {
		return 1
	}
	return 0
}

func handleMigrateTestShadow(kwargs map[string]interface{}) int {
	dbURL := kwargs["db"].(string)
	quiet := kwargs["quiet"].(bool)
	timeout := kwargs["timeout"].(int)

	// Require path arg for shadow mode.
	rawPaths, ok := kwargs["path"].([]interface{})
	if !ok || len(rawPaths) == 0 {
		fmt.Fprintln(os.Stderr, "error: schema path is required for --shadow mode")
		return 1
	}
	paths := make([]string, len(rawPaths))
	for i, v := range rawPaths {
		paths[i] = v.(string)
	}

	// Build desired schema from TOML.
	schema, _, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	cfg := loadProjectConfig(paths[0])

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = string(cfg.Project.MigrationsDir)
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// Connect to the source database for admin operations (CREATE/DROP DATABASE).
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	// Check for stale shadow databases.
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'pgdesign_shadow_%'")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot check for stale shadow databases: %v\n", err)
	} else {
		var stale []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				stale = append(stale, name)
			}
		}
		rows.Close()
		if len(stale) > 0 {
			fmt.Fprintf(os.Stderr, "warning: found %d stale shadow database(s):\n", len(stale))
			for _, s := range stale {
				fmt.Fprintf(os.Stderr, "  - %s\n", s)
			}
			fmt.Fprintln(os.Stderr, "  Run DROP DATABASE manually to clean up.")
		}
	}

	// Create shadow database.
	shadowName := fmt.Sprintf("pgdesign_shadow_%d", time.Now().Unix())
	if !quiet {
		fmt.Printf("Creating shadow database: %s\n", shadowName)
	}

	// CREATE DATABASE cannot run inside a transaction.
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", shadowName)); err != nil {
		fmt.Fprintf(os.Stderr, "error: create shadow database: %v\n", err)
		return 1
	}

	// Ensure cleanup on exit.
	defer func() {
		// Use a fresh context for cleanup in case the original was cancelled.
		cleanCtx := context.Background()
		if _, err := conn.Exec(cleanCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", shadowName)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to drop shadow database %s: %v\n", shadowName, err)
			fmt.Fprintf(os.Stderr, "  Clean up manually: DROP DATABASE %s;\n", shadowName)
		} else if !quiet {
			fmt.Printf("Dropped shadow database: %s\n", shadowName)
		}
	}()

	// Build connection string for the shadow database.
	shadowURL, err := buildShadowURL(dbURL, shadowName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build shadow URL: %v\n", err)
		return 1
	}

	// Connect to shadow and replay migrations.
	shadowConn, err := pgx.Connect(ctx, shadowURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect to shadow: %v\n", err)
		return 1
	}

	if !quiet {
		fmt.Printf("Replaying migrations from %s...\n", dir)
	}

	lockTimeout := cfg.Migrate.LockTimeout
	applied, err := migrate.Apply(ctx, shadowConn, dir, lockTimeout)
	shadowConn.Close(ctx) // Must close before DROP DATABASE.
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: replay migrations: %v\n", err)
		if len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "Applied before failure: %v\n", applied)
		}
		return 1
	}

	if !quiet {
		fmt.Printf("Applied %d migration(s) to shadow.\n", len(applied))
	}

	// Introspect the shadow database.
	if !quiet {
		fmt.Println("Introspecting shadow database...")
	}
	actual, intrDiags, err := introspect.Introspect(ctx, shadowURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: introspect shadow: %v\n", err)
		return 1
	}
	if len(intrDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(intrDiags, true))
	}
	if diagnostic.Diagnostics(intrDiags).HasErrors() {
		return 1
	}

	// Resolve PGVersion.
	pgVersion, pgErr := requirePGVersion(actual.PGVersion, cfg.Database.PGVersion, schema.PGVersion)
	if pgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", pgErr)
		return 1
	}
	schema.PGVersion = pgVersion

	// Diff shadow against desired.
	d := diff.Diff(schema, actual)
	if d.IsEmpty() {
		if !quiet {
			fmt.Println("\nResult: PASS")
			fmt.Println("Shadow database matches desired schema exactly.")
		}
		return 0
	}

	// Report discrepancies.
	fmt.Println("\nResult: FAIL")
	fmt.Println("Shadow database diverges from desired schema:")
	printSchemaDiffSummary(d)
	return 1
}

// buildShadowURL takes a PostgreSQL connection URL and swaps the database name.
func buildShadowURL(dbURL, shadowDB string) (string, error) {
	return dbutil.SwapDatabase(dbURL, shadowDB)
}

// printSchemaDiffSummary prints a human-readable summary of schema differences.
func printSchemaDiffSummary(d *diff.SchemaDiff) {
	if len(d.TablesAdded) > 0 {
		fmt.Printf("  Tables in TOML but not in shadow: %s\n", strings.Join(d.TablesAdded, ", "))
	}
	if len(d.TablesRemoved) > 0 {
		fmt.Printf("  Tables in shadow but not in TOML: %s\n", strings.Join(d.TablesRemoved, ", "))
	}
	for _, td := range d.TablesChanged {
		fmt.Printf("  Table %s differs:\n", td.Name)
		if len(td.ColumnsAdded) > 0 {
			names := make([]string, len(td.ColumnsAdded))
			for i, c := range td.ColumnsAdded {
				names[i] = c.Name
			}
			fmt.Printf("    Missing columns: %s\n", strings.Join(names, ", "))
		}
		if len(td.ColumnsRemoved) > 0 {
			fmt.Printf("    Extra columns: %s\n", strings.Join(td.ColumnsRemoved, ", "))
		}
		if len(td.ColumnsChanged) > 0 {
			names := make([]string, len(td.ColumnsChanged))
			for i, c := range td.ColumnsChanged {
				names[i] = c.Name
			}
			fmt.Printf("    Changed columns: %s\n", strings.Join(names, ", "))
		}
		if len(td.IndexesAdded) > 0 {
			names := make([]string, len(td.IndexesAdded))
			for i, idx := range td.IndexesAdded {
				names[i] = idx.Name
			}
			fmt.Printf("    Missing indexes: %s\n", strings.Join(names, ", "))
		}
		if len(td.IndexesRemoved) > 0 {
			fmt.Printf("    Extra indexes: %s\n", strings.Join(td.IndexesRemoved, ", "))
		}
		if len(td.FKsAdded) > 0 {
			names := make([]string, len(td.FKsAdded))
			for i, fk := range td.FKsAdded {
				names[i] = fk.Name
			}
			fmt.Printf("    Missing foreign keys: %s\n", strings.Join(names, ", "))
		}
		if len(td.FKsRemoved) > 0 {
			fmt.Printf("    Extra foreign keys: %s\n", strings.Join(td.FKsRemoved, ", "))
		}
		if len(td.ChecksAdded) > 0 {
			names := make([]string, len(td.ChecksAdded))
			for i, c := range td.ChecksAdded {
				names[i] = c.Name
			}
			fmt.Printf("    Missing check constraints: %s\n", strings.Join(names, ", "))
		}
		if len(td.ChecksRemoved) > 0 {
			fmt.Printf("    Extra check constraints: %s\n", strings.Join(td.ChecksRemoved, ", "))
		}
		if td.PKChanged != nil {
			fmt.Printf("    Primary key differs: shadow=%v, desired=%v\n", td.PKChanged[0], td.PKChanged[1])
		}
		if td.CommentChanged != nil {
			fmt.Println("    Comment differs")
		}
	}
	if len(d.EnumsAdded) > 0 {
		fmt.Printf("  Enums in TOML but not in shadow: %s\n", strings.Join(d.EnumsAdded, ", "))
	}
	if len(d.EnumsRemoved) > 0 {
		fmt.Printf("  Enums in shadow but not in TOML: %s\n", strings.Join(d.EnumsRemoved, ", "))
	}
	for _, ed := range d.EnumsChanged {
		fmt.Printf("  Enum %s differs:\n", ed.Name)
		if len(ed.ValuesAdded) > 0 {
			fmt.Printf("    Missing values: %s\n", strings.Join(ed.ValuesAdded, ", "))
		}
		if len(ed.ValuesRemoved) > 0 {
			fmt.Printf("    Extra values: %s\n", strings.Join(ed.ValuesRemoved, ", "))
		}
	}
	if len(d.ExtensionsAdded) > 0 {
		fmt.Printf("  Extensions in TOML but not in shadow: %s\n", strings.Join(d.ExtensionsAdded, ", "))
	}
	if len(d.ExtensionsRemoved) > 0 {
		fmt.Printf("  Extensions in shadow but not in TOML: %s\n", strings.Join(d.ExtensionsRemoved, ", "))
	}
	if len(d.ViewsAdded) > 0 {
		fmt.Printf("  Views in TOML but not in shadow: %s\n", strings.Join(d.ViewsAdded, ", "))
	}
	if len(d.ViewsRemoved) > 0 {
		fmt.Printf("  Views in shadow but not in TOML: %s\n", strings.Join(d.ViewsRemoved, ", "))
	}
	if len(d.MaterializedViewsAdded) > 0 {
		fmt.Printf("  Materialized views in TOML but not in shadow: %s\n", strings.Join(d.MaterializedViewsAdded, ", "))
	}
	if len(d.MaterializedViewsRemoved) > 0 {
		fmt.Printf("  Materialized views in shadow but not in TOML: %s\n", strings.Join(d.MaterializedViewsRemoved, ", "))
	}
	if len(d.SequencesAdded) > 0 {
		fmt.Printf("  Sequences in TOML but not in shadow: %s\n", strings.Join(d.SequencesAdded, ", "))
	}
	if len(d.SequencesRemoved) > 0 {
		fmt.Printf("  Sequences in shadow but not in TOML: %s\n", strings.Join(d.SequencesRemoved, ", "))
	}
	if len(d.FunctionsAdded) > 0 {
		fmt.Printf("  Functions in TOML but not in shadow: %s\n", strings.Join(d.FunctionsAdded, ", "))
	}
	if len(d.FunctionsRemoved) > 0 {
		fmt.Printf("  Functions in shadow but not in TOML: %s\n", strings.Join(d.FunctionsRemoved, ", "))
	}
	if len(d.DomainsAdded) > 0 {
		fmt.Printf("  Domains in TOML but not in shadow: %s\n", strings.Join(d.DomainsAdded, ", "))
	}
	if len(d.DomainsRemoved) > 0 {
		fmt.Printf("  Domains in shadow but not in TOML: %s\n", strings.Join(d.DomainsRemoved, ", "))
	}
	if len(d.CompositeTypesAdded) > 0 {
		fmt.Printf("  Composite types in TOML but not in shadow: %s\n", strings.Join(d.CompositeTypesAdded, ", "))
	}
	if len(d.CompositeTypesRemoved) > 0 {
		fmt.Printf("  Composite types in shadow but not in TOML: %s\n", strings.Join(d.CompositeTypesRemoved, ", "))
	}
}
