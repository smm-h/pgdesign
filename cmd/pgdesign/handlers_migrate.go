package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/migrate"
)

func handleMigratePlan(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
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
	schema.PGVersion = resolvePGVersion(actual.PGVersion, cfg.Database.PGVersion, schema.PGVersion)

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
	schema, exitCode := parseAndBuild(paths)
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
		dir = cfg.Project.MigrationsDir
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
	schema.PGVersion = resolvePGVersion(actual.PGVersion, cfg.Database.PGVersion, schema.PGVersion)

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
		dir = cfg.Project.MigrationsDir
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
		dir = cfg.Project.MigrationsDir
	}

	lockTimeout := cfg.Migrate.LockTimeout

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

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
		dir = cfg.Project.MigrationsDir
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
		dir = cfg.Project.MigrationsDir
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

	cfg := loadProjectConfig(".")

	dir := kwargs["dir"].(string)
	if dir == "migrations" && cfg.Project.MigrationsDir != "" {
		dir = cfg.Project.MigrationsDir
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
