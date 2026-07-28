package migrate

// Builders for self-contained ops (roadmap 5.1).
//
// Each builder stores the op's def (and, for create_table, its enum/domain type
// closure) in the object store BY CONTENT ID, assembles the op body, stores it,
// and resolves the down reference. Construction REQUIRES the store: there is no
// way to build a self-contained op that cannot render its true SQL.
//
// The down is a pure function of the up payload (edge_format.md TENSION 1):
//   - mechanically-invertible creates derive their down structurally (a drop);
//   - declared-inverse ops (create_or_replace_*, alter_sequence, RawSQL-bodied
//     families, DML) embed their recorded inverse INSIDE the up body (opBody.Down),
//     so the down never escapes the up payload's content id.

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// putDef encodes and stores a def, returning its content id.
func putDef(store *objstore.Store, encode func() ([]byte, error)) (string, error) {
	data, err := encode()
	if err != nil {
		return "", fmt.Errorf("migrate: encode def: %w", err)
	}
	return store.Put(data)
}

// putJSONDef stores a canonical-JSON def (Trigger/Policy/PartitionSpec).
func putJSONDef(store *objstore.Store, v any) (string, error) {
	data, err := canonicalOpJSON(v)
	if err != nil {
		return "", fmt.Errorf("migrate: encode def: %w", err)
	}
	return store.Put(data)
}

// newOp finalizes a self-contained op: it stores the up body, sets the declared
// invertibility, and resolves the down (structural for mechanical creates,
// embedded for declared-inverse).
func newOp(store *objstore.Store, kind string, target enc.Key, up opBody) (SelfContainedOp, error) {
	return newOpWithClass(store, kind, target, up, invClassForKind(kind))
}

// newOpWithClass finalizes a self-contained op with an EXPLICIT L4 class. The
// conversion shim (roadmap 5.1b) uses it because a legacy op's invertibility is
// determined by its recorded DownOp (Irreversible -> non-invertible; a real down
// -> declared-inverse), not by the kind alone. Kind-based builders keep using
// newOp, which derives the class from invClassForKind.
func newOpWithClass(store *objstore.Store, kind string, target enc.Key, up opBody, inv chain.InvertibilityClass) (SelfContainedOp, error) {
	up.Kind = kind
	payload, err := putBody(store, up)
	if err != nil {
		return SelfContainedOp{}, err
	}
	op := SelfContainedOp{kind: kind, target: target, inv: inv, payload: payload}
	down, err := deriveDown(store, op, up)
	if err != nil {
		return SelfContainedOp{}, err
	}
	op.down = down
	return op, nil
}

// deriveDown re-derives an op's down from its up body, per L4 class. It is the
// single derivation the LOAD-time verifier (VerifyDown) re-runs and compares
// against the stored down cache. Returns nil for non-invertible ops.
func deriveDown(store *objstore.Store, up SelfContainedOp, body opBody) (*SelfContainedOp, error) {
	switch up.inv {
	case chain.NonInvertible:
		return nil, nil

	case chain.MechanicallyInvertible:
		// Renames invert to the swapped rename (roadmap 5.1b / 5.9 rename gate):
		// the down is a pure structural function of the up delta, no prior state.
		if up.kind == "rename_column" || up.kind == "rename_table" {
			if body.Delta == nil {
				return nil, fmt.Errorf("migrate: rename op %q carries no delta to invert", up.kind)
			}
			swapped := swapRename(*body.Delta)
			down := opBody{Kind: up.kind, Delta: &swapped}
			id, err := putBody(store, down)
			if err != nil {
				return nil, err
			}
			return &SelfContainedOp{kind: up.kind, target: up.target, inv: chain.NonInvertible, payload: id}, nil
		}
		// comment_on inverts by restoring the prior comment (roadmap 5.8a): the down
		// is the same op with old/new swapped — a pure structural function of the up
		// delta, which carries both comments. Same shape as the rename swap.
		if up.kind == "comment_on" {
			if body.Delta == nil {
				return nil, fmt.Errorf("migrate: comment_on op carries no delta to invert")
			}
			swapped := swapComment(*body.Delta)
			down := opBody{Kind: up.kind, Delta: &swapped}
			id, err := putBody(store, down)
			if err != nil {
				return nil, err
			}
			return &SelfContainedOp{kind: up.kind, target: up.target, inv: chain.NonInvertible, payload: id}, nil
		}
		dk := dropKindFor(up.kind)
		if dk == "" {
			return nil, fmt.Errorf("migrate: no structural inverse for mechanically-invertible kind %q", up.kind)
		}
		down := opBody{
			Kind:   dk,
			Schema: body.Schema,
			Name:   body.Name,
			Table:  body.Table,
		}
		// drop_function needs the function def for its argument-type signature.
		if dk == "drop_function" {
			down.DefID = body.DefID
		}
		id, err := putBody(store, down)
		if err != nil {
			return nil, err
		}
		return &SelfContainedOp{
			kind:    dk,
			target:  up.target, // a drop acts on the same object
			inv:     chain.NonInvertible,
			payload: id,
		}, nil

	case chain.DeclaredInverse:
		if body.Down == nil {
			return nil, fmt.Errorf("migrate: declared-inverse op %q is missing its embedded inverse", up.kind)
		}
		down := *body.Down
		id, err := putBody(store, down)
		if err != nil {
			return nil, err
		}
		return &SelfContainedOp{
			kind:    down.Kind,
			target:  downTarget(down, up.target),
			inv:     chain.NonInvertible,
			payload: id,
		}, nil

	default:
		return nil, fmt.Errorf("migrate: op %q has invalid invertibility class %d", up.kind, int(up.inv))
	}
}

// downTarget computes the manifest key for a declared inverse's down op: a
// raw/dml pseudo-key when the down is an opaque blob, else the up op's object
// key (the inverse acts on the same object).
func downTarget(down opBody, upTarget enc.Key) enc.Key {
	if down.BlobID != "" {
		if dmlTargeted(down.Kind) {
			return enc.KeyForDML(down.Seq)
		}
		return enc.KeyForRaw(down.Seq)
	}
	return upTarget
}

// ---- pointer-def families (mechanically invertible) ----

// BuildCreateTable stores the table plus its transitive enum/domain closure by
// content id and builds a create_table op. The closure lets rendering qualify
// enum/domain type names; PGVersion gates version-dependent DDL (e.g. STORED vs
// VIRTUAL generated columns) — both are recorded on the op, never hardcoded.
func BuildCreateTable(store *objstore.Store, tbl model.Table, schema string, pgVersion int, enums []model.Enum, domains []model.Domain) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeTable(tbl) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	enumIDs := make([]string, 0, len(enums))
	for _, e := range enums {
		id, err := putDef(store, func() ([]byte, error) { return enc.EncodeEnum(e) })
		if err != nil {
			return SelfContainedOp{}, err
		}
		enumIDs = append(enumIDs, id)
	}
	domainIDs := make([]string, 0, len(domains))
	for _, d := range domains {
		id, err := putDef(store, func() ([]byte, error) { return enc.EncodeDomain(d) })
		if err != nil {
			return SelfContainedOp{}, err
		}
		domainIDs = append(domainIDs, id)
	}
	up := opBody{
		Schema:    schema,
		Name:      tbl.Name,
		PGVersion: pgVersion,
		DefID:     defID,
		EnumIDs:   enumIDs,
		DomainIDs: domainIDs,
	}
	return newOp(store, "create_table", enc.Key{Kind: enc.KindTable, Schema: schema, Name: tbl.Name}, up)
}

// BuildCreateView builds a create_view op referencing the view def by content id.
func BuildCreateView(store *objstore.Store, v model.View, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeView(v) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	up := opBody{Schema: schema, Name: v.Name, DefID: defID}
	return newOp(store, "create_view", enc.Key{Kind: enc.KindView, Schema: schema, Name: v.Name}, up)
}

// BuildCreateMaterializedView builds a create_materialized_view op.
func BuildCreateMaterializedView(store *objstore.Store, mv model.MaterializedView, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeMaterializedView(mv) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	up := opBody{Schema: schema, Name: mv.Name, DefID: defID}
	return newOp(store, "create_materialized_view", enc.Key{Kind: enc.KindMatView, Schema: schema, Name: mv.Name}, up)
}

// BuildCreateSequence builds a create_sequence op preserving all parameters
// (start/increment/min/max/cache/cycle/owned_by) via the encoded def.
func BuildCreateSequence(store *objstore.Store, s model.Sequence, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeSequence(s) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	up := opBody{Schema: schema, Name: s.Name, DefID: defID}
	return newOp(store, "create_sequence", enc.Key{Kind: enc.KindSequence, Schema: schema, Name: s.Name}, up)
}

// BuildCreateCompositeType builds a create_composite_type op.
func BuildCreateCompositeType(store *objstore.Store, c model.CompositeType, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeCompositeType(c) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	up := opBody{Schema: schema, Name: c.Name, DefID: defID}
	return newOp(store, "create_composite_type", enc.Key{Kind: enc.KindComposite, Schema: schema, Name: c.Name}, up)
}

// BuildCreateDomain builds a create_domain op.
func BuildCreateDomain(store *objstore.Store, d model.Domain, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeDomain(d) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	up := opBody{Schema: schema, Name: d.Name, DefID: defID}
	return newOp(store, "create_domain", enc.Key{Kind: enc.KindDomain, Schema: schema, Name: d.Name}, up)
}

// BuildCreateFunction builds a create_function op. The function def (args,
// return type, body, volatility, ...) is stored by content id, so a parsed op
// renders the true function — never the deny-mutation fallback.
func BuildCreateFunction(store *objstore.Store, f model.Function, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeFunction(f) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	up := opBody{Schema: schema, Name: f.Name, DefID: defID}
	return newOp(store, "create_function", enc.Key{Kind: enc.KindFunction, Schema: schema, Name: f.Name, ArgSig: enc.FunctionArgSig(f.Args)}, up)
}

// BuildCreateTrigger builds a create_trigger op on a table. The trigger def is
// stored by content id, so a parsed op renders the true trigger — never the
// append-only fallback.
func BuildCreateTrigger(store *objstore.Store, t model.Trigger, table string, pgVersion int) (SelfContainedOp, error) {
	defID, err := putJSONDef(store, t)
	if err != nil {
		return SelfContainedOp{}, err
	}
	schema, tableName := splitQualifiedName(table)
	up := opBody{Schema: schema, Table: table, Name: t.Name, PGVersion: pgVersion, DefID: defID}
	return newOp(store, "create_trigger", enc.Key{Kind: enc.KindTable, Schema: schema, Name: tableName}, up)
}

// BuildCreatePolicy builds a create_policy op on a table.
func BuildCreatePolicy(store *objstore.Store, p model.Policy, table string, pgVersion int) (SelfContainedOp, error) {
	defID, err := putJSONDef(store, p)
	if err != nil {
		return SelfContainedOp{}, err
	}
	schema, tableName := splitQualifiedName(table)
	up := opBody{Schema: schema, Table: table, Name: p.Name, PGVersion: pgVersion, DefID: defID}
	return newOp(store, "create_policy", enc.Key{Kind: enc.KindTable, Schema: schema, Name: tableName}, up)
}

// BuildCreatePartition builds a create_partition op. The child PartitionSpec and
// its parent table are recorded by content id / value, so a parsed op renders
// the true CREATE TABLE ... PARTITION OF.
func BuildCreatePartition(store *objstore.Store, childSpec model.PartitionSpec, parentTable string) (SelfContainedOp, error) {
	defID, err := putJSONDef(store, childSpec)
	if err != nil {
		return SelfContainedOp{}, err
	}
	schema, _ := splitQualifiedName(parentTable)
	up := opBody{Schema: schema, Name: childSpec.Name, ParentTable: parentTable, DefID: defID}
	return newOp(store, "create_partition", enc.Key{Kind: enc.KindTable, Schema: schema, Name: childSpec.Name}, up)
}

// ---- declared-inverse object families ----

// BuildCreateOrReplaceView builds a create_or_replace_view op whose recorded
// inverse restores the previous view definition. prev is nil when there is no
// prior view (then the down is a drop_view).
func BuildCreateOrReplaceView(store *objstore.Store, v model.View, prev *model.View, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeView(v) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	var down opBody
	if prev != nil {
		prevID, err := putDef(store, func() ([]byte, error) { return enc.EncodeView(*prev) })
		if err != nil {
			return SelfContainedOp{}, err
		}
		down = opBody{Kind: "create_or_replace_view", Schema: schema, Name: prev.Name, Replace: true, DefID: prevID}
	} else {
		down = opBody{Kind: "drop_view", Schema: schema, Name: v.Name}
	}
	up := opBody{Schema: schema, Name: v.Name, Replace: true, DefID: defID, Down: &down}
	return newOp(store, "create_or_replace_view", enc.Key{Kind: enc.KindView, Schema: schema, Name: v.Name}, up)
}

// BuildCreateOrReplaceFunction builds a create_or_replace_function op whose
// recorded inverse restores the previous function. prev is nil when there is no
// prior function (then the down is a drop_function).
func BuildCreateOrReplaceFunction(store *objstore.Store, f model.Function, prev *model.Function, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeFunction(f) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	var down opBody
	if prev != nil {
		prevID, err := putDef(store, func() ([]byte, error) { return enc.EncodeFunction(*prev) })
		if err != nil {
			return SelfContainedOp{}, err
		}
		down = opBody{Kind: "create_or_replace_function", Schema: schema, Name: prev.Name, DefID: prevID}
	} else {
		down = opBody{Kind: "drop_function", Schema: schema, Name: f.Name, DefID: defID}
	}
	up := opBody{Schema: schema, Name: f.Name, DefID: defID, Down: &down}
	return newOp(store, "create_or_replace_function", enc.Key{Kind: enc.KindFunction, Schema: schema, Name: f.Name, ArgSig: enc.FunctionArgSig(f.Args)}, up)
}

// BuildAlterSequence builds an alter_sequence op whose recorded inverse restores
// the previous sequence parameters (prev), so a rollback re-issues the ALTER.
func BuildAlterSequence(store *objstore.Store, s model.Sequence, prev model.Sequence, schema string) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeSequence(s) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	prevID, err := putDef(store, func() ([]byte, error) { return enc.EncodeSequence(prev) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	down := opBody{Kind: "alter_sequence", Schema: schema, Name: prev.Name, DefID: prevID}
	up := opBody{Schema: schema, Name: s.Name, DefID: defID, Down: &down}
	return newOp(store, "alter_sequence", enc.Key{Kind: enc.KindSequence, Schema: schema, Name: s.Name}, up)
}

// ---- schema-meta family (roadmap 5.1b) ----

// BuildSchemaMeta builds a schema_meta op covering Extensions/PGVersion/Groups
// changes (the manifest's schema:<name> entry). The payload is the POST-STATE
// schema-meta form id; simulation maps the schema key to it. The recorded
// inverse restores the prior schema-meta form (prev; pass an empty-meta schema
// for a genesis boundary).
func BuildSchemaMeta(store *objstore.Store, desired *model.Schema, prev *model.Schema) (SelfContainedOp, error) {
	defID, err := putDef(store, func() ([]byte, error) { return enc.EncodeSchemaMeta(desired) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	if prev == nil {
		prev = &model.Schema{Name: desired.Name}
	}
	prevID, err := putDef(store, func() ([]byte, error) { return enc.EncodeSchemaMeta(prev) })
	if err != nil {
		return SelfContainedOp{}, err
	}
	down := opBody{Kind: "schema_meta", Name: prev.Name, DefID: prevID}
	up := opBody{Name: desired.Name, DefID: defID, Down: &down}
	return newOp(store, "schema_meta", enc.Key{Kind: enc.KindSchemaMeta, Name: desired.Name}, up)
}

// ---- RawSQL-bodied and DML families (opaque blobs, declared inverse) ----

// BuildRawOp builds an opaque-SQL op (SM triggers, partman config) whose body
// is a content-addressed blob and whose recorded inverse is the down blob. seq
// and downSeq are the ops' zero-based positions within their edge (they pin the
// raw:<seq> pseudo-targets). kind is the semantic family name (e.g.
// "create_sm_trigger"); downKind names the inverse family.
func BuildRawOp(store *objstore.Store, kind string, seq int, upSQL string, downKind string, downSeq int, downSQL string) (SelfContainedOp, error) {
	blobID, err := store.Put([]byte(upSQL))
	if err != nil {
		return SelfContainedOp{}, err
	}
	downBlob, err := store.Put([]byte(downSQL))
	if err != nil {
		return SelfContainedOp{}, err
	}
	down := opBody{Kind: downKind, BlobID: downBlob, Seq: downSeq}
	up := opBody{BlobID: blobID, Seq: seq, Down: &down}
	return newOp(store, kind, enc.KeyForRaw(seq), up)
}

// BuildDMLOp builds a data-manipulation op (backfill/transform) whose body is a
// content-addressed SQL blob. Its inverse is DECLARED and may be VACUOUS (data
// is not restored — today's reversibility semantics, made explicit): pass the
// reverse-DML SQL, or a vacuous marker, as downSQL.
func BuildDMLOp(store *objstore.Store, kind string, seq int, upSQL string, downSeq int, downSQL string) (SelfContainedOp, error) {
	blobID, err := store.Put([]byte(upSQL))
	if err != nil {
		return SelfContainedOp{}, err
	}
	downBlob, err := store.Put([]byte(downSQL))
	if err != nil {
		return SelfContainedOp{}, err
	}
	down := opBody{Kind: kind, BlobID: downBlob, Seq: downSeq}
	up := opBody{BlobID: blobID, Seq: seq, Down: &down}
	return newOp(store, kind, enc.KeyForDML(seq), up)
}
