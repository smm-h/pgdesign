package predicate

import (
	"fmt"
	"strings"
)

// RenderAssert compiles a precondition into a DO block that RAISEs EXCEPTION when
// the precondition is VIOLATED (the SQL backend of the same IR). This is the
// RAISE-on-mismatch primitive generate --idempotent builds on, and the second
// computation of the predicate the conformance matrix pins against the Go executor.
//
// Two shapes:
//
//   - existence / OID-type / not-null / index-validity are pure boolean catalog
//     reads → a single `IF NOT (<ok>) THEN RAISE` guard;
//   - definitional bodies (constraint def, non-empty column default) need the in-DB
//     round-trip → a DECLARE block that canonicalizes the MODEL text through a
//     throwaway temp object and compares PG's own pg_get_* form to the live one.
//
// Both compute the SAME verdict as the Go executor, keeping the conformance matrix
// honest.
func RenderAssert(p Precondition) string {
	if p.needsRoundTrip() {
		return renderRoundTripAssert(p)
	}
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

// renderRoundTripAssert emits the DECLARE-block DO for a definitional-body
// precondition: it first enforces the boolean guard (existence + any OID-type /
// not-null dimensions), then round-trips the MODEL text and compares.
func renderRoundTripAssert(p Precondition) string {
	presentMsg := escapeLit(fmt.Sprintf("pgdesign precondition violated: %s (expected present)", p.object()))
	guard := okExpr(p) // existence AND boolean match dimensions (round-trip dims excluded)
	switch p.Class {
	case ClassConstraint:
		return renderConstraintRoundTrip(p, guard, presentMsg)
	case ClassColumn:
		return renderDefaultRoundTrip(p, guard, presentMsg)
	default:
		// Unreachable: needsRoundTrip only returns true for the two classes above.
		okExpr := okExpr(p)
		return fmt.Sprintf("DO $pgdpred$\nBEGIN\n    IF NOT (%s) THEN\n        RAISE EXCEPTION '%s';\n    END IF;\nEND\n$pgdpred$;", okExpr, presentMsg)
	}
}

// renderConstraintRoundTrip clones the owning table into a temp table, adds the
// MODEL constraint clause, and compares PG's pg_get_constraintdef to the live one.
func renderConstraintRoundTrip(p Precondition, guard, presentMsg string) string {
	return fmt.Sprintf(`DO $pgdpred$
DECLARE
    found_def text;
    expected_def text;
BEGIN
    IF NOT (%s) THEN
        RAISE EXCEPTION '%s';
    END IF;
%s
END
$pgdpred$;`, guard, presentMsg, constraintCompareBlock(p, "    "))
}

// renderDefaultRoundTrip clones the owning table into a temp table, sets the MODEL
// default on the target column, and compares PG's pg_get_expr to the live one.
func renderDefaultRoundTrip(p Precondition, guard, presentMsg string) string {
	return fmt.Sprintf(`DO $pgdpred$
DECLARE
    found_def text;
    expected_def text;
BEGIN
    IF NOT (%s) THEN
        RAISE EXCEPTION '%s';
    END IF;
%s
END
$pgdpred$;`, guard, presentMsg, defaultCompareBlock(p, "    "))
}

// constraintCompareBlock renders the round-trip comparison statements for a
// constraint definitional body: read the live pg_get_constraintdef, canonicalize
// the MODEL clause through a throwaway temp table, and RAISE (naming object /
// expected / found) when they differ. The block assumes the owning table exists
// and the constraint is present (both callers guarantee this before entering it).
// indent is prepended to every line so the same block nests either at BEGIN level
// (RenderAssert) or inside an ELSE branch (RenderIdempotentCreate).
func constraintCompareBlock(p Precondition, indent string) string {
	src := qualIdent(p.Schema, p.Table)
	relTable := sqlLit(src)
	mismatchMsg := escapeLit(fmt.Sprintf("pgdesign precondition violated: %s (definition mismatch)", p.object()))
	lines := []string{
		"SELECT pg_get_constraintdef(con.oid) INTO found_def",
		"    FROM pg_constraint con",
		fmt.Sprintf("    WHERE con.conrelid = to_regclass(%s) AND con.conname = %s;", relTable, sqlLit(p.Name)),
		fmt.Sprintf("DROP TABLE IF EXISTS %s;", quoteIdent(tempName)),
		fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s);", quoteIdent(tempName), src),
		fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s;", quoteIdent(tempName), quoteIdent("_pgd_c"), p.Match.ConstraintDef),
		"SELECT pg_get_constraintdef(con.oid) INTO expected_def",
		"    FROM pg_constraint con",
		"    JOIN pg_class r ON r.oid = con.conrelid",
		fmt.Sprintf("    WHERE r.relname = %s AND con.conname = '_pgd_c' AND r.relnamespace = pg_my_temp_schema();", sqlLit(tempName)),
		fmt.Sprintf("DROP TABLE IF EXISTS %s;", quoteIdent(tempName)),
		"IF found_def IS DISTINCT FROM expected_def THEN",
		fmt.Sprintf("    RAISE EXCEPTION '%s: expected %%, found %%', expected_def, found_def;", mismatchMsg),
		"END IF;",
	}
	return indent + strings.Join(lines, "\n"+indent)
}

// defaultCompareBlock renders the round-trip comparison statements for a column
// default: read the live pg_get_expr, canonicalize the MODEL default through a
// throwaway temp table, and RAISE (naming object / expected / found) when they
// differ. See constraintCompareBlock for the indent contract.
func defaultCompareBlock(p Precondition, indent string) string {
	src := qualIdent(p.Schema, p.Table)
	relTable := sqlLit(src)
	mismatchMsg := escapeLit(fmt.Sprintf("pgdesign precondition violated: %s (default mismatch)", p.object()))
	lines := []string{
		"SELECT COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') INTO found_def",
		"    FROM pg_attribute a",
		"    LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum",
		fmt.Sprintf("    WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped;", relTable, sqlLit(p.Name)),
		fmt.Sprintf("DROP TABLE IF EXISTS %s;", quoteIdent(tempName)),
		fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s);", quoteIdent(tempName), src),
		fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;", quoteIdent(tempName), quoteIdent(p.Name), *p.Match.ColumnDefault),
		"SELECT COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') INTO expected_def",
		"    FROM pg_attribute a",
		"    JOIN pg_class r ON r.oid = a.attrelid",
		"    LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum",
		fmt.Sprintf("    WHERE r.relname = %s AND a.attname = %s AND r.relnamespace = pg_my_temp_schema();", sqlLit(tempName), sqlLit(p.Name)),
		fmt.Sprintf("DROP TABLE IF EXISTS %s;", quoteIdent(tempName)),
		"IF found_def IS DISTINCT FROM expected_def THEN",
		fmt.Sprintf("    RAISE EXCEPTION '%s: expected %%, found %%', expected_def, found_def;", mismatchMsg),
		"END IF;",
	}
	return indent + strings.Join(lines, "\n"+indent)
}

// okExpr builds the boolean SQL expression that is true iff the precondition's
// existence and boolean-match dimensions hold. Round-trip dimensions (constraint
// def, non-empty column default) are excluded — those are compared by the
// round-trip DO block, not here.
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

// matchExpr builds a boolean SQL expression for the BOOLEAN match dimensions only:
// column TYPE (OID probe via to_regtype — alias-robust), column NOT NULL, the
// "no default" case, and index VALIDITY. Definitional-body dimensions (constraint
// def, non-empty column default) return "true" here and are compared by the
// round-trip DO block instead.
func matchExpr(p Precondition) string {
	m := p.Match
	relTable := sqlLit(qualIdent(p.Schema, p.Table))
	rel := sqlLit(qualIdent(p.Schema, p.Name))
	switch p.Class {
	case ClassColumn:
		var conds []string
		if m.ColumnType != "" {
			// OID equality via to_regtype: int4 == integer, varchar == character
			// varying resolve to the same OID, so equivalent spellings do not drift.
			conds = append(conds, fmt.Sprintf(
				"(SELECT COALESCE(a.atttypid = to_regtype(%s)::oid, false) FROM pg_attribute a WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped) IS TRUE",
				sqlLit(m.ColumnType), relTable, sqlLit(p.Name)))
		}
		if m.ColumnNotNull != nil {
			conds = append(conds, fmt.Sprintf(
				"(SELECT a.attnotnull FROM pg_attribute a WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped) = %t",
				relTable, sqlLit(p.Name), *m.ColumnNotNull))
		}
		if m.ColumnDefault != nil && *m.ColumnDefault == "" {
			// Asserting NO default is a pure boolean read; a non-empty default is a
			// round-trip dimension handled elsewhere.
			conds = append(conds, fmt.Sprintf(
				"(SELECT COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') FROM pg_attribute a LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum WHERE a.attrelid = to_regclass(%s) AND a.attname = %s AND a.attnum > 0 AND NOT a.attisdropped) = ''",
				relTable, sqlLit(p.Name)))
		}
		if len(conds) == 0 {
			return "true"
		}
		return "(" + strings.Join(conds, ") AND (") + ")"
	case ClassIndex:
		if m.IndexMustBeValid {
			return fmt.Sprintf(
				"(SELECT i.indisvalid FROM pg_index i WHERE i.indexrelid = to_regclass(%s)) IS TRUE", rel)
		}
		return "true"
	default:
		// ClassConstraint def is a round-trip dimension; all other classes have no
		// match semantics.
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
