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
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/dbutil"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/livenorm"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/strictcli/go/strictcli"
)

// preUpgradeGuardURL connects to dbURL solely to run the pre-upgrade guard, then
// closes. Used by the DB-diffing subcommands (plan, legacy generate) that do not
// hold a persistent connection at preflight time.
func preUpgradeGuardURL(ctx context.Context, dbURL string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	return migrate.GuardNotPreUpgrade(ctx, conn)
}

func registerMigratePlanCmd(g *strictcli.Group) {
	g.Command("plan", "Plan migrations by diffing the TOML schema against a live database without writing any files. Shows which tables, columns, indexes, and constraints would change, along with risk levels and required lock types for each operation. Useful for previewing changes before generating migration files.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			paths := kwargsStrSlice(kwargs["path"])
			schema, _, exitCode := parseAndBuild(cfgOverride, paths)
			if exitCode != 0 {
				return strictcli.Exit(exitCode)
			}

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate plan")
				return strictcli.Exit(1)
			}

			schemaNames := modelSchemaNames(schema)

			ctx := context.Background()
			if err := preUpgradeGuardURL(ctx, dbURL); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			if len(diags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
			}
			if diagnostic.Diagnostics(diags).HasErrors() {
				return strictcli.Exit(1)
			}

			if collErr := diff.CheckTruncationCollisions(schema); collErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", collErr)
				return strictcli.Exit(1)
			}
			// migrationDiff resolves the live PG version onto the desired schema
			// BEFORE diffing. Otherwise an in-sync project with an UNPINNED
			// pg_version (schema version 0) diffs against the live server version and
			// reports a spurious PGVersionChanged, losing "No changes detected".
			d := migrationDiff(schema, actual)
			if d.IsEmpty() {
				if !quiet {
					fmt.Println("No changes detected. Schema is up to date.")
				}
				return strictcli.Exit(0)
			}

			if _, pgErr := requireSchemaPGVersion(schema); pgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", pgErr)
				return strictcli.Exit(1)
			}

			m, migDiags := migrate.GenerateMigration(d, schema, "0.0.0", extregistry.NewBuiltinRegistry())

			fmt.Println("Migration plan:")
			fmt.Printf("  Description: %s\n", m.Description)
			fmt.Println()

			if migrate.HasPhases(m) {
				ddlIdx := 0
				dmlIdx := 0
				for _, phase := range []string{migrate.PhaseExpand, migrate.PhaseMigrate, migrate.PhaseContract} {
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

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory containing them", strictcli.Variadic()),
		),
	)
}

func registerMigrateGenerateCmd(g *strictcli.Group) {
	g.Command("generate", "Generate versioned migration files by comparing the TOML schema against a live database. Produces up and down SQL files with risk annotations, safety linting, and expand-migrate-contract phase classification. Volatile defaults and operations on large tables are automatically detected and handled safely.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			paths := kwargsStrSlice(kwargs["path"])
			schema, _, exitCode := parseAndBuild(cfgOverride, paths)
			if exitCode != 0 {
				return strictcli.Exit(exitCode)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, paths[0])
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			// Chain-mode project: pure generation against the reconstructed head
			// model (no DB), routing through GenerateEdge. Identity is
			// content-derived, so there is no --version to assign.
			if migrate.IsChainMode(dir) {
				return strictcli.Exit(handleMigrateGenerateChain(schema, dir, cfg, quiet))
			}

			// Legacy-mode: introspect the live DB and write a semver TOML. The
			// --version flag was removed; the next version is auto-derived (max
			// existing + patch bump) as the transitional behavior.
			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate generate")
				return strictcli.Exit(1)
			}

			version, verr := migrate.NextSemverVersion(dir)
			if verr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", verr)
				return strictcli.Exit(1)
			}

			schemaNames := modelSchemaNames(schema)

			ctx := context.Background()
			if err := preUpgradeGuardURL(ctx, dbURL); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			if len(diags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
			}
			if diagnostic.Diagnostics(diags).HasErrors() {
				return strictcli.Exit(1)
			}

			if collErr := diff.CheckTruncationCollisions(schema); collErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", collErr)
				return strictcli.Exit(1)
			}
			// migrationDiff resolves the live PG version BEFORE diffing (see plan):
			// an unpinned pg_version must not register as a spurious
			// PGVersionChanged and cause a zero-op migration file to be written.
			d := migrationDiff(schema, actual)
			if d.IsEmpty() {
				fmt.Println("No changes detected. Nothing to generate.")
				return strictcli.Exit(0)
			}

			if _, pgErr := requireSchemaPGVersion(schema); pgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", pgErr)
				return strictcli.Exit(1)
			}

			m, migDiags := migrate.GenerateMigration(d, schema, version, extregistry.NewBuiltinRegistry())

			if len(migDiags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(migDiags, true))
			}

			// Never write a migration file with an empty op-list. A non-empty diff
			// can still lower to zero ops (e.g. an identity-only difference the op
			// generator does not translate); writing a zero-op file would mint a
			// spurious, un-applyable migration. Match plan's no-op behavior.
			if len(m.DDLOps) == 0 && len(m.DMLOps) == 0 {
				fmt.Println("No operations generated. Nothing to write.")
				return strictcli.Exit(0)
			}

			if err := os.MkdirAll(dir, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error: create migrations dir: %v\n", err)
				return strictcli.Exit(1)
			}

			path := filepath.Join(dir, version+".toml")
			if err := migrate.WriteMigrationFile(path, m); err != nil {
				fmt.Fprintf(os.Stderr, "error: write migration: %v\n", err)
				return strictcli.Exit(1)
			}

			if !quiet {
				fmt.Printf("Generated migration: %s\n", path)
				fmt.Printf("  Description: %s\n", m.Description)
				fmt.Printf("  DDL ops: %d\n", len(m.DDLOps))
				fmt.Printf("  DML ops: %d\n", len(m.DMLOps))
			}

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server (legacy-mode only; chain-mode generate is pure)", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory containing them", strictcli.Variadic()),
		),
	)
}

// handleMigrateGenerateChain generates a chain edge for a chain-mode project. It
// is PURE (no database): the head model is reconstructed from the on-disk manifest
// + object store (prev), diffed against the built schema (desired), lowered to
// ops, and written as a content-addressed edge (plus its objects and to-revision
// manifest) via GenerateEdge. A genesis edge is produced for an empty chain.
func handleMigrateGenerateChain(schema *model.Schema, dir string, cfg *config.RawConfig, quiet bool) int {
	if _, pgErr := requireSchemaPGVersion(schema); pgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", pgErr)
		return 1
	}
	p, err := migrate.OpenChainProject(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	head, prev, err := migrate.ChainHead(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Diff baseline: the reconstructed head model, or an empty model at genesis
	// (matched pg_version so an unpinned change does not register spuriously).
	base := prev
	if base == nil {
		base = &model.Schema{Name: schema.Name, PGVersion: schema.PGVersion}
	}
	if collErr := diff.CheckTruncationCollisions(schema); collErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", collErr)
		return 1
	}
	d := diff.Diff(schema, base)
	if d.IsEmpty() {
		fmt.Println("No changes detected. Nothing to generate.")
		return 0
	}

	m, migDiags := migrate.GenerateMigration(d, schema, "", extregistry.NewBuiltinRegistry())
	if len(migDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(migDiags, true))
	}
	if len(m.DDLOps) == 0 && len(m.DMLOps) == 0 {
		fmt.Println("No operations generated. Nothing to write.")
		return 0
	}

	slug := slugifyDescription(m.Description)
	name, err := migrate.GenerateEdge(p, m, schema, prev, head, rev.RegistryPresent, slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: generate edge: %v\n", err)
		return 1
	}
	if !quiet {
		fmt.Printf("Generated edge: %s\n", name)
		fmt.Printf("  Description: %s\n", m.Description)
		fmt.Printf("  DDL ops: %d\n", len(m.DDLOps))
		fmt.Printf("  DML ops: %d\n", len(m.DMLOps))
	}
	return 0
}

// slugifyDescription turns a migration description into a filesystem-safe edge
// slug: lowercase alphanumerics separated by single hyphens, capped in length. An
// empty result falls back to "migration".
func slugifyDescription(desc string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(desc) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "migration"
	}
	return s
}

func registerMigrateApplyCmd(g *strictcli.Group) {
	g.Command("apply", "Apply all pending migrations to the target database in order. Each migration runs inside its own transaction with advisory locking to prevent concurrent execution. Non-transactional operations like CREATE INDEX CONCURRENTLY execute outside transactions automatically. Use --dry-run to preview the SQL without executing.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate apply")
				return strictcli.Exit(1)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			dryRun := kwargs["dry_run"].(bool)

			ctx := context.Background()
			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			lockTimeout := cfg.Migrate.LockTimeout

			// Chain-mode project: route through the on-disk chain (path-finder +
			// self-contained renderer). Legacy-mode projects keep the semver path.
			if migrate.IsChainMode(dir) {
				return strictcli.Exit(handleMigrateApplyChain(ctx, conn, dbURL, dir, lockTimeout, dryRun, quiet))
			}

			if dryRun {
				return strictcli.Exit(handleMigrateApplyDryRun(ctx, conn, dir, quiet))
			}

			applied, err := migrate.Apply(ctx, conn, dir, lockTimeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				if len(applied) > 0 {
					fmt.Fprintf(os.Stderr, "Applied before failure: %v\n", applied)
				}
				return strictcli.Exit(1)
			}

			if len(applied) == 0 {
				if !quiet {
					fmt.Println("No pending migrations.")
				}
				return strictcli.Exit(0)
			}

			if !quiet {
				fmt.Printf("Applied %d migration(s):\n", len(applied))
				for _, v := range applied {
					fmt.Printf("  - %s\n", v)
				}
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
			strictcli.BoolFlag("dry-run", "Preview the migration SQL statements without executing", strictcli.Default(false)),
		),
	)
}

// handleMigrateApplyChain applies (or previews, with dryRun) an on-disk chain
// project: the path-finder chooses the edges from the database's chain position,
// and each edge's ops render through the self-contained renderer. dry-run prints
// the chosen edges and their SQL without executing.
func handleMigrateApplyChain(ctx context.Context, conn *pgx.Conn, dbURL, dir, lockTimeout string, dryRun, quiet bool) int {
	p, err := migrate.OpenChainProject(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if dryRun {
		plans, err := migrate.PlanApplyChain(ctx, conn, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(plans) == 0 {
			if !quiet {
				fmt.Println("No pending migrations.")
			}
			return 0
		}
		for i, plan := range plans {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("-- Edge: %s %s\n", plan.Edge.ID()[:12], plan.Edge.Slug)
			for _, stmt := range plan.SQL {
				if stmt != "" {
					fmt.Println(stmt)
				}
			}
		}
		return 0
	}

	applied, err := migrate.ApplyChain(ctx, conn, p, dbURL, lockTimeout, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "Applied before failure: %v\n", applied)
		}
		return 1
	}
	if len(applied) == 0 {
		if !quiet {
			fmt.Println("No pending migrations.")
		}
		return 0
	}
	if !quiet {
		fmt.Printf("Applied %d edge(s):\n", len(applied))
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

func registerMigrateRollbackCmd(g *strictcli.Group) {
	g.Command("rollback", "Rollback applied database migrations to a specified target version. Executes down migration SQL in reverse application order with advisory locking. Multi-step rollbacks verify reversibility of all steps before starting. The target version is exclusive, meaning that version stays applied after rollback completes.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate rollback")
				return strictcli.Exit(1)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			lockTimeout := cfg.Migrate.LockTimeout

			ctx := context.Background()
			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			toVersion := kwargs["to"].(string)

			// Chain-mode project: journal-driven rollback (roadmap 5.6). Reverses
			// recorded down-ops in reverse journal order; --to is a target REVISION.
			if migrate.IsChainMode(dir) {
				return strictcli.Exit(handleMigrateRollbackChain(ctx, conn, dir, toVersion, lockTimeout, quiet))
			}

			if toVersion != "" {
				rolledBack, err := migrate.RollbackTo(ctx, conn, dir, toVersion, lockTimeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					if len(rolledBack) > 0 {
						fmt.Fprintf(os.Stderr, "Rolled back before failure: %v\n", rolledBack)
					}
					return strictcli.Exit(1)
				}
				if !quiet {
					fmt.Printf("Rolled back %d migration(s) to %s:\n", len(rolledBack), toVersion)
					for _, v := range rolledBack {
						fmt.Printf("  - %s\n", v)
					}
				}
				return strictcli.Exit(0)
			}

			version, err := migrate.Rollback(ctx, conn, dir, lockTimeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			if !quiet {
				fmt.Printf("Rolled back: %s\n", version)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
			strictcli.StringFlag("to", "Target version to rollback to (exclusive -- this version stays applied)", strictcli.Default("")),
		),
	)
}

// handleMigrateRollbackChain reverses applied chain edges via the journal-driven
// path (roadmap 5.6). toRevision is empty for a single-step rollback or a target
// revision string for `rollback --to`.
func handleMigrateRollbackChain(ctx context.Context, conn *pgx.Conn, dir, toRevision, lockTimeout string, quiet bool) int {
	p, err := migrate.OpenChainProject(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	rolled, err := migrate.RollbackChain(ctx, conn, p, toRevision, lockTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if len(rolled) > 0 {
			fmt.Fprintf(os.Stderr, "Rolled back before failure: %v\n", rolled)
		}
		return 1
	}
	if len(rolled) == 0 {
		if !quiet {
			fmt.Println("No applied edges to roll back.")
		}
		return 0
	}
	if !quiet {
		fmt.Printf("Rolled back %d edge(s):\n", len(rolled))
		for _, v := range rolled {
			fmt.Printf("  - %s\n", v)
		}
	}
	return 0
}

func registerMigrateStatusCmd(g *strictcli.Group) {
	g.Command("status", "Show which migrations have been applied to the target database and which are still pending. Reads the migration tracking table and compares it with the migrations directory to display version numbers, applied timestamps, and current execution status for each migration file.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate status")
				return strictcli.Exit(1)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			ctx := context.Background()
			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			// Mode check FIRST. A chain-mode project uses chain-aware status, a
			// pure READ that NEVER creates any managed structure (the legacy
			// handler resurrected the dropped pgdesign_migrations table on an
			// upgraded database by calling EnsureMigrationsTable).
			if migrate.IsChainMode(dir) {
				p, err := migrate.OpenChainProject(dir)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return strictcli.Exit(1)
				}
				st, err := migrate.ComputeChainStatus(ctx, conn, p)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return strictcli.Exit(1)
				}
				printChainStatus(st)
				return strictcli.Exit(0)
			}

			// Legacy-mode status. Refuse to create a legacy tracking table on a
			// database that is already on the chain (an upgraded DB paired with a
			// legacy-format migrations directory) — that would resurrect the table.
			if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			if chainExists, err := migrate.ChainStructuresExist(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			} else if chainExists {
				fmt.Fprintf(os.Stderr, "error: this database is on the migration chain but %s is a legacy-format directory (no chain/ subdir) — refusing to create a legacy tracking table; regenerate the chain or point --dir at the chain project\n", dir)
				return strictcli.Exit(1)
			}

			if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			applied, err := migrate.AppliedVersions(ctx, conn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			appliedSet := make(map[string]bool, len(applied))
			for _, v := range applied {
				appliedSet[v] = true
			}

			entries, readErr := os.ReadDir(dir)
			if readErr != nil && !os.IsNotExist(readErr) {
				fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", readErr)
				return strictcli.Exit(1)
			}

			var allVersions []string
			for _, e := range entries {
				if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
					continue
				}
				v := e.Name()[:len(e.Name())-5]
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

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
		),
	)
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

// printChainStatus renders chain-aware status: the chain position, the confirmed
// edges (from the applied-migrations view), and the pending edges (path-finder).
func printChainStatus(st *migrate.ChainStatus) {
	fmt.Printf("Chain position: %s\n", displayRevString(st.Position))
	fmt.Printf("Applied migrations: %d\n", len(st.Applied))
	for _, a := range st.Applied {
		fmt.Printf("  [applied] %s\n", a.Version)
	}
	if len(st.Pending) == 0 {
		fmt.Println("Up to date (no pending edges).")
		return
	}
	for _, e := range st.Pending {
		fmt.Printf("  [pending] %s %s\n", e.ID()[:12], e.Slug)
	}
	fmt.Printf("\n%d pending edge(s).\n", len(st.Pending))
}

// displayRevString renders a possibly-empty (genesis) revision for CLI output.
func displayRevString(s string) string {
	if s == "" {
		return "genesis"
	}
	return s
}

func registerMigrateSquashCmd(g *strictcli.Group) {
	g.Command("squash", "Consolidate a range of sequential migrations. In chain mode (a migrations/chain/ project) squash mints a CONSOLIDATION EDGE whose op-list is the ordered concatenation of the range, retiring the superseded originals intact to migrations/archive/ (never a rewrite) so mid-range databases resume via the path-finder; --from/--to are revision-or-edge references. In legacy (semver-TOML) mode squash concatenates the range into one combined migration file. --db is required.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			from := kwargs["from"].(string)
			if from == "" {
				fmt.Fprintln(os.Stderr, "error: --from is required")
				return strictcli.Exit(1)
			}
			to := kwargs["to"].(string)
			if to == "" {
				fmt.Fprintln(os.Stderr, "error: --to is required")
				return strictcli.Exit(1)
			}

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate squash (the M200 applied-version safety check is mandatory; offline squash is not permitted)")
				return strictcli.Exit(1)
			}

			ctx := context.Background()
			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect for safety check: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			// Chain mode: consolidation edge (never a rewrite). The M200 applied-check
			// dies here — originals archive intact and mid-range DBs resume via the
			// path-finder, so applied state is irrelevant.
			if migrate.IsChainMode(dir) {
				p, err := migrate.OpenChainProject(dir)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return strictcli.Exit(1)
				}
				slug := ""
				if s := kwargsOptString(kwargs, "slug"); s != nil {
					slug = *s
				}
				res, err := migrate.SquashChain(p, from, to, slug)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return strictcli.Exit(1)
				}
				if !quiet {
					fmt.Printf("Consolidated %d edges into %s\n", len(res.SupersededIDs), res.ConsolidationFile)
					fmt.Printf("  From: %s\n", displayRevString(res.FromRevision))
					fmt.Printf("  To:   %s\n", res.ToRevision)
					fmt.Printf("  Ops: %d\n", res.OpCount)
					fmt.Printf("  Down form: %s\n", res.DownForm)
					fmt.Printf("  Archived %d originals to %s\n", len(res.ArchivedFiles), filepath.Join(dir, "archive"))
				}
				return strictcli.Exit(0)
			}

			// Legacy (semver-TOML) mode: concatenate into one combined file.
			result, err := migrate.SquashMigrations(ctx, conn, dir, from, to)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			outputPath := migrate.OutputPath(dir, to)
			tmpPath := outputPath + ".squash-tmp"
			if err := migrate.WriteMigrationFile(tmpPath, result.Squashed); err != nil {
				fmt.Fprintf(os.Stderr, "error: write squashed migration: %v\n", err)
				return strictcli.Exit(1)
			}

			args := []string{"delete", "--description", fmt.Sprintf("Squashed into %s (from %s to %s)", to, from, to)}
			args = append(args, result.OriginalPaths...)
			cmd := exec.Command("saferm", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				os.Remove(tmpPath)
				fmt.Fprintf(os.Stderr, "error: archive originals: %v\n", err)
				return strictcli.Exit(1)
			}

			if err := os.Rename(tmpPath, outputPath); err != nil {
				fmt.Fprintf(os.Stderr, "error: rename squashed migration: %v\n", err)
				return strictcli.Exit(1)
			}

			if !quiet {
				fmt.Printf("Squashed %d migrations into %s\n", result.OriginalCount, outputPath)
				fmt.Printf("  Description: %s\n", result.Squashed.Description)
				fmt.Printf("  DDL ops: %d\n", len(result.Squashed.DDLOps))
				fmt.Printf("  DML ops: %d\n", len(result.Squashed.DMLOps))
			}

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("from", "Start of the squash range: a semver version (legacy) or a revision-or-edge reference (chain mode; 'genesis', a revision string, or a live edge-id prefix)"),
			strictcli.StringFlag("to", "End of the squash range: a semver version (legacy) or a revision-or-edge reference (chain mode)"),
			strictcli.StringFlag("slug", "Display slug for the consolidation edge (chain mode; auto-derived from endpoint hashes when omitted)", strictcli.Default(nil)),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
			strictcli.StringFlag("db", "PostgreSQL connection URL (REQUIRED); the pre-upgrade guard runs against it (legacy mode also runs the M200 applied-version check)", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
		),
	)
}

func registerMigrateTestCmd(g *strictcli.Group) {
	g.Command("test", "Test migrations by applying them against a staging database to verify correctness before production deployment. With --shadow mode, replays all migrations into a fresh database and diffs the result against the TOML schema to catch drift between migration files and schema definitions.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate test")
				return strictcli.Exit(1)
			}

			shadow := kwargs["shadow"].(bool)
			dirFlag := kwargsOptString(kwargs, "dir")
			timeout := kwargs["timeout"].(int)
			paths := kwargsStrSlice(kwargs["path"])

			if shadow {
				return strictcli.Exit(runMigrateTestShadow(dbURL, dirFlag, timeout, paths, cfgOverride, quiet))
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(dirFlag, string(cfg.Project.MigrationsDir))

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			if err := migrate.EnsureMigrationsTable(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			applied, err := migrate.AppliedVersions(ctx, conn)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			appliedSet := make(map[string]bool, len(applied))
			for _, v := range applied {
				appliedSet[v] = true
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: read migrations dir: %v\n", err)
				return strictcli.Exit(1)
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
				return strictcli.Exit(0)
			}

			if !quiet {
				fmt.Printf("Testing %d pending migration(s)...\n", len(pending))
			}

			totalStart := time.Now()

			tx, err := conn.Begin(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: begin transaction: %v\n", err)
				return strictcli.Exit(1)
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
				return strictcli.Exit(1)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the staging test database", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
			strictcli.IntFlag("timeout", "Maximum time in seconds before the test run is aborted", strictcli.Default(60)),
			strictcli.BoolFlag("shadow", "Test by replaying migrations into a shadow database and diffing against TOML schema", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Schema file(s) or directory (required with --shadow)", strictcli.Variadic(), strictcli.ArgRequired(false)),
		),
	)
}

// runMigrateTestShadow implements --shadow mode. dirFlag is the raw --dir flag
// value (nil when unset) so an explicit "--dir migrations" is distinguishable
// from the default.
func runMigrateTestShadow(dbURL string, dirFlag *string, timeout int, paths []string, configOverride *string, quiet bool) int {
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "error: schema path is required for --shadow mode")
		return 1
	}

	schema, _, exitCode := parseAndBuild(configOverride, paths)
	if exitCode != 0 {
		return exitCode
	}

	cfg, cfgErr := loadProjectConfig(configOverride, paths[0])
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return 1
	}

	dir := resolveMigrationsDir(dirFlag, string(cfg.Project.MigrationsDir))

	schemaNames := modelSchemaNames(schema)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
		return 1
	}
	defer conn.Close(ctx)

	// Pre-upgrade guard FIRST — before any shadow database is created — so a
	// pre-upgrade database is refused (naming `migrate upgrade`) exactly like
	// every other --db subcommand, rather than silently proceeding.
	if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

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

	shadowName := fmt.Sprintf("pgdesign_shadow_%d", time.Now().Unix())
	if !quiet {
		fmt.Printf("Creating shadow database: %s\n", shadowName)
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", shadowName)); err != nil {
		fmt.Fprintf(os.Stderr, "error: create shadow database: %v\n", err)
		return 1
	}

	defer func() {
		cleanCtx := context.Background()
		if _, err := conn.Exec(cleanCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", shadowName)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to drop shadow database %s: %v\n", shadowName, err)
			fmt.Fprintf(os.Stderr, "  Clean up manually: DROP DATABASE %s;\n", shadowName)
		} else if !quiet {
			fmt.Printf("Dropped shadow database: %s\n", shadowName)
		}
	}()

	shadowURL, err := buildShadowURL(dbURL, shadowName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build shadow URL: %v\n", err)
		return 1
	}

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
	shadowConn.Close(ctx)
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

	if collErr := diff.CheckTruncationCollisions(schema); collErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", collErr)
		return 1
	}
	// actual is the introspected shadow DB (registry-absent); migrationDiff
	// applies the live PG version before diffing via DiffLive.
	d := migrationDiff(schema, actual)
	if d.IsEmpty() {
		if !quiet {
			fmt.Println("\nResult: PASS")
			fmt.Println("Shadow database matches desired schema exactly.")
		}
		return 0
	}

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

func registerMigrateUpgradeCmd(g *strictcli.Group) {
	g.Command("upgrade", "One-time adoption of a legacy (semver-TOML) database onto the on-disk chain. Verifies the schema TOML matches the live database exactly (refusing to stamp over drift), folds the existing pgdesign_migrations rows into the chain journal, writes the content-addressed prefix edge, and stamps this database's upgrade boundary in a single transaction. Requires a clean working tree for the schema files when inside a git repository. Run once per database; a fresh database uses `migrate apply` directly.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate upgrade")
				return strictcli.Exit(1)
			}

			paths := kwargsStrSlice(kwargs["path"])
			desired, _, exitCode := parseAndBuild(cfgOverride, paths)
			if exitCode != 0 {
				return strictcli.Exit(exitCode)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, paths[0])
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			schemaNames := modelSchemaNames(desired)

			schemaFiles, err := resolveSchemaPaths(cfgOverride, paths)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			ctx := context.Background()
			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			// Introspect the live database for the reconcile, resolving the live PG
			// version onto the desired model so an unpinned pg_version does not
			// register as spurious drift (mirrors migrate generate/plan).
			actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: introspect: %v\n", err)
				return strictcli.Exit(1)
			}
			if len(diags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
			}
			if diagnostic.Diagnostics(diags).HasErrors() {
				return strictcli.Exit(1)
			}
			applyLivePGVersion(desired, actual.PGVersion)

			p, err := migrate.OpenChainProject(dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			// LIVE ROUND-TRIP NORMALIZATION for the reconcile (best-effort).
			var ln diff.LiveNormalizer
			if n, nerr := livenorm.New(ctx, dbURL); nerr == nil {
				defer n.Close()
				ln = n
			}

			report, err := migrate.Upgrade(ctx, conn, p, desired, actual, ln, dir, schemaFiles, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			if report.AlreadyUpgraded {
				if !quiet {
					fmt.Println("Already upgraded: this database is already on the chain (chain_position present). Nothing to do.")
				}
				return strictcli.Exit(0)
			}

			if len(report.Amnesty) > 0 {
				fmt.Fprintf(os.Stderr, "CHECKSUM AMNESTY: %d migration file(s) differ from their recorded checksum (historical post-apply edits; the fold proceeded by content):\n", len(report.Amnesty))
				for _, a := range report.Amnesty {
					fmt.Fprintf(os.Stderr, "  %s\n    recorded: %s\n    actual:   %s\n", a.File, a.Recorded, a.Actual)
				}
			}

			if !quiet {
				fmt.Printf("Upgraded to the on-disk chain.\n")
				fmt.Printf("  Boundary revision: %s\n", report.Boundary)
				fmt.Printf("  Prefix edge:       %s\n", report.PrefixEdgeFile)
				fmt.Printf("  Folded rows:       %d\n", report.PrefixRows)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the database to upgrade", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory containing them", strictcli.Variadic()),
		),
	)
}

func registerMigrateBaselineCmd(g *strictcli.Group) {
	g.Command("baseline", "Mark an existing database as being at a specific migration version without executing any migration SQL. Use this when adopting pgdesign migrations for a database whose schema was already created by other means. Idempotent: re-running with the same version succeeds; a different version errors.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for migrate baseline")
				return strictcli.Exit(1)
			}

			version := kwargs["version"].(string)
			if version == "" {
				fmt.Fprintln(os.Stderr, "error: --version is required for migrate baseline")
				return strictcli.Exit(1)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			dir := resolveMigrationsDir(kwargsOptString(kwargs, "dir"), string(cfg.Project.MigrationsDir))

			description := kwargs["description"].(string)

			ctx := context.Background()
			conn, err := pgx.Connect(ctx, dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
				return strictcli.Exit(1)
			}
			defer conn.Close(ctx)

			if err := migrate.GuardNotPreUpgrade(ctx, conn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			if err := migrate.Baseline(ctx, conn, dir, version, description); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			if !quiet {
				fmt.Printf("Baseline recorded: %s (%s)\n", version, description)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("dir", "Directory containing migration files to read or write (defaults to project config migrations_dir, else migrations)", strictcli.Default(nil)),
			strictcli.StringFlag("version", "Version label for the baseline record"),
			strictcli.StringFlag("description", "Human-readable description", strictcli.Default("Initial baseline")),
		),
	)
}
