package migrate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sql"
)

// OpToSQL converts a DDLOp to a SQL statement.
func OpToSQL(op DDLOp) string {
	switch op.Op {
	case "create_table":
		return opCreateTable(op)
	case "create_partition":
		return opCreatePartition(op)
	case "drop_table":
		return opDropTable(op)
	case "add_column":
		return opAddColumn(op)
	case "drop_column":
		return opDropColumn(op)
	case "alter_column_type":
		return opAlterColumnType(op)
	case "set_not_null":
		return opSetNotNull(op)
	case "drop_not_null":
		return opDropNotNull(op)
	case "alter_column_default":
		return opAlterColumnDefault(op)
	case "drop_column_default":
		return opDropColumnDefault(op)
	case "rename_column":
		return opRenameColumn(op)
	case "rename_table":
		return opRenameTable(op)
	case "add_fk":
		return opAddFK(op)
	case "drop_fk":
		return opDropFK(op)
	case "create_index", "add_index":
		return opCreateIndex(op)
	case "drop_index":
		return opDropIndex(op)
	case "create_index_concurrently":
		return opCreateIndexConcurrently(op)
	case "drop_index_concurrently":
		return opDropIndexConcurrently(op)
	case "alter_index_set":
		return opAlterIndexSet(op)
	case "add_unique":
		return opAddUnique(op)
	case "drop_unique":
		return opDropUnique(op)
	case "add_check":
		return opAddCheck(op)
	case "drop_check":
		return opDropCheck(op)
	case "create_enum":
		return opCreateEnum(op)
	case "alter_enum_add_value":
		return opAlterEnumAddValue(op)
	case "drop_enum":
		return opDropEnum(op)
	case "set_owner":
		return opSetOwner(op)
	case "create_function":
		return opCreateFunction(op)
	case "drop_function":
		return opDropFunction(op)
	case "create_trigger":
		return opCreateTrigger(op)
	case "drop_trigger":
		return opDropTrigger(op)
	case "create_view":
		return opCreateView(op)
	case "drop_view":
		return opDropView(op)
	case "create_or_replace_view":
		return opCreateOrReplaceView(op)
	default:
		return fmt.Sprintf("-- unknown op: %s", op.Op)
	}
}

// IsNonTransactional returns true if the op must run outside a transaction.
func IsNonTransactional(op DDLOp) bool {
	switch op.Op {
	case "create_index_concurrently", "drop_index_concurrently", "alter_enum_add_value":
		return true
	default:
		return false
	}
}

func opCreateTable(op DDLOp) string {
	if op.TableDef != nil {
		schema, _ := splitQualifiedName(op.Table)
		return sql.CreateTable(op.TableDef, schema, false, 0, nil)
	}

	// Fallback: generate from op fields (no full table def available).
	return fmt.Sprintf("CREATE TABLE %s ();", quoteQualified(op.Table))
}

func opCreatePartition(op DDLOp) string {
	if op.PartitionChildSpec != nil && op.ParentTable != "" {
		schema, parentName := splitQualifiedName(op.ParentTable)
		return sql.CreatePartitionOf(schema, op.PartitionChildSpec, parentName, false)
	}
	// Fallback: cannot generate without child spec.
	return fmt.Sprintf("-- create_partition: missing child spec for %s", op.Table)
}

func opDropTable(op DDLOp) string {
	return fmt.Sprintf("DROP TABLE %s;", quoteQualified(op.Table))
}

func opAddColumn(op DDLOp) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column), op.Type))
	if op.NotNull {
		parts = append(parts, "NOT NULL")
	}
	if op.Default != nil {
		parts = append(parts, fmt.Sprintf("DEFAULT %s", formatDefault(op.Default, op.Type)))
	}
	return strings.Join(parts, " ") + ";"
}

func opDropColumn(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column))
}

func opAlterColumnType(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column), op.Type)
}

func opSetNotNull(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column))
}

func opDropNotNull(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column))
}

func opAlterColumnDefault(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column), formatDefault(op.Default, op.Type))
}

func opDropColumnDefault(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column))
}

func opRenameColumn(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Column), sql.QuoteIdent(op.Name))
}

func opRenameTable(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s RENAME TO %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name))
}

func opAddFK(op DDLOp) string {
	localCols := quoteIdentSlice(op.Columns)
	refCols := quoteIdentSlice(op.RefCols)

	stmt := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name),
		strings.Join(localCols, ", "),
		quoteQualified(op.RefTable), strings.Join(refCols, ", "))

	if op.OnDelete != "" {
		stmt += " ON DELETE " + strings.ToUpper(op.OnDelete)
	}
	return stmt + ";"
}

func opDropFK(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name))
}

func opCreateIndex(op DDLOp) string {
	idx := &model.Index{
		Name:      op.Name,
		Columns:   op.Columns,
		Desc:      op.Desc,
		Method:    op.Method,
		Opclasses: op.Opclasses,
		Where:     op.Where,
		Include:   op.Include,
		With:      op.With,
	}
	schema, tableName := splitQualifiedName(op.Table)
	return sql.CreateIndex(schema, idx, tableName, false, false)
}

func opDropIndex(op DDLOp) string {
	schema, _ := splitQualifiedName(op.Table)
	if schema != "" {
		return fmt.Sprintf("DROP INDEX %s.%s;", sql.QuoteIdent(schema), sql.QuoteIdent(op.Name))
	}
	return fmt.Sprintf("DROP INDEX %s;", sql.QuoteIdent(op.Name))
}

func opCreateIndexConcurrently(op DDLOp) string {
	idx := &model.Index{
		Name:      op.Name,
		Columns:   op.Columns,
		Desc:      op.Desc,
		Method:    op.Method,
		Opclasses: op.Opclasses,
		Where:     op.Where,
		Include:   op.Include,
		With:      op.With,
	}
	schema, tableName := splitQualifiedName(op.Table)
	return sql.CreateIndex(schema, idx, tableName, false, true)
}

func opDropIndexConcurrently(op DDLOp) string {
	schema, _ := splitQualifiedName(op.Table)
	if schema != "" {
		return fmt.Sprintf("DROP INDEX CONCURRENTLY %s.%s;", sql.QuoteIdent(schema), sql.QuoteIdent(op.Name))
	}
	return fmt.Sprintf("DROP INDEX CONCURRENTLY %s;", sql.QuoteIdent(op.Name))
}

func opAlterIndexSet(op DDLOp) string {
	schema, _ := splitQualifiedName(op.Table)
	var parts []string
	for k, v := range op.With {
		parts = append(parts, fmt.Sprintf("%s = %s", k, v))
	}
	sort.Strings(parts)
	idxName := op.Name
	if schema != "" {
		idxName = fmt.Sprintf("%s.%s", sql.QuoteIdent(schema), sql.QuoteIdent(op.Name))
	} else {
		idxName = sql.QuoteIdent(op.Name)
	}
	return fmt.Sprintf("ALTER INDEX %s SET (%s);", idxName, strings.Join(parts, ", "))
}

func opAddUnique(op DDLOp) string {
	cols := quoteIdentSlice(op.Columns)
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s);",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name), strings.Join(cols, ", "))
}

func opDropUnique(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name))
}

func opAddCheck(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name), op.Expr)
}

func opDropCheck(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name))
}

func opCreateEnum(op DDLOp) string {
	schema := op.Schema
	name := op.Name
	if schema == "" {
		// Try to parse from table field (schema.name).
		schema, name = splitQualifiedName(op.Table)
		if name == "" {
			name = op.Name
		}
	}
	return sql.CreateEnum(schema, name, op.Values, false)
}

func opAlterEnumAddValue(op DDLOp) string {
	qualified := quoteQualified(op.Name)
	if op.Schema != "" {
		qualified = sql.QualifiedName(op.Schema, op.Name)
	}
	var stmts []string
	for _, v := range op.Values {
		escaped := strings.ReplaceAll(v, "'", "''")
		stmts = append(stmts, fmt.Sprintf("ALTER TYPE %s ADD VALUE '%s';", qualified, escaped))
	}
	return strings.Join(stmts, "\n")
}

func opDropEnum(op DDLOp) string {
	if op.Schema != "" {
		return fmt.Sprintf("DROP TYPE %s;", sql.QualifiedName(op.Schema, op.Name))
	}
	return fmt.Sprintf("DROP TYPE %s;", quoteQualified(op.Name))
}

func opSetOwner(op DDLOp) string {
	return fmt.Sprintf("ALTER TABLE %s OWNER TO %s;",
		quoteQualified(op.Table), sql.QuoteIdent(op.Name))
}

func opCreateFunction(op DDLOp) string {
	schema, _ := splitQualifiedName(op.Table)
	return sql.CreateDenyMutationFunction(schema)
}

func opDropFunction(op DDLOp) string {
	schema, _ := splitQualifiedName(op.Table)
	qualified := sql.QualifiedName(schema, "pgdesign_deny_mutation")
	return fmt.Sprintf("DROP FUNCTION IF EXISTS %s();", qualified)
}

func opCreateTrigger(op DDLOp) string {
	schema, tableName := splitQualifiedName(op.Table)
	return sql.CreateAppendOnlyTrigger(schema, tableName)
}

func opDropTrigger(op DDLOp) string {
	schema, tableName := splitQualifiedName(op.Table)
	qualifiedTable := sql.QualifiedName(schema, tableName)
	return fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;", sql.QuoteIdent(op.Name), qualifiedTable)
}

func opCreateView(op DDLOp) string {
	if op.ViewDef != nil {
		schema, _ := splitQualifiedName(op.Name)
		return sql.CreateView(schema, op.ViewDef, false)
	}
	return fmt.Sprintf("-- create_view: missing view definition for %s", op.Name)
}

func opDropView(op DDLOp) string {
	schema, name := splitQualifiedName(op.Name)
	return sql.DropView(schema, name, false)
}

func opCreateOrReplaceView(op DDLOp) string {
	if op.ViewDef != nil {
		schema, _ := splitQualifiedName(op.Name)
		return sql.CreateView(schema, op.ViewDef, true)
	}
	return fmt.Sprintf("-- create_or_replace_view: missing view definition for %s", op.Name)
}

// splitQualifiedName splits "schema.table" into ("schema", "table").
// If there's no dot, returns ("public", name).
func splitQualifiedName(name string) (string, string) {
	if idx := strings.IndexByte(name, '.'); idx >= 0 {
		return name[:idx], name[idx+1:]
	}
	return "public", name
}

// quoteQualified quotes a potentially schema-qualified name.
func quoteQualified(name string) string {
	schema, table := splitQualifiedName(name)
	return sql.QualifiedName(schema, table)
}

// quoteIdentSlice quotes each element as an identifier.
func quoteIdentSlice(names []string) []string {
	result := make([]string, len(names))
	for i, n := range names {
		result[i] = sql.QuoteIdent(n)
	}
	return result
}

// formatDefault formats a default value for use in DDL.
func formatDefault(val interface{}, pgType string) string {
	if val == nil {
		return "NULL"
	}
	switch v := val.(type) {
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return sql.LiteralValue(v, pgType)
	default:
		return fmt.Sprintf("'%v'", v)
	}
}
