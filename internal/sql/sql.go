// Package sql provides shared SQL builder functions for PostgreSQL DDL generation.
// It is the single place where SQL text is constructed -- no other package builds
// SQL strings directly.
package sql

import (
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
)

// reservedWords is a set of common PostgreSQL reserved words that require quoting.
var reservedWords = map[string]bool{
	"user":       true,
	"table":      true,
	"order":      true,
	"group":      true,
	"select":     true,
	"where":      true,
	"index":      true,
	"column":     true,
	"constraint": true,
	"check":      true,
	"primary":    true,
	"foreign":    true,
	"key":        true,
	"default":    true,
	"not":        true,
	"null":       true,
	"type":       true,
	"schema":     true,
	"create":     true,
	"alter":      true,
	"drop":       true,
	"references": true,
	"cascade":    true,
	"unique":     true,
	"comment":    true,
}

// QuoteIdent quotes a PostgreSQL identifier with double-quotes if needed.
// Quoting is applied when the name is a reserved word, contains special characters,
// has uppercase letters, or starts with a digit.
func QuoteIdent(name string) string {
	if needsQuoting(name) {
		escaped := strings.ReplaceAll(name, `"`, `""`)
		return `"` + escaped + `"`
	}
	return name
}

// needsQuoting determines if an identifier needs double-quote quoting.
func needsQuoting(name string) bool {
	if name == "" {
		return true
	}
	if reservedWords[strings.ToLower(name)] {
		return true
	}
	for i, ch := range name {
		if i == 0 && ch >= '0' && ch <= '9' {
			return true
		}
		if ch >= 'A' && ch <= 'Z' {
			return true
		}
		if !isIdentChar(ch) {
			return true
		}
	}
	return false
}

// isIdentChar returns true if the character is valid in an unquoted PG identifier.
func isIdentChar(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '_'
}

// QualifiedName returns a schema-qualified name with proper quoting.
func QualifiedName(schema, name string) string {
	return QuoteIdent(schema) + "." + QuoteIdent(name)
}

// LiteralValue formats a value as a SQL literal based on its PG type.
// Strings get single quotes (with escaping), numbers are bare, booleans are bare,
// and empty values return "NULL".
func LiteralValue(value string, pgType string) string {
	if value == "" {
		return "NULL"
	}

	lower := strings.ToLower(pgType)

	// Boolean types.
	if lower == "boolean" || lower == "bool" {
		return strings.ToLower(value)
	}

	// Numeric types.
	if isNumericType(lower) {
		return value
	}

	// Everything else gets single-quoted.
	escaped := strings.ReplaceAll(value, "'", "''")
	return "'" + escaped + "'"
}

// isNumericType returns true if the PG type is numeric.
func isNumericType(lower string) bool {
	numericTypes := []string{
		"integer", "int", "int4",
		"bigint", "int8",
		"smallint", "int2",
		"numeric", "decimal",
		"real", "float4",
		"double precision", "float8",
		"serial", "bigserial", "smallserial",
	}
	for _, nt := range numericTypes {
		if lower == nt {
			return true
		}
	}
	return false
}

// ExprValue returns an expression verbatim (for DEFAULT expressions like now()).
func ExprValue(expr string) string {
	return expr
}

// ConstraintName generates a constraint name following the convention:
// pk_<table>, fk_<table>_<ref>, idx_<table>_<cols>, uq_<table>_<col>, ck_<table>_<name>.
// Kind must be one of: "pk", "fk", "idx", "uq", "ck".
func ConstraintName(table, kind string, refs ...string) string {
	parts := []string{kind, table}
	parts = append(parts, refs...)
	return strings.Join(parts, "_")
}

// CreateSchema generates a CREATE SCHEMA statement.
func CreateSchema(name string, idempotent bool) string {
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}
	return fmt.Sprintf("CREATE SCHEMA%s %s;", ifne, QuoteIdent(name))
}

// CreateExtension generates a CREATE EXTENSION statement.
func CreateExtension(name string, idempotent bool) string {
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}
	return fmt.Sprintf("CREATE EXTENSION%s %s;", ifne, QuoteIdent(name))
}

// CreateEnum generates a CREATE TYPE ... AS ENUM statement.
func CreateEnum(schema, name string, values []string, idempotent bool) string {
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}
	qualified := QualifiedName(schema, name)

	quotedValues := make([]string, len(values))
	for i, v := range values {
		escaped := strings.ReplaceAll(v, "'", "''")
		quotedValues[i] = "'" + escaped + "'"
	}

	return fmt.Sprintf("CREATE TYPE%s %s AS ENUM (%s);",
		ifne, qualified, strings.Join(quotedValues, ", "))
}

// CreateTable generates a CREATE TABLE statement with columns, inline PK, and
// PARTITION BY. Foreign keys are NOT included (they use ALTER TABLE for cycle safety).
// pgVersion controls version-specific DDL: when > 0 and < 10, identity columns
// fall back to bigserial. When 0 (unspecified) or >= 10, GENERATED AS IDENTITY is used.
// enums is the list of enum types defined in the schema; when a column's PG type
// matches an enum name, the type is emitted with its schema prefix.
func CreateTable(table *model.Table, schemaName string, idempotent bool, pgVersion int, enums []model.Enum) string {
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}

	qualified := QualifiedName(schemaName, table.Name)

	var lines []string

	// Column definitions.
	for _, col := range table.Columns {
		lines = append(lines, "    "+columnDef(col, pgVersion, enums))
	}

	// Inline PRIMARY KEY constraint.
	if len(table.PK) > 0 {
		pkName := ConstraintName(table.Name, "pk")
		quotedCols := make([]string, len(table.PK))
		for i, c := range table.PK {
			quotedCols[i] = QuoteIdent(c)
		}
		lines = append(lines, fmt.Sprintf("    CONSTRAINT %s PRIMARY KEY (%s)",
			QuoteIdent(pkName), strings.Join(quotedCols, ", ")))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE%s %s (\n", ifne, qualified))
	sb.WriteString(strings.Join(lines, ",\n"))
	sb.WriteString("\n)")

	// PARTITION BY clause.
	if table.Partitioning != nil {
		sb.WriteString(fmt.Sprintf(" PARTITION BY %s (%s)",
			strings.ToUpper(table.Partitioning.Strategy),
			QuoteIdent(table.Partitioning.Column)))
	}

	sb.WriteString(";")
	return sb.String()
}

// columnDef builds a single column definition line.
// pgVersion controls version-specific DDL (0 means unspecified, treated as latest).
// enums is used to schema-qualify enum type names in column definitions.
func columnDef(col model.Column, pgVersion int, enums []model.Enum) string {
	// Pre-PG10 identity fallback: replace identity column with bigserial.
	if col.Identity != "" && pgVersion > 0 && pgVersion < 10 {
		var parts []string
		pgType := "bigserial"
		if col.Array {
			pgType += "[]"
		}
		parts = append(parts, QuoteIdent(col.Name), pgType)
		if col.NotNull {
			parts = append(parts, "NOT NULL")
		}
		return strings.Join(parts, " ")
	}

	var parts []string
	pgType := resolveColumnType(col.PGType, enums)
	if col.Array {
		pgType += "[]"
	}
	parts = append(parts, QuoteIdent(col.Name), pgType)

	if col.NotNull {
		parts = append(parts, "NOT NULL")
	}

	if col.Identity != "" {
		parts = append(parts, fmt.Sprintf("GENERATED %s AS IDENTITY", col.Identity))
	} else if col.Generated != "" {
		parts = append(parts, fmt.Sprintf("GENERATED ALWAYS AS (%s)", col.Generated))
		if col.Stored {
			parts = append(parts, "STORED")
		}
	} else if col.DefaultExpr != "" {
		parts = append(parts, "DEFAULT "+ExprValue(col.DefaultExpr))
	} else if col.Default != "" {
		parts = append(parts, "DEFAULT "+LiteralValue(col.Default, pgType))
	}

	return strings.Join(parts, " ")
}

// resolveColumnType returns the SQL type string for a column. If the type
// matches a known enum, the enum's schema-qualified name is returned so that
// the DDL works without relying on search_path.
func resolveColumnType(pgType string, enums []model.Enum) string {
	for _, e := range enums {
		if e.Name == pgType {
			return QualifiedName(e.Schema, e.Name)
		}
	}
	return pgType
}

// AlterTableAddFK generates an ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY statement.
// When idempotent is true, wraps the statement in a DO $$ block that checks
// pg_constraint before adding.
func AlterTableAddFK(schemaName string, table *model.Table, fk *model.FK, idempotent bool) string {
	qualified := QualifiedName(schemaName, table.Name)
	constraintName := fk.Name
	if constraintName == "" {
		constraintName = ConstraintName(table.Name, "fk", fk.RefTable)
	}

	localCols := make([]string, len(fk.Columns))
	for i, c := range fk.Columns {
		localCols[i] = QuoteIdent(c)
	}

	refQualified := QualifiedName(fk.RefSchema, fk.RefTable)
	refCols := make([]string, len(fk.RefColumns))
	for i, c := range fk.RefColumns {
		refCols[i] = QuoteIdent(c)
	}

	stmt := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		qualified, QuoteIdent(constraintName),
		strings.Join(localCols, ", "),
		refQualified, strings.Join(refCols, ", "))

	if fk.OnDelete != "" {
		stmt += " ON DELETE " + strings.ToUpper(fk.OnDelete)
	}

	stmt += ";"

	if idempotent {
		return wrapIdempotentConstraint(constraintName, qualified, stmt)
	}
	return stmt
}

// AlterTableAddUnique generates an ALTER TABLE ... ADD CONSTRAINT ... UNIQUE statement.
// When idempotent is true, wraps the statement in a DO $$ block that checks
// pg_constraint before adding.
func AlterTableAddUnique(schemaName, tableName string, uq *model.UniqueConstraint, idempotent bool) string {
	qualified := QualifiedName(schemaName, tableName)
	constraintName := uq.Name
	if constraintName == "" {
		constraintName = ConstraintName(tableName, "uq", uq.Columns...)
	}

	quotedCols := make([]string, len(uq.Columns))
	for i, c := range uq.Columns {
		quotedCols[i] = QuoteIdent(c)
	}

	stmt := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s);",
		qualified, QuoteIdent(constraintName), strings.Join(quotedCols, ", "))

	if idempotent {
		return wrapIdempotentConstraint(constraintName, qualified, stmt)
	}
	return stmt
}

// AlterTableAddCheck generates an ALTER TABLE ... ADD CONSTRAINT ... CHECK statement.
// When idempotent is true, wraps the statement in a DO $$ block that checks
// pg_constraint before adding.
func AlterTableAddCheck(schemaName, tableName string, ck *model.CheckConstraint, idempotent bool) string {
	qualified := QualifiedName(schemaName, tableName)
	constraintName := ck.Name
	if constraintName == "" {
		constraintName = ConstraintName(tableName, "ck")
	}

	stmt := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);",
		qualified, QuoteIdent(constraintName), ck.Expr)

	if idempotent {
		return wrapIdempotentConstraint(constraintName, qualified, stmt)
	}
	return stmt
}

// wrapIdempotentConstraint wraps an ALTER TABLE ADD CONSTRAINT statement in a
// DO $$ block that checks pg_constraint before executing, making it idempotent.
func wrapIdempotentConstraint(constraintName, qualifiedTable, stmt string) string {
	escapedName := strings.ReplaceAll(constraintName, "'", "''")
	escapedTable := strings.ReplaceAll(qualifiedTable, "'", "''")
	return fmt.Sprintf(`DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = '%s'
    AND conrelid = '%s'::regclass
  ) THEN
    %s
  END IF;
END $$;`, escapedName, escapedTable, stmt)
}

// CreateIndex generates a CREATE INDEX statement.
// Handles Method (default btree), per-column Opclasses, WHERE, INCLUDE, and
// CONCURRENTLY. When concurrently is true, IF NOT EXISTS is omitted because
// PostgreSQL does not support combining them reliably.
func CreateIndex(schemaName string, index *model.Index, tableName string, idempotent bool, concurrently bool) string {
	// CONCURRENTLY is incompatible with IF NOT EXISTS in some PG versions,
	// so when both are requested, prefer CONCURRENTLY without IF NOT EXISTS.
	ifne := ""
	if idempotent && !concurrently {
		ifne = " IF NOT EXISTS"
	}

	conc := ""
	if concurrently {
		conc = " CONCURRENTLY"
	}

	idxName := index.Name
	if idxName == "" {
		idxName = ConstraintName(tableName, "idx", index.Columns...)
	}

	qualified := QualifiedName(schemaName, tableName)

	// Build column list with optional per-column opclass and sort direction.
	colExprs := make([]string, len(index.Columns))
	for i, c := range index.Columns {
		expr := QuoteIdent(c)
		if oc, ok := index.Opclasses[c]; ok && oc != "" {
			expr += " " + oc
		}
		if i < len(index.Desc) && index.Desc[i] {
			expr += " DESC"
		}
		colExprs[i] = expr
	}

	unique := ""
	if index.Unique {
		unique = " UNIQUE"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE%s INDEX%s%s %s ON %s",
		unique, conc, ifne, QuoteIdent(idxName), qualified))

	// USING clause (only if not btree, since btree is the default).
	method := strings.ToLower(index.Method)
	if method != "" && method != "btree" {
		sb.WriteString(fmt.Sprintf(" USING %s", method))
	}

	sb.WriteString(fmt.Sprintf(" (%s)", strings.Join(colExprs, ", ")))

	// INCLUDE clause.
	if len(index.Include) > 0 {
		includeCols := make([]string, len(index.Include))
		for i, c := range index.Include {
			includeCols[i] = QuoteIdent(c)
		}
		sb.WriteString(fmt.Sprintf(" INCLUDE (%s)", strings.Join(includeCols, ", ")))
	}

	// WHERE clause.
	if index.Where != "" {
		sb.WriteString(fmt.Sprintf(" WHERE %s", index.Where))
	}

	sb.WriteString(";")
	return sb.String()
}

// CommentOn generates a COMMENT ON statement.
func CommentOn(objectType, qualifiedName, comment string) string {
	escaped := strings.ReplaceAll(comment, "'", "''")
	return fmt.Sprintf("COMMENT ON %s %s IS '%s';",
		strings.ToUpper(objectType), qualifiedName, escaped)
}

// CreatePartitionOf generates a CREATE TABLE ... PARTITION OF statement for a
// child partition. The bound expression is emitted verbatim (e.g.
// "FROM ('2024-01-01') TO ('2024-02-01')").
func CreatePartitionOf(schemaName string, childSpec *model.PartitionSpec, parentTable string, idempotent bool) string {
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}
	childQualified := QualifiedName(schemaName, childSpec.Name)
	parentQualified := QualifiedName(schemaName, parentTable)
	return fmt.Sprintf("CREATE TABLE%s %s PARTITION OF %s\n  FOR VALUES %s;",
		ifne, childQualified, parentQualified, childSpec.Bound)
}

// CreatePartmanParent generates a SELECT partman.create_parent() call to
// register a table with pg_partman for automatic partition management.
func CreatePartmanParent(schemaName, tableName, column, interval string, premake int) string {
	qualified := QualifiedName(schemaName, tableName)
	escapedQualified := strings.ReplaceAll(qualified, "'", "''")
	escapedColumn := strings.ReplaceAll(column, "'", "''")
	escapedInterval := strings.ReplaceAll(interval, "'", "''")
	return fmt.Sprintf(`SELECT partman.create_parent(
  p_parent_table := '%s',
  p_control := '%s',
  p_interval := '%s',
  p_premake := %d
);`, escapedQualified, escapedColumn, escapedInterval, premake)
}

// UpdatePartmanConfig generates an UPDATE partman.part_config statement to
// configure retention settings for a pg_partman-managed table.
func UpdatePartmanConfig(schemaName, tableName, retention string, keepTable bool) string {
	qualified := QualifiedName(schemaName, tableName)
	escapedQualified := strings.ReplaceAll(qualified, "'", "''")
	escapedRetention := strings.ReplaceAll(retention, "'", "''")
	keepTableStr := "false"
	if keepTable {
		keepTableStr = "true"
	}
	return fmt.Sprintf(`UPDATE partman.part_config
SET retention = '%s',
    retention_keep_table = %s
WHERE parent_table = '%s';`, escapedRetention, keepTableStr, escapedQualified)
}

// AlterTableOwner generates an ALTER TABLE ... OWNER TO statement.
func AlterTableOwner(schemaName, tableName, owner string) string {
	qualified := QualifiedName(schemaName, tableName)
	return fmt.Sprintf("ALTER TABLE %s OWNER TO %s;", qualified, QuoteIdent(owner))
}

// AlterTableEnableRLS generates an ALTER TABLE ... ENABLE ROW LEVEL SECURITY statement.
func AlterTableEnableRLS(schemaName, tableName string) string {
	return fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY;", QualifiedName(schemaName, tableName))
}

// CreatePolicy generates a CREATE POLICY statement for row-level security.
// The FOR clause is omitted when operation is "ALL" (the PostgreSQL default).
// The TO clause is omitted when role is empty (defaults to PUBLIC).
// USING and WITH CHECK are wrapped in parentheses when present.
func CreatePolicy(schemaName, tableName string, p model.Policy) string {
	qualified := QualifiedName(schemaName, tableName)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE POLICY %s ON %s", QuoteIdent(p.Name), qualified))

	if p.Operation != "" && strings.ToUpper(p.Operation) != "ALL" {
		sb.WriteString(fmt.Sprintf(" FOR %s", strings.ToUpper(p.Operation)))
	}

	if p.Role != "" {
		sb.WriteString(fmt.Sprintf(" TO %s", QuoteIdent(p.Role)))
	}

	if p.Using != "" {
		sb.WriteString(fmt.Sprintf(" USING (%s)", p.Using))
	}

	if p.WithCheck != "" {
		sb.WriteString(fmt.Sprintf(" WITH CHECK (%s)", p.WithCheck))
	}

	sb.WriteString(";")
	return sb.String()
}

// CreateDenyMutationFunction generates a CREATE OR REPLACE FUNCTION statement
// for the shared pgdesign_deny_mutation trigger function. This function raises
// an exception when UPDATE or DELETE is attempted on an append-only table.
func CreateDenyMutationFunction(schemaName string) string {
	qualified := QualifiedName(schemaName, "pgdesign_deny_mutation")
	return fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'table %% is append-only: UPDATE and DELETE are not allowed', TG_TABLE_NAME;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;`, qualified)
}

// CreateAppendOnlyTrigger generates a CREATE TRIGGER statement that fires
// BEFORE UPDATE OR DELETE to enforce append-only behavior on a table.
func CreateAppendOnlyTrigger(schemaName, tableName string) string {
	qualifiedTable := QualifiedName(schemaName, tableName)
	qualifiedFunc := QualifiedName(schemaName, "pgdesign_deny_mutation")
	return fmt.Sprintf("CREATE TRIGGER deny_mutation BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION %s();",
		qualifiedTable, qualifiedFunc)
}
