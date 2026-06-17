package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/audit"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/discover"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
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
				for j := range fds {
					fds[j].Source = "discovered"
				}
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

// analyzeCoverage checks constraint completeness and returns diagnostics with codes C100-C104.
func analyzeCoverage(schema *model.Schema) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	for _, table := range schema.Tables {
		// C100: Table without check constraints
		if len(table.Checks) == 0 && len(table.Columns) > 2 && !table.AppendOnly {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C100",
				Table:    table.Name,
				Message:  "table has no check constraints",
			})
		}

		// C101: FK columns without index
		for _, fk := range table.FKs {
			if !table.HasIndexCovering(fk.Columns) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Warning,
					Code:     "C101",
					Table:    table.Name,
					Message:  fmt.Sprintf("foreign key %q on columns [%s] has no covering index", fk.Name, strings.Join(fk.Columns, ", ")),
				})
			}
		}

		// C104: Missing index for FK join pattern
		for _, fk := range table.FKs {
			refTable := schema.TableByName(fk.RefSchema, fk.RefTable)
			if refTable == nil {
				continue
			}
			for _, col := range refTable.Columns {
				isFilter := col.Name == "status" || col.Name == "type" || col.Name == "kind" || col.Name == "category" ||
					strings.HasSuffix(col.Name, "_at") || strings.HasSuffix(col.Name, "_date")
				if !isFilter {
					continue
				}
				suggested := make([]string, len(fk.Columns)+1)
				copy(suggested, fk.Columns)
				suggested[len(fk.Columns)] = col.Name
				if !table.HasIndexCovering(suggested) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Info,
						Code:     "C104",
						Table:    table.Name,
						Message:  fmt.Sprintf("consider index on [%s] for filtered joins on %q", strings.Join(suggested, ", "), fk.RefTable),
					})
				}
			}
		}
	}

	// C102: Unused enum type
	for _, enum := range schema.Enums {
		used := false
		for _, table := range schema.Tables {
			for _, col := range table.Columns {
				if col.PGType == enum.Name {
					used = true
					break
				}
			}
			if used {
				break
			}
		}
		if !used {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C102",
				Message:  fmt.Sprintf("enum type %q is not referenced by any column", enum.Name),
			})
		}
	}

	// C103: Orphan table
	for _, table := range schema.Tables {
		if len(table.Columns) <= 2 {
			continue
		}
		hasOutgoingFK := len(table.FKs) > 0
		referencedByOther := false
		for _, other := range schema.Tables {
			if other.Name == table.Name && other.Schema == table.Schema {
				continue
			}
			for _, fk := range other.FKs {
				if fk.RefTable == table.Name && fk.RefSchema == table.Schema {
					referencedByOther = true
					break
				}
			}
			if referencedByOther {
				break
			}
		}
		if !hasOutgoingFK && !referencedByOther {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C103",
				Table:    table.Name,
				Message:  "table has no foreign key relationships (orphan)",
			})
		}
	}

	return diags
}

// designCodes are the validation diagnostic codes for schema design checks.
var designCodes = map[string]bool{
	"W013": true, // CASCADE depth
	"W014": true, // CASCADE breadth
	"W015": true, // Mixed ON DELETE
	"I001": true, // Natural key candidate
	"W016": true, // PK subsumes UNIQUE
	"W017": true, // Redundant IS NOT NULL CHECK
	"W018": true, // Domain CHECK duplicate
	"W019": true, // Range subsumption
}

func checkDesign(ctx strictcli.CheckContext) strictcli.CheckResult {
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

	// Filter to design-related codes only.
	var designDiags []diagnostic.Diagnostic
	for _, d := range diags {
		if designCodes[d.Code] {
			designDiags = append(designDiags, d)
		}
	}

	if len(designDiags) == 0 {
		return strictcli.CheckResult{
			Status:  "pass",
			Message: "no design issues found",
		}
	}

	return strictcli.CheckResult{
		Status:  "warn",
		Message: fmt.Sprintf("%d design issue(s) found", len(designDiags)),
		Details: diagDetails(designDiags),
	}
}

func checkCoverage(ctx strictcli.CheckContext) strictcli.CheckResult {
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

	allDiags := analyzeCoverage(schema)

	if diagnostic.Diagnostics(allDiags).HasErrors() {
		return strictcli.CheckResult{
			Status:  "fail",
			Message: "coverage errors found",
			Details: diagDetails(allDiags),
		}
	}

	warnings := diagnostic.Diagnostics(allDiags).Warnings()
	if len(warnings) > 0 {
		return strictcli.CheckResult{
			Status:  "warn",
			Message: fmt.Sprintf("%d coverage issue(s) found", len(warnings)),
			Details: diagDetails(allDiags),
		}
	}

	return strictcli.CheckResult{
		Status:  "pass",
		Message: "all coverage checks passed",
	}
}
