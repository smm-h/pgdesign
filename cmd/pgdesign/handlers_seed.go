package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/seed"
	"github.com/smm-h/pgdesign/pkg/genkit"
	"github.com/smm-h/strictcli/go/strictcli"
)

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
