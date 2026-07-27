package predicate

import (
	"fmt"
	"strings"
)

// RenderIdempotentCreate wraps a raw, NON-idempotent create statement (createSQL,
// terminated by ';') in a DO block that implements create-if-absent-OR-verify
// semantics — the single idempotent-create renderer that generate --idempotent
// routes its non-schema/extension creates through (roadmap 5.5+5.7). It is the
// SQL-side fold of the same predicate IR RenderAssert compiles, so the two share
// the existence probes and the definitional-body round-trip.
//
// Three shapes, chosen by class per the recorded inventory below:
//
//   - DEFINITIONAL-BODY (round-trip) classes — a CHECK/FK/UNIQUE/EXCLUDE
//     constraint clause (Match.ConstraintDef) or a non-empty column default
//     (Match.ColumnDefault): create when absent; when present, canonicalize the
//     MODEL text through a throwaway temp object and RAISE (naming object /
//     expected / found) if PG's own pg_get_* form differs. This is the class that
//     makes re-applying idempotent DDL fail LOUDLY on definition drift instead of
//     silently skipping.
//   - BOOLEAN-MATCH classes — a Match with only pure-catalog dimensions (column
//     TYPE via to_regtype OID probe, NOT NULL, index validity): create when
//     absent, RAISE on drift via a single ELSIF guard.
//   - EXISTENCE-ONLY classes — a nil Match (no sound live match strategy that a
//     clean round-trip can reach): create when absent, otherwise NO-OP. These
//     DEGRADE to create-if-absent by design and NEVER RAISE a false mismatch. The
//     per-class rationale is documented in idempotentInventory below so the
//     silence is deliberate and auditable, never accidental.
//
// PER-CLASS IDEMPOTENT INVENTORY (as shipped):
//
//	CONSTRAINT (CHECK/FK/UNIQUE/EXCLUDE) — round-trip RAISE on definition drift.
//	    pg_get_constraintdef canonicalizes a temp-applied clone of the MODEL
//	    clause; alias-spelled equivalents converge, genuine differences RAISE.
//	COLUMN default                       — round-trip RAISE on default drift.
//	    pg_get_expr over a temp SET DEFAULT; same convergence guarantee. (Not
//	    emitted by generate today — generate folds defaults into ADD COLUMN
//	    IF NOT EXISTS — but the primitive supports it for the apply-loop executor.)
//	COLUMN type / NOT NULL               — boolean-match ELSIF RAISE (OID probe).
//	INDEX                                — EXISTENCE-ONLY. pg_get_indexdef embeds
//	    the index name and owning table, which differ for a throwaway temp object,
//	    so a clean round-trip cannot reach the definition body. Create-if-absent.
//	ENUM / DOMAIN / COMPOSITE            — EXISTENCE-ONLY. A type's body (labels,
//	    CHECK, fields) has no LIKE-cloneable temp carrier the round-trip can use,
//	    and CREATE TYPE has no IF NOT EXISTS; create-if-absent via a pg_type probe.
//	TABLE / VIEW / MATVIEW / SEQUENCE    — EXISTENCE-ONLY. Whole-relation bodies
//	    are out of round-trip reach (self-referential names); create-if-absent
//	    (native IF NOT EXISTS / CREATE OR REPLACE handles these upstream, so
//	    generate does not route them here).
//	FUNCTION                             — CREATE OR REPLACE upstream (natively
//	    idempotent, definition-updating); not routed here.
//	POLICY / TRIGGER                     — EXISTENCE-ONLY. Create-if-absent.
//	SCHEMA / EXTENSION                   — native IF NOT EXISTS upstream; never
//	    routed here (no definitional body).
func RenderIdempotentCreate(p Precondition, createSQL string) string {
	exists := existsExpr(p)
	if idempotentNeedsRoundTrip(p) {
		return renderIdempotentRoundTrip(p, exists, createSQL)
	}
	if p.Match != nil {
		if m := matchExpr(p); m != "true" {
			mismatch := escapeLit(fmt.Sprintf(
				"pgdesign idempotent create: %s already exists but does not match the desired definition", p.object()))
			return fmt.Sprintf(`DO $pgdidem$
BEGIN
    IF NOT (%s) THEN
        %s
    ELSIF NOT (%s) THEN
        RAISE EXCEPTION '%s';
    END IF;
END
$pgdidem$;`, exists, createSQL, m, mismatch)
		}
	}
	return fmt.Sprintf(`DO $pgdidem$
BEGIN
    IF NOT (%s) THEN
        %s
    END IF;
END
$pgdidem$;`, exists, createSQL)
}

// idempotentNeedsRoundTrip reports whether an idempotent create must round-trip a
// definitional body to detect drift. Unlike Precondition.needsRoundTrip it does not
// gate on Existence: RenderIdempotentCreate ignores Existence entirely (it renders
// its own create-vs-verify control flow), keying only on the Match body fields.
func idempotentNeedsRoundTrip(p Precondition) bool {
	if p.Match == nil {
		return false
	}
	switch p.Class {
	case ClassConstraint:
		return p.Match.ConstraintDef != ""
	case ClassColumn:
		return p.Match.ColumnDefault != nil && *p.Match.ColumnDefault != ""
	default:
		return false
	}
}

// renderIdempotentRoundTrip emits the DECLARE-block create-or-verify DO for a
// definitional-body class: create when absent, else round-trip the MODEL body and
// RAISE on drift. The compare block is the exact same text RenderAssert uses, nested
// one level deeper (inside the ELSE branch).
func renderIdempotentRoundTrip(p Precondition, exists, createSQL string) string {
	var compare string
	switch p.Class {
	case ClassConstraint:
		compare = constraintCompareBlock(p, "        ")
	case ClassColumn:
		compare = defaultCompareBlock(p, "        ")
	default:
		// Unreachable: idempotentNeedsRoundTrip only returns true for the two
		// classes above.
		compare = "        NULL;"
	}
	return fmt.Sprintf(`DO $pgdidem$
DECLARE
    found_def text;
    expected_def text;
BEGIN
    IF NOT (%s) THEN
        %s
    ELSE
%s
    END IF;
END
$pgdidem$;`, exists, strings.TrimSpace(createSQL), compare)
}
