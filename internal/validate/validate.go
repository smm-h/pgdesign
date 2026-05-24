// Package validate provides the strict validation engine for pgdesign schemas.
// It operates on the resolved IR and returns diagnostics for rule violations.
package validate

import (
	"regexp"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
)

// Config controls which rules run and their parameters.
type Config struct {
	Disabled      []string             // codes to skip, e.g. ["W002", "W005"]
	NamingPattern string               // "snake_case" (default)
	MaxColumns    int                  // default 30
	Extensions    []string             // declared extensions (from meta)
	ExtRegistry   *extregistry.Registry
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		NamingPattern: "snake_case",
		MaxColumns:    30,
	}
}

// Validate runs all validation rules against the schema and returns diagnostics.
func Validate(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
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
		{"E214", checkOpclassMissingExtension},
		{"W001", checkGodTable},
		{"W002", checkOrphanTable},
		{"W005", checkMissingTimestamps},
		{"W006", checkPreferText},
		{"W008", checkCircularFK},
	}

	for _, r := range rules {
		if disabled[r.code] {
			continue
		}
		diags = append(diags, r.fn(schema, config)...)
	}

	return diags
}

// --- Error rules ---

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

// checkFKRefNotFound (E204): FK references a table that doesn't exist in schema.
func checkFKRefNotFound(schema *model.Schema, _ *Config) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for _, t := range schema.Tables {
		for _, fk := range t.FKs {
			if schema.TableByName(fk.RefSchema, fk.RefTable) == nil {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E204",
					Table:    t.Name,
					Message:  "FK " + fk.Name + " references non-existent table " + fk.RefSchema + "." + fk.RefTable,
				})
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
		// Only snake_case is supported for now; other patterns could be added.
		return nil
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
	}
	return diags
}

// checkOpclassMissingExtension (E214): index opclass requires an extension not declared.
func checkOpclassMissingExtension(schema *model.Schema, config *Config) []diagnostic.Diagnostic {
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
			if idx.Opclass == "" {
				continue
			}
			reqExt, found := config.ExtRegistry.RequiredExtension(idx.Opclass)
			if !found {
				continue
			}
			if !declaredExts[reqExt] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Error,
					Code:       "E214",
					Table:      t.Name,
					Message:    "index " + idx.Name + " uses opclass " + idx.Opclass + " which requires extension " + reqExt,
					Suggestion: "Add \"" + reqExt + "\" to [meta].extensions",
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
