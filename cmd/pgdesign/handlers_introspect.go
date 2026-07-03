package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/strictcli/go/strictcli"
)

type introspectHandler struct {
	DB         string   `cli:"db" help:"PostgreSQL connection URL for the target database server"`
	Schemas    []string `cli:"schema" help:"PostgreSQL schema name(s) to introspect (repeatable)"`
	Output     *string  `cli:"output" help:"Write output to a file at this path instead of stdout"`
	Extensions bool     `cli:"extensions" help:"Discover extension types, functions, and opclasses" default:"false"`
}

func (h *introspectHandler) Run(cliCtx *strictcli.Context) int {
	g := strictcli.Globals[Globals](cliCtx)

	dbURL := h.DB
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for introspect")
		return 1
	}

	// Load config for default schema names.
	cfg := loadProjectConfig(".")

	// Collect schema names from repeatable --schema flag.
	schemaNames := h.Schemas
	if len(schemaNames) == 0 {
		schemaNames = configSchemaNames(cfg)
	}
	if len(schemaNames) == 0 {
		schemaNames = []string{"public"}
	}

	ctx := context.Background()
	schema, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Print diagnostics (warnings/info) to stderr.
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}

	data, err := introspect.Export(schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: export failed: %v\n", err)
		return 1
	}

	// Write to file or stdout.
	if h.Output != nil && *h.Output != "" {
		if err := os.WriteFile(*h.Output, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
			return 1
		}
	} else {
		fmt.Print(string(data))
	}

	// Extension discovery (--extensions flag)
	if h.Extensions {
		conn, err := pgx.Connect(ctx, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: connect for extension discovery: %v\n", err)
			return 1
		}
		defer conn.Close(ctx)

		// Query installed extensions, excluding plpgsql (always present).
		rows, err := conn.Query(ctx,
			"SELECT extname FROM pg_extension WHERE extname != 'plpgsql' ORDER BY extname")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: query extensions: %v\n", err)
			return 1
		}
		defer rows.Close()

		var extNames []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				fmt.Fprintf(os.Stderr, "error: scan extension: %v\n", err)
				return 1
			}
			extNames = append(extNames, name)
		}
		if err := rows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "error: iterate extensions: %v\n", err)
			return 1
		}

		if len(extNames) == 0 {
			if !g.Quiet {
				fmt.Fprintln(os.Stderr, "# No extensions found (excluding plpgsql).")
			}
			return 0
		}

		fmt.Println() // separator between introspect output and extensions

		for i, extName := range extNames {
			types, err := queryExtensionDeps(ctx, conn, extName,
				"SELECT t.typname FROM pg_type t JOIN pg_depend d ON d.objid = t.oid "+
					"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY t.typname")
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: query types for %s: %v\n", extName, err)
				return 1
			}

			functions, err := queryExtensionDeps(ctx, conn, extName,
				"SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.objid = p.oid "+
					"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY p.proname")
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: query functions for %s: %v\n", extName, err)
				return 1
			}

			opclasses, err := queryExtensionDeps(ctx, conn, extName,
				"SELECT o.opcname FROM pg_opclass o JOIN pg_depend d ON d.objid = o.oid "+
					"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY o.opcname")
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: query opclasses for %s: %v\n", extName, err)
				return 1
			}

			if i > 0 {
				fmt.Println()
			}
			fmt.Println("[[extensions]]")
			fmt.Printf("name = %q\n", extName)
			if len(types) > 0 {
				fmt.Printf("types = [%s]\n", quotedList(types))
			}
			if len(opclasses) > 0 {
				fmt.Printf("opclasses = [%s]\n", quotedList(opclasses))
			}
			if len(functions) > 0 {
				fmt.Printf("functions = [%s]\n", quotedList(functions))
			}
		}
	}

	return 0
}

// queryExtensionDeps runs a query that returns a single text column of names
// dependent on the given extension.
func queryExtensionDeps(ctx context.Context, conn *pgx.Conn, extName, query string) ([]string, error) {
	rows, err := conn.Query(ctx, query, extName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// quotedList formats a string slice as a TOML inline array body: "a", "b", "c".
func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}
