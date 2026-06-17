package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/seed"
)

func handleSeed(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	rows := kwargs["rows"].(int)
	quiet := kwargs["quiet"].(bool)
	apply := kwargs["apply"].(bool)
	outputPath, _ := kwargs["output"].(string)
	dbURL, _ := kwargs["db"].(string)

	if apply && dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required when using --apply")
		return 1
	}

	var rngSeed int64
	if seedVal, hasSeed := kwargs["seed"].(int); hasSeed {
		rngSeed = int64(seedVal)
	} else {
		rngSeed = time.Now().UnixNano()
		if !quiet {
			fmt.Fprintf(os.Stderr, "seed: %d\n", rngSeed)
		}
	}
	rng := rand.New(rand.NewSource(rngSeed))
	sql, seedDiags := seed.Generate(schema, rows, rng, nil)
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
