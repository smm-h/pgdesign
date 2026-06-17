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
	pg "github.com/pganalyze/pg_query_go/v6"
	pg_query "github.com/wasilibs/go-pgquery"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// Introspect connects to a PostgreSQL database, extracts schema information
// for the given schema names, and returns a unified model.Schema. The provided
// context controls connection and query timeouts.
func Introspect(ctx context.Context, connStr string, schemaNames []string) (*model.Schema, []diagnostic.Diagnostic, error) {
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

	// Extract composite types from all requested schemas.
	for _, sn := range schemaNames {
		cts, err := queryCompositeTypes(ctx, conn, sn)
		if err != nil {
			return nil, nil, fmt.Errorf("composite types for schema %q: %w", sn, err)
		}
		schema.CompositeTypes = append(schema.CompositeTypes, cts...)
	}

	// Extract tables from all requested schemas.
	for _, sn := range schemaNames {
		tables, tableDiags, err := queryTables(ctx, conn, sn, pgVersion)
		if err != nil {
			return nil, nil, fmt.Errorf("tables for schema %q: %w", sn, err)
		}
		diags = append(diags, tableDiags...)
		schema.Tables = append(schema.Tables, tables...)
	}

	// Extract views from all requested schemas.
	for _, sn := range schemaNames {
		views, err := queryViews(ctx, conn, sn)
		if err != nil {
			return nil, nil, fmt.Errorf("views for schema %q: %w", sn, err)
		}
		schema.Views = append(schema.Views, views...)
	}

	// Extract materialized views from all requested schemas.
	for _, sn := range schemaNames {
		mvs, mvDiags, err := queryMaterializedViews(ctx, conn, sn)
		if err != nil {
			return nil, nil, fmt.Errorf("materialized views for schema %q: %w", sn, err)
		}
		diags = append(diags, mvDiags...)
		schema.MaterializedViews = append(schema.MaterializedViews, mvs...)
	}

	// Extract sequences from all requested schemas.
	for _, sn := range schemaNames {
		seqs, err := querySequences(ctx, conn, sn)
		if err != nil {
			return nil, nil, fmt.Errorf("sequences for schema %q: %w", sn, err)
		}
		schema.Sequences = append(schema.Sequences, seqs...)
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

// queryCompositeTypes returns user-defined composite types in the given schema,
// excluding table row types (every table auto-creates a composite type for its rows).
func queryCompositeTypes(ctx context.Context, conn *pgx.Conn, schemaName string) ([]model.CompositeType, error) {
	rows, err := conn.Query(ctx, `
		SELECT t.typname,
		       array_agg(a.attname ORDER BY a.attnum) as field_names,
		       array_agg(format_type(a.atttypid, a.atttypmod) ORDER BY a.attnum) as field_types,
		       d.description
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		JOIN pg_attribute a ON a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped
		LEFT JOIN pg_description d ON d.objoid = t.oid
		WHERE n.nspname = $1
		  AND t.typtype = 'c'
		  AND t.typrelid NOT IN (
		      SELECT oid FROM pg_class
		      WHERE relkind IN ('r', 'v', 'm', 'p', 'f', 't')
		  )
		GROUP BY t.typname, d.description
		ORDER BY t.typname
	`, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cts []model.CompositeType
	for rows.Next() {
		var name string
		var fieldNames []string
		var fieldTypes []string
		var comment *string
		if err := rows.Scan(&name, &fieldNames, &fieldTypes, &comment); err != nil {
			return nil, err
		}
		ct := model.CompositeType{
			Name:   name,
			Schema: schemaName,
		}
		if comment != nil {
			ct.Comment = *comment
		}
		for i := range fieldNames {
			ct.Fields = append(ct.Fields, model.CompositeField{
				Name:   fieldNames[i],
				PGType: fieldTypes[i],
			})
		}
		cts = append(cts, ct)
	}
	return cts, rows.Err()
}

// queryTables returns all tables (regular + partitioned) in the given schema.
func queryTables(ctx context.Context, conn *pgx.Conn, schemaName string, pgVersion int) ([]model.Table, []diagnostic.Diagnostic, error) {
	rows, err := conn.Query(ctx, `
		SELECT c.oid, c.relname, c.relkind::text, d.description
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
		relkind string // "r" = regular, "p" = partitioned
		comment string
	}

	var infos []tableInfo
	for rows.Next() {
		var ti tableInfo
		var comment *string
		if err := rows.Scan(&ti.oid, &ti.name, &ti.relkind, &comment); err != nil {
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
		cols, err := queryColumns(ctx, conn, ti.oid, pgVersion)
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

		// Exclusion constraints
		excls, err := queryExclusionConstraints(ctx, conn, ti.oid)
		if err != nil {
			return nil, nil, fmt.Errorf("exclusions for %s.%s: %w", schemaName, ti.name, err)
		}
		t.Exclusions = excls

		// Partition metadata (only for partitioned tables).
		if ti.relkind == "p" {
			ps, err := queryPartitionSpec(ctx, conn, ti.oid, t.Columns, pgVersion)
			if err != nil {
				return nil, nil, fmt.Errorf("partitioning for %s.%s: %w", schemaName, ti.name, err)
			}
			t.Partitioning = ps
		}

		tables = append(tables, t)
	}

	return tables, diags, nil
}

// queryColumns returns columns for a table OID.
func queryColumns(ctx context.Context, conn *pgx.Conn, tableOID uint32, pgVersion int) ([]model.Column, error) {
	// PG 12+ has attgenerated; older versions don't.
	var query string
	if pgVersion >= 12 {
		query = `
			SELECT a.attname, format_type(a.atttypid, a.atttypmod) as type,
			       a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid) as default_expr,
			       d.description, a.attgenerated::text, a.attidentity::text,
			       COALESCE(co.collname, '') as collation,
			       a.attstattarget
			FROM pg_attribute a
			LEFT JOIN pg_attrdef ad ON a.attrelid = ad.adrelid AND a.attnum = ad.adnum
			LEFT JOIN pg_description d ON d.objoid = a.attrelid AND d.objsubid = a.attnum
			LEFT JOIN pg_collation co ON co.oid = a.attcollation AND a.attcollation <> 0
			WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
			ORDER BY a.attnum`
	} else {
		query = `
			SELECT a.attname, format_type(a.atttypid, a.atttypmod) as type,
			       a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid) as default_expr,
			       d.description, '' as attgenerated, '' as attidentity,
			       COALESCE(co.collname, '') as collation,
			       a.attstattarget
			FROM pg_attribute a
			LEFT JOIN pg_attrdef ad ON a.attrelid = ad.adrelid AND a.attnum = ad.adnum
			LEFT JOIN pg_description d ON d.objoid = a.attrelid AND d.objsubid = a.attnum
			LEFT JOIN pg_collation co ON co.oid = a.attcollation AND a.attcollation <> 0
			WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
			ORDER BY a.attnum`
	}

	rows, err := conn.Query(ctx, query, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []model.Column
	for rows.Next() {
		var c model.Column
		var defaultExpr *string
		var comment *string
		var attgenerated string
		var attidentity string
		var collation string
		var attstattarget *int32
		if err := rows.Scan(&c.Name, &c.PGType, &c.NotNull, &defaultExpr, &comment, &attgenerated, &attidentity, &collation, &attstattarget); err != nil {
			return nil, err
		}

		// Map attgenerated: 's' = stored, 'v' = virtual, '' = not generated.
		switch attgenerated {
		case "s":
			if defaultExpr != nil {
				c.Generated = *defaultExpr
				defaultExpr = nil // Don't also set it as DefaultExpr.
			}
			c.Stored = true
		case "v":
			if defaultExpr != nil {
				c.Generated = *defaultExpr
				defaultExpr = nil
			}
			c.Stored = false
		}

		// Map attidentity: 'a' = ALWAYS, 'd' = BY DEFAULT, '' = not identity.
		switch attidentity {
		case "a":
			c.Identity = "ALWAYS"
		case "d":
			c.Identity = "BY DEFAULT"
		}

		// Identity columns report nextval() as their default, but the Identity
		// field captures the semantics. Clear the default to avoid duplication.
		if c.Identity != "" {
			defaultExpr = nil
			c.Default = nil
			c.DefaultExpr = ""
		}

		// Classify the default: simple literals go into Default (for TOML
		// round-trip compatibility), complex expressions stay in DefaultExpr.
		if defaultExpr != nil {
			if rawVal, ok := parseSimpleDefault(*defaultExpr); ok {
				c.Default = model.StrPtr(rawVal)
			} else {
				c.DefaultExpr = *defaultExpr
			}
		}
		if comment != nil {
			c.Comment = *comment
		}
		// Detect array types: format_type() returns "text[]", "integer[]", etc.
		if strings.HasSuffix(c.PGType, "[]") {
			c.Array = true
			c.PGType = strings.TrimSuffix(c.PGType, "[]")
		}
		// Collation: non-empty means explicit collation was set.
		// Filter out "default" collation -- it's the same as no collation.
		if collation != "" && collation != "default" {
			c.Collation = collation
		}
		// Statistics: NULL (PG 17+) or -1 (older) means database default.
		if attstattarget != nil && *attstattarget >= 0 {
			v := int(*attstattarget)
			c.Statistics = &v
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// parseSimpleDefault detects simple literal defaults from pg_get_expr output
// and returns the raw value suitable for Column.Default. Complex defaults
// (function calls, expressions) return isSimple=false and should remain in
// Column.DefaultExpr.
func parseSimpleDefault(pgExpr string) (rawValue string, isSimple bool) {
	// Function calls or expressions with parentheses are complex.
	if strings.Contains(pgExpr, "(") {
		return "", false
	}
	// NULL is not a default -- nullable columns have no default.
	if pgExpr == "NULL" {
		return "", false
	}
	// Bare integer: 0, 42, -1
	if matched, _ := regexp.MatchString(`^-?[0-9]+$`, pgExpr); matched {
		return pgExpr, true
	}
	// Bare boolean.
	if pgExpr == "true" || pgExpr == "false" {
		return pgExpr, true
	}
	// Single-quoted string, optionally with a ::type cast.
	if len(pgExpr) > 0 && pgExpr[0] == '\'' {
		return parseQuotedDefault(pgExpr)
	}
	return "", false
}

// parseQuotedDefault extracts the value from a single-quoted PG literal,
// handling escaped quotes (''), chained ::type casts, and COLLATE clauses.
// Returns the unquoted value and true if the expression is a simple quoted
// literal (with optional casts/collation), or ("", false) if there's trailing
// content that makes it complex (operators, etc).
func parseQuotedDefault(pgExpr string) (string, bool) {
	// Walk past the opening quote to find the matching close quote.
	// PostgreSQL escapes single quotes as ''.
	var val strings.Builder
	i := 1 // skip opening '
	for i < len(pgExpr) {
		if pgExpr[i] == '\'' {
			if i+1 < len(pgExpr) && pgExpr[i+1] == '\'' {
				// Escaped quote: '' -> '
				val.WriteByte('\'')
				i += 2
				continue
			}
			// Closing quote found.
			i++ // skip the closing '
			break
		}
		val.WriteByte(pgExpr[i])
		i++
	}
	// After the closing quote, strip optional chained ::type casts,
	// then an optional COLLATE clause.
	rest := pgExpr[i:]
	rest = stripCastChain(rest)
	rest = stripCollate(rest)
	if rest == "" {
		return val.String(), true
	}
	return "", false
}

// stripCastChain strips zero or more ::type cast suffixes from the front of s.
// A type name can contain alphanumerics, underscores, dots (schema.type),
// and brackets (e.g., "text[]"). Spaces are allowed for multi-word types
// like "character varying", but not before keywords like COLLATE.
func stripCastChain(s string) string {
	for strings.HasPrefix(s, "::") {
		s = s[2:]
		j := 0
		for j < len(s) {
			c := s[j]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '[' || c == ']' {
				j++
				continue
			}
			if c == ' ' {
				// Space might be part of a multi-word type (e.g., "character varying")
				// or it might separate the type from a COLLATE clause or another ::cast.
				// Peek ahead: if what follows the space is COLLATE or ::, stop here.
				after := strings.TrimLeft(s[j:], " ")
				if strings.HasPrefix(after, "::") || strings.HasPrefix(strings.ToUpper(after), "COLLATE") || after == "" {
					break
				}
				j++
				continue
			}
			break
		}
		s = s[j:]
	}
	return s
}

// stripCollate strips an optional COLLATE clause from the front of s.
// Handles both quoted identifiers (COLLATE "C") and bare identifiers
// (COLLATE en_US).
func stripCollate(s string) string {
	rest := strings.TrimLeft(s, " ")
	if !strings.HasPrefix(strings.ToUpper(rest), "COLLATE") {
		return s
	}
	rest = rest[len("COLLATE"):]
	// Must be followed by whitespace (COLLATE is not a prefix of a type name).
	if len(rest) == 0 || rest[0] != ' ' {
		return s
	}
	rest = strings.TrimLeft(rest, " ")
	if len(rest) > 0 && rest[0] == '"' {
		// Quoted identifier: find closing double quote.
		end := strings.Index(rest[1:], "\"")
		if end < 0 {
			return s // unterminated quote -- leave it
		}
		return rest[end+2:]
	}
	// Bare identifier: alphanumerics, underscores, dots.
	j := 0
	for j < len(rest) {
		c := rest[j]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' {
			j++
			continue
		}
		break
	}
	return rest[j:]
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
		       ix.indisunique,
		       i.reloptions
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
		var reloptions []string
		if err := rows.Scan(&name, &method, &definition, &isUnique, &reloptions); err != nil {
			return nil, nil, err
		}

		idx := model.Index{
			Name:   name,
			Method: method,
			Unique: isUnique,
		}

		// Parse the index definition to extract columns, WHERE, INCLUDE, opclass, sort order.
		parsed := parseIndexDef(definition)
		idx.Columns = parsed.columns
		idx.Desc = parsed.desc
		idx.Where = parsed.where
		idx.Include = parsed.include
		idx.Opclasses = parsed.opclasses
		idx.Collations = parsed.collations
		idx.With = parseReloptions(reloptions)

		// If the index is unique but not backed by a unique constraint,
		// record it as a unique index (method is already set).
		// pgdesign doesn't have a separate "unique index" field on Index,
		// but unique indexes that aren't constraints show up here.

		indexes = append(indexes, idx)
	}

	return indexes, diags, rows.Err()
}

// parseReloptions converts pg_class.reloptions (text[] like ["m=16", "ef_construction=200"])
// into a map[string]string.
func parseReloptions(opts []string) map[string]string {
	if len(opts) == 0 {
		return nil
	}
	m := make(map[string]string, len(opts))
	for _, opt := range opts {
		if idx := strings.IndexByte(opt, '='); idx >= 0 {
			m[opt[:idx]] = opt[idx+1:]
		}
	}
	return m
}

// parsedIndex holds parsed components of a pg_get_indexdef() string.
type parsedIndex struct {
	columns    []string
	desc       []bool // parallel to columns; true if DESC
	where      string
	include    []string
	opclasses  map[string]string
	collations map[string]string
}

// parseIndexDef parses a pg_get_indexdef() string into its components
// using go-pgquery's AST instead of regex.
func parseIndexDef(def string) parsedIndex {
	p := parsedIndex{}

	result, err := pg_query.Parse(def)
	if err != nil || len(result.Stmts) == 0 {
		return p
	}

	idxStmt := result.Stmts[0].Stmt.GetIndexStmt()
	if idxStmt == nil {
		return p
	}

	// Parse index columns.
	anyDesc := false
	for _, paramNode := range idxStmt.IndexParams {
		elem := paramNode.GetIndexElem()
		if elem == nil {
			continue
		}

		// Column name: either a simple name or an expression.
		var colName string
		if elem.Name != "" {
			colName = elem.Name
		} else if elem.Expr != nil {
			deparsed, err := sqlparse.DeparseExpr(elem.Expr)
			if err != nil {
				continue
			}
			colName = deparsed
		}

		p.columns = append(p.columns, colName)

		// Sort direction.
		isDesc := elem.Ordering == pg.SortByDir_SORTBY_DESC
		if isDesc {
			anyDesc = true
		}
		p.desc = append(p.desc, isDesc)

		// Opclass.
		if len(elem.Opclass) > 0 {
			var opclassName string
			for _, ocNode := range elem.Opclass {
				if s := ocNode.GetString_(); s != nil {
					if opclassName != "" {
						opclassName += "."
					}
					opclassName += s.Sval
				}
			}
			if opclassName != "" {
				if p.opclasses == nil {
					p.opclasses = make(map[string]string)
				}
				p.opclasses[colName] = opclassName
			}
		}
		// Collation.
		if len(elem.Collation) > 0 {
			var collName string
			for _, cNode := range elem.Collation {
				if s := cNode.GetString_(); s != nil {
					if collName != "" {
						collName += "."
					}
					collName += s.Sval
				}
			}
			if collName != "" {
				if p.collations == nil {
					p.collations = make(map[string]string)
				}
				p.collations[colName] = collName
			}
		}
	}

	// Omit desc slice if all columns are ASC.
	if !anyDesc {
		p.desc = nil
	}

	// INCLUDE columns.
	for _, inclNode := range idxStmt.IndexIncludingParams {
		elem := inclNode.GetIndexElem()
		if elem == nil {
			continue
		}
		if elem.Name != "" {
			p.include = append(p.include, elem.Name)
		}
	}

	// WHERE clause.
	if idxStmt.WhereClause != nil {
		deparsed, err := sqlparse.DeparseExpr(idxStmt.WhereClause)
		if err == nil {
			p.where = deparsed
		}
	}

	return p
}

// queryUniqueConstraints returns unique constraints for a table OID.
func queryUniqueConstraints(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]model.UniqueConstraint, error) {
	rows, err := conn.Query(ctx, `
		SELECT con.conname,
		       array_agg(a.attname ORDER BY array_position(con.conkey, a.attnum)),
		       con.condeferrable,
		       con.condeferred
		FROM pg_constraint con
		JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY(con.conkey)
		WHERE con.conrelid = $1 AND con.contype = 'u'
		GROUP BY con.conname, con.condeferrable, con.condeferred
		ORDER BY con.conname
	`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var uqs []model.UniqueConstraint
	for rows.Next() {
		var uq model.UniqueConstraint
		if err := rows.Scan(&uq.Name, &uq.Columns, &uq.Deferrable, &uq.InitiallyDeferred); err != nil {
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

// queryExclusionConstraints returns exclusion constraints for a table OID.
func queryExclusionConstraints(ctx context.Context, conn *pgx.Conn, tableOID uint32) ([]model.ExclusionConstraint, error) {
	rows, err := conn.Query(ctx, `
		SELECT con.conname, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		WHERE con.conrelid = $1 AND con.contype = 'x'
		ORDER BY con.conname
	`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var excls []model.ExclusionConstraint
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		exc := parseExclusionDef(name, def)
		excls = append(excls, exc)
	}
	return excls, rows.Err()
}

// parseExclusionDef parses the output of pg_get_constraintdef for an exclusion
// constraint. The format is:
//
//	EXCLUDE USING method (col1 WITH op1, col2 WITH op2) WHERE (predicate)
//	optionally: DEFERRABLE INITIALLY DEFERRED
func parseExclusionDef(name, def string) model.ExclusionConstraint {
	exc := model.ExclusionConstraint{Name: name}

	s := def

	// Strip leading "EXCLUDE USING "
	if strings.HasPrefix(strings.ToUpper(s), "EXCLUDE USING ") {
		s = s[len("EXCLUDE USING "):]
	}

	// Extract method (everything before the first '(')
	parenIdx := strings.Index(s, "(")
	if parenIdx > 0 {
		exc.Method = strings.TrimSpace(s[:parenIdx])
		s = s[parenIdx:]
	}

	// Find matching closing paren for elements
	if len(s) > 0 && s[0] == '(' {
		depth := 0
		closeIdx := -1
		for i, ch := range s {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth == 0 {
					closeIdx = i
					break
				}
			}
		}
		if closeIdx > 0 {
			elemStr := s[1:closeIdx]
			s = strings.TrimSpace(s[closeIdx+1:])

			// Parse elements: "col1 WITH op1, col2 WITH op2"
			parts := splitExclusionElements(elemStr)
			for _, part := range parts {
				part = strings.TrimSpace(part)
				// Each part is "column WITH operator"
				withIdx := strings.Index(strings.ToUpper(part), " WITH ")
				if withIdx >= 0 {
					col := strings.TrimSpace(part[:withIdx])
					op := strings.TrimSpace(part[withIdx+6:])
					exc.Elements = append(exc.Elements, model.ExclusionElement{
						Column:   col,
						Operator: op,
					})
				}
			}
		}
	}

	// Check for WHERE clause
	upperS := strings.ToUpper(s)
	if whereIdx := strings.Index(upperS, "WHERE"); whereIdx >= 0 {
		rest := strings.TrimSpace(s[whereIdx+5:])
		// Strip surrounding parens
		if len(rest) > 0 && rest[0] == '(' {
			depth := 0
			closeIdx := -1
			for i, ch := range rest {
				if ch == '(' {
					depth++
				} else if ch == ')' {
					depth--
					if depth == 0 {
						closeIdx = i
						break
					}
				}
			}
			if closeIdx > 0 {
				exc.Where = rest[1:closeIdx]
				s = strings.TrimSpace(rest[closeIdx+1:])
			}
		}
	}

	// Check for DEFERRABLE
	if strings.Contains(strings.ToUpper(s), "DEFERRABLE") {
		exc.Deferrable = true
		if strings.Contains(strings.ToUpper(s), "INITIALLY DEFERRED") {
			exc.InitiallyDeferred = true
		}
	}

	return exc
}

// splitExclusionElements splits a comma-separated element list, respecting
// parenthesized expressions (e.g., type casts or function calls in column expressions).
func splitExclusionElements(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

// mapPartStrategy maps a pg_partitioned_table.partstrat character to a strategy name.
func mapPartStrategy(code string) string {
	switch code {
	case "r":
		return "range"
	case "l":
		return "list"
	case "h":
		return "hash"
	default:
		return code
	}
}

// queryPartitionSpec queries partition metadata for a partitioned table and
// returns a fully populated PartitionSpec including recursive children.
func queryPartitionSpec(ctx context.Context, conn *pgx.Conn, tableOID uint32, columns []model.Column, pgVersion int) (*model.PartitionSpec, error) {
	// Query partition strategy and key column attribute numbers.
	var stratCode string
	var partAttrs []int32
	err := conn.QueryRow(ctx, `
		SELECT partstrat::text, string_to_array(partattrs::text, ' ')::int[]
		FROM pg_partitioned_table
		WHERE partrelid = $1
	`, tableOID).Scan(&stratCode, &partAttrs)
	if err != nil {
		return nil, fmt.Errorf("pg_partitioned_table: %w", err)
	}

	// Resolve attribute numbers to column names.
	partColumns := resolvePartColumns(partAttrs, columns)

	ps := &model.PartitionSpec{
		Strategy: mapPartStrategy(stratCode),
		Columns:  partColumns,
	}

	// Query child partitions.
	children, err := queryPartitionChildren(ctx, conn, tableOID, pgVersion)
	if err != nil {
		return nil, err
	}
	ps.Children = children

	return ps, nil
}

// resolvePartColumns maps partition key attribute numbers to column names.
// Returns one name per partition key column.
func resolvePartColumns(attNums []int32, columns []model.Column) []string {
	// Build attnum-to-name map. attnum is 1-based.
	attnumMap := make(map[int32]string, len(columns))
	for i, c := range columns {
		attnumMap[int32(i+1)] = c.Name
	}

	var names []string
	for _, num := range attNums {
		if num == 0 {
			// attnum 0 means an expression-based partition key.
			names = append(names, "(expression)")
		} else if name, ok := attnumMap[num]; ok {
			names = append(names, name)
		} else {
			names = append(names, fmt.Sprintf("attnum_%d", num))
		}
	}

	return names
}

// queryPartitionChildren returns child partitions for a parent OID,
// recursing into sub-partitioned children.
func queryPartitionChildren(ctx context.Context, conn *pgx.Conn, parentOID uint32, pgVersion int) ([]model.PartitionSpec, error) {
	rows, err := conn.Query(ctx, `
		SELECT c.oid, c.relname, c.relkind::text,
		       pg_get_expr(c.relpartbound, c.oid) as bound_expr
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = $1
		ORDER BY c.relname
	`, parentOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type childInfo struct {
		oid       uint32
		name      string
		relkind   string
		boundExpr string
	}

	var childInfos []childInfo
	for rows.Next() {
		var ci childInfo
		var boundExpr *string
		if err := rows.Scan(&ci.oid, &ci.name, &ci.relkind, &boundExpr); err != nil {
			return nil, err
		}
		if boundExpr != nil {
			ci.boundExpr = *boundExpr
		}
		childInfos = append(childInfos, ci)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var children []model.PartitionSpec
	for _, ci := range childInfos {
		child := model.PartitionSpec{
			Name:  ci.name,
			Bound: ci.boundExpr,
		}

		// If the child is itself partitioned, recurse to get its sub-partitions.
		if ci.relkind == "p" {
			// Query child's columns for resolving its own partition key.
			childCols, err := queryColumns(ctx, conn, ci.oid, pgVersion)
			if err != nil {
				return nil, fmt.Errorf("columns for child %s: %w", ci.name, err)
			}
			subSpec, err := queryPartitionSpec(ctx, conn, ci.oid, childCols, pgVersion)
			if err != nil {
				return nil, fmt.Errorf("sub-partition for %s: %w", ci.name, err)
			}
			// Merge sub-partition info into the child.
			child.Children = subSpec.Children
		}

		children = append(children, child)
	}

	return children, nil
}

// queryViewDependsOn returns the names of tables, views, materialized views,
// and partitioned tables that the given view (or materialized view) depends on,
// as reported by pg_depend. PostgreSQL tracks view-to-table dependencies
// through the view's rewrite rule (pg_rewrite), not the view's pg_class entry
// directly, so we join through pg_rewrite to find referenced relations.
func queryViewDependsOn(ctx context.Context, conn *pgx.Conn, viewName, schemaName string) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT DISTINCT c.relname
		FROM pg_rewrite rw
		JOIN pg_depend d ON d.objid = rw.oid AND d.classid = 'pg_rewrite'::regclass
		JOIN pg_class c ON c.oid = d.refobjid AND d.refclassid = 'pg_class'::regclass
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE rw.ev_class = (
			SELECT c2.oid
			FROM pg_class c2
			JOIN pg_namespace n2 ON n2.oid = c2.relnamespace
			WHERE c2.relname = $1 AND n2.nspname = $2
		)
		AND d.deptype = 'n'
		AND c.relkind IN ('r', 'v', 'm', 'p')
		AND c.relname != $1
		ORDER BY c.relname
	`, viewName, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deps []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		deps = append(deps, name)
	}
	return deps, rows.Err()
}

// queryViews returns views defined in the given schema. DependsOn is resolved
// via pg_depend.
func queryViews(ctx context.Context, conn *pgx.Conn, schemaName string) ([]model.View, error) {
	rows, err := conn.Query(ctx, `
		SELECT v.viewname, pg_get_viewdef(c.oid, true), d.description
		FROM pg_views v
		JOIN pg_class c ON c.relname = v.viewname
		JOIN pg_namespace n ON c.relnamespace = n.oid AND n.nspname = v.schemaname
		LEFT JOIN pg_description d ON d.objoid = c.oid AND d.objsubid = 0
		WHERE v.schemaname = $1
		ORDER BY v.viewname
	`, schemaName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []model.View
	for rows.Next() {
		var name, definition string
		var comment *string
		if err := rows.Scan(&name, &definition, &comment); err != nil {
			return nil, err
		}

		v := model.View{
			Name:   name,
			Schema: schemaName,
			Query:  strings.TrimSpace(definition),
		}
		if comment != nil {
			v.Comment = *comment
		}

		views = append(views, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Resolve DependsOn via pg_depend in a second pass (the connection
	// cannot execute a nested query while rows are open).
	for i := range views {
		deps, err := queryViewDependsOn(ctx, conn, views[i].Name, schemaName)
		if err != nil {
			return nil, fmt.Errorf("depends_on for view %s: %w", views[i].Name, err)
		}
		views[i].DependsOn = deps
	}

	return views, nil
}

// queryMaterializedViewIndexes returns indexes defined on a materialized view.
func queryMaterializedViewIndexes(ctx context.Context, conn *pgx.Conn, schemaName, mvName string) ([]model.Index, []diagnostic.Diagnostic, error) {
	// Look up the matview's OID.
	var oid uint32
	err := conn.QueryRow(ctx, `
		SELECT c.oid
		FROM pg_class c
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE c.relname = $1 AND n.nspname = $2 AND c.relkind = 'm'
	`, mvName, schemaName).Scan(&oid)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup OID for %s.%s: %w", schemaName, mvName, err)
	}

	return queryIndexes(ctx, conn, oid, schemaName, mvName)
}

// queryMaterializedViews returns materialized views defined in the given schema.
// DependsOn is resolved via pg_depend.
func queryMaterializedViews(ctx context.Context, conn *pgx.Conn, schemaName string) ([]model.MaterializedView, []diagnostic.Diagnostic, error) {
	rows, err := conn.Query(ctx, `
		SELECT m.matviewname, pg_get_viewdef(c.oid, true), d.description, m.ispopulated
		FROM pg_matviews m
		JOIN pg_class c ON c.relname = m.matviewname
		JOIN pg_namespace n ON c.relnamespace = n.oid AND n.nspname = m.schemaname
		LEFT JOIN pg_description d ON d.objoid = c.oid AND d.objsubid = 0
		WHERE m.schemaname = $1
		ORDER BY m.matviewname
	`, schemaName)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var mvs []model.MaterializedView
	var diags []diagnostic.Diagnostic
	for rows.Next() {
		var name, definition string
		var comment *string
		var ispopulated bool
		if err := rows.Scan(&name, &definition, &comment, &ispopulated); err != nil {
			return nil, nil, err
		}

		mv := model.MaterializedView{
			Name:     name,
			Schema:   schemaName,
			Query:    strings.TrimSpace(definition),
			WithData: ispopulated,
		}
		if comment != nil {
			mv.Comment = *comment
		}

		mvs = append(mvs, mv)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// Resolve DependsOn via pg_depend in a second pass (the connection
	// cannot execute a nested query while rows are open).
	for i := range mvs {
		deps, err := queryViewDependsOn(ctx, conn, mvs[i].Name, schemaName)
		if err != nil {
			return nil, nil, fmt.Errorf("depends_on for materialized view %s: %w", mvs[i].Name, err)
		}
		mvs[i].DependsOn = deps
	}

	// Introspect indexes for each materialized view.
	for i := range mvs {
		indexes, idxDiags, err := queryMaterializedViewIndexes(ctx, conn, schemaName, mvs[i].Name)
		if err != nil {
			return nil, nil, fmt.Errorf("indexes for materialized view %s: %w", mvs[i].Name, err)
		}
		mvs[i].Indexes = indexes
		diags = append(diags, idxDiags...)
	}

	return mvs, diags, nil
}

// querySequences extracts standalone sequences from pg_catalog.
// Identity-backed sequences are filtered out (they're managed by identity columns).
func querySequences(ctx context.Context, conn *pgx.Conn, schemaName string) ([]model.Sequence, error) {
	query := `
		SELECT
			c.relname AS seq_name,
			s.seqstart,
			s.seqincrement,
			s.seqmin,
			s.seqmax,
			s.seqcache,
			s.seqcycle,
			pg_catalog.obj_description(c.oid, 'pg_class') AS comment,
			-- OWNED BY: find the dependent table.column via pg_depend
			(
				SELECT t.relname || '.' || a.attname
				FROM pg_depend d
				JOIN pg_class t ON t.oid = d.refobjid
				JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
				WHERE d.objid = c.oid
				  AND d.deptype = 'a'
				  AND d.classid = 'pg_class'::regclass
				  AND d.refclassid = 'pg_class'::regclass
				  AND d.refobjsubid > 0
				LIMIT 1
			) AS owned_by
		FROM pg_sequence s
		JOIN pg_class c ON c.oid = s.seqrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1
		  AND c.relkind = 'S'
		  -- Exclude identity-backed sequences: they have an internal dependency
		  -- from pg_depend where the owning column has attidentity set.
		  AND NOT EXISTS (
			SELECT 1
			FROM pg_depend d
			JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
			WHERE d.objid = c.oid
			  AND d.deptype = 'i'
			  AND a.attidentity != ''
		  )
		ORDER BY c.relname`

	rows, err := conn.Query(ctx, query, schemaName)
	if err != nil {
		return nil, fmt.Errorf("query sequences: %w", err)
	}
	defer rows.Close()

	var seqs []model.Sequence
	for rows.Next() {
		var (
			name      string
			start     int64
			increment int64
			minVal    int64
			maxVal    int64
			cache     int64
			cycle     bool
			comment   *string
			ownedBy   *string
		)
		if err := rows.Scan(&name, &start, &increment, &minVal, &maxVal, &cache, &cycle, &comment, &ownedBy); err != nil {
			return nil, fmt.Errorf("scan sequence: %w", err)
		}

		seq := model.Sequence{
			Name:      name,
			Schema:    schemaName,
			Start:     &start,
			Increment: &increment,
			MinValue:  &minVal,
			MaxValue:  &maxVal,
			Cache:     &cache,
			Cycle:     cycle,
		}
		if comment != nil {
			seq.Comment = *comment
		}
		if ownedBy != nil {
			seq.OwnedBy = *ownedBy
		}

		seqs = append(seqs, seq)
	}

	return seqs, rows.Err()
}
