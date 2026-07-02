// Package sql provides shared SQL builder functions for PostgreSQL DDL generation, the single place where SQL text is constructed across all packages.
package sql

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/pgcap"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
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
// Kind must be one of: "pk", "fk", "idx", "uq", "ck", "excl".
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
// PostgreSQL does not support CREATE TYPE IF NOT EXISTS, so when idempotent
// is true, the statement is wrapped in a DO $$ block that checks pg_type
// before creating (same approach as CreateDomain and CreateCompositeType).
func CreateEnum(schema, name string, values []string, idempotent bool) string {
	qualified := QualifiedName(schema, name)

	quotedValues := make([]string, len(values))
	for i, v := range values {
		escaped := strings.ReplaceAll(v, "'", "''")
		quotedValues[i] = "'" + escaped + "'"
	}

	stmt := fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);",
		qualified, strings.Join(quotedValues, ", "))

	if idempotent {
		escapedType := strings.ReplaceAll(name, "'", "''")
		escapedSchema := strings.ReplaceAll(schema, "'", "''")
		catalogCheck := fmt.Sprintf(
			"SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = '%s' AND n.nspname = '%s' AND t.typtype = 'e'",
			escapedType, escapedSchema)
		return wrapIdempotentCatalogCheck(catalogCheck, stmt)
	}
	return stmt
}

// CreateDomain generates a CREATE DOMAIN statement.
// Emits: CREATE DOMAIN [schema.]name AS basetype [NOT NULL] [DEFAULT ...] [CHECK (...)].
// When idempotent is true, wraps in a DO $$ block that checks pg_type before creating.
func CreateDomain(schemaName string, d model.Domain, idempotent bool) string {
	qualified := QualifiedName(schemaName, d.Name)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE DOMAIN %s AS %s", qualified, typeinfo.Reconstruct(d.BaseType)))

	if d.NotNull {
		sb.WriteString(" NOT NULL")
	}

	if d.DefaultExpr != "" {
		sb.WriteString(" DEFAULT " + ExprValue(d.DefaultExpr))
	} else if d.Default != "" {
		sb.WriteString(" DEFAULT " + LiteralValue(d.Default, d.BaseType.Base))
	}

	if d.Check != "" {
		sb.WriteString(fmt.Sprintf(" CHECK (%s)", d.Check))
	}

	sb.WriteString(";")
	stmt := sb.String()

	if idempotent {
		escapedType := strings.ReplaceAll(d.Name, "'", "''")
		escapedSchema := strings.ReplaceAll(schemaName, "'", "''")
		catalogCheck := fmt.Sprintf(
			"SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = '%s' AND n.nspname = '%s' AND t.typtype = 'd'",
			escapedType, escapedSchema)
		return wrapIdempotentCatalogCheck(catalogCheck, stmt)
	}
	return stmt
}

// CreateCompositeType generates a CREATE TYPE ... AS statement for a composite type.
// Emits: CREATE TYPE [schema.]name AS (field1 type1, field2 type2, ...)
// When idempotent is true, wraps in a DO $$ block that checks pg_type before creating.
func CreateCompositeType(schemaName string, ct model.CompositeType, idempotent bool) string {
	qualified := QualifiedName(schemaName, ct.Name)

	fieldDefs := make([]string, len(ct.Fields))
	for i, f := range ct.Fields {
		fieldDefs[i] = fmt.Sprintf("%s %s", QuoteIdent(f.Name), typeinfo.Reconstruct(f.PGType))
	}

	stmt := fmt.Sprintf("CREATE TYPE %s AS (\n    %s\n);",
		qualified, strings.Join(fieldDefs, ",\n    "))

	if idempotent {
		escapedType := strings.ReplaceAll(ct.Name, "'", "''")
		escapedSchema := strings.ReplaceAll(schemaName, "'", "''")
		catalogCheck := fmt.Sprintf(
			"SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = '%s' AND n.nspname = '%s' AND t.typtype = 'c'",
			escapedType, escapedSchema)
		return wrapIdempotentCatalogCheck(catalogCheck, stmt)
	}
	return stmt
}

// DropCompositeType generates a DROP TYPE statement for a composite type.
func DropCompositeType(schemaName, name string, cascade bool) string {
	qualified := QualifiedName(schemaName, name)
	cascadeStr := ""
	if cascade {
		cascadeStr = " CASCADE"
	}
	return fmt.Sprintf("DROP TYPE %s%s;", qualified, cascadeStr)
}

// DropDomain generates a DROP DOMAIN statement.
func DropDomain(schemaName, name string, cascade bool) string {
	qualified := QualifiedName(schemaName, name)
	cascadeStr := ""
	if cascade {
		cascadeStr = " CASCADE"
	}
	return fmt.Sprintf("DROP DOMAIN %s%s;", qualified, cascadeStr)
}

// CreateTable generates a CREATE TABLE statement with columns, inline PK, and
// PARTITION BY. Foreign keys are NOT included (they use ALTER TABLE for cycle safety).
// pgVersion controls version-specific DDL: when the target version lacks identity
// column support (pre-PG10), identity columns fall back to bigserial.
// enums and domains are the enum and domain types defined in the schema; when a
// column's PG type matches an enum or domain name, the type is emitted with its
// schema prefix so the DDL works without relying on search_path.
func CreateTable(table *model.Table, schemaName string, idempotent bool, pgVersion int, enums []model.Enum, domains []model.Domain) string {
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}

	qualified := QualifiedName(schemaName, table.Name)

	var lines []string

	// Column definitions.
	for _, col := range table.Columns {
		lines = append(lines, "    "+columnDef(col, pgVersion, enums, domains))
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
		quotedPartCols := make([]string, len(table.Partitioning.Columns))
		for i, col := range table.Partitioning.Columns {
			quotedPartCols[i] = QuoteIdent(col)
		}
		sb.WriteString(fmt.Sprintf(" PARTITION BY %s (%s)",
			strings.ToUpper(table.Partitioning.Strategy),
			strings.Join(quotedPartCols, ", ")))
	}

	sb.WriteString(";")
	return sb.String()
}

// columnDef builds a single column definition line.
// pgVersion controls version-specific DDL via pgcap capability checks.
// enums is used to schema-qualify enum type names in column definitions.
//
// Generated column DDL varies by PostgreSQL version:
//   - PG 12-17: only STORED generated columns are supported.
//   - PG 18+: both STORED and VIRTUAL are supported; when stored is omitted
//     from the TOML definition, the model layer defaults stored to true, so
//     STORED is emitted unless the user explicitly sets stored = false.
//
// When the target version does not support VIRTUAL, this function defensively
// emits STORED rather than VIRTUAL, since validate should have already flagged
// this as E218.
//
// Note: transitioning a generated column from STORED to VIRTUAL (or vice
// versa) is destructive -- PostgreSQL does not support ALTER COLUMN to change
// the storage mode. The column must be DROPped and recreated.

// GeneratedStorageKeyword returns "STORED" or "VIRTUAL" for a generated column
// based on the stored flag and target PostgreSQL version.
func GeneratedStorageKeyword(stored bool, pgVersion int) string {
	if stored {
		return "STORED"
	}
	if pgcap.Has(pgVersion, pgcap.VirtualGeneratedCols) {
		return "VIRTUAL"
	}
	// Pre-PG18: VIRTUAL not supported. Defensively emit STORED
	// (validate should have caught this via E218).
	return "STORED"
}

func columnDef(col model.Column, pgVersion int, enums []model.Enum, domains []model.Domain) string {
	// Pre-PG10 identity fallback: replace identity column with bigserial.
	if col.Identity != "" && !pgcap.Has(pgVersion, pgcap.IdentityColumns) {
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
	pgType := resolveColumnType(typeinfo.Reconstruct(col.PGType), enums, domains)
	if col.Array {
		pgType += "[]"
	}
	parts = append(parts, QuoteIdent(col.Name), pgType)

	if col.Collation != "" {
		parts = append(parts, fmt.Sprintf("COLLATE %s", QuoteIdent(col.Collation)))
	}

	if col.NotNull {
		parts = append(parts, "NOT NULL")
	}

	if col.Identity != "" {
		parts = append(parts, fmt.Sprintf("GENERATED %s AS IDENTITY", col.Identity))
	} else if col.Generated != "" {
		parts = append(parts, fmt.Sprintf("GENERATED ALWAYS AS (%s)", col.Generated))
		parts = append(parts, GeneratedStorageKeyword(col.Stored, pgVersion))
	} else if col.DefaultExpr != "" {
		parts = append(parts, "DEFAULT "+ExprValue(col.DefaultExpr))
	} else if col.Default != nil {
		parts = append(parts, "DEFAULT "+LiteralValue(*col.Default, pgType))
	}

	return strings.Join(parts, " ")
}

// resolveColumnType returns the SQL type string for a column. If the type
// matches a known enum or domain, its schema-qualified name is returned so
// that the DDL works without relying on search_path.
func resolveColumnType(pgType string, enums []model.Enum, domains []model.Domain) string {
	for _, e := range enums {
		if e.Name == pgType {
			return QualifiedName(e.Schema, e.Name)
		}
	}
	for _, d := range domains {
		if d.Name == pgType {
			return QualifiedName(d.Schema, d.Name)
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

	var buf strings.Builder
	fmt.Fprintf(&buf, "ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s)",
		qualified, QuoteIdent(constraintName), strings.Join(quotedCols, ", "))
	if uq.Deferrable {
		buf.WriteString(" DEFERRABLE")
		if uq.InitiallyDeferred {
			buf.WriteString(" INITIALLY DEFERRED")
		}
	}
	buf.WriteString(";")

	stmt := buf.String()
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

// AlterTableAddExclusion generates an ALTER TABLE ... ADD CONSTRAINT ... EXCLUDE statement.
func AlterTableAddExclusion(schemaName, tableName string, exc *model.ExclusionConstraint, idempotent bool) string {
	qualified := QualifiedName(schemaName, tableName)
	constraintName := exc.Name
	if constraintName == "" {
		constraintName = ConstraintName(tableName, "excl", exc.Elements[0].Column)
	}

	// Build element list: col1 WITH op1, col2 WITH op2
	elems := make([]string, len(exc.Elements))
	for i, e := range exc.Elements {
		elems[i] = fmt.Sprintf("%s WITH %s", QuoteIdent(e.Column), e.Operator)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "ALTER TABLE %s ADD CONSTRAINT %s EXCLUDE USING %s (%s)",
		qualified, QuoteIdent(constraintName), exc.Method, strings.Join(elems, ", "))

	if exc.Where != "" {
		fmt.Fprintf(&buf, " WHERE (%s)", exc.Where)
	}
	if exc.Deferrable {
		buf.WriteString(" DEFERRABLE")
		if exc.InitiallyDeferred {
			buf.WriteString(" INITIALLY DEFERRED")
		}
	}
	buf.WriteString(";")

	stmt := buf.String()
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

// wrapIdempotentCatalogCheck wraps a DDL statement in a DO $$ block that checks
// a catalog condition before executing, making it idempotent. catalogCheckSQL is
// a SELECT query that returns rows when the object already exists; createStmt is
// the DDL to execute when it does not.
func wrapIdempotentCatalogCheck(catalogCheckSQL, createStmt string) string {
	escapedStmt := strings.ReplaceAll(createStmt, "'", "''")
	return fmt.Sprintf(`DO $$
BEGIN
  IF NOT EXISTS (%s) THEN
    EXECUTE '%s';
  END IF;
END $$;`, catalogCheckSQL, escapedStmt)
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
		if coll, ok := index.Collations[c]; ok && coll != "" {
			expr += " COLLATE " + QuoteIdent(coll)
		}
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

	// WITH clause (storage parameters).
	if len(index.With) > 0 {
		keys := make([]string, 0, len(index.With))
		for k := range index.With {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		params := make([]string, len(keys))
		for i, k := range keys {
			params[i] = fmt.Sprintf("%s = %s", k, index.With[k])
		}
		sb.WriteString(fmt.Sprintf(" WITH (%s)", strings.Join(params, ", ")))
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

// AlterTableForceRLS generates an ALTER TABLE ... FORCE ROW LEVEL SECURITY statement.
// This causes RLS policies to apply even to table owners.
func AlterTableForceRLS(schemaName, tableName string) string {
	return fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY;", QualifiedName(schemaName, tableName))
}

// CreatePolicy generates a CREATE POLICY statement for row-level security.
// The FOR clause is omitted when operation is "ALL" (the PostgreSQL default).
// The TO clause is omitted when role is empty (defaults to PUBLIC).
// USING and WITH CHECK are wrapped in parentheses when present.
//
// When idempotent is true and pgVersion >= 15, uses CREATE OR REPLACE POLICY.
// When idempotent is true and pgVersion < 15, wraps in a DO $$ block that
// checks pg_policy before executing.
func CreatePolicy(schemaName, tableName string, p model.Policy, idempotent bool, pgVersion int) string {
	qualified := QualifiedName(schemaName, tableName)

	createVerb := "CREATE POLICY"
	if idempotent && pgcap.Has(pgVersion, pgcap.CreateOrReplacePolicy) {
		createVerb = "CREATE OR REPLACE POLICY"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s %s ON %s", createVerb, QuoteIdent(p.Name), qualified))

	// AS RESTRICTIVE is only emitted when explicitly set. PERMISSIVE is the
	// PostgreSQL default and is omitted for brevity (same as FOR ALL).
	if p.Type == "RESTRICTIVE" {
		sb.WriteString(" AS RESTRICTIVE")
	}

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
	stmt := sb.String()

	// For pre-PG15 idempotent mode, wrap in a DO $$ block with a pg_policy check.
	if idempotent && !pgcap.Has(pgVersion, pgcap.CreateOrReplacePolicy) {
		escapedName := strings.ReplaceAll(p.Name, "'", "''")
		escapedQualified := strings.ReplaceAll(qualified, "'", "''")
		catalogCheck := fmt.Sprintf(
			"SELECT 1 FROM pg_policy WHERE polname = '%s' AND polrelid = '%s'::regclass",
			escapedName, escapedQualified,
		)
		return wrapIdempotentCatalogCheck(catalogCheck, stmt)
	}

	return stmt
}

// DropPolicy generates a DROP POLICY statement.
func DropPolicy(schemaName, tableName, policyName string) string {
	return fmt.Sprintf("DROP POLICY %s ON %s;", QuoteIdent(policyName), QualifiedName(schemaName, tableName))
}

// AlterTableDisableRLS generates an ALTER TABLE ... DISABLE ROW LEVEL SECURITY statement.
func AlterTableDisableRLS(schemaName, tableName string) string {
	return fmt.Sprintf("ALTER TABLE %s DISABLE ROW LEVEL SECURITY;", QualifiedName(schemaName, tableName))
}

// AlterTableNoForceRLS generates an ALTER TABLE ... NO FORCE ROW LEVEL SECURITY statement.
func AlterTableNoForceRLS(schemaName, tableName string) string {
	return fmt.Sprintf("ALTER TABLE %s NO FORCE ROW LEVEL SECURITY;", QualifiedName(schemaName, tableName))
}

// CreateView generates a CREATE VIEW statement.
// When idempotent is true, uses CREATE OR REPLACE VIEW instead of CREATE VIEW.
func CreateView(schemaName string, view *model.View, idempotent bool) string {
	qualified := QualifiedName(schemaName, view.Name)
	var sb strings.Builder
	if idempotent {
		sb.WriteString(fmt.Sprintf("CREATE OR REPLACE VIEW %s AS\n", qualified))
	} else {
		sb.WriteString(fmt.Sprintf("CREATE VIEW %s AS\n", qualified))
	}
	sb.WriteString(view.Query)
	sb.WriteString(";\n")
	return sb.String()
}

// DropView generates a DROP VIEW statement.
// When idempotent is true, includes IF EXISTS.
func DropView(schemaName, viewName string, idempotent bool) string {
	qualified := QualifiedName(schemaName, viewName)
	ifExists := ""
	if idempotent {
		ifExists = " IF EXISTS"
	}
	return fmt.Sprintf("DROP VIEW%s %s;\n", ifExists, qualified)
}

// CreateMaterializedView generates a CREATE MATERIALIZED VIEW statement.
// PostgreSQL does not support CREATE OR REPLACE or IF NOT EXISTS for materialized views.
// When idempotent is true, wraps in a DO $$ block that checks pg_matviews before creating.
func CreateMaterializedView(schemaName string, mv *model.MaterializedView, idempotent bool) string {
	qualified := QualifiedName(schemaName, mv.Name)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE MATERIALIZED VIEW %s AS\n", qualified))
	sb.WriteString(mv.Query)
	if mv.WithData {
		sb.WriteString("\nWITH DATA;\n")
	} else {
		sb.WriteString("\nWITH NO DATA;\n")
	}
	stmt := sb.String()

	if idempotent {
		escapedName := strings.ReplaceAll(mv.Name, "'", "''")
		escapedSchema := strings.ReplaceAll(schemaName, "'", "''")
		catalogCheck := fmt.Sprintf(
			"SELECT 1 FROM pg_matviews WHERE matviewname = '%s' AND schemaname = '%s'",
			escapedName, escapedSchema,
		)
		return wrapIdempotentCatalogCheck(catalogCheck, stmt)
	}

	return stmt
}

// DropMaterializedView generates a DROP MATERIALIZED VIEW statement.
// When idempotent is true, includes IF EXISTS.
func DropMaterializedView(schemaName, viewName string, idempotent bool) string {
	qualified := QualifiedName(schemaName, viewName)
	ifExists := ""
	if idempotent {
		ifExists = " IF EXISTS"
	}
	return fmt.Sprintf("DROP MATERIALIZED VIEW%s %s;\n", ifExists, qualified)
}

// RefreshMaterializedView generates a REFRESH MATERIALIZED VIEW statement.
// When concurrently is true, adds CONCURRENTLY (requires a unique index to exist).
func RefreshMaterializedView(schemaName, name string, concurrently bool) string {
	qualified := QualifiedName(schemaName, name)
	conc := ""
	if concurrently {
		conc = " CONCURRENTLY"
	}
	return fmt.Sprintf("REFRESH MATERIALIZED VIEW%s %s;\n", conc, qualified)
}

// CreateSequence generates a CREATE SEQUENCE statement.
// When idempotent is true, IF NOT EXISTS is included.
func CreateSequence(schemaName string, seq *model.Sequence, idempotent bool) string {
	qualified := QualifiedName(schemaName, seq.Name)
	var sb strings.Builder
	ifne := ""
	if idempotent {
		ifne = " IF NOT EXISTS"
	}
	sb.WriteString(fmt.Sprintf("CREATE SEQUENCE%s %s", ifne, qualified))

	if seq.Start != nil {
		sb.WriteString(fmt.Sprintf(" START WITH %d", *seq.Start))
	}
	if seq.Increment != nil {
		sb.WriteString(fmt.Sprintf(" INCREMENT BY %d", *seq.Increment))
	}
	if seq.MinValue != nil {
		sb.WriteString(fmt.Sprintf(" MINVALUE %d", *seq.MinValue))
	} else {
		sb.WriteString(" NO MINVALUE")
	}
	if seq.MaxValue != nil {
		sb.WriteString(fmt.Sprintf(" MAXVALUE %d", *seq.MaxValue))
	} else {
		sb.WriteString(" NO MAXVALUE")
	}
	if seq.Cache != nil {
		sb.WriteString(fmt.Sprintf(" CACHE %d", *seq.Cache))
	}
	if seq.Cycle {
		sb.WriteString(" CYCLE")
	} else {
		sb.WriteString(" NO CYCLE")
	}
	if seq.OwnedBy != "" {
		// OwnedBy is in "table.column" format; schema-qualify the table part.
		parts := strings.SplitN(seq.OwnedBy, ".", 2)
		if len(parts) == 2 {
			sb.WriteString(fmt.Sprintf(" OWNED BY %s.%s", QualifiedName(schemaName, parts[0]), QuoteIdent(parts[1])))
		}
	}

	sb.WriteString(";")
	return sb.String()
}

// DropSequence generates a DROP SEQUENCE statement.
func DropSequence(schemaName, name string, cascade bool) string {
	qualified := QualifiedName(schemaName, name)
	cascadeStr := ""
	if cascade {
		cascadeStr = " CASCADE"
	}
	return fmt.Sprintf("DROP SEQUENCE %s%s;", qualified, cascadeStr)
}

// AlterSequence generates an ALTER SEQUENCE statement for changing parameters.
func AlterSequence(schemaName string, seq *model.Sequence) string {
	qualified := QualifiedName(schemaName, seq.Name)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ALTER SEQUENCE %s", qualified))

	if seq.Start != nil {
		sb.WriteString(fmt.Sprintf(" START WITH %d", *seq.Start))
	}
	if seq.Increment != nil {
		sb.WriteString(fmt.Sprintf(" INCREMENT BY %d", *seq.Increment))
	}
	if seq.MinValue != nil {
		sb.WriteString(fmt.Sprintf(" MINVALUE %d", *seq.MinValue))
	} else {
		sb.WriteString(" NO MINVALUE")
	}
	if seq.MaxValue != nil {
		sb.WriteString(fmt.Sprintf(" MAXVALUE %d", *seq.MaxValue))
	} else {
		sb.WriteString(" NO MAXVALUE")
	}
	if seq.Cache != nil {
		sb.WriteString(fmt.Sprintf(" CACHE %d", *seq.Cache))
	}
	if seq.Cycle {
		sb.WriteString(" CYCLE")
	} else {
		sb.WriteString(" NO CYCLE")
	}
	if seq.OwnedBy != "" {
		parts := strings.SplitN(seq.OwnedBy, ".", 2)
		if len(parts) == 2 {
			sb.WriteString(fmt.Sprintf(" OWNED BY %s.%s", QualifiedName(schemaName, parts[0]), QuoteIdent(parts[1])))
		}
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
// When idempotent is true and pgVersion supports CREATE OR REPLACE TRIGGER (PG 14+),
// it emits CREATE OR REPLACE TRIGGER; otherwise it emits DROP TRIGGER IF EXISTS
// followed by CREATE TRIGGER.
func CreateAppendOnlyTrigger(schemaName, tableName string, idempotent bool, pgVersion int) string {
	qualifiedTable := QualifiedName(schemaName, tableName)
	qualifiedFunc := QualifiedName(schemaName, "pgdesign_deny_mutation")
	trigName := "deny_mutation"
	if idempotent && pgcap.Has(pgVersion, pgcap.CreateOrReplaceTrigger) {
		return fmt.Sprintf("CREATE OR REPLACE TRIGGER %s BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION %s();",
			trigName, qualifiedTable, qualifiedFunc)
	}
	stmt := fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION %s();",
		trigName, qualifiedTable, qualifiedFunc)
	if idempotent {
		return fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\n%s",
			QuoteIdent(trigName), qualifiedTable, stmt)
	}
	return stmt
}

// StateMachineTriggerFuncName returns the reserved function name for a state machine
// enforcement trigger: _pgdesign_sm_<table>_<col>.
func StateMachineTriggerFuncName(tableName, colName string) string {
	return "_pgdesign_sm_" + tableName + "_" + colName
}

// CreateStateMachineTriggerFunction generates a PL/pgSQL function that enforces
// state machine transitions on a column. It checks that each UPDATE of the column
// follows a valid transition and that any required columns are non-null.
func CreateStateMachineTriggerFunction(schemaName, tableName, colName string, transitions []semtype.SMTransitionDef) string {
	funcName := StateMachineTriggerFuncName(tableName, colName)
	qualifiedFunc := QualifiedName(schemaName, funcName)
	quotedCol := QuoteIdent(colName)

	// Group transitions by from-state: from -> set of to-states.
	type fromGroup struct {
		toStates []string
	}
	fromMap := make(map[string]*fromGroup)
	var fromOrder []string
	for _, tr := range transitions {
		for _, from := range tr.From {
			g, ok := fromMap[from]
			if !ok {
				g = &fromGroup{}
				fromMap[from] = g
				fromOrder = append(fromOrder, from)
			}
			// Add to-state if not already present.
			found := false
			for _, ts := range g.toStates {
				if ts == tr.To {
					found = true
					break
				}
			}
			if !found {
				g.toStates = append(g.toStates, tr.To)
			}
		}
	}
	sort.Strings(fromOrder)

	// Build the valid-transition check lines.
	var transitionLines []string
	for _, from := range fromOrder {
		g := fromMap[from]
		sort.Strings(g.toStates)
		quotedTo := make([]string, len(g.toStates))
		for i, ts := range g.toStates {
			quotedTo[i] = "'" + strings.ReplaceAll(ts, "'", "''") + "'"
		}
		escapedFrom := strings.ReplaceAll(from, "'", "''")
		transitionLines = append(transitionLines,
			fmt.Sprintf("      (OLD.%s = '%s' AND NEW.%s IN (%s))",
				quotedCol, escapedFrom, quotedCol, strings.Join(quotedTo, ", ")))
	}

	// Build the requires-column checks.
	var requiresChecks []string
	for _, tr := range transitions {
		if len(tr.Requires) == 0 {
			continue
		}
		// Sort required columns for deterministic output.
		reqCols := make([]string, 0, len(tr.Requires))
		for col := range tr.Requires {
			reqCols = append(reqCols, col)
		}
		sort.Strings(reqCols)
		for _, reqCol := range reqCols {
			for _, from := range tr.From {
				escapedFrom := strings.ReplaceAll(from, "'", "''")
				escapedTo := strings.ReplaceAll(tr.To, "'", "''")
				escapedName := strings.ReplaceAll(tr.Name, "'", "''")
				requiresChecks = append(requiresChecks,
					fmt.Sprintf("    IF OLD.%s = '%s' AND NEW.%s = '%s' AND NEW.%s IS NULL THEN\n"+
						"      RAISE EXCEPTION 'transition %s requires non-null %s'\n"+
						"        USING ERRCODE = 'P0001';\n"+
						"    END IF;",
						quotedCol, escapedFrom, quotedCol, escapedTo,
						QuoteIdent(reqCol), escapedName, reqCol))
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE OR REPLACE FUNCTION %s() RETURNS trigger AS $pgdesign$\n", qualifiedFunc))
	sb.WriteString("BEGIN\n")
	sb.WriteString(fmt.Sprintf("  IF OLD.%s IS DISTINCT FROM NEW.%s THEN\n", quotedCol, quotedCol))
	sb.WriteString("    IF NOT (\n")
	sb.WriteString(strings.Join(transitionLines, " OR\n"))
	sb.WriteString("\n    ) THEN\n")
	sb.WriteString(fmt.Sprintf("      RAISE EXCEPTION 'invalid state transition: %%s -> %%s', OLD.%s, NEW.%s\n", quotedCol, quotedCol))
	sb.WriteString("        USING ERRCODE = 'P0001';\n")
	sb.WriteString("    END IF;\n")
	for _, check := range requiresChecks {
		sb.WriteString(check)
		sb.WriteString("\n")
	}
	sb.WriteString("  END IF;\n")
	sb.WriteString("  RETURN NEW;\n")
	sb.WriteString("END;\n")
	sb.WriteString("$pgdesign$ LANGUAGE plpgsql;")
	return sb.String()
}

// CreateStateMachineTrigger generates a CREATE TRIGGER statement that fires BEFORE
// UPDATE OF <col> to enforce state machine transitions.
// When idempotent is true and pgVersion supports CREATE OR REPLACE TRIGGER (PG 14+),
// it emits CREATE OR REPLACE TRIGGER; otherwise it emits DROP TRIGGER IF EXISTS
// followed by CREATE TRIGGER.
func CreateStateMachineTrigger(schemaName, tableName, colName string, idempotent bool, pgVersion int) string {
	trigName := StateMachineTriggerFuncName(tableName, colName)
	qualifiedTable := QualifiedName(schemaName, tableName)
	qualifiedFunc := QualifiedName(schemaName, trigName)
	quotedTrigName := QuoteIdent(trigName)
	if idempotent && pgcap.Has(pgVersion, pgcap.CreateOrReplaceTrigger) {
		return fmt.Sprintf("CREATE OR REPLACE TRIGGER %s BEFORE UPDATE OF %s ON %s FOR EACH ROW EXECUTE FUNCTION %s();",
			quotedTrigName, QuoteIdent(colName), qualifiedTable, qualifiedFunc)
	}
	stmt := fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE OF %s ON %s FOR EACH ROW EXECUTE FUNCTION %s();",
		quotedTrigName, QuoteIdent(colName), qualifiedTable, qualifiedFunc)
	if idempotent {
		return fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\n%s",
			quotedTrigName, qualifiedTable, stmt)
	}
	return stmt
}

// CreateTrigger generates a CREATE [CONSTRAINT] TRIGGER statement for a user-defined trigger.
// Emits: CREATE [OR REPLACE] [CONSTRAINT] TRIGGER name timing events ON [schema.]table
//
//	[REFERENCING OLD TABLE AS x NEW TABLE AS y]
//	FOR EACH ROW|STATEMENT [WHEN (condition)]
//	EXECUTE FUNCTION [schema.]func_name()
//
// When idempotent is true and pgVersion supports CREATE OR REPLACE TRIGGER (PG 14+),
// it emits CREATE OR REPLACE [CONSTRAINT] TRIGGER; otherwise it emits DROP TRIGGER IF EXISTS
// followed by CREATE [CONSTRAINT] TRIGGER.
func CreateTrigger(schemaName, tableName string, t model.Trigger, idempotent bool, pgVersion int) string {
	qualifiedTable := QualifiedName(schemaName, tableName)
	useOrReplace := idempotent && pgcap.Has(pgVersion, pgcap.CreateOrReplaceTrigger)

	var sb strings.Builder
	if useOrReplace {
		sb.WriteString("CREATE OR REPLACE ")
	} else {
		sb.WriteString("CREATE ")
	}
	if t.Constraint {
		sb.WriteString("CONSTRAINT ")
	}
	sb.WriteString(fmt.Sprintf("TRIGGER %s %s %s ON %s",
		QuoteIdent(t.Name),
		t.Timing,
		strings.Join(t.Events, " OR "),
		qualifiedTable))

	// Deferrable (only meaningful for constraint triggers).
	if t.Deferrable {
		sb.WriteString(" DEFERRABLE")
		if t.InitiallyDeferred {
			sb.WriteString(" INITIALLY DEFERRED")
		}
	}

	// REFERENCING clause for transition tables.
	if t.ReferencingOld != "" || t.ReferencingNew != "" {
		sb.WriteString(" REFERENCING")
		if t.ReferencingOld != "" {
			sb.WriteString(fmt.Sprintf(" OLD TABLE AS %s", QuoteIdent(t.ReferencingOld)))
		}
		if t.ReferencingNew != "" {
			sb.WriteString(fmt.Sprintf(" NEW TABLE AS %s", QuoteIdent(t.ReferencingNew)))
		}
	}

	sb.WriteString(fmt.Sprintf(" FOR EACH %s", t.ForEach))

	if t.When != "" {
		sb.WriteString(fmt.Sprintf(" WHEN (%s)", t.When))
	}

	// Function name: schema-qualify it.
	qualifiedFunc := QualifiedName(schemaName, t.Function)
	sb.WriteString(fmt.Sprintf(" EXECUTE FUNCTION %s();", qualifiedFunc))

	createStmt := sb.String()

	// For pre-PG14 idempotent mode, prepend DROP TRIGGER IF EXISTS.
	if idempotent && !useOrReplace {
		return fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\n%s",
			QuoteIdent(t.Name), qualifiedTable, createStmt)
	}

	return createStmt
}

// DropTrigger generates a DROP TRIGGER statement.
func DropTrigger(schemaName, tableName, triggerName string) string {
	qualifiedTable := QualifiedName(schemaName, tableName)
	return fmt.Sprintf("DROP TRIGGER %s ON %s;", QuoteIdent(triggerName), qualifiedTable)
}

// CreateFunction generates a CREATE OR REPLACE FUNCTION/PROCEDURE statement.
// For procedures (f.IsProc), RETURNS and volatility/parallel/cost/rows are omitted.
func CreateFunction(schemaName string, f model.Function) string {
	qualified := QualifiedName(schemaName, f.Name)

	// Build argument list
	argParts := make([]string, len(f.Args))
	for i, arg := range f.Args {
		argDef := QuoteIdent(arg.Name) + " " + typeinfo.Reconstruct(arg.Type)
		if arg.Default != "" {
			argDef += " DEFAULT " + arg.Default
		}
		argParts[i] = argDef
	}
	argList := strings.Join(argParts, ", ")

	var sb strings.Builder
	if f.IsProc {
		sb.WriteString(fmt.Sprintf("CREATE OR REPLACE PROCEDURE %s(%s)", qualified, argList))
	} else {
		sb.WriteString(fmt.Sprintf("CREATE OR REPLACE FUNCTION %s(%s)", qualified, argList))
		sb.WriteString(fmt.Sprintf("\nRETURNS %s", f.ReturnType))
	}

	sb.WriteString(fmt.Sprintf("\nAS $pgdesign$\n%s\n$pgdesign$", f.Body))
	sb.WriteString(fmt.Sprintf("\nLANGUAGE %s", f.Language))

	if !f.IsProc {
		if f.Volatility != "" {
			sb.WriteString("\n" + f.Volatility)
		}
		if f.Parallel != "" {
			sb.WriteString("\nPARALLEL " + f.Parallel)
		}
	}

	if f.SecurityDefiner {
		sb.WriteString("\nSECURITY DEFINER")
	}

	if !f.IsProc {
		if f.Cost != nil {
			sb.WriteString(fmt.Sprintf("\nCOST %g", *f.Cost))
		}
		if f.Rows != nil {
			sb.WriteString(fmt.Sprintf("\nROWS %g", *f.Rows))
		}
	}

	sb.WriteString(";")
	return sb.String()
}

// DropFunction generates a DROP FUNCTION/PROCEDURE statement.
// Includes argument types for overload resolution.
func DropFunction(schemaName string, f model.Function, cascade bool) string {
	qualified := QualifiedName(schemaName, f.Name)

	// Build argument type list for overload resolution
	argTypes := make([]string, len(f.Args))
	for i, arg := range f.Args {
		argTypes[i] = typeinfo.Reconstruct(arg.Type)
	}

	kind := "FUNCTION"
	if f.IsProc {
		kind = "PROCEDURE"
	}

	cascadeStr := ""
	if cascade {
		cascadeStr = " CASCADE"
	}

	return fmt.Sprintf("DROP %s %s(%s)%s;", kind, qualified, strings.Join(argTypes, ", "), cascadeStr)
}
