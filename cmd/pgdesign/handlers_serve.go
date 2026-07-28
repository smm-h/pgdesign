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

			cfg, cfgErr := loadProjectConfig(cfgOverride, ".")
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

			port := kwargs["port"].(int)
			bind := kwargs["bind"].(string)

			// Namespaces come from the explicit --schema flag; absent, default to
			// public. (The config lists schema FILE paths, not PG namespace names,
			// so it never correctly drove introspection namespaces.)
			schemaNames := kwargsStrSlice(kwargs["schema"])
			if len(schemaNames) == 0 {
				schemaNames = []string{"public"}
			}

			// serve has no --dir flag; the config's migrations_dir (else the
			// "migrations" default) applies via the shared resolver.
			migrationsDir := resolveMigrationsDir(nil, string(cfg.Project.MigrationsDir))

			// Mode is an EXPLICIT choice (never a silent runtime fallback): --db
			// present selects database mode (introspect + stats/migrations/diff/
			// audit); --db absent selects DB-free project mode, which compiles the
			// project from disk and serves it. In project mode the database-backed
			// endpoints degrade with an explicit 503.
			var srv *serve.Server
			dbURL := kwargsDBURL(kwargs)
			if dbURL != "" {
				poolCfg := serve.PoolConfig{
					MaxConns: cfg.Database.PoolMaxConns,
					MinConns: cfg.Database.PoolMinConns,
				}
				var err error
				srv, err = serve.New(dbURL, schemaNames, migrationsDir, poolCfg)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return strictcli.Exit(1)
				}
			} else {
				// Project mode: compile the project from the current directory through
				// the shared loader (registry + config extensions + imports +
				// pg_version), the same path build/codegen/revise use.
				schema, reg, exitCode := parseAndBuild(cfgOverride, []string{"."})
				if exitCode != 0 {
					return strictcli.Exit(exitCode)
				}
				srv = serve.NewProject(schema, reg, nil, schemaNames, migrationsDir)
			}
			defer srv.Close()

			addr := fmt.Sprintf("%s:%d", bind, port)
			if !quiet {
				mode := "project (DB-free)"
				if dbURL != "" {
					mode = "database"
				}
				fmt.Printf("pgdesign serving on http://%s (%s mode)\n", addr, mode)
			}
			if err := srv.ListenAndServe(addr); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server (omit for DB-free project mode)", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.IntFlag("port", "TCP port number for the HTTP API server to listen on", strictcli.Default(8080)),
			strictcli.StringFlag("bind", "Network interface address to bind the HTTP server to. Defaults to 127.0.0.1 (loopback only). WARNING: the server has NO AUTHENTICATION; binding to a non-loopback address (e.g. 0.0.0.0) exposes the schema, database statistics, and diff endpoints to anyone who can reach that address.", strictcli.Default("127.0.0.1")),
			strictcli.StringFlag("schema", "PostgreSQL schema name to serve via the API (repeatable)", strictcli.Repeatable(), strictcli.Unique(true)),
			strictcli.IntFlag("timeout", "Maximum time in seconds for each HTTP request to complete", strictcli.Default(30)),
		),
	)
}
