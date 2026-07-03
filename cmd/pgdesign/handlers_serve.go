package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/serve"
	"github.com/smm-h/strictcli/go/strictcli"
)

type serveHandler struct {
	DB      string   `cli:"db" help:"PostgreSQL connection URL for the target database server"`
	Port    int      `cli:"port" help:"TCP port number for the HTTP API server to listen on" default:"8080"`
	Schemas []string `cli:"schema" help:"PostgreSQL schema name to serve via the API (repeatable)"`
	// Timeout is registered but not currently consumed by the server
	// implementation; kept for CLI schema compatibility.
	Timeout int `cli:"timeout" help:"Maximum time in seconds for each HTTP request to complete" default:"30"`
}

func (h *serveHandler) Run(ctx *strictcli.Context) int {
	g := strictcli.Globals[Globals](ctx)

	dbURL := h.DB
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for serve")
		return 1
	}

	// Load config for default schema names and migrations dir.
	cfg, cfgErr := loadProjectConfig(g.Config, ".")
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return 1
	}

	port := h.Port

	// Collect schema names from repeatable --schema flag.
	schemaNames := h.Schemas
	if len(schemaNames) == 0 {
		schemaNames = configSchemaNames(cfg)
	}
	if len(schemaNames) == 0 {
		schemaNames = []string{"public"}
	}

	migrationsDir := "migrations"
	if cfg.Project.MigrationsDir != "" {
		migrationsDir = string(cfg.Project.MigrationsDir)
	}

	poolCfg := serve.PoolConfig{
		MaxConns: cfg.Database.PoolMaxConns,
		MinConns: cfg.Database.PoolMinConns,
	}
	srv, err := serve.New(dbURL, schemaNames, migrationsDir, poolCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer srv.Close()

	addr := fmt.Sprintf(":%d", port)
	if !g.Quiet {
		fmt.Printf("pgdesign serving on http://localhost:%d\n", port)
	}
	if err := srv.ListenAndServe(addr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
