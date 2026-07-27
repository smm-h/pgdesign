// Package catalog is the shared, SCOPED pg_catalog query layer (roadmap
// 5.5+5.7). It answers PER-OBJECT existence and attribute questions at
// PRECONDITION granularity ("does this one table exist?", "does this column have
// this type/nullability/default?", "is this index present AND valid?"), which is
// exactly what the migration predicate executor (internal/predicate) needs and
// what introspect's ~45 per-schema BULK extractors are the wrong granularity for.
//
// It is the SINGLE place per-object pg_catalog queries live: the divergence bug
// class (two independent sets of catalog queries drifting apart) is the class
// this extraction exists to kill. introspect adopts the shared entry points where
// natural (e.g. Version); it is NOT rewritten wholesale.
//
// Version-conditional queries gate through the EXISTING internal/pgcap capability
// registry — catalog never regrows version logic of its own.
package catalog

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/pgcap"
	"github.com/smm-h/pgdesign/internal/sql"
)

// Querier is the read surface catalog needs. Both *pgx.Conn and pgx.Tx satisfy
// it, so callers pass whichever transactional scope a precondition runs in.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// qualified renders a (schema, name) pair as a quoted identifier suitable for
// to_regclass / to_regtype (which parse SQL identifier syntax). An empty schema
// yields the bare quoted name, resolved through the connection's search_path.
func qualified(schema, name string) string {
	if schema == "" {
		return sql.QuoteIdent(name)
	}
	return sql.QualifiedName(schema, name)
}

// Version returns the server's major version (e.g. 18). It is the ONE catalog
// entry point introspect naturally shares. Parses "18.3 (Fedora ...)" etc.
func Version(ctx context.Context, q Querier) (int, error) {
	var s string
	if err := q.QueryRow(ctx, "SHOW server_version").Scan(&s); err != nil {
		return 0, fmt.Errorf("catalog: read server_version: %w", err)
	}
	major := strings.TrimSpace(strings.SplitN(s, ".", 2)[0])
	v, err := strconv.Atoi(major)
	if err != nil {
		return 0, fmt.Errorf("catalog: parse major version from %q: %w", s, err)
	}
	return v, nil
}

// relkindExists reports whether a relation named (schema, name) exists with one
// of the given relkinds. It resolves through to_regclass (honoring search_path
// when schema is empty) and returns false when the name does not resolve.
func relkindExists(ctx context.Context, q Querier, schema, name string, kinds ...string) (bool, error) {
	var kind string
	err := q.QueryRow(ctx,
		"SELECT c.relkind::text FROM pg_class c WHERE c.oid = to_regclass($1)",
		qualified(schema, name)).Scan(&kind)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe relation %s: %w", qualified(schema, name), err)
	}
	for _, k := range kinds {
		if kind == k {
			return true, nil
		}
	}
	return false, nil
}

// TableExists reports whether an ordinary or partitioned table exists.
func TableExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return relkindExists(ctx, q, schema, name, "r", "p")
}

// ViewExists reports whether a view exists.
func ViewExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return relkindExists(ctx, q, schema, name, "v")
}

// MatViewExists reports whether a materialized view exists.
func MatViewExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return relkindExists(ctx, q, schema, name, "m")
}

// SequenceExists reports whether a sequence exists.
func SequenceExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return relkindExists(ctx, q, schema, name, "S")
}

// typtypeExists reports whether a type named (schema, name) exists with the given
// pg_type.typtype ('e' enum, 'd' domain, 'c' composite).
func typtypeExists(ctx context.Context, q Querier, schema, name, typtype string) (bool, error) {
	var tt string
	err := q.QueryRow(ctx,
		"SELECT t.typtype::text FROM pg_type t WHERE t.oid = to_regtype($1)",
		qualified(schema, name)).Scan(&tt)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe type %s: %w", qualified(schema, name), err)
	}
	return tt == typtype, nil
}

// EnumExists reports whether an enum type exists.
func EnumExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return typtypeExists(ctx, q, schema, name, "e")
}

// DomainExists reports whether a domain type exists.
func DomainExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return typtypeExists(ctx, q, schema, name, "d")
}

// CompositeExists reports whether a free-standing composite type exists. (Table
// row types are also typtype 'c'; this is scoped by the caller passing a
// composite-type name, matching how the model records them.)
func CompositeExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return typtypeExists(ctx, q, schema, name, "c")
}

// FunctionExists reports whether a function with the given (schema, name) and
// parenthesized argument-type signature exists (e.g. argSig "(integer, text)").
// It resolves through to_regprocedure, which keys on argument TYPES — matching
// PostgreSQL overload resolution and the manifest key's ArgSig.
func FunctionExists(ctx context.Context, q Querier, schema, name, argSig string) (bool, error) {
	ident := qualified(schema, name) + argSig
	var ok bool
	err := q.QueryRow(ctx,
		"SELECT true FROM pg_proc WHERE oid = to_regprocedure($1)", ident).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe function %s: %w", ident, err)
	}
	return ok, nil
}

// ColumnInfo is a column's precondition-relevant attributes.
type ColumnInfo struct {
	Type      string // canonical type via ::regtype (e.g. "integer", "character varying(20)")
	NotNull   bool
	Default   string // pg_get_expr of the default, "" when none
	Generated bool   // stored/virtual generated column (PG 12+; false on older servers)
}

// Column returns a column's attributes, or (nil, false) when the table or column
// does not exist. pgVersion gates the generated-column probe through pgcap: the
// pg_attribute.attgenerated column exists only on PG 12+, so on older servers the
// Generated flag is left false rather than querying a nonexistent column.
func Column(ctx context.Context, q Querier, pgVersion int, schema, table, column string) (*ColumnInfo, bool, error) {
	rel := qualified(schema, table)
	genExpr := "false"
	if pgcap.Has(pgVersion, pgcap.AttGeneratedColumn) {
		genExpr = "(a.attgenerated <> '')"
	}
	query := fmt.Sprintf(`
		SELECT format_type(a.atttypid, a.atttypmod),
		       a.attnotnull,
		       COALESCE(pg_get_expr(ad.adbin, ad.adrelid), ''),
		       %s
		FROM pg_attribute a
		LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
		WHERE a.attrelid = to_regclass($1) AND a.attname = $2
		      AND a.attnum > 0 AND NOT a.attisdropped`, genExpr)
	var info ColumnInfo
	err := q.QueryRow(ctx, query, rel, column).
		Scan(&info.Type, &info.NotNull, &info.Default, &info.Generated)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("catalog: probe column %s.%s: %w", rel, column, err)
	}
	return &info, true, nil
}

// ColumnTypeMatches reports whether the column's type equals expectedType by OID
// via to_regtype — the alias-robust, pure-computable probe (roadmap 5.5+5.7
// matching-strategy resolution). to_regtype resolves aliases (int4 == integer,
// varchar == character varying) to the same OID, so equivalent spellings do NOT
// false-drift. It returns the found canonical type text for diagnostics. When the
// column is absent, present is false. A NULL to_regtype (unparseable expectedType)
// yields match=false rather than an error.
//
// NOTE (typmod gap): to_regtype discards the type modifier, so this OID probe does
// not distinguish e.g. varchar(10) from varchar(20) on the same base type. Length/
// precision drift on an unchanged base type is not caught by the type probe; it is
// a documented limitation of the pure to_regtype probe (a full typmod comparison
// would require the round-trip mechanism reserved for definitional bodies).
func ColumnTypeMatches(ctx context.Context, q Querier, schema, table, column, expectedType string) (match, present bool, found string, err error) {
	rel := qualified(schema, table)
	err = q.QueryRow(ctx, `
		SELECT COALESCE(a.atttypid = to_regtype($3)::oid, false),
		       format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		WHERE a.attrelid = to_regclass($1) AND a.attname = $2
		      AND a.attnum > 0 AND NOT a.attisdropped`,
		rel, column, expectedType).Scan(&match, &found)
	if err == pgx.ErrNoRows {
		return false, false, "", nil
	}
	if err != nil {
		return false, false, "", fmt.Errorf("catalog: probe column type %s.%s: %w", rel, column, err)
	}
	return match, true, found, nil
}

// ConstraintDef returns pg_get_constraintdef for the named constraint on the
// table, or ("", false) when absent. The returned text is the canonical Postgres
// rendering used for present-and-matching precondition comparison.
func ConstraintDef(ctx context.Context, q Querier, schema, table, constraint string) (string, bool, error) {
	rel := qualified(schema, table)
	var def string
	err := q.QueryRow(ctx,
		`SELECT pg_get_constraintdef(con.oid)
		 FROM pg_constraint con
		 WHERE con.conrelid = to_regclass($1) AND con.conname = $2`,
		rel, constraint).Scan(&def)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("catalog: probe constraint %s on %s: %w", constraint, rel, err)
	}
	return def, true, nil
}

// IndexInfo is an index's precondition-relevant state.
type IndexInfo struct {
	Def   string // pg_get_indexdef
	Valid bool   // pg_index.indisvalid — false after an interrupted CREATE INDEX CONCURRENTLY
}

// Index returns an index's definition and validity, or (nil, false) when absent.
// Valid=false is the recoverable state the create-index resume protocol keys on
// (roadmap L8): an index present but invalid must be DROP-rebuilt, not skipped.
func Index(ctx context.Context, q Querier, schema, name string) (*IndexInfo, bool, error) {
	rel := qualified(schema, name)
	var info IndexInfo
	err := q.QueryRow(ctx,
		`SELECT pg_get_indexdef(i.indexrelid), i.indisvalid
		 FROM pg_index i
		 WHERE i.indexrelid = to_regclass($1)`,
		rel).Scan(&info.Def, &info.Valid)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("catalog: probe index %s: %w", rel, err)
	}
	return &info, true, nil
}

// EnumHasValue reports whether the enum type has the given label.
func EnumHasValue(ctx context.Context, q Querier, schema, name, value string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx,
		`SELECT true FROM pg_enum e
		 WHERE e.enumtypid = to_regtype($1) AND e.enumlabel = $2`,
		qualified(schema, name), value).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe enum value %s on %s: %w", value, qualified(schema, name), err)
	}
	return ok, nil
}

// TriggerExists reports whether a (non-internal) trigger of the given name exists
// on the table.
func TriggerExists(ctx context.Context, q Querier, schema, table, trigger string) (bool, error) {
	rel := qualified(schema, table)
	var ok bool
	err := q.QueryRow(ctx,
		`SELECT true FROM pg_trigger tr
		 WHERE tr.tgrelid = to_regclass($1) AND tr.tgname = $2 AND NOT tr.tgisinternal`,
		rel, trigger).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe trigger %s on %s: %w", trigger, rel, err)
	}
	return ok, nil
}

// PolicyExists reports whether an RLS policy of the given name exists on the table.
func PolicyExists(ctx context.Context, q Querier, schema, table, policy string) (bool, error) {
	rel := qualified(schema, table)
	var ok bool
	err := q.QueryRow(ctx,
		`SELECT true FROM pg_policy p
		 WHERE p.polrelid = to_regclass($1) AND p.polname = $2`,
		rel, policy).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe policy %s on %s: %w", policy, rel, err)
	}
	return ok, nil
}

// ExtensionExists reports whether an extension is installed.
func ExtensionExists(ctx context.Context, q Querier, name string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx,
		"SELECT true FROM pg_extension WHERE extname = $1", name).Scan(&ok)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("catalog: probe extension %s: %w", name, err)
	}
	return ok, nil
}
