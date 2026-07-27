package main

import (
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/serve"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerServeCmd(app *strictcli.App) {
	app.Command("serve", "Start the pgdesign HTTP API server and web interface",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := kwargsQuiet(kwargs)
			cfgOverride := kwargsConfigOverride(kwargs)

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for serve")
				return strictcli.Exit(1)
			}

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			port := kwargs["port"].(int)

			schemaNames := kwargsStrSlice(kwargs["schema"])
			if len(schemaNames) == 0 {
				schemaNames = configSchemaNames(cfg)
			}
			if len(schemaNames) == 0 {
				schemaNames = []string{"public"}
			}

			// serve has no --dir flag; the config's migrations_dir (else the
			// "migrations" default) applies via the shared resolver.
			migrationsDir := resolveMigrationsDir(nil, string(cfg.Project.MigrationsDir))

			poolCfg := serve.PoolConfig{
				MaxConns: cfg.Database.PoolMaxConns,
				MinConns: cfg.Database.PoolMinConns,
			}
			srv, err := serve.New(dbURL, schemaNames, migrationsDir, poolCfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			defer srv.Close()

			addr := fmt.Sprintf(":%d", port)
			if !quiet {
				fmt.Printf("pgdesign serving on http://localhost:%d\n", port)
			}
			if err := srv.ListenAndServe(addr); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.IntFlag("port", "TCP port number for the HTTP API server to listen on", strictcli.Default(8080)),
			strictcli.StringFlag("schema", "PostgreSQL schema name to serve via the API (repeatable)", strictcli.Repeatable(), strictcli.Unique(true)),
			strictcli.IntFlag("timeout", "Maximum time in seconds for each HTTP request to complete", strictcli.Default(30)),
		),
	)
}
