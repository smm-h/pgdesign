package migrate

// Legacy-DDLOp -> self-contained conversion shim (roadmap 5.1b, the 5.2
// foundation). DDLOpToSelfContained converts any generate-emitted DDLOp into the
// self-contained form 5.2's generate-to-edge and upgrade-fold will store:
//
//   - WHOLE-OBJECT creates carry the object's POST-STATE def id (from `desired`),
//     so the id equals the to-manifest entry and simulation is exact.
//   - NESTED-MODIFIER ops carry the OWNING object's POST-STATE def id plus a
//     scalar render DELTA (the amendment's adopted resolution): rendering
//     re-invokes OpToSQL on the delta (byte-identical), simulation maps the
//     owning key to the post-state id.
//   - WHOLE-OBJECT drops carry only the render delta; simulation deletes the key.
//   - The append-only / SM / partman machinery becomes distinct raw-blob kinds
//     with raw:<seq> pseudo-targets (manifest no-ops) — the nil-def magic-name
//     fallback is dead at the self-contained layer.
//
// There is NO fallback: a kind the inventory does not cover, or a whole-object
// create whose object is absent from `desired` with no pointer def, is a HARD
// ERROR (the no-fallback rule). `seq` is the op's zero-based position within its
// edge; it pins the raw/dml pseudo-targets (edge_format.md TENSION 2).

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// DDLOpToSelfContained converts one legacy DDLOp (at edge position seq) against
// the POST-STATE model `desired` into a self-contained op.
func DDLOpToSelfContained(store *objstore.Store, op DDLOp, desired *model.Schema, seq int) (SelfContainedOp, error) {
	kind, target, up, err := ddlOpToBody(store, op, desired, seq)
	if err != nil {
		return SelfContainedOp{}, err
	}
	inv, down, err := shimInvAndDown(store, op, kind, seq)
	if err != nil {
		return SelfContainedOp{}, err
	}
	up.Down = down
	return newOpWithClass(store, kind, target, up, inv)
}

// shimInvAndDown decides the op's L4 class from its recorded DownOp and, for
// declared-inverse ops, builds the embedded down body from the legacy down. A
// mechanically-invertible op needs no embedded down (deriveDown derives it
// structurally). Renames are mechanically-invertible (structural swap).
func shimInvAndDown(store *objstore.Store, op DDLOp, kind string, seq int) (chain.InvertibilityClass, *opBody, error) {
	// Renames are the pure structural inverse (5.9 rename gate); deriveDown swaps
	// the delta, no embedded down needed.
	if kind == "rename_column" || kind == "rename_table" {
		return chain.MechanicallyInvertible, nil, nil
	}
	// Mechanically-invertible creates (table/view/matview/sequence/composite/
	// domain/function/enum/trigger/policy/partition) derive their drop
	// structurally — no embedded down.
	if invClassForKind(kind) == chain.MechanicallyInvertible {
		return chain.MechanicallyInvertible, nil, nil
	}
	// Everything else (drops, nested modifiers, rls, or_replace, alter_sequence,
	// raw machinery): the recorded legacy down decides. Irreversible / absent ->
	// non-invertible; a real down -> declared-inverse with the embedded down.
	if op.Down == nil || op.Down.Irreversible || len(op.Down.Ops) == 0 {
		return chain.NonInvertible, nil, nil
	}
	down, err := shimDownBody(store, op.Down.Ops[0], seq)
	if err != nil {
		return 0, nil, err
	}
	return chain.DeclaredInverse, down, nil
}

// shimDownBody builds the embedded down body from a legacy down DDLOp. Downs are
// never simulated, so PostDefID is irrelevant and whole-object-create downs draw
// their def from the op's OWN pointer def (the prior state generate recorded),
// never from the post-state model (which no longer contains the object).
func shimDownBody(store *objstore.Store, down DDLOp, seq int) (*opBody, error) {
	kind, _, body, err := ddlOpToBody(store, down, nil, seq)
	if err != nil {
		return nil, fmt.Errorf("migrate: shim down %q: %w", down.Op, err)
	}
	body.Kind = kind
	return &body, nil
}

// ddlOpToBody is the core converter: it classifies op, stores whatever the
// self-contained body needs (defs by content id, opaque blobs), and returns the
// self-contained KIND, manifest TARGET, and body. `desired` is the post-state
// model for UP ops (nil for embedded downs).
func ddlOpToBody(store *objstore.Store, op DDLOp, desired *model.Schema, seq int) (string, enc.Key, opBody, error) {
	// Append-only / deny-mutation machinery: distinct raw-blob kinds, manifest
	// no-ops. The nil-def is the reliable per-op signal.
	switch {
	case op.Op == "create_function" && op.FunctionDef == nil:
		return rawMachineryBody(store, "create_deny_mutation_function", op, seq)
	case op.Op == "create_trigger" && op.TriggerDef == nil:
		return rawMachineryBody(store, "create_append_only_trigger", op, seq)
	case op.Op == "drop_function" && op.FunctionDef == nil:
		return rawMachineryBody(store, "drop_deny_mutation_function", op, seq)
	}
	// SM-trigger / partman ops carry pre-rendered RawSQL: opaque raw blobs.
	if op.RawSQL != "" {
		return rawBlobBody(store, op.Op, op.RawSQL, seq)
	}

	cat, ok := categoryForKind(op.Op)
	if !ok {
		return "", enc.Key{}, opBody{}, fmt.Errorf("migrate: shim: op kind %q is not in the self-contained inventory", op.Op)
	}

	switch cat {
	case catWholeCreate:
		return shimWholeCreate(store, op, desired)

	case catWholeDrop:
		key := wholeObjectKey(op)
		// drop_function renders from the function def (its argument signature), so
		// it carries the def by content id rather than a scalar delta.
		if op.Op == "drop_function" {
			if op.FunctionDef == nil {
				return "", enc.Key{}, opBody{}, fmt.Errorf("migrate: shim: drop_function without a function def")
			}
			data, err := enc.EncodeFunction(*op.FunctionDef)
			if err != nil {
				return "", enc.Key{}, opBody{}, err
			}
			defID, err := store.Put(data)
			if err != nil {
				return "", enc.Key{}, opBody{}, err
			}
			return op.Op, key, opBody{Schema: schemaOrPublic(op.Schema), Name: op.Name, DefID: defID}, nil
		}
		return op.Op, key, opBody{Delta: deltaOf(op)}, nil

	case catManifestNoop: // refresh_materialized_view
		schema, name := splitQualifiedName(op.Name)
		return op.Op, enc.Key{Kind: enc.KindMatView, Schema: schema, Name: name}, opBody{Delta: deltaOf(op)}, nil

	case catRenameTable:
		schema, _ := splitQualifiedName(op.Table)
		newKey := enc.Key{Kind: enc.KindTable, Schema: schema, Name: op.Name}
		postID, _, err := maybePostID(store, desired, newKey)
		if err != nil {
			return "", enc.Key{}, opBody{}, err
		}
		return op.Op, newKey, opBody{Delta: deltaOf(op), PostDefID: postID, OldTable: op.Table}, nil

	case catNestedModifier:
		return shimNestedModifier(store, op, desired)

	default:
		return "", enc.Key{}, opBody{}, fmt.Errorf("migrate: shim: unhandled category for %q", op.Op)
	}
}

// shimWholeCreate builds a whole-object create body. The def id is the POST-STATE
// object from `desired` (so it equals the to-manifest entry); for embedded downs
// (desired nil) or objects absent from desired, it falls back to the op's own
// pointer def.
func shimWholeCreate(store *objstore.Store, op DDLOp, desired *model.Schema) (string, enc.Key, opBody, error) {
	if op.Op == "create_partition" {
		if op.PartitionChildSpec == nil {
			return "", enc.Key{}, opBody{}, fmt.Errorf("migrate: shim: create_partition without child spec")
		}
		defID, err := putJSONDef(store, *op.PartitionChildSpec)
		if err != nil {
			return "", enc.Key{}, opBody{}, err
		}
		schema, _ := splitQualifiedName(op.ParentTable)
		key := enc.Key{Kind: enc.KindTable, Schema: schema, Name: op.PartitionChildSpec.Name}
		return op.Op, key, opBody{Schema: schema, Name: op.PartitionChildSpec.Name, ParentTable: op.ParentTable, DefID: defID}, nil
	}

	key := wholeObjectKey(op)
	defID, err := createDefID(store, op, desired, key)
	if err != nil {
		return "", enc.Key{}, opBody{}, err
	}
	b := opBody{Schema: key.Schema, Name: key.Name, PGVersion: op.PGVersion, DefID: defID}
	if op.Op == "create_or_replace_view" {
		b.Replace = true
	}
	// create_table carries the enum/domain closure the op recorded so rendering
	// schema-qualifies user type references (an SM state enum in a non-public
	// schema would otherwise render bare and fail to resolve on apply). The closure
	// is the SAME slice opCreateTable renders from, so chain and legacy renders stay
	// byte-identical. It does NOT affect the def id (the table encoding), so the
	// manifest entry is unchanged.
	if op.Op == "create_table" {
		enumIDs, domainIDs, err := storeTypeClosure(store, op.TableEnums, op.TableDomains)
		if err != nil {
			return "", enc.Key{}, opBody{}, err
		}
		b.EnumIDs = enumIDs
		b.DomainIDs = domainIDs
	}
	return op.Op, key, b, nil
}

// storeTypeClosure stores a create_table op's enum/domain closure by content id,
// preserving order so the decoded closure qualifies column types identically to
// the legacy OpToSQL render.
func storeTypeClosure(store *objstore.Store, enums []model.Enum, domains []model.Domain) (enumIDs, domainIDs []string, err error) {
	for _, e := range enums {
		id, err := putDef(store, func() ([]byte, error) { return enc.EncodeEnum(e) })
		if err != nil {
			return nil, nil, err
		}
		enumIDs = append(enumIDs, id)
	}
	for _, d := range domains {
		id, err := putDef(store, func() ([]byte, error) { return enc.EncodeDomain(d) })
		if err != nil {
			return nil, nil, err
		}
		domainIDs = append(domainIDs, id)
	}
	return enumIDs, domainIDs, nil
}

// createDefID resolves a whole-object create's def id: the post-state object from
// desired when present, else the op's own pointer def.
func createDefID(store *objstore.Store, op DDLOp, desired *model.Schema, key enc.Key) (string, error) {
	if desired != nil {
		if id, ok, err := putObjectByKey(store, desired, key); err != nil {
			return "", err
		} else if ok {
			return id, nil
		}
	}
	data, err := encodePointerDef(op)
	if err != nil {
		return "", err
	}
	return store.Put(data)
}

// encodePointerDef encodes a create op's own pointer def (used for embedded downs
// and objects absent from the post-state model).
func encodePointerDef(op DDLOp) ([]byte, error) {
	switch {
	case op.TableDef != nil:
		return enc.EncodeTable(*op.TableDef)
	case op.ViewDef != nil:
		return enc.EncodeView(*op.ViewDef)
	case op.MaterializedViewDef != nil:
		return enc.EncodeMaterializedView(*op.MaterializedViewDef)
	case op.SequenceDef != nil:
		return enc.EncodeSequence(*op.SequenceDef)
	case op.CompositeTypeDef != nil:
		return enc.EncodeCompositeType(*op.CompositeTypeDef)
	case op.DomainDef != nil:
		return enc.EncodeDomain(*op.DomainDef)
	case op.FunctionDef != nil:
		return enc.EncodeFunction(*op.FunctionDef)
	default:
		return nil, fmt.Errorf("migrate: shim: create op %q carries no pointer def and its object is absent from the post-state model", op.Op)
	}
}

// shimNestedModifier builds a nested-modifier body: the owning object's
// post-state def id (for simulation) plus the render source. create_trigger /
// create_policy WITH a def render from the stored def; every other nested op
// renders from the scalar delta via OpToSQL.
func shimNestedModifier(store *objstore.Store, op DDLOp, desired *model.Schema) (string, enc.Key, opBody, error) {
	owner := owningKeyForDelta(op)
	postID, _, err := maybePostID(store, desired, owner)
	if err != nil {
		return "", enc.Key{}, opBody{}, err
	}

	if op.Op == "create_trigger" && op.TriggerDef != nil {
		defID, err := putJSONDef(store, *op.TriggerDef)
		if err != nil {
			return "", enc.Key{}, opBody{}, err
		}
		schema, _ := splitQualifiedName(op.Table)
		return op.Op, owner, opBody{Schema: schema, Table: op.Table, Name: op.Name, PGVersion: op.PGVersion, DefID: defID, PostDefID: postID}, nil
	}
	if op.Op == "create_policy" && op.PolicyDef != nil {
		defID, err := putJSONDef(store, *op.PolicyDef)
		if err != nil {
			return "", enc.Key{}, opBody{}, err
		}
		schema, _ := splitQualifiedName(op.Table)
		return op.Op, owner, opBody{Schema: schema, Table: op.Table, Name: op.Name, PGVersion: op.PGVersion, DefID: defID, PostDefID: postID}, nil
	}
	return op.Op, owner, opBody{Delta: deltaOf(op), PostDefID: postID}, nil
}

// maybePostID stores the post-state object a key names and returns its id, or ""
// when the object is absent from desired (its owner was dropped in the same edge)
// — a graceful case: simulation leaves such a key untouched until a later drop
// removes it.
func maybePostID(store *objstore.Store, desired *model.Schema, key enc.Key) (string, bool, error) {
	if desired == nil {
		return "", false, nil
	}
	return putObjectByKey(store, desired, key)
}

// rawBlobBody stores an opaque pre-rendered SQL blob and returns a raw:<seq>
// pseudo-target body.
func rawBlobBody(store *objstore.Store, kind, sqlText string, seq int) (string, enc.Key, opBody, error) {
	blob, err := store.Put([]byte(sqlText))
	if err != nil {
		return "", enc.Key{}, opBody{}, err
	}
	return kind, enc.KeyForRaw(seq), opBody{BlobID: blob, Seq: seq}, nil
}

// rawMachineryBody renders a machinery op via OpToSQL and stores it as an opaque
// raw blob (manifest no-op) under a distinct self-contained kind.
func rawMachineryBody(store *objstore.Store, kind string, op DDLOp, seq int) (string, enc.Key, opBody, error) {
	return rawBlobBody(store, kind, OpToSQL(op), seq)
}
