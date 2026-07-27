package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerTestdbSetupCmd(g *strictcli.Group) {
	g.Command("setup", "Create an ephemeral test database on the PostgreSQL server and apply the specified DDL schema to it. The database is created with a unique name containing a timestamp and random suffix to allow parallel test execution. Returns the connection URL for the new database.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for testdb setup")
				return strictcli.Exit(1)
			}

			ddlPath := kwargs["ddl"].(string)
			if ddlPath == "" {
				fmt.Fprintln(os.Stderr, "error: --ddl is required for testdb setup")
				return strictcli.Exit(1)
			}

			ddlFile, err := os.Open(ddlPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: open DDL file: %v\n", err)
				return strictcli.Exit(1)
			}
			defer ddlFile.Close()

			manager, err := testdb.NewManager(dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
				return strictcli.Exit(1)
			}

			ctx := context.Background()
			db, err := manager.Create(ctx, testdb.CreateOptions{DDL: ddlFile})
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: create ephemeral database: %v\n", err)
				return strictcli.Exit(1)
			}

			fmt.Println(db.URL)
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("ddl", "Path to the SQL DDL file to apply to the test database"),
		),
	)
}

func registerTestdbTeardownCmd(g *strictcli.Group) {
	g.Command("teardown", "Drop an ephemeral test database that was previously created by testdb setup. Terminates any remaining connections to the database before dropping it. Should be called in test cleanup to prevent orphaned databases from accumulating on the PostgreSQL server over time.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for testdb teardown")
				return strictcli.Exit(1)
			}

			u, err := url.Parse(dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: parse URL: %v\n", err)
				return strictcli.Exit(1)
			}
			dbName := strings.TrimPrefix(u.Path, "/")
			if dbName == "" {
				fmt.Fprintln(os.Stderr, "error: URL has no database name")
				return strictcli.Exit(1)
			}

			manager, err := testdb.NewManager(dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
				return strictcli.Exit(1)
			}

			ctx := context.Background()
			if err := manager.DropByName(ctx, dbName); err != nil {
				fmt.Fprintf(os.Stderr, "error: drop database %s: %v\n", dbName, err)
				return strictcli.Exit(1)
			}

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
		),
	)
}

func registerTestdbGCCmd(g *strictcli.Group) {
	g.Command("gc", "Drop orphaned test databases that were not properly torn down after test runs. Scans the PostgreSQL server for databases matching the pgdesign test naming pattern and removes those older than the specified duration. Useful for cleaning up after interrupted or failed test runs in CI and local development.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for testdb gc")
				return strictcli.Exit(1)
			}

			olderThanStr := kwargs["older_than"].(string)
			if olderThanStr == "" {
				fmt.Fprintln(os.Stderr, "error: --older-than is required for testdb gc")
				return strictcli.Exit(1)
			}

			olderThan, err := time.ParseDuration(olderThanStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: parse duration %q: %v\n", olderThanStr, err)
				return strictcli.Exit(1)
			}

			manager, err := testdb.NewManager(dbURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
				return strictcli.Exit(1)
			}

			ctx := context.Background()
			orphans, err := manager.ListOrphans(ctx, olderThan)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: list orphans: %v\n", err)
				return strictcli.Exit(1)
			}

			if len(orphans) == 0 {
				fmt.Fprintln(os.Stderr, "no orphaned databases found")
				return strictcli.Exit(0)
			}

			var failures int
			for _, orphan := range orphans {
				if err := manager.DropByName(ctx, orphan.Name); err != nil {
					fmt.Fprintf(os.Stderr, "error: drop %s: %v\n", orphan.Name, err)
					failures++
				} else {
					conns := ""
					if orphan.ActiveConnections != nil {
						conns = fmt.Sprintf(" (%d active connections)", *orphan.ActiveConnections)
					}
					fmt.Fprintf(os.Stderr, "  dropped %s%s\n", orphan.Name, conns)
				}
			}

			fmt.Fprintf(os.Stderr, "dropped %d databases, %d failures\n", len(orphans)-failures, failures)
			if failures > 0 {
				return strictcli.Exit(1)
			}
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("older-than", "Drop databases older than this duration (e.g., 2h, 30m)"),
		),
	)
}

func registerTestdbInitCmd(g *strictcli.Group) {
	g.Command("init", "Generate test database wrapper code for consumer projects that need to run integration tests against a pgdesign-managed schema. Produces language-specific helper modules with setup and teardown functions that create ephemeral databases, apply DDL, and clean up automatically after each test run.",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			cfgOverride := kwargsConfigOverride(kwargs)

			languages := kwargsStrSlice(kwargs["language"])
			if len(languages) == 0 {
				fmt.Fprintln(os.Stderr, "error: at least one --language is required")
				return strictcli.Exit(1)
			}

			supported := make(map[string]bool)
			for _, lang := range testdb.SupportedLanguages() {
				supported[lang] = true
			}
			for _, lang := range languages {
				if !supported[lang] {
					fmt.Fprintf(os.Stderr, "error: unsupported language %q (supported: %s)\n",
						lang, strings.Join(testdb.SupportedLanguages(), ", "))
					return strictcli.Exit(1)
				}
			}

			force := kwargs["force_overwrite"].(bool)
			outputName := ""
			if v := kwargsOptString(kwargs, "output"); v != nil {
				outputName = *v
			}
			ciProvider := ""
			if v := kwargsOptString(kwargs, "ci"); v != nil {
				ciProvider = *v
			}
			partman := kwargs["partman"].(bool)

			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: get working directory: %v\n", err)
				return strictcli.Exit(1)
			}

			configPath, found, err := resolveConfigPath(cfgOverride, cwd)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}
			if !found {
				fmt.Fprintln(os.Stderr, "error: pgdesign.toml not found in current directory or any ancestor")
				return strictcli.Exit(1)
			}

			cfg, err := config.LoadAndResolve(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
				return strictcli.Exit(1)
			}

			var sqlOutputName string
			var sqlOutput config.OutputConfig[config.AbsolutePath]
			var sqlOutputNames []string
			for name, out := range cfg.Output {
				if out.Format == "sql" {
					sqlOutputNames = append(sqlOutputNames, name)
				}
			}
			sort.Strings(sqlOutputNames)

			switch {
			case len(sqlOutputNames) == 0:
				fmt.Fprintln(os.Stderr, "error: no SQL output section found in pgdesign.toml")
				return strictcli.Exit(1)
			case len(sqlOutputNames) == 1:
				sqlOutputName = sqlOutputNames[0]
				sqlOutput = cfg.Output[sqlOutputName]
			case outputName != "":
				out, ok := cfg.Output[outputName]
				if !ok {
					fmt.Fprintf(os.Stderr, "error: output section %q not found in pgdesign.toml\n", outputName)
					return strictcli.Exit(1)
				}
				if out.Format != "sql" {
					fmt.Fprintf(os.Stderr, "error: output section %q has format %q, expected sql\n", outputName, out.Format)
					return strictcli.Exit(1)
				}
				sqlOutputName = outputName
				sqlOutput = out
			default:
				fmt.Fprintf(os.Stderr, "error: multiple SQL output sections found: %s\n", strings.Join(sqlOutputNames, ", "))
				fmt.Fprintln(os.Stderr, "  use --output to specify which one")
				return strictcli.Exit(1)
			}
			_ = sqlOutputName

			sqlPath := string(sqlOutput.Path)
			splitPath := sqlPath + ".sqlsplit"

			oldSplitJSON := sqlPath + ".split.json"
			if _, err := os.Stat(oldSplitJSON); err == nil {
				fmt.Fprintf(os.Stderr, "warning: old %s exists alongside %s -- consider deleting it\n",
					filepath.Base(oldSplitJSON), filepath.Base(splitPath))
			}

			baseURL := cfg.Database.URL
			if baseURL == "" {
				fmt.Fprintln(os.Stderr, "error: [database].url is not set in pgdesign.toml")
				return strictcli.Exit(1)
			}

			u, err := url.Parse(baseURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: parse database URL: %v\n", err)
				return strictcli.Exit(1)
			}
			baseName := strings.TrimPrefix(u.Path, "/")
			if baseName == "" {
				fmt.Fprintln(os.Stderr, "error: database URL has no database name")
				return strictcli.Exit(1)
			}

			for _, lang := range languages {
				relPath := testdb.WrapperOutputPath(lang)
				absPath := filepath.Join(cwd, relPath)

				if _, err := os.Stat(absPath); err == nil && !force {
					fmt.Fprintf(os.Stderr, "error: %s already exists (use --force-overwrite to overwrite)\n", relPath)
					return strictcli.Exit(1)
				}

				content, err := testdb.RenderTemplate(lang, splitPath, baseURL, baseName)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: render template for %s: %v\n", lang, err)
					return strictcli.Exit(1)
				}

				dir := filepath.Dir(absPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					fmt.Fprintf(os.Stderr, "error: create directory %s: %v\n", dir, err)
					return strictcli.Exit(1)
				}

				if err := os.WriteFile(absPath, content, 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error: write %s: %v\n", relPath, err)
					return strictcli.Exit(1)
				}

				fmt.Fprintf(os.Stderr, "wrote %s\n", relPath)
			}

			if ciProvider != "" {
				if cfg.Database.PGVersion == 0 {
					fmt.Fprintln(os.Stderr, "error: pg_version is required in pgdesign.toml for CI template generation")
					return strictcli.Exit(1)
				}
				pgVersion := fmt.Sprintf("%d", cfg.Database.PGVersion)

				content, err := testdb.RenderCITemplate(ciProvider, pgVersion, languages, testdb.CITemplateOptions{
					Partman: partman,
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: render CI template: %v\n", err)
					return strictcli.Exit(1)
				}

				ciRelPath := ".github/workflows/pgdesign-testdb.yml"
				ciAbsPath := filepath.Join(cwd, ciRelPath)

				if _, err := os.Stat(ciAbsPath); err == nil && !force {
					fmt.Fprintf(os.Stderr, "error: %s already exists (use --force-overwrite to overwrite)\n", ciRelPath)
					return strictcli.Exit(1)
				}

				dir := filepath.Dir(ciAbsPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					fmt.Fprintf(os.Stderr, "error: create directory %s: %v\n", dir, err)
					return strictcli.Exit(1)
				}

				if err := os.WriteFile(ciAbsPath, content, 0644); err != nil {
					fmt.Fprintf(os.Stderr, "error: write %s: %v\n", ciRelPath, err)
					return strictcli.Exit(1)
				}

				fmt.Fprintf(os.Stderr, "wrote %s\n", ciRelPath)
			}

			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.StringFlag("language", "Target programming language(s) for wrapper generation", strictcli.Repeatable(), strictcli.Unique(true)),
			strictcli.StringFlag("output", "Name of the SQL output section (for disambiguation)", strictcli.Default(nil)),
			strictcli.BoolFlag("force-overwrite", "Overwrite existing wrapper files without prompting", strictcli.Default(false)),
			strictcli.StringFlag("ci", "CI provider for workflow generation (e.g., github-actions)", strictcli.Default(nil)),
			strictcli.BoolFlag("partman", "Include pg_partman installation step in CI workflow", strictcli.Default(false)),
		),
	)
}
