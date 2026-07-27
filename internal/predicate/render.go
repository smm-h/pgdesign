package predicate

import (
	"fmt"
	"strings"
)

// RenderAssert compiles a precondition into a DO block that RAISEs EXCEPTION when
// the precondition is VIOLATED (the SQL backend of the same IR). It computes a
// boolean `ok` with the same meaning as the Go executor's Result.OK and raises
// when it is false. This is the RAISE-on-mismatch primitive generate --idempotent
// builds on, and the second computation of the predicate the conformance matrix
// pins against the Go executor.
func RenderAssert(p Precondition) string {
	okExpr := okExpr(p)
	msg := fmt.Sprintf("pgdesign precondition violated: %s (expected %s)", p.object(), p.Existence.String())
	return fmt.Sprintf(`DO $pgdpred$
BEGIN
    IF NOT (%s) THEN
        RAISE EXCEPTION '%s';
    END IF;
END
$pgdpred$;`, okExpr, escapeLit(msg))
}

// okExpr builds the boolean SQL expression that is true iff the precondition holds.
func okExpr(p Precondition) string {
	exists := existsExpr(p)
	switch p.Existence {
	case MustBeAbsent:
		return "NOT (" + exists + ")"
	case MustBePresent:
		if p.Match == nil {
			return exists
		}
		return "(" + exists + ") AND (" + matchExpr(p) + ")"
	default:
		return "false"
	}
}

// existsExpr builds a boolean SQL expression: does the object exist? It mirrors
// the internal/catalog probes exactly (same relkind/typtype filters), so the SQL
// and Go backends see the same objects as present.
func existsExpr(p Precondition) string {
	rel := sqlLit(qualIdent(p.Schema, p.Name))
	relTable := sqlLit(qualIdent(p.Schema, p.Table))
	switch p.Class {
	case ClassTable:
		return fmt.Sprintf("(SELECT c.relkind IN ('r','p') FROM pg_class c WHERE c.oid = to_regclass(%s)) IS TRUE", rel)
	case ClassView:
		return fmt.Sprintf("(SELECT c.relkind = 'v' FROM pg_class c WHERE c.oid = to_regclass(%s)) IS TRUE", rel)
	case ClassMatView:
		return fmt.Sprintf("(SELECT c.relkind = 'm' FROM pg_class c WHERE c.oid = to_regclass(%s)) IS TRUE", rel)
	case ClassSequence:
		return fmt.Sprintf("(SELECT c.relkind = 'S' FROM pg_class c WHERE c.oid = to_regclass(%s)) IS TRUE", rel)
	case ClassEnum:
		return fmt.Sprintf("(SELECT t.typtype = 'e' FROM pg_type t WHERE t.oid = to_regtype(%s)) IS TRUE", rel)
	case ClassDomain:
		return fmt.Sprintf("(SELECT t.typtype = 'd' FROM pg_type t WHERE t.oid = to_regtype(%s)) IS TRUE", rel)
	case ClassComposite:
		return fmt.Sprintf("(SELECT t.typtype = 'c' FROM pg_type t WHERE t.oid = to_regtype(%s)) IS TRUE", rel)
	case ClassFunction:
		return fmt.Sprintf("to_regprocedure(%s) IS NOT NULL", sqlLit(qualIdent(p.Schema, p.Name)+p.ArgSig))
	case ClassExtension:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_extension WHERE extname = %s)", sqlLit(p.Name))
	case ClassEnumValue:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_enum e WHERE e.enumtypid = to_regtype(%s) AND e.enumlabel = %s)",
			rel, sqlLit(p.Value))
	case ClassTrigger:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_trigger tr WHERE tr.tgrelid = to_regclass(%s) AND tr.tgname = %s AND NOT tr.tgisinternal)",
			relTable, sqlLit(p.Name))
	case ClassPolicy:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_policy pol WHERE pol.polrelid = to_regclass(%s) AND pol.polname = %s)",
			relTable, sqlLit(p.Name))
	case ClassColumn:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped)",
			relTable, sqlLit(p.Name))
	case ClassConstraint:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conrelid = to_regclass(%s) AND con.conname = %s)",
			relTable, sqlLit(p.Name))
	case ClassIndex:
		return fmt.Sprintf("(SELECT true FROM pg_index i WHERE i.indexrelid = to_regclass(%s)) IS TRUE", rel)
	default:
		return "false"
	}
}

// matchExpr builds a boolean SQL expression: does the present object match
// p.Match? It uses EXACT equality on the catalog's own rendering, mirroring the
// Go executor's comparison.
func matchExpr(p Precondition) string {
	m := p.Match
	relTable := sqlLit(qualIdent(p.Schema, p.Table))
	rel := sqlLit(qualIdent(p.Schema, p.Name))
	switch p.Class {
	case ClassColumn:
		var conds []string
		if m.ColumnType != "" {
			conds = append(conds, fmt.Sprintf(
				"(SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped) = %s",
				relTable, sqlLit(p.Name), sqlLit(m.ColumnType)))
		}
		if m.ColumnNotNull != nil {
			conds = append(conds, fmt.Sprintf(
				"(SELECT a.attnotnull FROM pg_attribute a WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped) = %t",
				relTable, sqlLit(p.Name), *m.ColumnNotNull))
		}
		if m.ColumnDefault != nil {
			conds = append(conds, fmt.Sprintf(
				"(SELECT COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') FROM pg_attribute a LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped) = %s",
				relTable, sqlLit(p.Name), sqlLit(*m.ColumnDefault)))
		}
		if len(conds) == 0 {
			return "true"
		}
		return "(" + strings.Join(conds, ") AND (") + ")"
	case ClassConstraint:
		if m.ConstraintDef == "" {
			return "true"
		}
		return fmt.Sprintf(
			"(SELECT pg_get_constraintdef(con.oid) FROM pg_constraint con WHERE con.conrelid = to_regclass(%s) AND con.conname = %s) = %s",
			relTable, sqlLit(p.Name), sqlLit(m.ConstraintDef))
	case ClassIndex:
		var conds []string
		if m.IndexMustBeValid {
			conds = append(conds, fmt.Sprintf(
				"(SELECT i.indisvalid FROM pg_index i WHERE i.indexrelid = to_regclass(%s)) IS TRUE", rel))
		}
		if m.IndexDef != "" {
			conds = append(conds, fmt.Sprintf(
				"pg_get_indexdef(to_regclass(%s)) = %s", rel, sqlLit(m.IndexDef)))
		}
		if len(conds) == 0 {
			return "true"
		}
		return "(" + strings.Join(conds, ") AND (") + ")"
	default:
		return "true"
	}
}

// qualIdent renders a double-quoted qualified identifier (for embedding in a
// to_regclass/to_regtype string literal). Empty schema yields the bare quoted name.
func qualIdent(schema, name string) string {
	if schema == "" {
		return quoteIdent(name)
	}
	return quoteIdent(schema) + "." + quoteIdent(name)
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sqlLit renders a single-quoted SQL string literal with embedded quotes escaped.
func sqlLit(s string) string {
	return "'" + escapeLit(s) + "'"
}

// escapeLit escapes embedded single quotes WITHOUT adding surrounding quotes (for
// interpolation into a context that already supplies them, e.g. a RAISE message).
func escapeLit(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
