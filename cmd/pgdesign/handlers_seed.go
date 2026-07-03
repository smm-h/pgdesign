package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/seed"
	"github.com/smm-h/strictcli/go/strictcli"
)

type seedHandler struct {
	Rows   int     `cli:"rows" help:"Number of rows to generate per table in the schema" default:"10"`
	Seed   *int    `cli:"seed" help:"Random number generator seed for deterministic output"`
	Output *string `cli:"output" help:"Write output to a file at this path instead of stdout"`
	Apply  bool    `cli:"apply" help:"Insert generated seed data directly into the database" default:"false"`
	DB     *string `cli:"db" help:"PostgreSQL connection URL, required when using --apply"`
	// Schemas is registered but not currently consumed by seed generation;
	// kept for CLI schema compatibility.
	Schemas []string `cli:"schema" help:"PostgreSQL schema name to filter seed generation to"`
	Format  string   `cli:"format" help:"SQL output format for generated seed data statements" default:"insert" choices:"insert,copy"`
	Clean   bool     `cli:"clean" help:"Emit TRUNCATE CASCADE statements before inserting seeds" default:"false"`
	Mode    string   `cli:"mode" help:"Data generation strategy: normal values or edge-cases" default:"normal" choices:"normal,edge-cases"`
	Paths   []string `arg:"path" help:"Path to TOML schema file(s) or directory for seed generation" variadic:"true"`
}

func (h *seedHandler) Run(cliCtx *strictcli.Context) int {
	g := strictcli.Globals[Globals](cliCtx)

	paths := h.Paths
	schema, _, exitCode := parseAndBuild(g.Config, paths)
	if exitCode != 0 {
		return exitCode
	}

	rows := h.Rows
	quiet := g.Quiet
	apply := h.Apply
	var outputPath string
	if h.Output != nil {
		outputPath = *h.Output
	}
	var dbURL string
	if h.DB != nil {
		dbURL = *h.DB
	}
	format := h.Format
	clean := h.Clean
	mode := h.Mode

	if apply && dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required when using --apply")
		return 1
	}

	var rngSeed int64
	if h.Seed != nil {
		rngSeed = int64(*h.Seed)
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
	sql, seedDiags := seed.Generate(schema, rows, rng, cfg)
	if seedDiags.HasErrors() {
		for _, d := range seedDiags.Errors() {
			fmt.Fprintf(os.Stderr, "error: %s\n", d.Message)
		}
		return 1
	}
	if !quiet {
		for _, d := range seedDiags.Warnings() {
			fmt.Fprintf(os.Stderr, "warning: %s\n", d.Message)
		}
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(sql), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: write output: %v\n", err)
			return 1
		}
		if !quiet {
			fmt.Printf("Seed data written to %s\n", outputPath)
		}
	} else if !apply {
		fmt.Print(sql)
	}

	if apply {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: connect: %v\n", err)
			return 1
		}
		defer conn.Close(ctx)

		tx, err := conn.Begin(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: begin transaction: %v\n", err)
			return 1
		}
		defer tx.Rollback(ctx)

		if _, err := tx.Exec(ctx, sql); err != nil {
			fmt.Fprintf(os.Stderr, "error: execute seed data: %v\n", err)
			return 1
		}

		if err := tx.Commit(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "error: commit: %v\n", err)
			return 1
		}

		if !quiet {
			fmt.Println("Seed data applied successfully.")
		}
	}

	return 0
}
