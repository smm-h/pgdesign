// Package predicate is the migration PRECONDITION IR (roadmap 5.5+5.7, law L5's
// domain check / L1's single ≈_syn / L8).
//
// A Precondition is a structured, per-op-class statement about the world BEFORE
// an op executes: "this object must be ABSENT" (for creates) or "this object must
// be PRESENT" — optionally "present AND matching" a recorded expectation (for
// alters/drops). DML and opaque-SQL ops are precondition-FREE: arbitrary SQL has
// no catalog precondition.
//
// The IR has TWO BACKENDS that must agree (the conformance obligation):
//
//   - the Go EXECUTOR (Check), consuming the shared internal/catalog layer and
//     returning STRUCTURED object/expected/found diagnostics — it exists for those
//     diagnostics, not for DB-freedom;
//   - the SQL RENDERER (RenderAssert), compiling the SAME structures into a DO
//     block that RAISEs on a violated precondition.
//
// The predicate is a second computation of ≈_syn in another language (SQL), which
// is why the two backends are conformance-matrixed: identical verdicts against
// live states.
package predicate

// Existence is the presence half of a precondition.
type Existence int

const (
	// MustBeAbsent: the object must NOT exist (a create's domain check —
	// re-creating an existing object is drift). Match is ignored.
	MustBeAbsent Existence = iota
	// MustBePresent: the object must exist (a drop/alter's domain check). When
	// Match is set, the present object must additionally match it.
	MustBePresent
)

func (e Existence) String() string {
	if e == MustBeAbsent {
		return "absent"
	}
	return "present"
}

// Class is the catalog object class a precondition targets.
type Class string

const (
	ClassTable      Class = "table"
	ClassColumn     Class = "column"
	ClassConstraint Class = "constraint"
	ClassIndex      Class = "index"
	ClassView       Class = "view"
	ClassMatView    Class = "matview"
	ClassSequence   Class = "sequence"
	ClassEnum       Class = "enum"
	ClassEnumValue  Class = "enum_value"
	ClassDomain     Class = "domain"
	ClassComposite  Class = "composite"
	ClassFunction   Class = "function"
	ClassTrigger    Class = "trigger"
	ClassPolicy     Class = "policy"
	ClassExtension  Class = "extension"
)

// Match, when non-nil on a MustBePresent precondition, additionally requires the
// present object's attribute(s) to equal these (present-AND-matching). Only the
// fields relevant to the class are consulted. Nil means existence-only.
//
// Present-and-matching comparisons are LIVE-PATH comparisons (roadmap 5.5+5.7
// matching-strategy resolution): they run against the target DB, so the fields
// carry MODEL text (alias-spelled, non-canonical) and each is compared with a
// strategy that is robust to equivalent spellings:
//
//   - ColumnType    — OID probe via to_regtype (alias-robust, pure-computable);
//   - ColumnNotNull — boolean;
//   - ColumnDefault — in-DB round-trip (temp column SET DEFAULT, pg_get_expr);
//   - ConstraintDef — in-DB round-trip (temp table ADD CONSTRAINT, pg_get_constraintdef);
//   - IndexMustBeValid — boolean (pg_index.indisvalid).
//
// Index DEFINITION bodies are NOT compared (existence + validity only): a clean
// round-trip cannot reach them because pg_get_indexdef embeds the index name and
// owning table, which differ for the throwaway temp object. This is the documented
// existence-only class for indexes.
type Match struct {
	// Column attributes (ClassColumn).
	ColumnType    string  // expected type (model text); OID probe. "" = don't check
	ColumnNotNull *bool   // expected NOT NULL; boolean. nil = don't check
	ColumnDefault *string // expected default expr (model text); round-trip. nil = don't check

	// ConstraintDef is the expected constraint clause (model text, e.g.
	// "CHECK (age >= 0)"); compared by round-trip against pg_get_constraintdef.
	ConstraintDef string // "" = don't check

	// IndexMustBeValid requires pg_index.indisvalid (ClassIndex) — the resume
	// protocol keys on this: a present-but-INVALID index (interrupted CIC) is a
	// mismatch, not a match.
	IndexMustBeValid bool
}

// needsRoundTrip reports whether the precondition's Match requires an in-DB
// round-trip (definitional body: a non-empty constraint clause or a non-empty
// column default). Existence, OID-type, not-null, and index-validity checks are
// pure catalog reads and never need the temp-object round-trip.
func (p Precondition) needsRoundTrip() bool {
	if p.Existence != MustBePresent || p.Match == nil {
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

// Precondition is one structured domain-check statement for an op.
type Precondition struct {
	Existence Existence
	Class     Class
	Schema    string // object schema ("" = search_path / public)
	Table     string // owning table for column/constraint/trigger/policy
	Name      string // object name, or column / enum-value / constraint name
	ArgSig    string // function argument signature, e.g. "(integer,text)"
	Value     string // enum label for ClassEnumValue
	PGVersion int    // for version-gated catalog probes (column generated flag)
	Match     *Match // present-and-matching expectation (nil = existence-only)
}

// object renders a human-readable object identity for diagnostics.
func (p Precondition) object() string {
	switch p.Class {
	case ClassColumn:
		return "column " + qual(p.Schema, p.Table) + "." + p.Name
	case ClassConstraint:
		return "constraint " + p.Name + " on " + qual(p.Schema, p.Table)
	case ClassTrigger:
		return "trigger " + p.Name + " on " + qual(p.Schema, p.Table)
	case ClassPolicy:
		return "policy " + p.Name + " on " + qual(p.Schema, p.Table)
	case ClassEnumValue:
		return "enum value " + p.Value + " of " + string(ClassEnum) + " " + qual(p.Schema, p.Name)
	case ClassFunction:
		return string(p.Class) + " " + qual(p.Schema, p.Name) + p.ArgSig
	case ClassExtension:
		return "extension " + p.Name
	default:
		return string(p.Class) + " " + qual(p.Schema, p.Name)
	}
}

func qual(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}
