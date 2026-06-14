package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/validate"
	"github.com/smm-h/strictcli/go/strictcli"
)

// pgdesignCheckContext implements strictcli.CheckContext for pgdesign checks.
type pgdesignCheckContext struct {
	root string
}

func (c *pgdesignCheckContext) ProjectRoot() string { return c.root }

// loadSchemaForCheck resolves schema paths from the project root directory,
// parses, and builds the schema. This is the shared entry point for check
// functions that need a resolved schema.
func loadSchemaForCheck(root string) ([]string, error) {
	configPath, hasConfig := config.FindConfig(root)
	if hasConfig {
		return resolveFromConfig(configPath)
	}
	return resolveSchemaPaths([]string{root})
}

// diagDetails converts diagnostics to string details for CheckResult.
func diagDetails(diags []diagnostic.Diagnostic) []string {
	details := make([]string, 0, len(diags))
	for _, d := range diags {
		loc := ""
		if d.Table != "" {
			loc = d.Table
		}
		if d.Column != "" {
			loc += "." + d.Column
		}
		if loc != "" {
			details = append(details, fmt.Sprintf("[%s] %s: %s", d.Code, loc, d.Message))
		} else {
			details = append(details, fmt.Sprintf("[%s] %s", d.Code, d.Message))
		}
	}
	return details
}

func checkValidation(ctx strictcli.CheckContext) strictcli.CheckResult {
	root := ctx.ProjectRoot()

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return strictcli.CheckResult{
			Status:  "fail",
			Message: fmt.Sprintf("cannot resolve schema paths: %v", err),
		}
	}

	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return strictcli.CheckResult{
			Status:  "fail",
			Message: "schema parse/build failed",
		}
	}

	cfg := loadProjectConfig(root)

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

	diags, _ := validate.Validate(schema, valCfg)

	if diagnostic.Diagnostics(diags).HasErrors() {
		return strictcli.CheckResult{
			Status:  "fail",
			Message: "validation errors found",
			Details: diagDetails(diags),
		}
	}

	warnings := diagnostic.Diagnostics(diags).Warnings()
	if len(warnings) > 0 {
		return strictcli.CheckResult{
			Status:  "warn",
			Message: fmt.Sprintf("%d validation warning(s)", len(warnings)),
			Details: diagDetails(warnings),
		}
	}

	return strictcli.CheckResult{
		Status:  "pass",
		Message: "all validation checks passed",
	}
}

// resolveDBURL looks for a database connection URL in the config file or
// environment. Returns empty string if no URL is available.
func resolveDBURL(cfg *config.Config) string {
	if cfg.Database.URL != "" {
		return cfg.Database.URL
	}
	return os.Getenv("PGDESIGN_DB")
}

func checkNF(ctx strictcli.CheckContext) strictcli.CheckResult {
	root := ctx.ProjectRoot()

	paths, err := loadSchemaForCheck(root)
	if err != nil {
		return strictcli.CheckResult{
			Status:  "fail",
			Message: fmt.Sprintf("cannot resolve schema paths: %v", err),
		}
	}

	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return strictcli.CheckResult{
			Status:  "fail",
			Message: "schema parse/build failed",
		}
	}

	cfg := loadProjectConfig(root)
	dbURL := resolveDBURL(cfg)

	// If a DB URL is available, discover FDs for tables without declared dependencies.
	if dbURL != "" {
		bgCtx := context.Background()
		conn, connErr := pgx.Connect(bgCtx, dbURL)
		if connErr != nil {
			return strictcli.CheckResult{
				Status:  "fail",
				Message: fmt.Sprintf("cannot connect to database: %v", connErr),
			}
		}
		defer conn.Close(bgCtx)

		for i := range schema.Tables {
			tbl := &schema.Tables[i]
			if len(tbl.Dependencies) > 0 {
				continue
			}
			schemaName := tbl.Schema
			if schemaName == "" {
				schemaName = "public"
			}
			fds, _, discErr := discover.Discover(conn, schemaName, tbl.Name, discover.Options{})
			if discErr != nil {
				// Discovery failure for one table is not fatal; skip it.
				continue
			}
			if len(fds) > 0 {
				schema.Tables[i].Dependencies = fds
			}
		}
	}

	diags := audit.Audit(schema)

	// Filter to NF violation diagnostics only.
	var nfDiags []diagnostic.Diagnostic
	for _, d := range diags {
		if nfViolationCodes[d.Code] {
			nfDiags = append(nfDiags, d)
		}
	}

	if len(nfDiags) > 0 {
		return strictcli.CheckResult{
			Status:  "warn",
			Message: fmt.Sprintf("%d normal form violation(s)", len(nfDiags)),
			Details: diagDetails(nfDiags),
		}
	}

	return strictcli.CheckResult{
		Status:  "pass",
		Message: "no normal form violations",
	}
}

func checkCoverage(ctx strictcli.CheckContext) strictcli.CheckResult {
	return strictcli.CheckResult{
		Status:  "pass",
		Message: "coverage check not yet implemented",
	}
}
