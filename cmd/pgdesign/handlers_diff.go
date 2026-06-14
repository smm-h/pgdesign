package main

import (
	"context"
	"fmt"
	"os"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/introspect"
)

func handleDiff(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Load config for default schema names.
	cfg := loadProjectConfig(paths[0])

	dbURL, _ := kwargs["live"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: specify --live <url> for DB comparison or --against <path> for TOML comparison")
		return 1
	}

	// Introspect the live database. Use schema name from parsed schema first,
	// then fall back to config-derived schema names, then "public".
	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	d := diff.Diff(schema, actual)

	if kwargs["json"].(bool) {
		fmt.Println(diff.FormatJSON(d))
		return 0
	}

	fmt.Print(diff.FormatTerminal(d))
	if d.IsEmpty() {
		return 0
	}
	return 0
}
