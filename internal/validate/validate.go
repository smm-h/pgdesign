// Package validate provides the strict validation engine for pgdesign schemas.
// It operates on the resolved IR and returns diagnostics for rule violations.
package validate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
	"github.com/smm-h/pgdesign/internal/sqlutil"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// Config controls which rules run and their parameters.
type Config struct {
	Disabled          []string              // codes to skip, e.g. ["W002", "W005"]
	Suppress          map[string]string     // per-table/column suppression, key: "table.column.CODE" or "table.CODE", value: reason
	NamingPattern     string                // "snake_case" (default)
	MaxColumns        int                   // default 30
	CascadeMaxDepth   int                   // default 3
	CascadeMaxBreadth int                   // default 5
	Extensions        []string              // declared extensions (from meta)
	ExtRegistry       *extregistry.Registry
	TypeRegistry      *semtype.Registry     // semantic type registry (for state machine checks)
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
	if config.CascadeMaxDepth == 0 {
		config.CascadeMaxDepth = 3
	}
	if config.CascadeMaxBreadth == 0 {
		config.CascadeMaxBreadth = 5
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
		{"E221", checkExclusionBtreeGist},
		{"E222", checkRestrictivePolicyRequiresPG10},
		{"W011", checkRLSWithoutPolicies},
		{"W012", checkRLSOperationGap},
		{"W013", checkCascadeDepth},
		{"W014", checkCascadeBreadth},
		{"W015", checkMixedOnDelete},
		{"I001", checkNaturalKey},
		{"W016", checkPKSubsumesUnique},
		{"W017", checkRedundantNullCheck},
		{"W018", checkDomainCheckDuplicate},
		{"W019", checkRangeSubsumption},
		{"I002", checkDeadColumn},
		{"I003", checkRowSize},
		{"W027", checkSMUnreachableState},
		{"W028", checkSMDeadEndState},
		{"E223", checkSMRequiresColumn},
		{"E224", checkSMDefaultMismatch},
		{"E226", checkSMReservedTriggerPrefix},
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
			if col.PGType.Base == "" {
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
			if col.PGType.Base == "varchar" || col.PGType.Base == "char" {
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
			if col.PGType.Base == "timestamp" {
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
			base := col.PGType.Base
			if base == "serial" || base == "bigserial" || base == "smallserial" {
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
	floatTypes := []string{"float4", "float8"}

	for _, t := range schema.Tables {
		for _, col := range t.Columns {
			base := col.PGType.Base
			isFloat := false
			for _, ft := range floatTypes {
				if base == ft {
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
			if col.PGType.Base == "bool" {
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
			if col.PGType.Base != "jsonb" {
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
			if col.PGType.Base == "char" && col.PGType.Params.Length != nil {
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

// checkExclusionBtreeGist checks whether exclusion constraints using non-range
// operators require the btree_gist extension.
func checkExclusionBtreeGist(s *model.Schema, config *Config) []diagnostic.Diagnostic {
	// Build a set of declared extensions.
	extSet := make(map[string]bool, len(config.Extensions))
	for _, ext := range config.Extensions {
		extSet[ext] = true
	}

	var diags []diagnostic.Diagnostic
	for _, t := range s.Tables {
		for _, exc := range t.Exclusions {
			needsBtreeGist := false
			for _, elem := range exc.Elements {
				// The && operator is native to GiST for range types.
				// All other operators (=, <>, <, >, etc.) on scalar types
				// require btree_gist to provide GiST operator classes.
				if elem.Operator != "&&" {
					needsBtreeGist = true
					break
				}
			}
			if needsBtreeGist && !extSet["btree_gist"] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E221",
					Table:    t.Name,
					Message:  fmt.Sprintf("exclusion constraint %q uses non-range operators; declare btree_gist in [[extensions]]", exc.Name),
				})
			}
		}
	}
	return diags
}

// checkRestrictivePolicyRequiresPG10 flags RESTRICTIVE policies when the target
// PG version does not support them (requires PG 10+).
func checkRestrictivePolicyRequiresPG10(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, pol := range t.Policies {
			if pol.Type != "RESTRICTIVE" {
				continue
			}
			if schema.PGVersion > 0 && schema.PGVersion < 10 {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E222",
					Table:      t.Name,
					Message:    fmt.Sprintf("RESTRICTIVE policy %q requires PostgreSQL 10+, but target version is %d", pol.Name, schema.PGVersion),
					Suggestion: "Use type = \"permissive\", or configure pg_version >= 10 in pgdesign.toml",
				})
			} else if schema.PGVersion == 0 {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "E222",
					Table:      t.Name,
					Message:    fmt.Sprintf("RESTRICTIVE policy %q requires PostgreSQL 10+; target version is not configured", pol.Name),
					Suggestion: "Set pg_version in [meta] to confirm PG 10+ support, or use type = \"permissive\"",
				})
			}
		}
	}
	return diags
}

// checkRLSWithoutPolicies warns when enable_rls is set but no policies are defined.
func checkRLSWithoutPolicies(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if t.EnableRLS && len(t.Policies) == 0 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W011",
				Table:      t.Name,
				Message:    fmt.Sprintf("table %q has enable_rls = true but no policies defined", t.Name),
				Suggestion: "Add RLS policies, or RLS will deny all access to non-owner roles",
			})
		}
	}
	return diags
}

// checkRLSOperationGap warns when RLS is enabled but policies don't cover all operations.
func checkRLSOperationGap(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if !t.EnableRLS || len(t.Policies) == 0 {
			continue
		}
		covered := map[string]bool{
			"SELECT": false,
			"INSERT": false,
			"UPDATE": false,
			"DELETE": false,
		}
		for _, pol := range t.Policies {
			op := strings.ToUpper(pol.Operation)
			if op == "ALL" {
				// ALL covers every operation.
				for k := range covered {
					covered[k] = true
				}
			} else if _, ok := covered[op]; ok {
				covered[op] = true
			}
		}
		var missing []string
		// Deterministic order for output.
		for _, op := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			if !covered[op] {
				missing = append(missing, op)
			}
		}
		if len(missing) > 0 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W012",
				Table:      t.Name,
				Message:    fmt.Sprintf("table %q has RLS enabled but no policies for: %s", t.Name, strings.Join(missing, ", ")),
				Suggestion: "Uncovered operations default to the table's PERMISSIVE/RESTRICTIVE behavior; add explicit policies if needed",
			})
		}
	}
	return diags
}

// checkCascadeDepth (W013): CASCADE chain exceeds max depth threshold.
func checkCascadeDepth(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if schema.FKGraph == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		depth := schema.FKGraph.CascadeDepth(t.Name)
		if depth > config.CascadeMaxDepth {
			chain := schema.FKGraph.CascadeChain(t.Name)
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W013",
				Table:      t.Name,
				Message:    fmt.Sprintf("CASCADE depth %d exceeds threshold %d: %s -> %s", depth, config.CascadeMaxDepth, t.Name, strings.Join(chain, " -> ")),
				Suggestion: "Consider using RESTRICT or SET NULL to break the cascade chain",
			})
		}
	}
	return diags
}

// checkCascadeBreadth (W014): single DELETE cascades to too many tables.
func checkCascadeBreadth(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if schema.FKGraph == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		breadth := schema.FKGraph.CascadeBreadth(t.Name)
		if breadth >= config.CascadeMaxBreadth {
			chain := schema.FKGraph.CascadeChain(t.Name)
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W014",
				Table:      t.Name,
				Message:    fmt.Sprintf("deleting from %q cascades to %d tables: %s", t.Name, breadth, strings.Join(chain, ", ")),
				Suggestion: "Consider using RESTRICT or SET NULL to limit cascade blast radius",
			})
		}
	}
	return diags
}

// checkMixedOnDelete (W015): incoming FKs to the same table use different ON DELETE actions.
func checkMixedOnDelete(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	if schema.FKGraph == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		edges := schema.FKGraph.Reverse[t.Name]
		if len(edges) == 0 {
			continue
		}
		// Deduplicate by FKName to handle multi-column FKs.
		actionByFK := make(map[string]string)
		for _, edge := range edges {
			if _, seen := actionByFK[edge.FKName]; !seen {
				actionByFK[edge.FKName] = strings.ToUpper(edge.OnDelete)
			}
		}
		// Collect distinct actions and which FKs use them.
		fksByAction := make(map[string][]string)
		for fkName, action := range actionByFK {
			fksByAction[action] = append(fksByAction[action], fkName)
		}
		if len(fksByAction) < 2 {
			continue
		}
		// Build a deterministic message listing each action and its FKs.
		var parts []string
		for action, fks := range fksByAction {
			sort.Strings(fks)
			parts = append(parts, fmt.Sprintf("%s (%s)", action, strings.Join(fks, ", ")))
		}
		// Sort for deterministic output.
		sort.Strings(parts)
		diags = append(diags, diagnostic.Diagnostic{
			Severity:   diagnostic.Warning,
			Code:       "W015",
			Table:      t.Name,
			Message:    fmt.Sprintf("mixed ON DELETE actions on incoming FKs to %q: %s", t.Name, strings.Join(parts, "; ")),
			Suggestion: "Consider using a consistent ON DELETE action for all FKs referencing this table",
		})
	}
	return diags
}

// checkNaturalKey (I001): surfaces natural key candidates from declared FDs.
func checkNaturalKey(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if len(t.Dependencies) == 0 {
			continue
		}
		candidates := t.CandidateKeys()
		for _, key := range candidates {
			if sameColumns(key, t.PK) {
				continue
			}
			if containsSurrogateCol(t, key) {
				continue
			}
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Info,
				Code:       "I001",
				Table:      t.Name,
				Message:    fmt.Sprintf("natural key candidate: (%s) — differs from PK (%s)", strings.Join(key, ", "), strings.Join(t.PK, ", ")),
				Suggestion: "Consider whether this natural key should be enforced with a UNIQUE constraint",
			})
		}
	}
	return diags
}

// sameColumns returns true if a and b contain the same columns (order-independent).
func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make([]string, len(a))
	copy(sa, a)
	sort.Strings(sa)
	sb := make([]string, len(b))
	copy(sb, b)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// containsSurrogateCol returns true if any column in the key has a surrogate semantic type.
func containsSurrogateCol(t model.Table, key []string) bool {
	for _, colName := range key {
		for _, col := range t.Columns {
			if col.Name == colName {
				switch col.SemanticTypeName {
				case "id", "auto_id", "ref":
					return true
				}
			}
		}
	}
	return false
}

// checkPKSubsumesUnique (W016): UNIQUE constraint whose columns are a subset of the PK.
func checkPKSubsumesUnique(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if len(t.PK) == 0 {
			continue
		}
		pkSet := make(map[string]bool, len(t.PK))
		for _, col := range t.PK {
			pkSet[col] = true
		}
		for _, u := range t.Uniques {
			allInPK := true
			for _, col := range u.Columns {
				if !pkSet[col] {
					allInPK = false
					break
				}
			}
			if allInPK {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W016",
					Table:      t.Name,
					Message:    fmt.Sprintf("redundant UNIQUE constraint %q: columns (%s) are already covered by the primary key", u.Name, strings.Join(u.Columns, ", ")),
					Suggestion: "Drop the UNIQUE constraint; the primary key already enforces uniqueness",
				})
			}
		}
	}
	return diags
}

// isNotNullRe matches CHECK expressions of the form "col IS NOT NULL".
var isNotNullRe = regexp.MustCompile(`(?i)^\s*\(?\s*(\w+)\s+IS\s+NOT\s+NULL\s*\)?\s*$`)

// checkRedundantNullCheck (W017): CHECK (col IS NOT NULL) on an already NOT NULL column.
func checkRedundantNullCheck(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		notNullCols := make(map[string]bool)
		for _, col := range t.Columns {
			if col.NotNull {
				notNullCols[col.Name] = true
			}
		}
		for _, chk := range t.Checks {
			colName := extractIsNotNullColumn(chk.Expr)
			if colName == "" {
				continue
			}
			if notNullCols[colName] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W017",
					Table:      t.Name,
					Message:    fmt.Sprintf("redundant CHECK %q: column %q is already NOT NULL", chk.Name, colName),
					Suggestion: "Drop the CHECK constraint; NOT NULL already prevents null values",
				})
			}
		}
	}
	return diags
}

// extractIsNotNullColumn returns the column name if the expression is "col IS NOT NULL", or "".
func extractIsNotNullColumn(expr string) string {
	// Try AST-based detection first.
	node, err := sqlexpr.Parse(expr)
	if err == nil {
		col := isNotNullAST(node)
		if col != "" {
			return col
		}
	}
	// Fall back to regex.
	m := isNotNullRe.FindStringSubmatch(expr)
	if m != nil {
		return m[1]
	}
	return ""
}

// isNotNullAST checks if the AST represents "col IS NOT NULL", unwrapping parens.
func isNotNullAST(node sqlexpr.Node) string {
	switch n := node.(type) {
	case *sqlexpr.ParenExpr:
		return isNotNullAST(n.Inner)
	case *sqlexpr.UnaryOp:
		if strings.EqualFold(n.Op, "IS NOT NULL") {
			if ref, ok := n.Operand.(*sqlexpr.ColumnRef); ok && len(ref.Parts) == 1 {
				return ref.Parts[0]
			}
		}
	}
	return ""
}

// checkDomainCheckDuplicate (W018): column CHECK identical to its domain CHECK.
func checkDomainCheckDuplicate(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	if len(schema.Domains) == 0 {
		return diags
	}
	domainByName := make(map[string]model.Domain, len(schema.Domains))
	for _, d := range schema.Domains {
		domainByName[d.Name] = d
	}
	for _, t := range schema.Tables {
		// Build column type map for this table.
		colType := make(map[string]string, len(t.Columns))
		for _, col := range t.Columns {
			colType[col.Name] = col.SemanticTypeName
		}
		for _, chk := range t.Checks {
			col := extractCheckColumn(chk.Expr)
			if col == "" {
				continue
			}
			typeName := colType[col]
			if typeName == "" {
				continue
			}
			dom, ok := domainByName[typeName]
			if !ok || dom.Check == "" {
				continue
			}
			// Replace VALUE in domain CHECK with the column name, then normalize both.
			domExpr := replaceValue(dom.Check, col)
			if normalizeExpr(domExpr) == normalizeExpr(chk.Expr) {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W018",
					Table:      t.Name,
					Message:    fmt.Sprintf("redundant CHECK %q on column %q: identical to domain %q CHECK", chk.Name, col, dom.Name),
					Suggestion: "Drop the column-level CHECK; the domain already enforces this constraint",
				})
			}
		}
	}
	return diags
}

// extractCheckColumn walks the AST for ColumnRef nodes. Returns the column name
// if exactly one distinct column is referenced, otherwise "".
func extractCheckColumn(expr string) string {
	node, err := sqlexpr.Parse(expr)
	if err != nil {
		return ""
	}
	cols := make(map[string]bool)
	walkColumns(node, cols)
	if len(cols) != 1 {
		return ""
	}
	for c := range cols {
		return c
	}
	return ""
}

// walkColumns collects all single-part ColumnRef names from the AST.
func walkColumns(node sqlexpr.Node, cols map[string]bool) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *sqlexpr.ColumnRef:
		if len(n.Parts) == 1 {
			cols[n.Parts[0]] = true
		}
	case *sqlexpr.BinaryOp:
		walkColumns(n.Left, cols)
		walkColumns(n.Right, cols)
	case *sqlexpr.UnaryOp:
		walkColumns(n.Operand, cols)
	case *sqlexpr.ParenExpr:
		walkColumns(n.Inner, cols)
	case *sqlexpr.FuncCall:
		for _, arg := range n.Args {
			walkColumns(arg, cols)
		}
	case *sqlexpr.Cast:
		walkColumns(n.Expr, cols)
	case *sqlexpr.CaseExpr:
		for _, w := range n.Whens {
			walkColumns(w.Condition, cols)
			walkColumns(w.Result, cols)
		}
		walkColumns(n.Else, cols)
	}
}

// replaceValue replaces occurrences of VALUE (case-insensitive, word boundary) with colName.
var valueRe = regexp.MustCompile(`(?i)\bVALUE\b`)

func replaceValue(expr string, colName string) string {
	return valueRe.ReplaceAllString(expr, colName)
}

// normalizeExpr produces a canonical string from an SQL expression for comparison.
func normalizeExpr(expr string) string {
	node, err := sqlexpr.Parse(expr)
	if err != nil {
		// Fallback: lowercase, collapse whitespace.
		return collapseWhitespace(strings.ToLower(expr))
	}
	return canonicalString(node)
}

// canonicalString recursively produces a canonical string representation of an AST node.
func canonicalString(node sqlexpr.Node) string {
	if node == nil {
		return ""
	}
	switch n := node.(type) {
	case *sqlexpr.BinaryOp:
		left := canonicalString(n.Left)
		right := canonicalString(n.Right)
		op := strings.ToUpper(n.Op)
		// Sort commutative operands alphabetically.
		if op == "AND" || op == "OR" {
			parts := []string{left, right}
			sort.Strings(parts)
			return parts[0] + " " + op + " " + parts[1]
		}
		return left + " " + op + " " + right
	case *sqlexpr.UnaryOp:
		return canonicalString(n.Operand) + " " + strings.ToUpper(n.Op)
	case *sqlexpr.ParenExpr:
		// Strip parens for normalization.
		return canonicalString(n.Inner)
	case *sqlexpr.ColumnRef:
		return strings.Join(n.Parts, ".")
	case *sqlexpr.NullLiteral:
		return "NULL"
	case *sqlexpr.IntLiteral:
		return strconv.Itoa(n.Value)
	case *sqlexpr.FloatLiteral:
		return strconv.FormatFloat(n.Value, 'f', -1, 64)
	case *sqlexpr.StringLiteral:
		return "'" + n.Value + "'"
	case *sqlexpr.BoolLiteral:
		if n.Value {
			return "TRUE"
		}
		return "FALSE"
	case *sqlexpr.FuncCall:
		args := make([]string, len(n.Args))
		for i, arg := range n.Args {
			args[i] = canonicalString(arg)
		}
		return strings.ToUpper(n.Name) + "(" + strings.Join(args, ", ") + ")"
	case *sqlexpr.Cast:
		return canonicalString(n.Expr) + "::" + strings.ToUpper(n.TypeName)
	default:
		return fmt.Sprintf("%v", n)
	}
}

// collapseWhitespace normalizes runs of whitespace to single spaces and trims.
func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// checkRangeSubsumption (W019): a CHECK constraint subsumed by another on the same column.
func checkRangeSubsumption(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if len(t.Checks) < 2 {
			continue
		}
		// Extract range info for each single-column CHECK.
		type checkRange struct {
			name  string
			rng   *rangeInfo
		}
		var ranges []checkRange
		for _, chk := range t.Checks {
			ri := extractRange(chk.Expr)
			if ri != nil {
				ranges = append(ranges, checkRange{name: chk.Name, rng: ri})
			}
		}
		// For each pair on the same column, check if one subsumes the other.
		for i := 0; i < len(ranges); i++ {
			for j := 0; j < len(ranges); j++ {
				if i == j {
					continue
				}
				ri := ranges[i]
				rj := ranges[j]
				if ri.rng.Column != rj.rng.Column {
					continue
				}
				if ri.rng.subsumes(rj.rng) && !rj.rng.subsumes(ri.rng) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Warning,
						Code:       "W019",
						Table:      t.Name,
						Message:    fmt.Sprintf("redundant CHECK %q: wider range subsumed by stricter %q on column %q", ri.name, rj.name, ri.rng.Column),
						Suggestion: "Drop the wider constraint; the stricter one already enforces the range",
					})
				}
			}
		}
	}
	return diags
}

// rangeInfo represents a range constraint on a single column.
type rangeInfo struct {
	Column   string
	Low      *float64
	High     *float64
	LowIncl  bool
	HighIncl bool
}

// extractRange parses a CHECK expression and extracts a range if it matches known patterns.
func extractRange(expr string) *rangeInfo {
	node, err := sqlexpr.Parse(expr)
	if err != nil {
		return nil
	}
	return extractRangeFromAST(unwrapParens(node))
}

// unwrapParens strips outer ParenExpr wrappers.
func unwrapParens(node sqlexpr.Node) sqlexpr.Node {
	for {
		p, ok := node.(*sqlexpr.ParenExpr)
		if !ok {
			return node
		}
		node = p.Inner
	}
}

// extractRangeFromAST extracts range info from an AST node.
func extractRangeFromAST(node sqlexpr.Node) *rangeInfo {
	// Check for AND combining two comparisons: col >= L AND col <= H
	if bin, ok := node.(*sqlexpr.BinaryOp); ok && strings.EqualFold(bin.Op, "AND") {
		left := extractRangeFromAST(unwrapParens(bin.Left))
		right := extractRangeFromAST(unwrapParens(bin.Right))
		if left != nil && right != nil && left.Column == right.Column {
			return mergeRanges(left, right)
		}
		return nil
	}
	// Single comparison: col >= N, col > N, col <= N, col < N
	if bin, ok := node.(*sqlexpr.BinaryOp); ok {
		col, val, colOnLeft := extractComparisonParts(bin)
		if col == "" {
			return nil
		}
		op := strings.ToUpper(bin.Op)
		if !colOnLeft {
			// Flip: 5 <= col becomes col >= 5
			op = flipOp(op)
		}
		ri := &rangeInfo{Column: col}
		switch op {
		case ">=":
			ri.Low = &val
			ri.LowIncl = true
		case ">":
			ri.Low = &val
			ri.LowIncl = false
		case "<=":
			ri.High = &val
			ri.HighIncl = true
		case "<":
			ri.High = &val
			ri.HighIncl = false
		default:
			return nil
		}
		return ri
	}
	return nil
}

// extractComparisonParts extracts the column name and numeric value from a comparison BinaryOp.
// Returns the column name, numeric value, and whether the column was on the left side.
func extractComparisonParts(bin *sqlexpr.BinaryOp) (col string, val float64, colOnLeft bool) {
	leftCol := extractSingleColumn(bin.Left)
	rightCol := extractSingleColumn(bin.Right)
	leftVal, leftIsNum := extractNumericValue(bin.Right)
	rightVal, rightIsNum := extractNumericValue(bin.Left)

	if leftCol != "" && leftIsNum {
		return leftCol, leftVal, true
	}
	if rightCol != "" && rightIsNum {
		return rightCol, rightVal, false
	}
	return "", 0, false
}

// extractSingleColumn returns the column name if the node is a single-part ColumnRef.
func extractSingleColumn(node sqlexpr.Node) string {
	node = unwrapParens(node)
	if ref, ok := node.(*sqlexpr.ColumnRef); ok && len(ref.Parts) == 1 {
		return ref.Parts[0]
	}
	return ""
}

// extractNumericValue returns a float64 if the node is a numeric literal.
func extractNumericValue(node sqlexpr.Node) (float64, bool) {
	node = unwrapParens(node)
	switch n := node.(type) {
	case *sqlexpr.IntLiteral:
		return float64(n.Value), true
	case *sqlexpr.FloatLiteral:
		return n.Value, true
	case *sqlexpr.UnaryOp:
		// Handle negative numbers: -5 is UnaryOp{Op: "-", Operand: IntLiteral{5}}
		if n.Op == "-" {
			if v, ok := extractNumericValue(n.Operand); ok {
				return -v, true
			}
		}
	}
	return 0, false
}

// flipOp reverses a comparison operator (for when column is on the right).
func flipOp(op string) string {
	switch op {
	case ">=":
		return "<="
	case ">":
		return "<"
	case "<=":
		return ">="
	case "<":
		return ">"
	default:
		return op
	}
}

// mergeRanges combines two single-bound ranges on the same column into one.
func mergeRanges(a, b *rangeInfo) *rangeInfo {
	ri := &rangeInfo{Column: a.Column}
	// Take low from whichever has it.
	if a.Low != nil {
		ri.Low = a.Low
		ri.LowIncl = a.LowIncl
	}
	if b.Low != nil {
		if ri.Low == nil {
			ri.Low = b.Low
			ri.LowIncl = b.LowIncl
		} else {
			// Both have a low bound; take the tighter one.
			if *b.Low > *ri.Low || (*b.Low == *ri.Low && !b.LowIncl) {
				ri.Low = b.Low
				ri.LowIncl = b.LowIncl
			}
		}
	}
	// Take high from whichever has it.
	if a.High != nil {
		ri.High = a.High
		ri.HighIncl = a.HighIncl
	}
	if b.High != nil {
		if ri.High == nil {
			ri.High = b.High
			ri.HighIncl = b.HighIncl
		} else {
			// Both have a high bound; take the tighter one.
			if *b.High < *ri.High || (*b.High == *ri.High && !b.HighIncl) {
				ri.High = b.High
				ri.HighIncl = b.HighIncl
			}
		}
	}
	return ri
}

// subsumes returns true if r's range entirely covers other's range.
func (r *rangeInfo) subsumes(other *rangeInfo) bool {
	if r.Column != other.Column {
		return false
	}
	// Check low bound: r's low must be <= other's low.
	if !lowBoundCovers(r.Low, r.LowIncl, other.Low, other.LowIncl) {
		return false
	}
	// Check high bound: r's high must be >= other's high.
	if !highBoundCovers(r.High, r.HighIncl, other.High, other.HighIncl) {
		return false
	}
	return true
}

// lowBoundCovers returns true if outer's low bound is wider than or equal to inner's.
// nil means unbounded (negative infinity).
func lowBoundCovers(outerLow *float64, outerIncl bool, innerLow *float64, innerIncl bool) bool {
	if outerLow == nil {
		// Unbounded outer always covers.
		return true
	}
	if innerLow == nil {
		// Inner is unbounded but outer is not.
		return false
	}
	if *outerLow < *innerLow {
		return true
	}
	if *outerLow > *innerLow {
		return false
	}
	// Equal values: outer must be at least as inclusive.
	if outerIncl {
		return true
	}
	return !innerIncl
}

// highBoundCovers returns true if outer's high bound is wider than or equal to inner's.
// nil means unbounded (positive infinity).
func highBoundCovers(outerHigh *float64, outerIncl bool, innerHigh *float64, innerIncl bool) bool {
	if outerHigh == nil {
		return true
	}
	if innerHigh == nil {
		return false
	}
	if *outerHigh > *innerHigh {
		return true
	}
	if *outerHigh < *innerHigh {
		return false
	}
	if outerIncl {
		return true
	}
	return !innerIncl
}

// checkDeadColumn (I002): column not referenced by any constraint, index, policy, or generated column.
func checkDeadColumn(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		// Build the set of column names for extractColumnRefs context.
		columnNames := make([]string, len(t.Columns))
		for i, col := range t.Columns {
			columnNames[i] = col.Name
		}

		referenced := make(map[string]bool)

		// PK columns.
		for _, col := range t.PK {
			referenced[col] = true
		}

		// FK source columns.
		for _, fk := range t.FKs {
			for _, col := range fk.Columns {
				referenced[col] = true
			}
		}

		// Unique constraint columns.
		for _, u := range t.Uniques {
			for _, col := range u.Columns {
				referenced[col] = true
			}
		}

		// Index columns and include columns.
		for _, idx := range t.Indexes {
			for _, col := range idx.Columns {
				referenced[col] = true
			}
			for _, col := range idx.Include {
				referenced[col] = true
			}
			// Index WHERE clause.
			if idx.Where != "" {
				refs, diag := extractColumnRefs(idx.Where, fmt.Sprintf("index %s WHERE on %s", idx.Name, t.Name))
				if diag == nil {
					for _, r := range refs {
						referenced[r] = true
					}
				}
			}
		}

		// Exclusion constraint columns and WHERE clauses.
		for _, excl := range t.Exclusions {
			for _, elem := range excl.Elements {
				referenced[elem.Column] = true
			}
			if excl.Where != "" {
				refs, diag := extractColumnRefs(excl.Where, fmt.Sprintf("exclusion %s WHERE on %s", excl.Name, t.Name))
				if diag == nil {
					for _, r := range refs {
						referenced[r] = true
					}
				}
			}
		}

		// Partition columns.
		if t.Partitioning != nil {
			for _, col := range t.Partitioning.Columns {
				referenced[col] = true
			}
		}

		// CHECK constraint expressions.
		for _, chk := range t.Checks {
			refs, diag := extractColumnRefs(chk.Expr, fmt.Sprintf("CHECK %s on %s", chk.Name, t.Name))
			if diag == nil {
				for _, r := range refs {
					referenced[r] = true
				}
			}
		}

		// Generated column expressions.
		for _, col := range t.Columns {
			if col.Generated != "" {
				refs, diag := extractColumnRefs(col.Generated, fmt.Sprintf("generated column %s.%s", t.Name, col.Name))
				if diag == nil {
					for _, r := range refs {
						referenced[r] = true
					}
				}
			}
		}

		// RLS policy expressions.
		for _, policy := range t.Policies {
			if policy.Using != "" {
				refs, diag := extractColumnRefs(policy.Using, fmt.Sprintf("policy %s USING on %s", policy.Name, t.Name))
				if diag == nil {
					for _, r := range refs {
						referenced[r] = true
					}
				}
			}
			if policy.WithCheck != "" {
				refs, diag := extractColumnRefs(policy.WithCheck, fmt.Sprintf("policy %s WITH CHECK on %s", policy.Name, t.Name))
				if diag == nil {
					for _, r := range refs {
						referenced[r] = true
					}
				}
			}
		}

		// Trigger WHEN clauses.
		for _, trigger := range t.Triggers {
			if trigger.When != "" {
				refs, diag := extractColumnRefs(trigger.When, fmt.Sprintf("trigger %s WHEN on %s", trigger.Name, t.Name))
				if diag == nil {
					for _, r := range refs {
						referenced[r] = true
					}
				}
			}
		}

		// FK references from other tables.
		for _, other := range schema.Tables {
			for _, fk := range other.FKs {
				if fk.RefTable == t.Name {
					for _, col := range fk.RefColumns {
						referenced[col] = true
					}
				}
			}
		}

		// Views referencing this table: mark all columns as referenced.
		for _, view := range schema.Views {
			if strings.Contains(view.Query, t.Name) {
				for _, col := range t.Columns {
					referenced[col.Name] = true
				}
			}
		}

		// Materialized views referencing this table: mark all columns as referenced.
		for _, mv := range schema.MaterializedViews {
			if strings.Contains(mv.Query, t.Name) {
				for _, col := range t.Columns {
					referenced[col.Name] = true
				}
			}
		}

		// Functions depending on this table: mark all columns as referenced.
		for _, fn := range schema.Functions {
			for _, dep := range fn.DependsOn {
				if dep == t.Name {
					for _, col := range t.Columns {
						referenced[col.Name] = true
					}
					break
				}
			}
		}

		// Report unreferenced columns.
		for _, col := range t.Columns {
			if !referenced[col.Name] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Info,
					Code:       "I002",
					Table:      t.Name,
					Column:     col.Name,
					Message:    fmt.Sprintf("column %s.%s is not referenced by any constraint, index, policy, or generated column", t.Name, col.Name),
					Suggestion: "Verify this column is needed; unreferenced columns may indicate incomplete schema design",
				})
			}
		}
	}
	return diags
}

// --- Row size estimation ---

// pgTypeInfo holds the storage length and alignment for a PostgreSQL type.
type pgTypeInfo struct {
	Len   int  // bytes; -1 = varlena (variable length)
	Align byte // 'c' = char(1), 's' = short(2), 'i' = int(4), 'd' = double(8)
}

var pgTypeWidths = map[string]pgTypeInfo{
	"boolean":           {1, 'c'},
	"bool":              {1, 'c'},
	"smallint":          {2, 's'},
	"int2":              {2, 's'},
	"integer":           {4, 'i'},
	"int":               {4, 'i'},
	"int4":              {4, 'i'},
	"bigint":            {8, 'd'},
	"int8":              {8, 'd'},
	"real":              {4, 'i'},
	"float4":            {4, 'i'},
	"float8":            {8, 'd'},
	"double precision":  {8, 'd'},
	"date":              {4, 'i'},
	"time":              {8, 'd'},
	"timetz":            {12, 'd'},
	"timestamp":         {8, 'd'},
	"timestamptz":       {8, 'd'},
	"uuid":              {16, 'c'},
	"text":              {-1, 'i'},
	"varchar":           {-1, 'i'},
	"char":              {-1, 'i'},
	"jsonb":             {-1, 'i'},
	"json":              {-1, 'i'},
	"numeric":           {-1, 'i'},
	"decimal":           {-1, 'i'},
	"bytea":             {-1, 'i'},
	"interval":          {16, 'd'},
	"inet":              {-1, 'i'},
	"cidr":              {-1, 'i'},
	"macaddr":           {6, 'i'},
	"macaddr8":          {8, 'd'},
	"bit":               {-1, 'i'},
	"varbit":            {-1, 'i'},
	"point":             {16, 'd'},
	"line":              {24, 'd'},
	"lseg":              {32, 'd'},
	"box":               {32, 'd'},
	"path":              {-1, 'i'},
	"polygon":           {-1, 'i'},
	"circle":            {24, 'd'},
	"money":             {8, 'd'},
	"oid":               {4, 'i'},
	"xml":               {-1, 'i'},
	"tsquery":           {-1, 'i'},
	"tsvector":          {-1, 'i'},
	"int4range":         {-1, 'i'},
	"int8range":         {-1, 'i'},
	"numrange":          {-1, 'i'},
	"tsrange":           {-1, 'i'},
	"tstzrange":         {-1, 'i'},
	"daterange":         {-1, 'i'},
	"int4multirange":    {-1, 'i'},
	"int8multirange":    {-1, 'i'},
	"nummultirange":     {-1, 'i'},
	"tsmultirange":      {-1, 'i'},
	"tstzmultirange":    {-1, 'i'},
	"datemultirange":    {-1, 'i'},
}

// estimateVarlenaSize returns the estimated average byte size for a varlena column type.
func estimateVarlenaSize(t typeinfo.Type) int {
	// varchar(N) -> N/2 + 4 (4 bytes varlena header + average half-fill)
	if t.Base == "varchar" && t.Params.Length != nil {
		return *t.Params.Length/2 + 4
	}
	// char(N) -> N + 4
	if t.Base == "char" && t.Params.Length != nil {
		return *t.Params.Length + 4
	}
	switch t.Base {
	case "jsonb", "json":
		return 64
	case "bytea":
		return 32
	case "numeric":
		return 16
	default:
		return 32 // generic varlena estimate for text, etc.
	}
}

// alignTo returns offset padded to the given alignment.
func alignTo(offset int, align byte) int {
	var a int
	switch align {
	case 'c':
		a = 1
	case 's':
		a = 2
	case 'i':
		a = 4
	case 'd':
		a = 8
	default:
		a = 4
	}
	if offset%a == 0 {
		return offset
	}
	return offset + (a - offset%a)
}

// lookupTypeInfo returns the pgTypeInfo for a column, using the type lookup table.
func lookupTypeInfo(col model.Column) pgTypeInfo {
	if col.Array {
		return pgTypeInfo{-1, 'i'} // arrays are varlena
	}
	if info, ok := pgTypeWidths[col.PGType.Base]; ok {
		return info
	}
	// Unknown type: assume varlena
	return pgTypeInfo{-1, 'i'}
}

// rawDataSize computes the sum of column data sizes without alignment padding.
func rawDataSize(columns []model.Column) int {
	total := 0
	for _, col := range columns {
		info := lookupTypeInfo(col)
		if info.Len > 0 {
			total += info.Len
		} else {
			total += estimateVarlenaSize(col.PGType)
		}
	}
	return total
}

// estimateRowSize computes the estimated byte size for a table row.
// It returns (estimatedSize, paddingWaste) following PostgreSQL's storage rules.
func estimateRowSize(columns []model.Column) (size int, padding int) {
	// HeapTupleHeaderData is 23 bytes, MAXALIGN to 24
	offset := 24

	// Null bitmap: ceil(ncols/8) bytes if any column is nullable
	hasNullable := false
	for _, col := range columns {
		if !col.NotNull {
			hasNullable = true
			break
		}
	}
	if hasNullable {
		bitmapBytes := (len(columns) + 7) / 8
		offset += bitmapBytes
		// MAXALIGN the header+bitmap
		if offset%8 != 0 {
			offset += 8 - offset%8
		}
	}

	headerEnd := offset

	for _, col := range columns {
		info := lookupTypeInfo(col)
		var colSize int
		if info.Len > 0 {
			colSize = info.Len
		} else {
			colSize = estimateVarlenaSize(col.PGType)
		}
		aligned := alignTo(offset, info.Align)
		offset = aligned + colSize
	}

	// Plus 4 bytes ItemIdData per tuple
	totalSize := offset + 4
	totalPadding := totalSize - headerEnd - rawDataSize(columns)
	return totalSize, totalPadding
}

// estimateRowSizeOptimal computes the row size with columns sorted by alignment
// descending (d, i, s, c) to minimize padding waste.
func estimateRowSizeOptimal(columns []model.Column) int {
	sorted := make([]model.Column, len(columns))
	copy(sorted, columns)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai := lookupTypeInfo(sorted[i])
		aj := lookupTypeInfo(sorted[j])
		oi := alignOrder(ai.Align)
		oj := alignOrder(aj.Align)
		if oi != oj {
			return oi < oj // lower order = higher alignment = first
		}
		si := ai.Len
		if si < 0 {
			si = estimateVarlenaSize(sorted[i].PGType)
		}
		sj := aj.Len
		if sj < 0 {
			sj = estimateVarlenaSize(sorted[j].PGType)
		}
		return si > sj
	})
	size, _ := estimateRowSize(sorted)
	return size
}

func alignOrder(a byte) int {
	switch a {
	case 'd':
		return 0
	case 'i':
		return 1
	case 's':
		return 2
	case 'c':
		return 3
	default:
		return 4
	}
}

// checkRowSize (I003/W021/I004): estimates row size and warns about oversized or poorly ordered rows.
func checkRowSize(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		if len(t.Columns) == 0 {
			continue
		}
		currentSize, _ := estimateRowSize(t.Columns)

		if currentSize > 8192 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W021",
				Table:      t.Name,
				Message:    fmt.Sprintf("estimated row size %d bytes exceeds page size (8192); rows require TOAST storage", currentSize),
				Suggestion: "Consider splitting wide columns into a separate table or using TOAST-friendly types",
			})
		} else if currentSize > 2048 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Info,
				Code:       "I003",
				Table:      t.Name,
				Message:    fmt.Sprintf("estimated row size %d bytes exceeds TOAST threshold (2048)", currentSize),
				Suggestion: "Large rows reduce cache efficiency; consider whether all columns belong in this table",
			})
		}

		// Check column ordering optimization
		optimalSize := estimateRowSizeOptimal(t.Columns)
		if currentSize-optimalSize > 16 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Info,
				Code:       "I004",
				Table:      t.Name,
				Message:    fmt.Sprintf("column reordering could save %d bytes per row (current: %d, optimal: %d)", currentSize-optimalSize, currentSize, optimalSize),
				Suggestion: "Reorder columns by alignment (8-byte, 4-byte, 2-byte, 1-byte) to minimize padding",
			})
		}
	}
	return diags
}

// --- State machine checks ---

// smColumnsForTable returns (column, typeDef) pairs for SM columns on a table.
func smColumnsForTable(t model.Table, config *Config) []struct {
	Col model.Column
	TD  *semtype.TypeDef
} {
	if config.TypeRegistry == nil {
		return nil
	}
	var result []struct {
		Col model.Column
		TD  *semtype.TypeDef
	}
	for _, col := range t.Columns {
		if model.IsStateMachineColumn(col, config.TypeRegistry) {
			td, _ := config.TypeRegistry.Resolve(col.SemanticTypeName)
			result = append(result, struct {
				Col model.Column
				TD  *semtype.TypeDef
			}{col, td})
		}
	}
	return result
}

// checkSMUnreachableState (W027): state not reachable from initial state via transitions.
func checkSMUnreachableState(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.TypeRegistry == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic

	// Collect unique SM types to avoid checking the same type multiple times.
	checked := make(map[string]bool)

	for _, t := range schema.Tables {
		for _, pair := range smColumnsForTable(t, config) {
			td := pair.TD
			if checked[td.Name] {
				continue
			}
			checked[td.Name] = true

			// BFS from initial state.
			reachable := make(map[string]bool)
			queue := []string{td.InitialState}
			reachable[td.InitialState] = true
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				for _, tr := range td.Transitions {
					for _, from := range tr.From {
						if from == current && !reachable[tr.To] {
							reachable[tr.To] = true
							queue = append(queue, tr.To)
						}
					}
				}
			}

			// Report unreachable states.
			for _, s := range td.States {
				if !reachable[s.Name] {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Warning,
						Code:       "W027",
						Message:    fmt.Sprintf("state machine %q: state %q is unreachable from initial state %q", td.Name, s.Name, td.InitialState),
						Suggestion: "Add a transition leading to this state, or remove it",
					})
				}
			}
		}
	}
	return diags
}

// checkSMDeadEndState (W028): non-terminal state has no outgoing transitions.
func checkSMDeadEndState(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.TypeRegistry == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic

	checked := make(map[string]bool)

	for _, t := range schema.Tables {
		for _, pair := range smColumnsForTable(t, config) {
			td := pair.TD
			if checked[td.Name] {
				continue
			}
			checked[td.Name] = true

			// Build set of states that appear in From of some transition.
			hasOutgoing := make(map[string]bool)
			for _, tr := range td.Transitions {
				for _, from := range tr.From {
					hasOutgoing[from] = true
				}
			}

			for _, s := range td.States {
				if s.Terminal {
					continue
				}
				if !hasOutgoing[s.Name] {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Warning,
						Code:       "W028",
						Message:    fmt.Sprintf("state machine %q: non-terminal state %q has no outgoing transitions", td.Name, s.Name),
						Suggestion: "Mark this state as terminal, or add a transition from it",
					})
				}
			}
		}
	}
	return diags
}

// checkSMRequiresColumn (E223): transition requires a column that doesn't exist on the table.
func checkSMRequiresColumn(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.TypeRegistry == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic

	for _, t := range schema.Tables {
		colSet := make(map[string]bool, len(t.Columns))
		for _, col := range t.Columns {
			colSet[col.Name] = true
		}

		for _, pair := range smColumnsForTable(t, config) {
			td := pair.TD
			for _, tr := range td.Transitions {
				for reqCol := range tr.Requires {
					if !colSet[reqCol] {
						diags = append(diags, diagnostic.Diagnostic{
							Severity:   diagnostic.Error,
							Code:       "E223",
							Table:      t.Name,
							Message:    fmt.Sprintf("state machine %q transition %q requires column %q which does not exist on table %q", td.Name, tr.Name, reqCol, t.Name),
							Suggestion: "Add the required column to the table, or remove it from the transition requires",
						})
					}
				}
			}
		}
	}
	return diags
}

// checkSMDefaultMismatch (E224): column default doesn't match the SM's initial state.
func checkSMDefaultMismatch(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
	if config.TypeRegistry == nil {
		return nil
	}
	var diags []diagnostic.Diagnostic

	for _, t := range schema.Tables {
		for _, pair := range smColumnsForTable(t, config) {
			col := pair.Col
			td := pair.TD
			if col.Default == nil {
				continue
			}
			if *col.Default != td.InitialState {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E224",
					Table:      t.Name,
					Column:     col.Name,
					Message:    fmt.Sprintf("column default %q does not match state machine %q initial state %q", *col.Default, td.Name, td.InitialState),
					Suggestion: fmt.Sprintf("Set the default to %q, or change the initial state", td.InitialState),
				})
			}
		}
	}
	return diags
}

// checkSMReservedTriggerPrefix (E226): user trigger uses reserved SM prefix.
func checkSMReservedTriggerPrefix(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, trig := range t.Triggers {
			if strings.HasPrefix(trig.Name, "_pgdesign_sm_") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E226",
					Table:      t.Name,
					Message:    fmt.Sprintf("trigger %q uses reserved prefix \"_pgdesign_sm_\"", trig.Name),
					Suggestion: "Rename the trigger to avoid the reserved prefix",
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
