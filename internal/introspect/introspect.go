// Package introspect provides live PostgreSQL database introspection.
// It extracts schema information from pg_catalog into the resolved IR.
package introspect

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// Introspect connects to a PostgreSQL database, extracts schema information
// for the given schema names, and returns a unified model.Schema.
func Introspect(connStr string, schemaNames []string) (*model.Schema, []diagnostic.Diagnostic, error) {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var diags []diagnostic.Diagnostic

	// Detect PG version.
	pgVersion, err := queryPGVersion(ctx, conn)
	if err != nil {
		return nil, nil, fmt.Errorf("pg version: %w", err)
	}

	schema := &model.Schema{
		PGVersion: pgVersion,
	}

	// Use first schema name as the schema name if there's exactly one.
	if len(schemaNames) == 1 {
		schema.Name = schemaNames[0]
	}

	// Extract extensions.
	exts, err := queryExtensions(ctx, conn)
	if err != nil {
		return nil, nil, fmt.Errorf("extensions: %w", err)
	}
	schema.Extensions = exts

	// Extract enums from all requested schemas.
	for _, sn := range schemaNames {
		enums, err := queryEnums(ctx, conn, sn)
		if err != nil {
			return nil, nil, fmt.Errorf("enums for schema %q: %w", sn, err)
		}
		schema.Enums = append(schema.Enums, enums...)
	}

	// Extract tables from all requested schemas.
	for _, sn := range schemaNames {
		tables, tableDiags, err := queryTables(ctx, conn, sn)
		if err != nil {
			return nil, nil, fmt.Errorf("tables for schema %q: %w", sn, err)
		}
		diags = append(diags, tableDiags...)
		schema.Tables = append(schema.Tables, tables...)
	}

	return schema, diags, nil
}

// queryPGVersion returns the major PostgreSQL version number.
func queryPGVersion(ctx context.Context, conn *pgx.Conn) (int, error) {
	var versionStr string
	err := conn.QueryRow(ctx, "SHOW server_version").Scan(&versionStr)
	if err != nil {
		return 0, err
	}
	// Parse major version from strings like "17.5 (Fedora 17.5-1.fc42)"
	// or "16.2" or "15.0beta1".
	parts := strings.SplitN(versionStr, ".", 2)
	if len(parts) == 0 {
		return 0, fmt.Errorf("cannot parse version %q", versionStr)
	}
	// The major version part may contain non-digits in beta/rc versions,
	// but the leading digits are the major version.
	majorStr := strings.TrimSpace(parts[0])
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, fmt.Errorf("cannot parse major version from %q: %w", versionStr, err)
	}
	return major, nil
}

// queryExtensions returns installed extensions (excluding plpgsql).
func queryExtensions(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT extname FROM pg_extension WHERE extname != 'plpgsql' ORDER BY extname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var exts []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		exts = append(exts, name)
	}
	return exts, rows.Err()
}

// queryEnums returns enum types defined in the given schema.
func queryEnums(ctx context.Context, conn *pgx.Conn, schemaName string) ([]model.Enum, error) {
	rows, err := conn.Query(ctx, `
		SELECT t.typname, array_agg(e.enumlabel ORDER BY e.enumsortorder),
		       d.description
		FROM pg_type t
		JOIN pg_enum e ON e.enumtypid = t.oid
		JOIN pg_namespace n ON n.oid = t.typnamespace
		LEFT JOIN pg_description d ON d.objoid = t.oid
		WHERE n.nspname = $1
		GROUP BY t.typname, d.description
		ORDER BY t.typname
	`, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var enums []model.Enum
	for rows.Next() {
		var e model.Enum
		var comment *string
		if err := rows.Scan(&e.Name, &e.Values, &comment); err != nil {
			return nil, err
		}
		e.Schema = schemaName
		if comment != nil {
			e.Comment = *comment
		}
		enums = append(enums, e)
	}
	return enums, rows.Err()
}

// queryTables returns all tables (regular + partitioned) in the given schema.
func queryTables(ctx context.Context, conn *pgx.Conn, schemaName string) ([]model.Table, []diagnostic.Diagnostic, error) {
	rows, err := conn.Query(ctx, `
		SELECT c.oid, c.relname, d.description
		FROM pg_class c
		JOIN pg_namespace n ON c.relnamespace = n.oid
		LEFT JOIN pg_description d ON d.objoid = c.oid AND d.objsubid = 0
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p')
		ORDER BY c.relname
	`, schemaName)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	type tableInfo struct {
		oid     uint32
		name    string
		comment string
	}

	var infos []tableInfo
	for rows.Next() {
		var ti tableInfo
		var comment *string
		if err := rows.Scan(&ti.oid, &ti.name, &comment); err != nil {
			return nil, nil, err
		}
		if comment != nil {
			ti.comment = *comment
		}
		infos = append(infos, ti)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var tables []model.Table
	var diags []diagnostic.Diagnostic

	for _, ti := range infos {
		t := model.Table{
			Name:    ti.name,
			Schema:  schemaName,
			Comment: ti.comment,
		}

		// Columns
		cols, err := queryColumns(ctx, conn, ti.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("columns for %s.%s: %w", schemaName, ti.name, err)
		}
		t.Columns = cols

		// Primary key
		pk, err := queryPrimaryKey(ctx, conn, ti.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("pk for %s.%s: %w", schemaName, ti.name, err)
		}
		t.PK = pk

		// Foreign keys
		fks, err := queryForeignKeys(ctx, conn, ti.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("fks for %s.%s: %w", schemaName, ti.name, err)
		}
		t.FKs = fks

		// Indexes
		idxs, idxDiags, err := queryIndexes(ctx, conn, ti.oid, schemaName, ti.name)
		if err != nil {
			return nil, nil, fmt.Errorf("indexes for %s.%s: %w", schemaName, ti.name, err)
		}
		t.Indexes = idxs
		diags = append(diags, idxDiags...)

		// Unique constraints
		uqs, err := queryUniqueConstraints(ctx, conn, ti.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("uniques for %s.%s: %w", schemaName, ti.name, err)
		}
		t.Uniques = uqs

		// Check constraints
		cks, err := queryCheckConstraints(ctx, conn, ti.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("checks for %s.%s: %w", schemaName, ti.name, err)
		}
		t.Checks = cks

		tables = append(tables, t)
	}

	return tables, diags, nil
}

// queryColumns returns columns for a table OID.
func queryColumns(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]model.Column, error) {
	rows, err := conn.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod) as type,
		       a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid) as default_expr,
		       d.description
		FROM pg_attribute a
		LEFT JOIN pg_attrdef ad ON a.attrelid = ad.adrelid AND a.attnum = ad.adnum
		LEFT JOIN pg_description d ON d.objoid = a.attrelid AND d.objsubid = a.attnum
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum
	`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []model.Column
	for rows.Next() {
		var c model.Column
		var defaultExpr *string
		var comment *string
		if err := rows.Scan(&c.Name, &c.PGType, &c.NotNull, &defaultExpr, &comment); err != nil {
			return nil, err
		}
		if defaultExpr != nil {
			c.DefaultExpr = *defaultExpr
		}
		if comment != nil {
			c.Comment = *comment
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// queryPrimaryKey returns the primary key column names for a table OID.
func queryPrimaryKey(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]string, error) {
	var pk []string
	err := conn.QueryRow(ctx, `
		SELECT array_agg(a.attname ORDER BY array_position(con.conkey, a.attnum))
		FROM pg_constraint con
		JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY(con.conkey)
		WHERE con.conrelid = $1 AND con.contype = 'p'
	`, tableOID).Scan(&pk)
	if err != nil {
		// No primary key is not an error.
		return nil, nil
	}
	return pk, nil
}

// queryForeignKeys returns foreign key constraints for a table OID.
func queryForeignKeys(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]model.FK, error) {
	rows, err := conn.Query(ctx, `
		SELECT con.conname,
		       array_agg(a.attname ORDER BY array_position(con.conkey, a.attnum)) as columns,
		       nref.nspname as ref_schema,
		       cref.relname as ref_table,
		       array_agg(aref.attname ORDER BY array_position(con.confkey, aref.attnum)) as ref_columns,
		       CASE con.confdeltype
		           WHEN 'c' THEN 'CASCADE'
		           WHEN 'n' THEN 'SET NULL'
		           WHEN 'd' THEN 'SET DEFAULT'
		           WHEN 'r' THEN 'RESTRICT'
		           WHEN 'a' THEN 'NO ACTION'
		       END as on_delete
		FROM pg_constraint con
		JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY(con.conkey)
		JOIN pg_class cref ON cref.oid = con.confrelid
		JOIN pg_namespace nref ON nref.oid = cref.relnamespace
		JOIN pg_attribute aref ON aref.attrelid = con.confrelid AND aref.attnum = ANY(con.confkey)
		WHERE con.conrelid = $1 AND con.contype = 'f'
		GROUP BY con.conname, nref.nspname, cref.relname, con.confdeltype
		ORDER BY con.conname
	`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []model.FK
	for rows.Next() {
		var fk model.FK
		if err := rows.Scan(&fk.Name, &fk.Columns, &fk.RefSchema, &fk.RefTable, &fk.RefColumns, &fk.OnDelete); err != nil {
			return nil, err
		}
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}

// queryIndexes returns non-primary-key indexes for a table OID.
func queryIndexes(ctx context.Context, conn *pgx.Conn, tableOID uint32, schemaName, tableName string) ([]model.Index, []diagnostic.Diagnostic, error) {
	rows, err := conn.Query(ctx, `
		SELECT i.relname as index_name,
		       am.amname as method,
		       pg_get_indexdef(ix.indexrelid) as definition,
		       ix.indisunique
		FROM pg_index ix
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_am am ON am.oid = i.relam
		WHERE ix.indrelid = $1 AND NOT ix.indisprimary
		ORDER BY i.relname
	`, tableOID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var indexes []model.Index
	var diags []diagnostic.Diagnostic

	for rows.Next() {
		var name, method, definition string
		var isUnique bool
		if err := rows.Scan(&name, &method, &definition, &isUnique); err != nil {
			return nil, nil, err
		}

		idx := model.Index{
			Name:   name,
			Method: method,
			Unique: isUnique,
		}

		// Parse the index definition to extract columns, WHERE, INCLUDE, opclass.
		parsed := parseIndexDef(definition)
		idx.Columns = parsed.columns
		idx.Where = parsed.where
		idx.Include = parsed.include
		idx.Opclass = parsed.opclass

		// If the index is unique but not backed by a unique constraint,
		// record it as a unique index (method is already set).
		// pgdesign doesn't have a separate "unique index" field on Index,
		// but unique indexes that aren't constraints show up here.

		indexes = append(indexes, idx)
	}

	return indexes, diags, rows.Err()
}

// parsedIndex holds parsed components of a pg_get_indexdef() string.
type parsedIndex struct {
	columns []string
	where   string
	include []string
	opclass string
}

// indexDefPattern matches CREATE [UNIQUE] INDEX name ON [ONLY] schema.table USING method (columns) [INCLUDE (cols)] [WHERE expr]
var indexDefPattern = regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+\S+\s+ON\s+(?:ONLY\s+)?\S+\s+USING\s+\S+\s+\((.+?)\)(?:\s+INCLUDE\s+\((.+?)\))?(?:\s+WHERE\s+(.+))?$`)

// parseIndexDef parses a pg_get_indexdef() string into its components.
func parseIndexDef(def string) parsedIndex {
	p := parsedIndex{}

	m := indexDefPattern.FindStringSubmatch(def)
	if m == nil {
		// Fallback: cannot parse. Return empty.
		return p
	}

	// Parse columns and detect opclass.
	colStr := m[1]
	p.columns, p.opclass = parseIndexColumns(colStr)

	// INCLUDE columns.
	if m[2] != "" {
		p.include = splitAndTrim(m[2])
	}

	// WHERE clause.
	if m[3] != "" {
		p.where = strings.TrimSpace(m[3])
	}

	return p
}

// parseIndexColumns parses the column list from an index definition,
// extracting opclass if present.
func parseIndexColumns(colStr string) ([]string, string) {
	parts := splitAndTrim(colStr)
	var columns []string
	var opclass string

	for _, part := range parts {
		// Column may have opclass suffix like "col varchar_pattern_ops"
		tokens := strings.Fields(part)
		if len(tokens) >= 2 {
			// Check if the last token looks like an opclass (contains _ops).
			last := tokens[len(tokens)-1]
			if strings.Contains(last, "_ops") {
				columns = append(columns, strings.Join(tokens[:len(tokens)-1], " "))
				opclass = last
			} else {
				columns = append(columns, part)
			}
		} else {
			columns = append(columns, part)
		}
	}

	return columns, opclass
}

// splitAndTrim splits a comma-separated string and trims whitespace.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// queryUniqueConstraints returns unique constraints for a table OID.
func queryUniqueConstraints(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]model.UniqueConstraint, error) {
	rows, err := conn.Query(ctx, `
		SELECT con.conname,
		       array_agg(a.attname ORDER BY array_position(con.conkey, a.attnum))
		FROM pg_constraint con
		JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY(con.conkey)
		WHERE con.conrelid = $1 AND con.contype = 'u'
		GROUP BY con.conname
		ORDER BY con.conname
	`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var uqs []model.UniqueConstraint
	for rows.Next() {
		var uq model.UniqueConstraint
		if err := rows.Scan(&uq.Name, &uq.Columns); err != nil {
			return nil, err
		}
		uqs = append(uqs, uq)
	}
	return uqs, rows.Err()
}

// queryCheckConstraints returns check constraints for a table OID.
func queryCheckConstraints(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]model.CheckConstraint, error) {
	rows, err := conn.Query(ctx, `
		SELECT con.conname, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		WHERE con.conrelid = $1 AND con.contype = 'c'
		ORDER BY con.conname
	`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cks []model.CheckConstraint
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		// pg_get_constraintdef returns "CHECK (expr)" -- strip the wrapper.
		expr := def
		if strings.HasPrefix(strings.ToUpper(expr), "CHECK (") && strings.HasSuffix(expr, ")") {
			expr = expr[7 : len(expr)-1]
		}
		cks = append(cks, model.CheckConstraint{
			Name: name,
			Expr: expr,
		})
	}
	return cks, rows.Err()
}
