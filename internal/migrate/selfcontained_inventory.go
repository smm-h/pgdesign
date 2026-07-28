package migrate

// Self-contained op inventory + classification (roadmap 5.1b — full op-kind
// coverage). The 5.1 layer made the THIRTEEN whole-object / RawSQL / DML
// families self-contained; 5.1b makes EVERY op kind generate.go emits (consumed
// by OpToSQL) self-contained, and makes endpoint simulation TOTAL by carrying,
// for a nested-modifier op, the OWNING object's POST-STATE def id alongside the
// render delta (edge_format.md's by-content-id rule; the amendment's adopted
// resolution).
//
// INVENTORY — every kind OpToSQL handles (sql_gen.go), plus the DML kinds and
// the three 5.1b-minted kinds (schema_meta + the two append-only-machinery
// kinds that replace the nil-def magic-name fallbacks). Columns: simulation
// CATEGORY and default L4 CLASS. The shim (selfcontained_shim.go) OVERRIDES the
// L4 class from the legacy op's recorded DownOp (Irreversible -> non-invertible;
// a real down -> declared-inverse), so the "class" here is the standalone-builder
// default; the category is authoritative for simulation.
//
//	KIND                          | CATEGORY        | RENDER  | L4 CLASS (default / shim rule)
//	------------------------------|-----------------|---------|-------------------------------
//	create_table                  | whole-create    | def     | mechanically-invertible (down drop_table)
//	create_partition              | whole-create    | def     | mechanically-invertible (down drop_table)
//	create_view                   | whole-create    | def     | mechanically-invertible (down drop_view)
//	create_or_replace_view        | whole-create    | def     | declared-inverse (down = prev view or drop_view)
//	create_materialized_view      | whole-create    | def     | mechanically-invertible (down drop_materialized_view)
//	create_sequence               | whole-create    | def     | mechanically-invertible (down drop_sequence)
//	alter_sequence                | whole-create    | def     | declared-inverse (down = prev params)
//	create_composite_type         | whole-create    | def     | mechanically-invertible (down drop_composite_type)
//	create_domain                 | whole-create    | def     | mechanically-invertible (down drop_domain)
//	create_function               | whole-create    | def     | mechanically-invertible (down drop_function)
//	create_or_replace_function    | whole-create    | def     | declared-inverse (down = prev fn or drop_function)
//	create_enum                   | whole-create    | def     | mechanically-invertible (down drop_enum)
//	drop_table                    | whole-drop      | delta   | declared-inverse (down create_table, STRUCTURE only)
//	drop_view                     | whole-drop      | delta   | declared-inverse (down create_view) / else non-invertible
//	drop_materialized_view        | whole-drop      | delta   | declared-inverse / non-invertible
//	drop_sequence                 | whole-drop      | delta   | declared-inverse / non-invertible
//	drop_composite_type           | whole-drop      | delta   | declared-inverse / non-invertible
//	drop_domain                   | whole-drop      | delta   | declared-inverse / non-invertible
//	drop_function                 | whole-drop      | delta   | declared-inverse / non-invertible
//	drop_enum                     | whole-drop      | delta   | non-invertible (enum values are not droppable)
//	rename_table                  | rename-table    | delta   | mechanically-invertible (down = swapped rename)
//	rename_column                 | nested-modifier | delta   | mechanically-invertible (down = swapped rename)
//	add_column                    | nested-modifier | delta   | declared-inverse (down drop_column; new col is empty)
//	drop_column                   | nested-modifier | delta   | declared-inverse (down add_column, STRUCTURE only) / non-invertible
//	alter_column_type             | nested-modifier | delta   | declared-inverse (down = prev type) / non-invertible
//	set_not_null                  | nested-modifier | delta   | declared-inverse (down drop_not_null)
//	drop_not_null                 | nested-modifier | delta   | declared-inverse (down set_not_null)
//	alter_column_default          | nested-modifier | delta   | declared-inverse (down = prev default)
//	drop_column_default           | nested-modifier | delta   | declared-inverse (down = prev default) / non-invertible
//	set_statistics                | nested-modifier | delta   | declared-inverse (down = prev statistics) / non-invertible
//	set_owner                     | nested-modifier | delta   | declared-inverse (down = prev owner) / non-invertible
//	add_fk                        | nested-modifier | delta   | declared-inverse (down drop_fk)
//	add_fk_not_valid              | nested-modifier | delta   | declared-inverse (down drop_fk)
//	drop_fk                       | nested-modifier | delta   | declared-inverse (down add_fk) / non-invertible
//	validate_constraint           | nested-modifier | delta   | declared-inverse (down re-validates: a no-op that runs before the paired NOT-VALID add's drop_fk on reverse-order rollback) [5.9: generation always splits FK adds]
//	create_index / add_index      | nested-modifier | delta   | declared-inverse (down drop_index)
//	drop_index                    | nested-modifier | delta   | declared-inverse (down create_index) / non-invertible
//	create_index_concurrently     | nested-modifier | delta   | declared-inverse (down drop_index_concurrently)
//	drop_index_concurrently       | nested-modifier | delta   | declared-inverse / non-invertible
//	alter_index_set               | nested-modifier | delta   | declared-inverse (down = prev storage params) / non-invertible
//	add_unique                    | nested-modifier | delta   | declared-inverse (down drop_unique)
//	drop_unique                   | nested-modifier | delta   | declared-inverse (down add_unique) / non-invertible
//	add_check                     | nested-modifier | delta   | declared-inverse (down drop_check)
//	drop_check                    | nested-modifier | delta   | declared-inverse (down add_check) / non-invertible
//	add_exclusion                 | nested-modifier | delta   | declared-inverse (down drop_exclusion)
//	drop_exclusion                | nested-modifier | delta   | declared-inverse (down add_exclusion) / non-invertible
//	alter_enum_add_value          | nested-modifier(enum)   | delta | non-invertible (enum values are not droppable)
//	alter_domain_add_constraint   | nested-modifier(domain) | delta | declared-inverse (down drop_constraint)
//	alter_domain_drop_constraint  | nested-modifier(domain) | delta | declared-inverse / non-invertible
//	alter_domain_set_default      | nested-modifier(domain) | delta | declared-inverse (down = prev)
//	alter_domain_drop_default     | nested-modifier(domain) | delta | declared-inverse / non-invertible
//	alter_domain_set_not_null     | nested-modifier(domain) | delta | declared-inverse (down drop_not_null)
//	alter_domain_drop_not_null    | nested-modifier(domain) | delta | declared-inverse (down set_not_null)
//	create_trigger                | nested-modifier | def     | mechanically-invertible (down drop_trigger)
//	drop_trigger                  | nested-modifier | delta   | declared-inverse (down create_trigger) / non-invertible
//	create_policy                 | nested-modifier | def     | mechanically-invertible (down drop_policy)
//	drop_policy                   | nested-modifier | delta   | declared-inverse (down create_policy) / non-invertible
//	enable_rls                    | nested-modifier | delta   | declared-inverse (down disable_rls)
//	disable_rls                   | nested-modifier | delta   | declared-inverse (down enable_rls)
//	force_rls                     | nested-modifier | delta   | declared-inverse (down no_force_rls)
//	no_force_rls                  | nested-modifier | delta   | declared-inverse (down force_rls)
//	refresh_materialized_view     | manifest-no-op  | delta   | non-invertible (a refresh has no inverse)
//	create_sm_trigger_function    | pseudo(raw)     | blob    | declared-inverse (down = drop_function SQL)
//	create_sm_trigger             | pseudo(raw)     | blob    | declared-inverse (down = drop_trigger SQL)
//	create_deny_mutation_function | pseudo(raw)     | blob    | declared-inverse (down = drop_function SQL) [5.1b: replaces nil-def create_function]
//	create_append_only_trigger    | pseudo(raw)     | blob    | declared-inverse (down = drop_trigger SQL) [5.1b: replaces nil-def create_trigger]
//	create_partman_parent         | pseudo(raw)     | blob    | declared-inverse (down = undo SQL)
//	update_partman_retention      | pseudo(raw)     | blob    | declared-inverse (down = prev config SQL)
//	update_partman_premake        | pseudo(raw)     | blob    | declared-inverse (down = prev config SQL)
//	backfill                      | pseudo(dml)     | blob    | declared-inverse (VACUOUS: data not restored)
//	transform                     | pseudo(dml)     | blob    | declared-inverse (VACUOUS: data not restored)
//	schema_meta                   | schema-meta     | def     | declared-inverse (down = prev schema-meta) [5.1b: Extensions/PGVersion/Groups]
//
// L4 JUDGMENT NOTES (amendment's explicit guidance):
//   - add_column is DECLARED-INVERSE, not mechanically-invertible: its down
//     drops the just-added column, which is EMPTY BY CONSTRUCTION (a fresh
//     column holds no user data), so the drop is mechanically safe; the down is
//     nonetheless RECORDED (declared) because a generic column-drop is not.
//   - drop_column emitted as an UP op is DECLARED-INVERSE whose down RECREATES
//     STRUCTURE ONLY — the data of the dropped column is NOT restored (L4's
//     vacuous-DML semantics made explicit). When generate records the down as
//     Irreversible, the op is non-invertible instead.
//   - renames are MECHANICALLY-INVERTIBLE (rename b->a is the pure structural
//     inverse; the rename-gate precedent, 5.9).
//   - enum-value adds and enum drops are NON-INVERTIBLE (PostgreSQL cannot drop
//     an enum value).

import (
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// simCategory is a self-contained op's endpoint-simulation behavior.
type simCategory int

const (
	catWholeCreate    simCategory = iota // set manifest key -> DefID (the object's own post-state)
	catWholeDrop                         // delete manifest key
	catNestedModifier                    // set OWNING key -> PostDefID (the owner's post-state)
	catRenameTable                       // delete OldTable key, set new key -> PostDefID
	catSchemaMeta                        // set schema:<name> key -> DefID (post-state schema-meta form)
	catPseudo                            // dml/raw: MANIFEST NO-OP (edge_format.md A2)
	catManifestNoop                      // refresh_materialized_view: no manifest effect
)

// categoryForKind classifies an op kind's endpoint-simulation behavior. It is
// TOTAL over every kind in the inventory; an unlisted kind returns (0, false).
func categoryForKind(kind string) (simCategory, bool) {
	switch kind {
	case "create_table", "create_partition", "create_view",
		"create_or_replace_view", "create_materialized_view", "create_sequence",
		"alter_sequence", "create_composite_type", "create_domain",
		"create_function", "create_or_replace_function", "create_enum",
		// create_sm_type is a whole-object create at the manifest layer (roadmap
		// 5.10 rider): it sets the KindSMType key to the post-state form id and
		// renders no DDL (the SM DDL rides the enum/trigger ops).
		"create_sm_type":
		return catWholeCreate, true
	case "drop_table", "drop_view", "drop_materialized_view", "drop_sequence",
		"drop_composite_type", "drop_domain", "drop_function", "drop_enum",
		// drop_sm_type deletes the KindSMType key; renders no DDL.
		"drop_sm_type":
		return catWholeDrop, true
	case "rename_table":
		return catRenameTable, true
	case "schema_meta":
		return catSchemaMeta, true
	case "refresh_materialized_view":
		return catManifestNoop, true
	case "backfill", "transform",
		"create_sm_trigger_function", "create_sm_trigger",
		"create_deny_mutation_function", "create_append_only_trigger",
		"create_partman_parent", "update_partman_retention", "update_partman_premake":
		return catPseudo, true
	case
		// column ops
		"rename_column", "add_column", "drop_column", "alter_column_type",
		"set_not_null", "drop_not_null", "alter_column_default",
		"drop_column_default", "set_statistics", "set_owner",
		// constraints / indexes on tables
		"add_fk", "add_fk_not_valid", "drop_fk", "validate_constraint",
		"create_index", "add_index", "drop_index",
		"create_index_concurrently", "drop_index_concurrently", "alter_index_set",
		"add_unique", "drop_unique", "add_check", "drop_check",
		"add_exclusion", "drop_exclusion",
		// enum / domain modifiers
		"alter_enum_add_value",
		"alter_domain_add_constraint", "alter_domain_drop_constraint",
		"alter_domain_set_default", "alter_domain_drop_default",
		"alter_domain_set_not_null", "alter_domain_drop_not_null",
		// table-owned trigger / policy / rls
		"create_trigger", "drop_trigger", "create_policy", "drop_policy",
		"enable_rls", "disable_rls", "force_rls", "no_force_rls",
		// comment metadata (roadmap 5.8a): maps the OWNING object's key to its
		// post-state (a comment change re-encodes the owning object).
		"comment_on":
		return catNestedModifier, true
	default:
		return 0, false
	}
}

// owningKeyForDelta returns the manifest key a nested-modifier op targets: the
// owning enum/domain for the type-modifier families, else the owning table.
func owningKeyForDelta(op DDLOp) enc.Key {
	switch op.Op {
	case "comment_on":
		return commentOwningKey(op)
	case "alter_enum_add_value":
		return enc.Key{Kind: enc.KindEnum, Schema: op.Schema, Name: op.Name}
	case "alter_domain_add_constraint", "alter_domain_drop_constraint",
		"alter_domain_set_default", "alter_domain_drop_default",
		"alter_domain_set_not_null", "alter_domain_drop_not_null":
		return enc.Key{Kind: enc.KindDomain, Schema: op.Schema, Name: op.Name}
	default:
		schema, name := splitQualifiedName(op.Table)
		return enc.Key{Kind: enc.KindTable, Schema: schema, Name: name}
	}
}

// commentOwningKey returns the manifest key a comment_on op's OWNING object
// occupies (roadmap 5.8a). It mirrors enc.KeyForX EXACTLY — raw op.Schema/op.Name
// (never normalized to "public"), and the function arg signature for overload
// distinctness — because the op's post-state def id is assigned to this key during
// endpoint simulation, and it must equal the key BuildManifestInto minted for the
// same object. A TABLE and a COLUMN comment both re-encode the OWNING TABLE, so
// both map to the table key.
func commentOwningKey(op DDLOp) enc.Key {
	switch op.CommentObject {
	case "TABLE", "COLUMN":
		return enc.Key{Kind: enc.KindTable, Schema: op.Schema, Name: op.Name}
	case "VIEW":
		return enc.Key{Kind: enc.KindView, Schema: op.Schema, Name: op.Name}
	case "MATERIALIZED VIEW":
		return enc.Key{Kind: enc.KindMatView, Schema: op.Schema, Name: op.Name}
	case "SEQUENCE":
		return enc.Key{Kind: enc.KindSequence, Schema: op.Schema, Name: op.Name}
	case "DOMAIN":
		return enc.Key{Kind: enc.KindDomain, Schema: op.Schema, Name: op.Name}
	case "TYPE":
		return enc.Key{Kind: enc.KindComposite, Schema: op.Schema, Name: op.Name}
	case "FUNCTION", "PROCEDURE":
		// Procedures share the function manifest kind; only the COMMENT ON SQL
		// keyword differs (handled at render time).
		return enc.Key{Kind: enc.KindFunction, Schema: op.Schema, Name: op.Name, ArgSig: op.FuncArgSig}
	default:
		return enc.Key{}
	}
}

// wholeObjectKey returns the manifest key a whole-object create/drop op names.
func wholeObjectKey(op DDLOp) enc.Key {
	switch op.Op {
	case "create_table", "drop_table":
		schema, name := splitQualifiedName(op.Table)
		return enc.Key{Kind: enc.KindTable, Schema: schema, Name: name}
	case "create_partition":
		schema, _ := splitQualifiedName(op.ParentTable)
		return enc.Key{Kind: enc.KindTable, Schema: schema, Name: op.PartitionChildSpec.Name}
	case "create_view", "drop_view", "create_or_replace_view":
		schema, name := splitQualifiedName(op.Name)
		return enc.Key{Kind: enc.KindView, Schema: schema, Name: name}
	case "create_materialized_view", "drop_materialized_view":
		schema, name := splitQualifiedName(op.Name)
		return enc.Key{Kind: enc.KindMatView, Schema: schema, Name: name}
	case "create_sequence", "drop_sequence", "alter_sequence":
		schema, name := seqSchemaName(op)
		return enc.Key{Kind: enc.KindSequence, Schema: schema, Name: name}
	case "create_composite_type", "drop_composite_type":
		return enc.Key{Kind: enc.KindComposite, Schema: schemaOrPublic(op.Schema), Name: op.Name}
	case "create_domain", "drop_domain":
		return enc.Key{Kind: enc.KindDomain, Schema: schemaOrPublic(op.Schema), Name: op.Name}
	case "create_function", "drop_function", "create_or_replace_function":
		schema := schemaOrPublic(op.Schema)
		sig := ""
		if op.FunctionDef != nil {
			sig = enc.FunctionArgSig(op.FunctionDef.Args)
		}
		return enc.Key{Kind: enc.KindFunction, Schema: schema, Name: op.Name, ArgSig: sig}
	case "create_enum", "drop_enum":
		return enc.Key{Kind: enc.KindEnum, Schema: enumSchemaName(op), Name: enumName(op)}
	case "create_sm_type", "drop_sm_type":
		// The schema is used VERBATIM (never defaulted to public) to match
		// enc.KeyForStateMachine, which keys on sm.Schema directly.
		return enc.Key{Kind: enc.KindSMType, Schema: op.Schema, Name: op.Name}
	default:
		return enc.Key{}
	}
}

// seqSchemaName resolves a sequence op's (schema, name). Create/alter carry the
// name split on Name; some carry a separate Schema field.
func seqSchemaName(op DDLOp) (string, string) {
	if op.Schema != "" {
		return op.Schema, op.Name
	}
	return splitQualifiedName(op.Name)
}

func enumSchemaName(op DDLOp) string {
	if op.Schema != "" {
		return op.Schema
	}
	s, _ := splitQualifiedName(op.Table)
	return s
}

func enumName(op DDLOp) string {
	if op.Schema != "" || op.Name != "" {
		return op.Name
	}
	_, n := splitQualifiedName(op.Table)
	return n
}

func schemaOrPublic(s string) string {
	if s == "" {
		return "public"
	}
	return s
}

// encodeObjectByKey encodes the object of s that a manifest key names, returning
// (bytes, true) when present. It is the bridge that lets the shim compute a
// POST-STATE content id straight from the desired model, guaranteeing the id
// equals the to-manifest entry (both are objstore.ID of the same canonical
// bytes).
func encodeObjectByKey(s *model.Schema, k enc.Key) ([]byte, bool, error) {
	switch k.Kind {
	case enc.KindTable:
		for _, t := range s.Tables {
			if t.Schema == k.Schema && t.Name == k.Name {
				b, err := enc.EncodeTable(t)
				return b, true, err
			}
		}
	case enc.KindView:
		for _, v := range s.Views {
			if v.Schema == k.Schema && v.Name == k.Name {
				b, err := enc.EncodeView(v)
				return b, true, err
			}
		}
	case enc.KindMatView:
		for _, mv := range s.MaterializedViews {
			if mv.Schema == k.Schema && mv.Name == k.Name {
				b, err := enc.EncodeMaterializedView(mv)
				return b, true, err
			}
		}
	case enc.KindSequence:
		for _, sq := range s.Sequences {
			if sq.Schema == k.Schema && sq.Name == k.Name {
				b, err := enc.EncodeSequence(sq)
				return b, true, err
			}
		}
	case enc.KindFunction:
		for _, fn := range s.Functions {
			if fn.Schema == k.Schema && fn.Name == k.Name && enc.FunctionArgSig(fn.Args) == k.ArgSig {
				b, err := enc.EncodeFunction(fn)
				return b, true, err
			}
		}
	case enc.KindEnum:
		for _, e := range s.Enums {
			if e.Schema == k.Schema && e.Name == k.Name {
				b, err := enc.EncodeEnum(e)
				return b, true, err
			}
		}
	case enc.KindDomain:
		for _, d := range s.Domains {
			if d.Schema == k.Schema && d.Name == k.Name {
				b, err := enc.EncodeDomain(d)
				return b, true, err
			}
		}
	case enc.KindComposite:
		for _, c := range s.CompositeTypes {
			if c.Schema == k.Schema && c.Name == k.Name {
				b, err := enc.EncodeCompositeType(c)
				return b, true, err
			}
		}
	case enc.KindSMType:
		for _, sm := range s.StateMachines {
			if sm.Schema == k.Schema && sm.Name == k.Name {
				b, err := enc.EncodeStateMachine(sm)
				return b, true, err
			}
		}
	case enc.KindSchemaMeta:
		b, err := enc.EncodeSchemaMeta(s)
		return b, true, err
	}
	return nil, false, nil
}

// putObjectByKey encodes the desired object a key names and stores it, returning
// its content id (equal to the to-manifest entry by construction).
func putObjectByKey(store *objstore.Store, s *model.Schema, k enc.Key) (string, bool, error) {
	b, ok, err := encodeObjectByKey(s, k)
	if err != nil || !ok {
		return "", ok, err
	}
	id, err := store.Put(b)
	return id, true, err
}

// deltaOf projects a legacy DDLOp to a scalar-only delta suitable for storage
// and OpToSQL rendering: pointer defs, the recursive Down, ConsolidatedOps, and
// the Phase tag are stripped (they are not read by OpToSQL and would inline a
// lossy mirror or bloat the content-addressed payload).
func deltaOf(op DDLOp) *DDLOp {
	d := op
	d.Phase = ""
	d.Down = nil
	d.ConsolidatedOps = nil
	d.TableDef = nil
	d.TableEnums = nil
	d.TableDomains = nil
	d.PartitionChildSpec = nil
	d.ViewDef = nil
	d.MaterializedViewDef = nil
	d.SequenceDef = nil
	d.CompositeTypeDef = nil
	d.DomainDef = nil
	d.FunctionDef = nil
	d.TriggerDef = nil
	d.PolicyDef = nil
	d.RawSQL = ""
	return &d
}

// swapComment produces the structural inverse of a comment_on delta (roadmap
// 5.8a): the down carries the SAME locator fields with Comment and CommentOld
// swapped, so applying it restores the prior comment.
func swapComment(up DDLOp) DDLOp {
	down := up
	down.Comment = up.CommentOld
	down.CommentOld = up.Comment
	return down
}

// swapRename produces the structural inverse of a rename delta (roadmap 5.1b):
// rename_column T old->new inverts to T new->old; rename_table s.old->new
// inverts to s.new->old.
func swapRename(up DDLOp) DDLOp {
	switch up.Op {
	case "rename_column":
		down := up
		down.Column = up.Name
		down.Name = up.Column
		return down
	case "rename_table":
		schema, oldName := splitQualifiedName(up.Table)
		down := up
		down.Table = schema + "." + up.Name
		down.Name = oldName
		return down
	default:
		return up
	}
}
