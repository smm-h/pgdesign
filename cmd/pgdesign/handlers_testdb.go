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

type testdbSetupHandler struct {
	DB  string `cli:"db" help:"PostgreSQL connection URL for the target database server"`
	DDL string `cli:"ddl" help:"Path to the SQL DDL file to apply to the test database"`
}

func (h *testdbSetupHandler) Run(_ *strictcli.Context) int {
	dbURL := h.DB
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for testdb setup")
		return 1
	}

	ddlPath := h.DDL
	if ddlPath == "" {
		fmt.Fprintln(os.Stderr, "error: --ddl is required for testdb setup")
		return 1
	}

	ddlFile, err := os.Open(ddlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open DDL file: %v\n", err)
		return 1
	}
	defer ddlFile.Close()

	manager, err := testdb.NewManager(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
		return 1
	}

	ctx := context.Background()
	db, err := manager.Create(ctx, testdb.CreateOptions{DDL: ddlFile})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create ephemeral database: %v\n", err)
		return 1
	}

	fmt.Println(db.URL)
	return 0
}

type testdbTeardownHandler struct {
	DB string `cli:"db" help:"PostgreSQL connection URL for the target database server"`
}

func (h *testdbTeardownHandler) Run(_ *strictcli.Context) int {
	dbURL := h.DB
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for testdb teardown")
		return 1
	}

	u, err := url.Parse(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse URL: %v\n", err)
		return 1
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		fmt.Fprintln(os.Stderr, "error: URL has no database name")
		return 1
	}

	manager, err := testdb.NewManager(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
		return 1
	}

	ctx := context.Background()
	if err := manager.DropByName(ctx, dbName); err != nil {
		fmt.Fprintf(os.Stderr, "error: drop database %s: %v\n", dbName, err)
		return 1
	}

	return 0
}

type testdbGCHandler struct {
	DB        string `cli:"db" help:"PostgreSQL connection URL for the target database server"`
	OlderThan string `cli:"older-than" help:"Drop databases older than this duration (e.g., 2h, 30m)"`
}

func (h *testdbGCHandler) Run(_ *strictcli.Context) int {
	dbURL := h.DB
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for testdb gc")
		return 1
	}

	olderThanStr := h.OlderThan
	if olderThanStr == "" {
		fmt.Fprintln(os.Stderr, "error: --older-than is required for testdb gc")
		return 1
	}

	olderThan, err := time.ParseDuration(olderThanStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse duration %q: %v\n", olderThanStr, err)
		return 1
	}

	manager, err := testdb.NewManager(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
		return 1
	}

	ctx := context.Background()
	orphans, err := manager.ListOrphans(ctx, olderThan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list orphans: %v\n", err)
		return 1
	}

	if len(orphans) == 0 {
		fmt.Fprintln(os.Stderr, "no orphaned databases found")
		return 0
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
		return 1
	}
	return 0
}

type testdbInitHandler struct {
	Languages      []string `cli:"language" help:"Target programming language(s) for wrapper generation"`
	Output         *string  `cli:"output" help:"Name of the SQL output section (for disambiguation)"`
	ForceOverwrite bool     `cli:"force-overwrite" help:"Overwrite existing wrapper files without prompting" default:"false"`
	CI             *string  `cli:"ci" help:"CI provider for workflow generation (e.g., github-actions)"`
}

func (h *testdbInitHandler) Run(_ *strictcli.Context) int {
	languages := h.Languages
	if len(languages) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one --language is required")
		return 1
	}

	// Validate languages.
	supported := make(map[string]bool)
	for _, lang := range testdb.SupportedLanguages() {
		supported[lang] = true
	}
	for _, lang := range languages {
		if !supported[lang] {
			fmt.Fprintf(os.Stderr, "error: unsupported language %q (supported: %s)\n",
				lang, strings.Join(testdb.SupportedLanguages(), ", "))
			return 1
		}
	}

	force := h.ForceOverwrite
	outputName := ""
	if h.Output != nil {
		outputName = *h.Output
	}
	ciProvider := ""
	if h.CI != nil {
		ciProvider = *h.CI
	}

	// Find pgdesign.toml.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: get working directory: %v\n", err)
		return 1
	}

	configPath, found := config.FindConfig(cwd)
	if !found {
		fmt.Fprintln(os.Stderr, "error: pgdesign.toml not found in current directory or any ancestor")
		return 1
	}

	cfg, err := config.LoadAndResolve(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		return 1
	}

	// Find the SQL output section.
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
		return 1
	case len(sqlOutputNames) == 1:
		sqlOutputName = sqlOutputNames[0]
		sqlOutput = cfg.Output[sqlOutputName]
	case outputName != "":
		out, ok := cfg.Output[outputName]
		if !ok {
			fmt.Fprintf(os.Stderr, "error: output section %q not found in pgdesign.toml\n", outputName)
			return 1
		}
		if out.Format != "sql" {
			fmt.Fprintf(os.Stderr, "error: output section %q has format %q, expected sql\n", outputName, out.Format)
			return 1
		}
		sqlOutputName = outputName
		sqlOutput = out
	default:
		fmt.Fprintf(os.Stderr, "error: multiple SQL output sections found: %s\n", strings.Join(sqlOutputNames, ", "))
		fmt.Fprintln(os.Stderr, "  use --output to specify which one")
		return 1
	}
	_ = sqlOutputName

	// Resolve DDL path: the .sqlsplit companion file.
	sqlPath := string(sqlOutput.Path)
	splitPath := sqlPath + ".sqlsplit"

	// Warn if an old .split.json file exists alongside the .sqlsplit.
	oldSplitJSON := sqlPath + ".split.json"
	if _, err := os.Stat(oldSplitJSON); err == nil {
		fmt.Fprintf(os.Stderr, "warning: old %s exists alongside %s -- consider deleting it\n",
			filepath.Base(oldSplitJSON), filepath.Base(splitPath))
	}

	// Get the base URL from config.
	baseURL := cfg.Database.URL
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "error: [database].url is not set in pgdesign.toml")
		return 1
	}

	// Parse base database name from the URL.
	u, err := url.Parse(baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse database URL: %v\n", err)
		return 1
	}
	baseName := strings.TrimPrefix(u.Path, "/")
	if baseName == "" {
		fmt.Fprintln(os.Stderr, "error: database URL has no database name")
		return 1
	}

	// Generate wrappers for each language.
	for _, lang := range languages {
		relPath := testdb.WrapperOutputPath(lang)
		absPath := filepath.Join(cwd, relPath)

		// Check if file exists.
		if _, err := os.Stat(absPath); err == nil && !force {
			fmt.Fprintf(os.Stderr, "error: %s already exists (use --force-overwrite to overwrite)\n", relPath)
			return 1
		}

		// Render template.
		content, err := testdb.RenderTemplate(lang, splitPath, baseURL, baseName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: render template for %s: %v\n", lang, err)
			return 1
		}

		// Create parent directories.
		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error: create directory %s: %v\n", dir, err)
			return 1
		}

		// Write the file.
		if err := os.WriteFile(absPath, content, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error: write %s: %v\n", relPath, err)
			return 1
		}

		fmt.Fprintf(os.Stderr, "wrote %s\n", relPath)
	}

	// Generate CI workflow if --ci is set.
	if ciProvider != "" {
		if cfg.Database.PGVersion == 0 {
			fmt.Fprintln(os.Stderr, "error: pg_version is required in pgdesign.toml for CI template generation")
			return 1
		}
		pgVersion := fmt.Sprintf("%d", cfg.Database.PGVersion)

		content, err := testdb.RenderCITemplate(ciProvider, pgVersion, languages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: render CI template: %v\n", err)
			return 1
		}

		ciRelPath := ".github/workflows/pgdesign-testdb.yml"
		ciAbsPath := filepath.Join(cwd, ciRelPath)

		if _, err := os.Stat(ciAbsPath); err == nil && !force {
			fmt.Fprintf(os.Stderr, "error: %s already exists (use --force-overwrite to overwrite)\n", ciRelPath)
			return 1
		}

		dir := filepath.Dir(ciAbsPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error: create directory %s: %v\n", dir, err)
			return 1
		}

		if err := os.WriteFile(ciAbsPath, content, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error: write %s: %v\n", ciRelPath, err)
			return 1
		}

		fmt.Fprintf(os.Stderr, "wrote %s\n", ciRelPath)
	}

	return 0
}
