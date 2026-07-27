package migrate

// Precondition derivation (roadmap 5.5+5.7): SelfContainedOp -> predicate IR.
//
// Each op class states a domain check about the world BEFORE it executes: creates
// require their object ABSENT (re-creating an existing object is drift); drops and
// alters require their object PRESENT. DML and opaque-SQL ops are precondition-FREE
// (arbitrary SQL has no catalog precondition). The IR lives in internal/predicate;
// this derivation lives in migrate to avoid an import cycle (predicate is a leaf).
//
// Existence-only preconditions are derived here; present-AND-matching refinements
// (column type, constraint def) are a future tightening — the existence checks
// already close the "missing table / already-present object" drift class.

import (
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/predicate"
)

// preconditions returns the domain-check preconditions for an op (usually zero or
// one). An unrecognized op is precondition-free (nil), which never false-errors.
func (o SelfContainedOp) preconditions(store *objstore.Store) ([]predicate.Precondition, error) {
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
			return deltaPreconditions(*body.Delta), nil
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
// scalar DDLOp delta.
func deltaPreconditions(d DDLOp) []predicate.Precondition {
	schema, table := splitQualifiedName(d.Table)
	col := func(name string, ex predicate.Existence) []predicate.Precondition {
		return []predicate.Precondition{{Existence: ex, Class: predicate.ClassColumn, Schema: schema, Table: table, Name: name, PGVersion: d.PGVersion}}
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

	switch d.Op {
	// Column adds: column must be absent.
	case "add_column":
		return col(d.Column, predicate.MustBeAbsent)
	// Column present (alter/drop/statistics/owner-on-column).
	case "drop_column", "alter_column_type", "set_not_null", "drop_not_null",
		"alter_column_default", "drop_column_default", "set_statistics":
		return col(d.Column, predicate.MustBePresent)
	case "rename_column":
		// The pre-rename column (d.Column) must be present.
		return col(d.Column, predicate.MustBePresent)

	// Constraint adds: absent.
	case "add_fk", "add_fk_not_valid", "add_unique", "add_check", "add_exclusion":
		return constraint(predicate.MustBeAbsent)
	// Constraint present.
	case "drop_fk", "drop_unique", "drop_check", "drop_exclusion", "validate_constraint":
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
