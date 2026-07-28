package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/seed"
	"github.com/smm-h/pgdesign/pkg/genkit"
	"github.com/smm-h/strictcli/go/strictcli"
)

// schemaHasImportedFK reports whether any owned table declares an imported FK
// (resolved through an import alias). Used to decide whether the seed command
// needs to load tier-1 real-key pools.
func schemaHasImportedFK(schema *model.Schema) bool {
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			if fk.RefAlias != "" {
				return true
			}
		}
	}
	return false
}

// loadImportedFKPools reads the deterministic, sorted real-key pools for every
// imported FK target from the live database (roadmap 7.4 tier 1). Keys are cast to
// text and quoted as SQL literals — PostgreSQL coerces the unknown-typed literal to
// the local FK column's type on insertion, so one formatting rule covers every
// referenced type. Pools are keyed by "<schema>.<table>.<column>" to match
// seed.SeedConfig.ImportedFKPools.
func loadImportedFKPools(ctx context.Context, conn *pgx.Conn, schema *model.Schema) (map[string][]string, error) {
	pools := map[string][]string{}
	seen := map[string]bool{}
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			if fk.RefAlias == "" {
				continue
			}
			for i := range fk.Columns {
				refCol := ""
				if i < len(fk.RefColumns) {
					refCol = fk.RefColumns[i]
				}
				key := fk.RefSchema + "." + fk.RefTable + "." + refCol
				if seen[key] {
					continue
				}
				seen[key] = true
				colIdent := pgx.Identifier{refCol}.Sanitize()
				tblIdent := pgx.Identifier{fk.RefSchema, fk.RefTable}.Sanitize()
				q := fmt.Sprintf("SELECT DISTINCT %s::text FROM %s WHERE %s IS NOT NULL ORDER BY %s::text",
					colIdent, tblIdent, colIdent, colIdent)
				rows, err := conn.Query(ctx, q)
				if err != nil {
					return nil, fmt.Errorf("querying imported pool %s: %w", key, err)
				}
				for rows.Next() {
					var v string
					if err := rows.Scan(&v); err != nil {
						rows.Close()
						return nil, fmt.Errorf("scanning imported pool %s: %w", key, err)
					}
					pools[key] = append(pools[key], "'"+strings.ReplaceAll(v, "'", "''")+"'")
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return nil, fmt.Errorf("reading imported pool %s: %w", key, err)
				}
			}
		}
	}
	return pools, nil
}

func registerSeedCmd(app *strictcli.App) {
	app.Command("seed", "Generate type-aware test data for all schema tables",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			paths := kwargsStrSlice(kwargs["path"])
			schema, _, exitCode := parseAndBuild(cfgOverride, paths)
			if exitCode != 0 {
				return strictcli.Exit(exitCode)
			}

			rows := kwargs["rows"].(int)
			apply := kwargs["apply"].(bool)
			outputPath := ""
			if v := kwargsOptString(kwargs, "output"); v != nil {
				outputPath = *v
			}
			dbURL := ""
			if v := kwargsOptString(kwargs, "db"); v != nil {
				dbURL = *v
			}
			format := kwargs["format"].(string)
			clean := kwargs["clean"].(bool)
			mode := kwargs["mode"].(string)

			if apply && dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required when using --apply")
				return strictcli.Exit(1)
			}

			var rngSeed int64
			if seedVal := kwargsOptInt(kwargs, "seed"); seedVal != nil {
				rngSeed = int64(*seedVal)
			} else {
				rngSeed = time.Now().UnixNano()
				if !quiet {
					fmt.Fprintf(os.Stderr, "seed: %d\n", rngSeed)
				}
			}
			rng := rand.New(rand.NewSource(rngSeed))
			cfg := &seed.SeedConfig{
				Format: format,
				Clean:  clean,
				Mode:   mode,
				Apply:  apply,
			}

			// Imported-FK seed tiers (roadmap 7.4): when a database is supplied, load
			// real-key pools from the live imported tables (tier 1). Without --db, seed
			// runs offline (tier 2/3) — the tier selection is an explicit mode choice
			// (the presence of --db IS the choice), never a runtime fallback.
			if dbURL != "" && schemaHasImportedFK(schema) {
				bgCtx := context.Background()
				conn, err := pgx.Connect(bgCtx, dbURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: connect for imported-FK pools: %v\n", err)
					return strictcli.Exit(1)
				}
				pools, err := loadImportedFKPools(bgCtx, conn, schema)
				conn.Close(bgCtx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: load imported-FK pools: %v\n", err)
					return strictcli.Exit(1)
				}
				cfg.DBAvailable = true
				cfg.ImportedFKPools = pools
			}
			// Thread the full-project revision so seed's provenance banner names
			// the producing revision (roadmap 4.2). schema is the TOML-built
			// model: registry-present class (L7). Reset after generation.
			projectRev, revErr := rev.Compute(schema, rev.RegistryPresent)
			if revErr != nil {
				fmt.Fprintf(os.Stderr, "error: compute project revision: %v\n", revErr)
				return strictcli.Exit(1)
			}
			if err := genkit.SetRevision(projectRev.String()); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			defer genkit.SetRevision("")

			sql, seedDiags := seed.Generate(schema, rows, rng, cfg)
			if seedDiags.HasErrors() {
				for _, d := range seedDiags.Errors() {
					fmt.Fprintf(os.Stderr, "error: %s\n", d.Message)
				}
				return strictcli.Exit(1)
			}
			if !quiet {
				for _, d := range seedDiags.Warnings() {
					fmt.Fprintf(os.Stderr, "warning: %s\n", d.Message)
				}
			}

			if outputPath != "" {
				if err := os.WriteFile(outputPath, []byte(sql), 0o644); err != nil {
					fmt.Fprintf(os.Stderr, "error: write output: %v\n", err)
					return strictcli.Exit(1)
				}
				if !quiet {
					fmt.Printf("Seed data written to %s\n", outputPath)
				}
			} else if !apply {
				fmt.Print(sql)
			}

			if apply {
				bgCtx := context.Background()
				conn, err := pgx.Connect(bgCtx, dbURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
					return strictcli.Exit(1)
				}
				defer conn.Close(bgCtx)

				tx, err := conn.Begin(bgCtx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: begin transaction: %v\n", err)
					return strictcli.Exit(1)
				}
				defer tx.Rollback(bgCtx)

				if _, err := tx.Exec(bgCtx, sql); err != nil {
					fmt.Fprintf(os.Stderr, "error: execute seed data: %v\n", err)
					return strictcli.Exit(1)
				}

				if err := tx.Commit(bgCtx); err != nil {
					fmt.Fprintf(os.Stderr, "error: commit: %v\n", err)
					return strictcli.Exit(1)
				}

				if !quiet {
					fmt.Println("Seed data applied successfully.")
				}
			}

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.IntFlag("rows", "Number of rows to generate per table in the schema", strictcli.Default(10)),
			strictcli.IntFlag("seed", "Random number generator seed for deterministic output", strictcli.Default(nil)),
			strictcli.StringFlag("output", "Write output to a file at this path instead of stdout", strictcli.Default(nil)),
			strictcli.BoolFlag("apply", "Insert generated seed data directly into the database", strictcli.Default(false)),
			strictcli.StringFlag("db", "PostgreSQL connection URL, required when using --apply", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("schema", "PostgreSQL schema name to filter seed generation to", strictcli.Repeatable(), strictcli.Unique(true)),
			strictcli.StringFlag("format", "SQL output format for generated seed data statements", strictcli.Default("insert"), strictcli.Choices("insert", "copy")),
			strictcli.BoolFlag("clean", "Emit TRUNCATE CASCADE statements before inserting seeds", strictcli.Default(false)),
			strictcli.StringFlag("mode", "Data generation strategy: normal values or edge-cases", strictcli.Default("normal"), strictcli.Choices("normal", "edge-cases")),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory for seed generation", strictcli.Variadic()),
		),
	)
}
