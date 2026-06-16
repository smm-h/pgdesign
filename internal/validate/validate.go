// Package validate provides the strict validation engine for pgdesign schemas.
// It operates on the resolved IR and returns diagnostics for rule violations.
package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
	"github.com/smm-h/pgdesign/internal/sqlutil"
)

// Config controls which rules run and their parameters.
type Config struct {
	Disabled      []string              // codes to skip, e.g. ["W002", "W005"]
	Suppress      map[string]string     // per-table/column suppression, key: "table.column.CODE" or "table.CODE", value: reason
	NamingPattern string                // "snake_case" (default)
	MaxColumns    int                   // default 30
	Extensions    []string              // declared extensions (from meta)
	ExtRegistry   *extregistry.Registry
}

// SuppressedDiagnostic pairs a diagnostic with the reason it was suppressed.
type SuppressedDiagnostic struct {
	diagnostic.Diagnostic
	Reason string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		NamingPattern: "snake_case",
		MaxColumns:    30,
	}
}

// Validate runs all validation rules against the schema and returns active and suppressed diagnostics.
func Validate(schema *model.Schema, config *Config) ([]diagnostic.Diagnostic, []SuppressedDiagnostic) {
	if config == nil {
		config = DefaultConfig()
	}
	if config.MaxColumns == 0 {
		config.MaxColumns = 30
	}
	if config.NamingPattern == "" {
		config.NamingPattern = "snake_case"
	}

	disabled := make(map[string]bool, len(config.Disabled))
	for _, code := range config.Disabled {
		disabled[code] = true
	}

	var diags []diagnostic.Diagnostic

	// Collect all rules and run non-disabled ones.
	type rule struct {
		code string
		fn   func(*model.Schema, *Config) []diagnostic.Diagnostic
	}

	rules := []rule{
		{"E200", checkMissingColumnType},
		{"E205", checkColumnDefaultQuotes},
		{"E201", checkFKMissingOnDelete},
		{"E202", checkTableMissingComment},
		{"E203", checkTableMissingPK},
		{"E204", checkFKRefNotFound},
		{"E206", checkDuplicateIndex},
		{"E207", checkVarcharUsage},
		{"E208", checkTimestampNoTZ},
		{"E209", checkSerialUsage},
		{"E210", checkFloatMoney},
		{"E211", checkNamingConvention},
		{"E212", checkFKMissingIndex},
		{"E213", checkGeneratedColRefsGenerated},
		{"E214", checkOpclassMissingExtension},
		{"E216", checkIndexWithParams},
		{"E217", checkUnknownIndexMethod},
		{"E219", checkIndexMethodMissingExtension},
		{"E218", checkVirtualRequiresPG18},
		{"E215", checkPolicyExprMismatch},
		{"W009", checkPolicyErrorCodeSnakeCase},
		{"W001", checkGodTable},
		{"W002", checkOrphanTable},
		{"W003", checkBooleanStates},
		{"W004", checkJSONCouldBeTable},
		{"W005", checkMissingTimestamps},
		{"W006", checkPreferText},
		{"W007", checkRedundantIndex},
		{"W008", checkCircularFK},
		{"W010", checkAppendOnlyUpdatedAt},
		{"E220", checkDependsOnResolvable},
	}

	for _, r := range rules {
		if disabled[r.code] {
			continue
		}
		diags = append(diags, r.fn(schema, config)...)
	}

	// Post-emission suppression filter.
	var active []diagnostic.Diagnostic
	var suppressed []SuppressedDiagnostic
	for _, d := range diags {
		if d.Suppressed {
			suppressed = append(suppressed, SuppressedDiagnostic{
				Diagnostic: d,
				Reason:     "programmatically suppressed",
			})
			continue
		}
		if config.Suppress != nil {
			// Try table.column.CODE first (most specific).
			if d.Table != "" && d.Column != "" {
				key := d.Table + "." + d.Column + "." + d.Code
				if reason, ok := config.Suppress[key]; ok {
					suppressed = append(suppressed, SuppressedDiagnostic{
						Diagnostic: d,
						Reason:     reason,
					})
					continue
				}
			}
			// Try table.CODE (less specific).
			if d.Table != "" {
				key := d.Table + "." + d.Code
				if reason, ok := config.Suppress[key]; ok {
					suppressed = append(suppressed, SuppressedDiagnostic{
						Diagnostic: d,
						Reason:     reason,
					})
					continue
				}
			}
		}
		active = append(active, d)
	}

	return active, suppressed
}

// --- Error rules ---

// checkMissingColumnType (E200): column has no PGType set.
func checkMissingColumnType(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			if col.PGType == "" {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E200",
					Table:      t.Name,
					Column:     col.Name,
					Message:    "column missing type",
					Suggestion: "Add a type to the column definition",
				})
			}
		}
	}
	return diags
}

// checkColumnDefaultQuotes (E205): default value contains embedded SQL quotes.
func checkColumnDefaultQuotes(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			if col.Default != nil && len(*col.Default) >= 2 && strings.HasPrefix(*col.Default, "'") && strings.HasSuffix(*col.Default, "'") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E205",
					Table:      t.Name,
					Column:     col.Name,
					Message:    fmt.Sprintf("default %q appears to contain SQL quotes; use raw values (e.g., \"created\" not \"'created'\")", *col.Default),
					Suggestion: "Remove the surrounding single quotes from the default value",
				})
			}
		}
	}
	return diags
}

// checkFKMissingOnDelete (E201): FK constraint has no ON DELETE clause.
func checkFKMissingOnDelete(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			if fk.OnDelete == "" {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E201",
					Table:      t.Name,
					Message:    "FK " + fk.Name + " missing ON DELETE clause",
					Suggestion: "Add on_delete = \"cascade\", \"restrict\", \"set null\", or \"no action\"",
				})
			}
		}
	}
	return diags
}

// checkTableMissingComment (E202): Table has no comment.
func checkTableMissingComment(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if t.Comment == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Error,
				Code:       "E202",
				Table:      t.Name,
				Message:    "table missing comment",
				Suggestion: "Add comment = \"...\" to the table definition",
			})
		}
	}
	for _, v := range schema.Views {
		if v.Comment == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Error,
				Code:       "E202",
				Table:      v.Name,
				Message:    "view missing comment",
				Suggestion: "Add comment = \"...\" to the view definition",
			})
		}
	}
	for _, mv := range schema.MaterializedViews {
		if mv.Comment == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Error,
				Code:       "E202",
				Table:      mv.Name,
				Message:    "materialized view missing comment",
				Suggestion: "Add comment = \"...\" to the materialized view definition",
			})
		}
	}
	return diags
}

// checkTableMissingPK (E203): Table has no primary key.
func checkTableMissingPK(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if len(t.PK) == 0 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Error,
				Code:       "E203",
				Table:      t.Name,
				Message:    "table missing primary key",
				Suggestion: "Add pk = [\"column\"] or use an id-typed column",
			})
		}
	}
	return diags
}

// checkFKRefNotFound (E204): FK references a table or column that doesn't exist in schema.
func checkFKRefNotFound(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			refTable := schema.TableByName(fk.RefSchema, fk.RefTable)
			if refTable == nil {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E204",
					Table:    t.Name,
					Message:  "FK " + fk.Name + " references non-existent table " + fk.RefSchema + "." + fk.RefTable,
				})
				continue
			}
			// Table exists; check that each referenced column exists in it.
			for _, refCol := range fk.RefColumns {
				found := false
				for _, col := range refTable.Columns {
					if col.Name == refCol {
						found = true
						break
					}
				}
				if !found {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Error,
						Code:     "E204",
						Table:    t.Name,
						Message:  fmt.Sprintf("FK %q references column %q which does not exist in table %q", fk.Name, refCol, fk.RefTable),
					})
				}
			}
		}
	}
	return diags
}

// checkDuplicateIndex (E206): An index's columns are a prefix of another index on the same table.
func checkDuplicateIndex(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for i, idx := range t.Indexes {
			for j, other := range t.Indexes {
				if i == j {
					continue
				}
				if isPrefix(idx.Columns, other.Columns) && len(idx.Columns) < len(other.Columns) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Error,
						Code:     "E206",
						Table:    t.Name,
						Message:  "index " + idx.Name + " is a prefix of index " + other.Name,
					})
					break
				}
			}
		}
	}
	for _, mv := range schema.MaterializedViews {
		for i, idx := range mv.Indexes {
			for j, other := range mv.Indexes {
				if i == j {
					continue
				}
				if isPrefix(idx.Columns, other.Columns) && len(idx.Columns) < len(other.Columns) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Error,
						Code:     "E206",
						Table:    mv.Name,
						Message:  "index " + idx.Name + " is a prefix of index " + other.Name,
					})
					break
				}
			}
		}
	}
	return diags
}

// checkVarcharUsage (E207): varchar/character varying usage detected.
func checkVarcharUsage(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			lower := strings.ToLower(col.PGType)
			if strings.Contains(lower, "varchar") || strings.Contains(lower, "character varying") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E207",
					Table:      t.Name,
					Column:     col.Name,
					Message:    "varchar usage: use text with CHECK constraint instead",
					Suggestion: "Replace with text + CHECK(length(col) <= N)",
				})
			}
		}
	}
	return diags
}

// checkTimestampNoTZ (E208): timestamp without time zone usage.
func checkTimestampNoTZ(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			lower := strings.ToLower(col.PGType)
			if lower == "timestamp" || lower == "timestamp without time zone" {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E208",
					Table:      t.Name,
					Column:     col.Name,
					Message:    "timestamp without time zone; use timestamptz",
					Suggestion: "Use timestamptz (timestamp with time zone)",
				})
			}
		}
	}
	return diags
}

// checkSerialUsage (E209): serial/bigserial usage detected.
func checkSerialUsage(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			lower := strings.ToLower(col.PGType)
			if strings.Contains(lower, "serial") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E209",
					Table:      t.Name,
					Column:     col.Name,
					Message:    "serial usage: use identity column (auto_id) or uuid (id) instead",
					Suggestion: "Use GENERATED ALWAYS AS IDENTITY or uuid",
				})
			}
		}
	}
	return diags
}

// checkFloatMoney (E210): float/real/double used on money-related column.
func checkFloatMoney(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	moneyKeywords := []string{"price", "cost", "amount", "balance", "total", "fee"}
	floatTypes := []string{"real", "float", "double precision"}

	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			lower := strings.ToLower(col.PGType)
			isFloat := false
			for _, ft := range floatTypes {
				if lower == ft || strings.HasPrefix(lower, "float") {
					isFloat = true
					break
				}
			}
			if !isFloat {
				continue
			}

			colLower := strings.ToLower(col.Name)
			for _, kw := range moneyKeywords {
				if strings.Contains(colLower, kw) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E210",
						Table:      t.Name,
						Column:     col.Name,
						Message:    "float type used for money-related column; use numeric/decimal",
						Suggestion: "Use numeric(precision, scale) for monetary values",
					})
					break
				}
			}
		}
	}
	return diags
}

// snakeCasePattern matches valid snake_case identifiers.
var snakeCasePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// checkNamingConvention (E211): table/column names must match snake_case.
func checkNamingConvention(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	pattern := snakeCasePattern
	if config.NamingPattern != "" && config.NamingPattern != "snake_case" {
		return []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Code:     "E211",
			Message:  fmt.Sprintf("unsupported naming pattern: %q", config.NamingPattern),
		}}
	}

	for _, t := range schema.Tables {
		if !pattern.MatchString(t.Name) {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E211",
				Table:    t.Name,
				Message:  "table name \"" + t.Name + "\" violates naming convention (snake_case)",
			})
		}
		for _, col := range t.Columns {
			if !pattern.MatchString(col.Name) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E211",
					Table:    t.Name,
					Column:   col.Name,
					Message:  "column name \"" + col.Name + "\" violates naming convention (snake_case)",
				})
			}
		}
		for _, idx := range t.Indexes {
			if !pattern.MatchString(idx.Name) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E211",
					Table:    t.Name,
					Message:  "index name \"" + idx.Name + "\" violates naming convention (snake_case)",
				})
			}
		}
	}
	for _, v := range schema.Views {
		if !pattern.MatchString(v.Name) {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E211",
				Table:    v.Name,
				Message:  "view name \"" + v.Name + "\" violates naming convention (snake_case)",
			})
		}
	}
	for _, mv := range schema.MaterializedViews {
		if !pattern.MatchString(mv.Name) {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E211",
				Table:    mv.Name,
				Message:  "materialized view name \"" + mv.Name + "\" violates naming convention (snake_case)",
			})
		}
		for _, idx := range mv.Indexes {
			if !pattern.MatchString(idx.Name) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E211",
					Table:    mv.Name,
					Message:  "index name \"" + idx.Name + "\" violates naming convention (snake_case)",
				})
			}
		}
	}
	return diags
}

// checkFKMissingIndex (E212): FK columns have no covering index.
func checkFKMissingIndex(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			if !t.HasIndexCovering(fk.Columns) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E212",
					Table:      t.Name,
					Message:    "FK " + fk.Name + " columns have no covering index",
					Suggestion: "Add an index on (" + strings.Join(fk.Columns, ", ") + ")",
				})
			}
		}
	}
	return diags
}

// extractColumnRefs extracts column name references from a SQL expression.
// It parses the expression into an AST, then walks it collecting bare column names.
// Qualified names (table.column) are returned as the final part only, since this
// function is used for checking generated column references within the same table.
func extractColumnRefs(expr, context string) ([]string, *diagnostic.Diagnostic) {
	node, diag := sqlutil.ParseExpr(expr, context)
	if diag != nil {
		return nil, diag
	}
	refs := sqlexpr.CollectColumnRefs(node)
	var names []string
	for _, ref := range refs {
		// Use the last part (column name) for bare and qualified refs.
		name := strings.ToLower(ref.Parts[len(ref.Parts)-1])
		names = append(names, name)
	}
	return names, nil
}

// checkGeneratedColRefsGenerated (E213): generated column expression references another generated column.
func checkGeneratedColRefsGenerated(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		// Build set of generated column names for this table.
		genCols := make(map[string]bool)
		for _, col := range t.Columns {
			if col.Generated != "" {
				genCols[col.Name] = true
			}
		}
		if len(genCols) < 2 {
			continue
		}

		// Check each generated column's expression for references to other generated columns.
		for _, col := range t.Columns {
			if col.Generated == "" {
				continue
			}
			refs, parseDiag := extractColumnRefs(col.Generated, fmt.Sprintf("generated column %s.%s", t.Name, col.Name))
			if parseDiag != nil {
				parseDiag.Code = "E213"
				parseDiag.Table = t.Name
				parseDiag.Column = col.Name
				diags = append(diags, *parseDiag)
				continue
			}
			for _, ref := range refs {
				if ref != col.Name && genCols[ref] {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E213",
						Table:      t.Name,
						Column:     col.Name,
						Message:    fmt.Sprintf("generated column %q references another generated column %q", col.Name, ref),
						Suggestion: "Generated columns should only reference regular (non-generated) columns",
					})
				}
			}
		}
	}
	return diags
}

// checkOpclassMissingExtension (E214): index opclass requires an extension not declared.
func checkOpclassMissingExtension(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.ExtRegistry == nil {
		return []diagnostic.Diagnostic{{
			Severity: diagnostic.Hint,
			Code:     "E214",
			Message:  "E214 skipped: no extension registry configured",
		}}
	}

	var diags []diagnostic.Diagnostic
	declaredExts := make(map[string]bool, len(config.Extensions))
	for _, ext := range config.Extensions {
		declaredExts[ext] = true
	}

	for _, t := range schema.Tables {
		for _, idx := range t.Indexes {
			if len(idx.Opclasses) == 0 {
				continue
			}
			// Check each per-column opclass. Deduplicate to avoid
			// reporting the same missing extension multiple times per index.
			checked := make(map[string]bool)
			for col, oc := range idx.Opclasses {
				if checked[oc] {
					continue
				}
				checked[oc] = true
				reqExt, found := config.ExtRegistry.RequiredExtension(oc)
				if !found {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Hint,
						Code:     "E214",
						Table:    t.Name,
						Message:  fmt.Sprintf("index %s: unrecognized opclass %q (on column %s); not validated", idx.Name, oc, col),
					})
					continue
				}
				if !declaredExts[reqExt] {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E214",
						Table:      t.Name,
						Message:    "index " + idx.Name + " uses opclass " + oc + " (on column " + col + ") which requires extension " + reqExt,
						Suggestion: "Add \"" + reqExt + "\" to [meta].extensions",
					})
				}
			}
		}
	}
	for _, mv := range schema.MaterializedViews {
		for _, idx := range mv.Indexes {
			if len(idx.Opclasses) == 0 {
				continue
			}
			checked := make(map[string]bool)
			for col, oc := range idx.Opclasses {
				if checked[oc] {
					continue
				}
				checked[oc] = true
				reqExt, found := config.ExtRegistry.RequiredExtension(oc)
				if !found {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Hint,
						Code:     "E214",
						Table:    mv.Name,
						Message:  fmt.Sprintf("index %s: unrecognized opclass %q (on column %s); not validated", idx.Name, oc, col),
					})
					continue
				}
				if !declaredExts[reqExt] {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E214",
						Table:      mv.Name,
						Message:    "index " + idx.Name + " uses opclass " + oc + " (on column " + col + ") which requires extension " + reqExt,
						Suggestion: "Add \"" + reqExt + "\" to [meta].extensions",
					})
				}
			}
		}
	}
	return diags
}

// checkIndexWithParams (E216): index WITH parameter is not valid for the index method.
func checkIndexWithParams(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.ExtRegistry == nil {
		return []diagnostic.Diagnostic{{
			Severity: diagnostic.Hint,
			Code:     "E216",
			Message:  "E216 skipped: no extension registry configured",
		}}
	}

	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, idx := range t.Indexes {
			if len(idx.With) == 0 {
				continue
			}
			method := idx.Method
			if method == "" {
				method = "btree"
			}
			validParams, ok := config.ExtRegistry.ValidIndexParams(method)
			if !ok {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Hint,
					Code:     "E216",
					Table:    t.Name,
					Message:  fmt.Sprintf("index %s: unrecognized method %q; WITH parameters not validated", idx.Name, method),
				})
				continue
			}
			for key := range idx.With {
				valid := false
				for _, vp := range validParams {
					if key == vp {
						valid = true
						break
					}
				}
				if !valid {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E216",
						Table:      t.Name,
						Message:    fmt.Sprintf("index %s has invalid WITH parameter %q for method %s", idx.Name, key, method),
						Suggestion: fmt.Sprintf("Valid parameters for %s: %s", method, strings.Join(validParams, ", ")),
					})
				}
			}
		}
	}
	for _, mv := range schema.MaterializedViews {
		for _, idx := range mv.Indexes {
			if len(idx.With) == 0 {
				continue
			}
			method := idx.Method
			if method == "" {
				method = "btree"
			}
			validParams, ok := config.ExtRegistry.ValidIndexParams(method)
			if !ok {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Hint,
					Code:     "E216",
					Table:    mv.Name,
					Message:  fmt.Sprintf("index %s: unrecognized method %q; WITH parameters not validated", idx.Name, method),
				})
				continue
			}
			for key := range idx.With {
				valid := false
				for _, vp := range validParams {
					if key == vp {
						valid = true
						break
					}
				}
				if !valid {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E216",
						Table:      mv.Name,
						Message:    fmt.Sprintf("index %s has invalid WITH parameter %q for method %s", idx.Name, key, method),
						Suggestion: fmt.Sprintf("Valid parameters for %s: %s", method, strings.Join(validParams, ", ")),
					})
				}
			}
		}
	}
	return diags
}

// builtinIndexMethods is the set of index methods built into PostgreSQL core.
// These never require an extension declaration.
var builtinIndexMethods = map[string]bool{
	"btree":  true,
	"gin":    true,
	"gist":   true,
	"brin":   true,
	"hash":   true,
	"spgist": true,
}

// checkUnknownIndexMethod (E217): index uses an unknown index method that is
// not built into PostgreSQL and not provided by any known extension.
func checkUnknownIndexMethod(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.ExtRegistry == nil {
		return nil
	}

	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, idx := range t.Indexes {
			method := strings.ToLower(idx.Method)
			if method == "" || builtinIndexMethods[method] {
				continue
			}
			_, found := config.ExtRegistry.RequiredExtensionForMethod(method)
			if !found {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E217",
					Table:      t.Name,
					Message:    fmt.Sprintf("unknown index method %q on index %s", method, idx.Name),
					Suggestion: "Use a built-in method (btree, gin, gist, brin, hash, spgist) or declare the providing extension",
				})
			}
		}
	}
	return diags
}

// checkIndexMethodMissingExtension (E219): index uses an extension-provided
// method (e.g., hnsw, ivfflat) without the providing extension being declared.
func checkIndexMethodMissingExtension(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.ExtRegistry == nil {
		return nil
	}

	var diags []diagnostic.Diagnostic
	declaredExts := make(map[string]bool, len(config.Extensions))
	for _, ext := range config.Extensions {
		declaredExts[ext] = true
	}

	for _, t := range schema.Tables {
		for _, idx := range t.Indexes {
			method := strings.ToLower(idx.Method)
			if method == "" || builtinIndexMethods[method] {
				continue
			}
			reqExt, found := config.ExtRegistry.RequiredExtensionForMethod(method)
			if !found {
				continue
			}
			if !declaredExts[reqExt] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E219",
					Table:      t.Name,
					Message:    fmt.Sprintf("index method %q requires extension %q which is not declared", method, reqExt),
					Suggestion: fmt.Sprintf("Add %q to [meta].extensions", reqExt),
				})
			}
		}
	}
	return diags
}

// checkPolicyExprMismatch (E215): RLS policy uses wrong expression for its operation.
// INSERT should use with_check (not using); SELECT/DELETE should use using (not with_check).
// UPDATE and ALL can use both.
func checkPolicyExprMismatch(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, pol := range t.Policies {
			switch pol.Operation {
			case "INSERT":
				if pol.Using != "" && pol.WithCheck == "" {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E215",
						Table:      t.Name,
						Message:    fmt.Sprintf("policy %q with FOR INSERT should use \"with_check\", not \"using\"", pol.Name),
						Suggestion: "INSERT policies filter new rows; use with_check instead of using",
					})
				}
			case "SELECT":
				if pol.WithCheck != "" {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E215",
						Table:      t.Name,
						Message:    fmt.Sprintf("policy %q with FOR SELECT cannot use \"with_check\"", pol.Name),
						Suggestion: "SELECT policies filter existing rows; use using instead of with_check",
					})
				}
			case "DELETE":
				if pol.WithCheck != "" {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Error,
						Code:       "E215",
						Table:      t.Name,
						Message:    fmt.Sprintf("policy %q with FOR DELETE cannot use \"with_check\"", pol.Name),
						Suggestion: "DELETE policies filter existing rows; use using instead of with_check",
					})
				}
			// UPDATE and ALL can have both -- no check needed.
			}
		}
	}
	return diags
}

// checkVirtualRequiresPG18 flags generated columns with stored=false when the
// target PG version does not support VIRTUAL generated columns (requires PG 18+).
//
// E218 is emitted as an error when pgVersion is between 1 and 17, since those
// versions only support STORED generated columns. E218 is emitted as a warning
// when pgVersion is 0 (not configured), because support cannot be verified.
// In both cases the suggestion directs users to either set stored = true or
// configure pg_version >= 18 in pgdesign.toml.
func checkVirtualRequiresPG18(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, c := range t.Columns {
			if c.Generated == "" || c.Stored {
				continue
			}
			// stored=false with a generated expression means VIRTUAL.
			if schema.PGVersion > 0 && schema.PGVersion < 18 {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E218",
					Table:      t.Name,
					Column:     c.Name,
					Message:    fmt.Sprintf("VIRTUAL generated column %q requires PostgreSQL 18+, but target version is %d", c.Name, schema.PGVersion),
					Suggestion: "Set stored = true, or configure pg_version >= 18 in pgdesign.toml",
				})
			} else if schema.PGVersion == 0 {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "E218",
					Table:      t.Name,
					Column:     c.Name,
					Message:    fmt.Sprintf("VIRTUAL generated column %q requires PostgreSQL 18+; target version is not configured", c.Name),
					Suggestion: "Set pg_version in [meta] to confirm PG 18+ support, or set stored = true",
				})
			}
		}
	}
	return diags
}

// checkPolicyErrorCodeSnakeCase (W009): RLS policy error_code should be snake_case.
func checkPolicyErrorCodeSnakeCase(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, pol := range t.Policies {
			if pol.ErrorCode == "" {
				continue
			}
			if !snakeCasePattern.MatchString(pol.ErrorCode) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W009",
					Table:      t.Name,
					Message:    fmt.Sprintf("policy %q error_code %q is not snake_case", pol.Name, pol.ErrorCode),
					Suggestion: "Use snake_case for error codes (e.g., \"chat_disabled\")",
				})
			}
		}
	}
	return diags
}

// --- Warning rules ---

// checkGodTable (W001): table has more columns than MaxColumns.
func checkGodTable(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if len(t.Columns) > config.MaxColumns {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W001",
				Table:      t.Name,
				Message:    "god table: too many columns (consider decomposition)",
				Suggestion: "Decompose into smaller, focused tables",
			})
		}
	}
	return diags
}

// checkOrphanTable (W002): table has no FK relationships at all.
func checkOrphanTable(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	// Build set of tables that are referenced by other tables' FKs.
	referenced := make(map[string]bool)
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			key := fk.RefSchema + "." + fk.RefTable
			referenced[key] = true
		}
	}

	for _, t := range schema.Tables {
		hasOutgoing := len(t.FKs) > 0
		key := t.Schema + "." + t.Name
		hasIncoming := referenced[key]
		if !hasOutgoing && !hasIncoming {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "W002",
				Table:    t.Name,
				Message:  "orphan table: no FK relationships (neither referencing nor referenced)",
			})
		}
	}
	return diags
}

// checkBooleanStates (W003): table has 3+ boolean columns, suggesting an enum state machine.
func checkBooleanStates(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		count := 0
		for _, col := range t.Columns {
			if strings.ToLower(col.PGType) == "boolean" {
				count++
			}
		}
		if count >= 3 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W003",
				Table:      t.Name,
				Message:    fmt.Sprintf("%d boolean columns suggest an enum/state machine", count),
				Suggestion: "Consider replacing boolean flags with an enum column",
			})
		}
	}
	return diags
}

// checkJSONCouldBeTable (W004): plural-named jsonb column with empty array default.
func checkJSONCouldBeTable(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			if strings.ToLower(col.PGType) != "jsonb" {
				continue
			}
			if !strings.HasSuffix(col.Name, "s") {
				continue
			}
			if (col.Default != nil && *col.Default == "'[]'::jsonb") || strings.Contains(col.DefaultExpr, "[]") {
				d := diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W004",
					Table:      t.Name,
					Column:     col.Name,
					Message:    "jsonb array column could be a separate table",
					Suggestion: "Consider normalizing into a related table with a foreign key",
				}
				if col.JSONSchema != "" {
					d.Suppressed = true
				}
				diags = append(diags, d)
			}
		}
	}
	return diags
}

// checkMissingTimestamps (W005): non-junction table lacks created_at.
func checkMissingTimestamps(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		// Skip junction tables (2 or fewer columns).
		if len(t.Columns) <= 2 {
			continue
		}
		hasCreatedAt := false
		for _, col := range t.Columns {
			if col.Name == "created_at" {
				hasCreatedAt = true
				break
			}
		}
		if !hasCreatedAt {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W005",
				Table:      t.Name,
				Message:    "missing created_at column",
				Suggestion: "Add created_at timestamptz column with default now()",
			})
		}
	}
	return diags
}

// checkPreferText (W006): char(n) usage detected.
func checkPreferText(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			lower := strings.ToLower(col.PGType)
			if strings.Contains(lower, "char(") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W006",
					Table:      t.Name,
					Column:     col.Name,
					Message:    "char(n) usage: prefer text",
					Suggestion: "Use text instead of char(n)",
				})
			}
		}
	}
	return diags
}

// checkRedundantIndex (W007): same method, one index's columns are a leading prefix of another.
func checkRedundantIndex(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for i, idx := range t.Indexes {
			for j, other := range t.Indexes {
				if i == j {
					continue
				}
				// Only compare indexes with the same method.
				if idx.Method != other.Method {
					continue
				}
				if isPrefix(idx.Columns, other.Columns) && len(idx.Columns) < len(other.Columns) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Warning,
						Code:       "W007",
						Table:      t.Name,
						Message:    "redundant index: " + idx.Name + " is a prefix of " + other.Name + " (same method)",
						Suggestion: "Drop " + idx.Name + "; " + other.Name + " already covers its queries",
					})
					break
				}
			}
		}
	}
	for _, mv := range schema.MaterializedViews {
		for i, idx := range mv.Indexes {
			for j, other := range mv.Indexes {
				if i == j {
					continue
				}
				if idx.Method != other.Method {
					continue
				}
				if isPrefix(idx.Columns, other.Columns) && len(idx.Columns) < len(other.Columns) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Warning,
						Code:       "W007",
						Table:      mv.Name,
						Message:    "redundant index: " + idx.Name + " is a prefix of " + other.Name + " (same method)",
						Suggestion: "Drop " + idx.Name + "; " + other.Name + " already covers its queries",
					})
					break
				}
			}
		}
	}
	return diags
}

// checkCircularFK (W008): circular FK dependencies detected.
func checkCircularFK(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, group := range schema.CycleGroups {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Warning,
			Code:     "W008",
			Message:  "circular FK dependency: " + strings.Join(group, " -> "),
		})
	}
	return diags
}

// checkAppendOnlyUpdatedAt (W010): append-only table has a column suggesting mutability.
func checkAppendOnlyUpdatedAt(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if !t.AppendOnly {
			continue
		}
		for _, col := range t.Columns {
			if col.SemanticTypeName == "timestamp" && strings.Contains(col.Name, "updated") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W010",
					Table:      t.Name,
					Column:     col.Name,
					Message:    fmt.Sprintf("append-only table %q has column %q suggesting mutability", t.Name, col.Name),
					Suggestion: "Append-only tables cannot be updated; consider removing this column",
				})
			}
		}
	}
	return diags
}

// checkDependsOnResolvable (E220): depends_on references must resolve to existing tables, views, or materialized views.
func checkDependsOnResolvable(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	// Build lookup set of all known names.
	known := make(map[string]bool)
	for _, t := range schema.Tables {
		known[t.Name] = true
	}
	for _, v := range schema.Views {
		known[v.Name] = true
	}
	for _, mv := range schema.MaterializedViews {
		known[mv.Name] = true
	}

	// Build lookup for matview names (for cross-type warning).
	matviewNames := make(map[string]bool)
	for _, mv := range schema.MaterializedViews {
		matviewNames[mv.Name] = true
	}

	var diags []diagnostic.Diagnostic
	for _, v := range schema.Views {
		for _, dep := range v.DependsOn {
			if !known[dep] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E220",
					Table:      v.Name,
					Message:    fmt.Sprintf("view %q depends_on %q which does not exist", v.Name, dep),
					Suggestion: "Check the depends_on list for typos or missing definitions",
				})
			} else if matviewNames[dep] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "E220",
					Table:      v.Name,
					Message:    fmt.Sprintf("view %q depends_on materialized view %q; cross-type dependency ordering is not enforced", v.Name, dep),
					Suggestion: "Views are emitted before materialized views in DDL; this dependency may not be satisfied at creation time",
				})
			}
		}
	}
	for _, mv := range schema.MaterializedViews {
		for _, dep := range mv.DependsOn {
			if !known[dep] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E220",
					Table:      mv.Name,
					Message:    fmt.Sprintf("materialized view %q depends_on %q which does not exist", mv.Name, dep),
					Suggestion: "Check the depends_on list for typos or missing definitions",
				})
			}
		}
	}
	return diags
}

// --- Helpers ---

// isPrefix returns true if a is a prefix of b (element-wise).
func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
