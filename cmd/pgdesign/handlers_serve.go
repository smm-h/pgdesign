package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/serve"
)

func handleServe(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for serve")
		return 1
	}

	// Load config for default schema names and migrations dir.
	cfg := loadProjectConfig(".")

	port := kwargs["port"].(int)

	// Collect schema names from repeatable --schema flag.
	var schemaNames []string
	if raw, ok := kwargs["schema"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				schemaNames = append(schemaNames, s)
			}
		}
	}
	if len(schemaNames) == 0 {
		schemaNames = configSchemaNames(cfg)
	}
	if len(schemaNames) == 0 {
		schemaNames = []string{"public"}
	}

	migrationsDir := "migrations"
	if cfg.Project.MigrationsDir != "" {
		migrationsDir = cfg.Project.MigrationsDir
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
	if !kwargs["quiet"].(bool) {
		fmt.Printf("pgdesign serving on http://localhost:%d\n", port)
	}
	if err := srv.ListenAndServe(addr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
