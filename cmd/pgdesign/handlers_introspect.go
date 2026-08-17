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

func registerIntrospectCmd(app *strictcli.App) {
	app.Command("introspect", "Introspect a live PostgreSQL database into TOML schema",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			quiet := ctx.Quiet()

			dbURL := kwargsDBURL(kwargs)
			if dbURL == "" {
				fmt.Fprintln(os.Stderr, "error: --db is required for introspect")
				return strictcli.Exit(1)
			}

			// Namespaces come from the explicit --schema flag; absent, default to
			// public. (The config lists schema FILE paths, not PG namespace names,
			// so it never correctly drove introspection namespaces.)
			schemaNames := kwargsStrSlice(kwargs["schema"])
			if len(schemaNames) == 0 {
				schemaNames = []string{"public"}
			}

			bgCtx := context.Background()
			schema, diags, err := introspect.Introspect(bgCtx, dbURL, schemaNames)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return strictcli.Exit(1)
			}

			if len(diags) > 0 {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
			}
			if diagnostic.Diagnostics(diags).HasErrors() {
				return strictcli.Exit(1)
			}

			data, err := introspect.Export(schema)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: export failed: %v\n", err)
				return strictcli.Exit(1)
			}

			output := kwargsOptString(kwargs, "output")
			if output != nil && *output != "" {
				if err := os.WriteFile(*output, data, 0o644); err != nil {
					fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
					return strictcli.Exit(1)
				}
				// introspect --output is a SCAFFOLDING writer (roadmap 6.2): a NEW
				// candidate source file, outside the revision invariant and never
				// flagged. Adopting it as project source is a source edit that
				// changes the revision — say so.
				if !quiet {
					fmt.Fprintf(os.Stderr, "%s: %s\n", *output, introspectAdoptionNote)
				}
			} else {
				fmt.Print(string(data))
			}

			// introspect is mutating (--output writes a file), so --extensions
			// declares Optional() rather than Default(false) and names its
			// fallback in its own help (contract §27.1).
			extensions := optBool(kwargs["extensions"], false)
			if extensions {
				conn, err := pgx.Connect(bgCtx, dbURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: connect for extension discovery: %v\n", err)
					return strictcli.Exit(1)
				}
				defer conn.Close(bgCtx)

				rows, err := conn.Query(bgCtx,
					"SELECT extname FROM pg_extension WHERE extname != 'plpgsql' ORDER BY extname")
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: query extensions: %v\n", err)
					return strictcli.Exit(1)
				}
				defer rows.Close()

				var extNames []string
				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						fmt.Fprintf(os.Stderr, "error: scan extension: %v\n", err)
						return strictcli.Exit(1)
					}
					extNames = append(extNames, name)
				}
				if err := rows.Err(); err != nil {
					fmt.Fprintf(os.Stderr, "error: iterate extensions: %v\n", err)
					return strictcli.Exit(1)
				}

				if len(extNames) == 0 {
					if !quiet {
						fmt.Fprintln(os.Stderr, "# No extensions found (excluding plpgsql).")
					}
					return strictcli.Exit(0)
				}

				fmt.Println()

				for i, extName := range extNames {
					types, err := queryExtensionDeps(bgCtx, conn, extName,
						"SELECT t.typname FROM pg_type t JOIN pg_depend d ON d.objid = t.oid "+
							"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY t.typname")
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: query types for %s: %v\n", extName, err)
						return strictcli.Exit(1)
					}

					functions, err := queryExtensionDeps(bgCtx, conn, extName,
						"SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.objid = p.oid "+
							"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY p.proname")
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: query functions for %s: %v\n", extName, err)
						return strictcli.Exit(1)
					}

					opclasses, err := queryExtensionDeps(bgCtx, conn, extName,
						"SELECT o.opcname FROM pg_opclass o JOIN pg_depend d ON d.objid = o.oid "+
							"WHERE d.refobjid = (SELECT oid FROM pg_extension WHERE extname = $1) AND d.deptype = 'e' ORDER BY o.opcname")
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: query opclasses for %s: %v\n", extName, err)
						return strictcli.Exit(1)
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

			return strictcli.Exit(0)
		},
		strictcli.WithEffect(strictcli.EffectMutating),
		strictcli.WithFlags(
			strictcli.StringFlag("db", "PostgreSQL connection URL for the target database server", strictcli.Optional(), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("schema", "PostgreSQL schema name(s) to introspect (repeatable); omitted means public", strictcli.Optional(), strictcli.Repeatable(), strictcli.Unique(true)),
			strictcli.StringFlag("output", "Write output to a file at this path instead of stdout", strictcli.Optional()),
			strictcli.BoolFlag("extensions", "Discover extension types, functions, and opclasses; omitted means they are not discovered", strictcli.Optional()),
		),
	)
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
