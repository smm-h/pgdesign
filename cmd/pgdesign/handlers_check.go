package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/validate"
	"golang.org/x/sync/errgroup"
)

func handleValidate(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	// Try to load project config from the directory of the first path argument.
	cfg := loadProjectConfig(paths[0])

	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))

	valCfg := &validate.Config{
		NamingPattern: cfg.Validate.NamingPattern,
		MaxColumns:    cfg.Validate.MaxColumns,
		Disabled:      cfg.Validate.Disable,
		Suppress:      cfg.Suppress,
		Extensions:    schema.Extensions,
		ExtRegistry:   extReg,
	}

	diags, suppressed := validate.Validate(schema, valCfg)
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}

	showSuppressed := kwargs["show_suppressed"].(bool)
	if showSuppressed && len(suppressed) > 0 {
		fmt.Fprintln(os.Stderr, "\nSuppressed diagnostics:")
		for _, s := range suppressed {
			fmt.Fprintf(os.Stderr, "  %s[%s]: %s\n", s.Severity.String(), s.Code, s.Message)
			location := ""
			if s.Table != "" {
				location = s.Table
			}
			if s.Column != "" {
				location += ":" + s.Column
			}
			if location != "" {
				fmt.Fprintf(os.Stderr, "    --> %s\n", location)
			}
			fmt.Fprintf(os.Stderr, "    reason: %s\n", s.Reason)
		}
	}

	if diagnostic.Diagnostics(diags).HasErrors() {
		return 1
	}
	return 0
}

func handleAudit(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	var allDiags []diagnostic.Diagnostic

	// When --db is provided, discover FDs from live data for tables without declared FDs.
	if dbURL, ok := kwargs["db"].(string); ok && dbURL != "" {
		approxThreshold := kwargs["approximate"].(float64)
		opts := discover.Options{
			ApproximateThreshold: approxThreshold,
		}

		// Collect --tables filter values.
		var tableFilter map[string]bool
		if raw, ok := kwargs["tables"].([]interface{}); ok && len(raw) > 0 {
			tableFilter = make(map[string]bool, len(raw))
			for _, v := range raw {
				if s, ok := v.(string); ok {
					tableFilter[s] = true
				}
			}
		}

		// Build list of table indices eligible for discovery.
		var eligible []int
		for i := range schema.Tables {
			tbl := &schema.Tables[i]
			if len(tbl.Dependencies) > 0 {
				continue
			}
			if tableFilter != nil && !tableFilter[tbl.Name] {
				continue
			}
			eligible = append(eligible, i)
		}

		if len(eligible) > 0 {
			ctx := context.Background()

			// Use a connection pool for parallel discovery. Each goroutine
			// acquires its own connection since pgx.Conn is not concurrency-safe.
			cfg := loadProjectConfig(paths[0])
			poolCfg, parseErr := pgxpool.ParseConfig(dbURL)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "error: parse connection config: %v\n", parseErr)
				return 1
			}
			if cfg.Database.PoolMaxConns > 0 {
				poolCfg.MaxConns = cfg.Database.PoolMaxConns
			}
			if cfg.Database.PoolMinConns > 0 {
				poolCfg.MinConns = cfg.Database.PoolMinConns
			}
			pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: connect for FD discovery: %v\n", err)
				return 1
			}
			defer pool.Close()

			// Per-table results collected under a mutex.
			type tableResult struct {
				tableIdx int
				diags    []diagnostic.Diagnostic
			}
			var (
				mu      sync.Mutex
				results []tableResult
			)

			g, gctx := errgroup.WithContext(ctx)
			g.SetLimit(runtime.GOMAXPROCS(0))

			for _, idx := range eligible {
				idx := idx
				g.Go(func() error {
					tbl := &schema.Tables[idx]
					schemaName := tbl.Schema
					if schemaName == "" {
						schemaName = "public"
					}

					conn, err := pool.Acquire(gctx)
					if err != nil {
						mu.Lock()
						results = append(results, tableResult{
							tableIdx: idx,
							diags: []diagnostic.Diagnostic{{
								Severity: diagnostic.Warning,
								Table:    tbl.Name,
								Message:  fmt.Sprintf("FD discovery failed: %v", err),
							}},
						})
						mu.Unlock()
						return nil
					}
					defer conn.Release()

					fds, discDiags, err := discover.Discover(conn.Conn(), schemaName, tbl.Name, opts)

					var diags []diagnostic.Diagnostic
					diags = append(diags, discDiags...)
					if err != nil {
						diags = append(diags, diagnostic.Diagnostic{
							Severity: diagnostic.Warning,
							Table:    tbl.Name,
							Message:  fmt.Sprintf("FD discovery failed: %v", err),
						})
					} else if len(fds) > 0 {
						diags = append(diags, diagnostic.Diagnostic{
							Severity: diagnostic.Info,
							Table:    tbl.Name,
							Message:  fmt.Sprintf("Discovered %d FD(s) from data sample.", len(fds)),
						})
					}

					mu.Lock()
					results = append(results, tableResult{
						tableIdx: idx,
						diags:    diags,
					})
					// Write discovered FDs back to the table (safe: each goroutine
					// writes to a distinct table index).
					if err == nil && len(fds) > 0 {
						schema.Tables[idx].Dependencies = fds
					}
					mu.Unlock()

					return nil
				})
			}

			// errgroup goroutines never return errors (handled inline), but
			// Wait() still collects goroutine panics.
			_ = g.Wait()

			// Merge diagnostics in table order for deterministic output.
			for _, idx := range eligible {
				for _, r := range results {
					if r.tableIdx == idx {
						allDiags = append(allDiags, r.diags...)
						break
					}
				}
			}
		}
	}

	allDiags = append(allDiags, audit.Audit(schema)...)
	if kwargs["strict_nf"].(bool) {
		allDiags = promoteNFViolations(allDiags)
	}
	if len(allDiags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(allDiags, true))
	}
	if diagnostic.Diagnostics(allDiags).HasErrors() {
		return 1
	}
	return 0
}
