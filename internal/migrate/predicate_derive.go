package migrate

// Precondition derivation (roadmap 5.5+5.7): SelfContainedOp -> predicate IR.
//
// Each op class states a domain check about the world BEFORE it executes: creates
// require their object ABSENT (re-creating an existing object is drift); drops and
// alters require their object PRESENT. DML and opaque-SQL ops are precondition-FREE
// (arbitrary SQL has no catalog precondition). The IR lives in internal/predicate;
// this derivation lives in migrate to avoid an import cycle (predicate is a leaf).
//
// Present-AND-matching refinements (attribute-level preconditions) are derived
// from the FROM-MANIFEST: for alters/drops of a nested object, the expected
// pre-state is the object as it stood in the edge's PARENT revision (the model
// reconstructed from that revision's manifest). The per-class strategy is the
// matching-strategy resolution's: column TYPE by OID (typeinfo.Reconstruct text
// probed via to_regtype), column NOT NULL / DEFAULT / CHECK def compared by the
// predicate backends' round-trip. Creates stay absence-checks; genesis edges (and
// any op whose owning object is absent from the from-model) fall to existence-only.
//
// The definitional-body classes limited to CHECK constraints here: FK / UNIQUE /
// EXCLUSION defs and index defs stay existence-only (documented) — their clause
// rendering / index-name embedding is out of scope for this pass.

import (
	"strings"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/predicate"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// preconditions returns the domain-check preconditions for an op (usually zero or
// one). from is the reconstructed PARENT-revision model (nil for a genesis edge or
// when unavailable); it supplies the expected pre-state for attribute-level
// matches. An unrecognized op is precondition-free (nil), which never false-errors.
func (o SelfContainedOp) preconditions(store *objstore.Store, from *model.Schema) ([]predicate.Precondition, error) {
	cat, ok := categoryForKind(o.kind)
	if !ok {
		return nil, nil
	}
	switch cat {
	case catPseudo, catSchemaMeta, catManifestNoop:
		// DML / raw / refresh: no catalog precondition.
		return nil, nil

	case catWholeCreate:
		switch o.kind {
		case "create_or_replace_view", "create_or_replace_function":
			// Idempotent by construction — no absence precondition.
			return nil, nil
		case "alter_sequence":
			return []predicate.Precondition{{
				Existence: predicate.MustBePresent, Class: predicate.ClassSequence,
				Schema: o.target.Schema, Name: o.target.Name,
			}}, nil
		}
		cls, ok := classForKind(o.target.Kind)
		if !ok {
			return nil, nil
		}
		return []predicate.Precondition{{
			Existence: predicate.MustBeAbsent, Class: cls,
			Schema: o.target.Schema, Name: o.target.Name, ArgSig: o.target.ArgSig,
		}}, nil

	case catWholeDrop:
		cls, ok := classForKind(o.target.Kind)
		if !ok {
			return nil, nil
		}
		return []predicate.Precondition{{
			Existence: predicate.MustBePresent, Class: cls,
			Schema: o.target.Schema, Name: o.target.Name, ArgSig: o.target.ArgSig,
		}}, nil

	case catRenameTable:
		// The pre-rename table must be present. OldTable carries its qualified name.
		body, err := loadBody(store, o.payload)
		if err != nil {
			return nil, err
		}
		schema, name := splitQualifiedName(body.OldTable)
		return []predicate.Precondition{{
			Existence: predicate.MustBePresent, Class: predicate.ClassTable,
			Schema: schema, Name: name,
		}}, nil

	case catNestedModifier:
		body, err := loadBody(store, o.payload)
		if err != nil {
			return nil, err
		}
		if body.Delta != nil {
			return deltaPreconditions(*body.Delta, from), nil
		}
		// create_trigger / create_policy carry Name/Table (no Delta): object absent.
		switch o.kind {
		case "create_trigger":
			schema, table := splitQualifiedName(body.Table)
			return []predicate.Precondition{{
				Existence: predicate.MustBeAbsent, Class: predicate.ClassTrigger,
				Schema: schema, Table: table, Name: body.Name,
			}}, nil
		case "create_policy":
			schema, table := splitQualifiedName(body.Table)
			return []predicate.Precondition{{
				Existence: predicate.MustBeAbsent, Class: predicate.ClassPolicy,
				Schema: schema, Table: table, Name: body.Name,
			}}, nil
		}
		return nil, nil

	default:
		return nil, nil
	}
}

// deltaPreconditions derives the precondition(s) for a nested-modifier op from its
// scalar DDLOp delta, refining present-checks with attribute-level matches drawn
// from the parent-revision model (from).
func deltaPreconditions(d DDLOp, from *model.Schema) []predicate.Precondition {
	schema, table := splitQualifiedName(d.Table)
	col := func(name string, ex predicate.Existence) []predicate.Precondition {
		return []predicate.Precondition{{Existence: ex, Class: predicate.ClassColumn, Schema: schema, Table: table, Name: name, PGVersion: d.PGVersion}}
	}
	// colMatch builds a MustBePresent column precondition carrying the given Match
	// (nil-safe: a nil match yields existence-only).
	colMatch := func(name string, m *predicate.Match) []predicate.Precondition {
		return []predicate.Precondition{{Existence: predicate.MustBePresent, Class: predicate.ClassColumn, Schema: schema, Table: table, Name: name, PGVersion: d.PGVersion, Match: m}}
	}
	constraint := func(ex predicate.Existence) []predicate.Precondition {
		return []predicate.Precondition{{Existence: ex, Class: predicate.ClassConstraint, Schema: schema, Table: table, Name: d.Name}}
	}
	index := func(ex predicate.Existence) []predicate.Precondition {
		return []predicate.Precondition{{Existence: ex, Class: predicate.ClassIndex, Schema: schema, Name: d.Name}}
	}
	present := func(cls predicate.Class, name string) []predicate.Precondition {
		return []predicate.Precondition{{Existence: predicate.MustBePresent, Class: cls, Schema: schema, Table: table, Name: name}}
	}
	pc := parentColumn(from, schema, table, d.Column)

	switch d.Op {
	// Column adds: column must be absent.
	case "add_column":
		return col(d.Column, predicate.MustBeAbsent)

	// Column type alter / drop / rename: pre-state is the parent column's TYPE
	// (OID-probed). For alter_column_type the delta carries the NEW type — only the
	// parent model records the OLD type the live column must currently have.
	case "alter_column_type", "drop_column", "rename_column":
		return colMatch(d.Column, columnTypeMatch(pc))

	// NOT NULL toggles: pre-state is the parent column's nullability.
	case "set_not_null", "drop_not_null":
		return colMatch(d.Column, columnNotNullMatch(pc))

	// Default set/drop: pre-state is the parent column's DEFAULT (round-tripped).
	case "alter_column_default", "drop_column_default":
		return colMatch(d.Column, columnDefaultMatch(pc))

	// Statistics: presence only (no attribute body).
	case "set_statistics":
		return col(d.Column, predicate.MustBePresent)

	// Constraint adds: absent.
	case "add_fk", "add_fk_not_valid", "add_unique", "add_check", "add_exclusion":
		return constraint(predicate.MustBeAbsent)
	// CHECK drop: pre-state is the parent CHECK's definition (round-tripped).
	case "drop_check":
		if m := checkDefMatch(from, schema, table, d.Name); m != nil {
			return []predicate.Precondition{{Existence: predicate.MustBePresent, Class: predicate.ClassConstraint, Schema: schema, Table: table, Name: d.Name, Match: m}}
		}
		return constraint(predicate.MustBePresent)
	// Other constraint present (def not matched — FK/UNIQUE/EXCLUSION clause
	// rendering and validate_constraint pre-state are existence-only for now).
	case "drop_fk", "drop_unique", "drop_exclusion", "validate_constraint":
		return constraint(predicate.MustBePresent)

	// Index adds: absent.
	case "create_index", "add_index", "create_index_concurrently":
		return index(predicate.MustBeAbsent)
	// Index present.
	case "drop_index", "drop_index_concurrently", "alter_index_set":
		return index(predicate.MustBePresent)

	// Enum add value: the enum must be present (ADD VALUE IF NOT EXISTS is idempotent).
	case "alter_enum_add_value":
		return []predicate.Precondition{{Existence: predicate.MustBePresent, Class: predicate.ClassEnum, Schema: d.Schema, Name: d.Name}}
	// Domain modifiers: the domain must be present.
	case "alter_domain_add_constraint", "alter_domain_drop_constraint",
		"alter_domain_set_default", "alter_domain_drop_default",
		"alter_domain_set_not_null", "alter_domain_drop_not_null":
		return []predicate.Precondition{{Existence: predicate.MustBePresent, Class: predicate.ClassDomain, Schema: d.Schema, Name: d.Name}}

	// Table-scoped RLS / owner: the table must be present.
	case "enable_rls", "disable_rls", "force_rls", "no_force_rls", "set_owner":
		return present(predicate.ClassTable, table)

	// Trigger / policy drops: present.
	case "drop_trigger":
		return present(predicate.ClassTrigger, d.Name)
	case "drop_policy":
		return present(predicate.ClassPolicy, d.Name)

	default:
		return nil
	}
}

// parentColumn looks up a column in the parent-revision model, returning nil when
// the model is absent (genesis / unavailable) or the table/column is not found —
// the caller then falls to an existence-only precondition.
func parentColumn(from *model.Schema, schema, table, column string) *model.Column {
	if from == nil || column == "" {
		return nil
	}
	t := from.TableByName(schema, table)
	if t == nil {
		return nil
	}
	for i := range t.Columns {
		if t.Columns[i].Name == column {
			return &t.Columns[i]
		}
	}
	return nil
}

// columnTypeReconstruct renders the parent column's type as SQL text for the OID
// probe (array suffix appended when Reconstruct omits it).
func columnTypeReconstruct(c *model.Column) string {
	txt := typeinfo.Reconstruct(c.PGType)
	if c.Array && txt != "" && !strings.HasSuffix(txt, "[]") {
		txt += "[]"
	}
	return txt
}

// columnTypeMatch builds a TYPE match from the parent column (nil when unavailable
// or the type does not reconstruct — existence-only fallback).
func columnTypeMatch(c *model.Column) *predicate.Match {
	if c == nil {
		return nil
	}
	txt := columnTypeReconstruct(c)
	if txt == "" {
		return nil
	}
	return &predicate.Match{ColumnType: txt}
}

// columnNotNullMatch builds a NOT NULL match from the parent column.
func columnNotNullMatch(c *model.Column) *predicate.Match {
	if c == nil {
		return nil
	}
	nn := c.NotNull
	return &predicate.Match{ColumnNotNull: &nn}
}

// columnDefaultMatch builds a DEFAULT match from the parent column: the recorded
// default text, or "" (assert no default) when the parent had none.
func columnDefaultMatch(c *model.Column) *predicate.Match {
	if c == nil {
		return nil
	}
	def := ""
	if c.Default != nil {
		def = *c.Default
	}
	return &predicate.Match{ColumnDefault: &def}
}

// checkDefMatch builds a CHECK-constraint definition match from the parent table's
// recorded CHECK by name (nil when unavailable — existence-only fallback). The
// clause "CHECK (<expr>)" is round-tripped by the predicate backends.
func checkDefMatch(from *model.Schema, schema, table, name string) *predicate.Match {
	if from == nil || name == "" {
		return nil
	}
	t := from.TableByName(schema, table)
	if t == nil {
		return nil
	}
	for i := range t.Checks {
		if t.Checks[i].Name == name && strings.TrimSpace(t.Checks[i].Expr) != "" {
			return &predicate.Match{ConstraintDef: "CHECK (" + t.Checks[i].Expr + ")"}
		}
	}
	return nil
}

// classForKind maps an enc.Kind (whole-object target) to a predicate class.
func classForKind(k enc.Kind) (predicate.Class, bool) {
	switch k {
	case enc.KindTable:
		return predicate.ClassTable, true
	case enc.KindView:
		return predicate.ClassView, true
	case enc.KindMatView:
		return predicate.ClassMatView, true
	case enc.KindSequence:
		return predicate.ClassSequence, true
	case enc.KindEnum:
		return predicate.ClassEnum, true
	case enc.KindDomain:
		return predicate.ClassDomain, true
	case enc.KindComposite:
		return predicate.ClassComposite, true
	case enc.KindFunction:
		return predicate.ClassFunction, true
	default:
		return "", false
	}
}
