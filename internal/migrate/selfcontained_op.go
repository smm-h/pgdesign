package migrate

// Self-contained migration ops (roadmap 5.1, laws L1+L2).
//
// A DDLOp today carries LOSSY MIRRORS: pointer-def fields (*model.Table, ...),
// RawSQL bodies, and partition/partman parameters that never survive a
// round-trip through disk, so a migration parsed from a file renders empty, the
// WRONG object (deny-mutation / append-only), or a "-- unknown op" comment. A
// SelfContainedOp fixes this by construction: its ENTIRE payload lives in the
// content-addressed object store (internal/objstore), referenced by content id.
// A pointer-def op references its target object AND the transitive
// composition-closure of type definitions (enums/domains a table's columns use)
// BY CONTENT ID; RawSQL/DML bodies are content-addressed opaque blobs. There is
// no degraded form: BuildOp REQUIRES the store payload, and ParseOp fails HARD
// if a payload id does not resolve. Rendering resolves the payload back to the
// def and honors the op's recorded PGVersion UNIFORMLY (no hardcoded zero).
//
// The on-disk SERIALIZATION is exactly the design's edge-file op schema
// (edge_format.md: kind/target/payload_id/invertibility/down), so roadmap 5.2
// wraps it into full chain-edge files without reworking the op schema.
//
// SelfContainedOp implements chain.Op, so the kernel reasons about these ops
// abstractly (edge identity, invertibility, manifest simulation) without
// importing migrate.

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// SelfContainedOp is a migration op whose full payload lives in the object
// store, referenced by content id. It implements chain.Op.
type SelfContainedOp struct {
	kind    string
	target  enc.Key
	inv     chain.InvertibilityClass
	payload string           // objstore content id of the op body
	down    *SelfContainedOp // resolved down reference (nil for non-invertible)
}

// Kind is the op-family name.
func (o SelfContainedOp) Kind() string { return o.kind }

// Target is the kind-qualified manifest key of the object the op acts on (a
// dml/raw pseudo-target for data/opaque-SQL ops).
func (o SelfContainedOp) Target() enc.Key { return o.target }

// Invertibility is the op's L4 class.
func (o SelfContainedOp) Invertibility() chain.InvertibilityClass { return o.inv }

// PayloadID is the objstore content id of the op body.
func (o SelfContainedOp) PayloadID() string { return o.payload }

// Inverse returns the op's down and true when it is mechanically-invertible or
// declared-inverse; (nil, false) exactly when non-invertible.
func (o SelfContainedOp) Inverse() (chain.Op, bool) {
	if o.inv == chain.NonInvertible || o.down == nil {
		return nil, false
	}
	return *o.down, true
}

// opBody is the canonical, content-addressed body of a self-contained op. It is
// stored in the object store as canonical JSON; the edge file references it by
// content id (payload_id) and never inlines it. Pointer-def payloads reference
// their def object (DefID) and, for create_table, the transitive enum/domain
// closure (EnumIDs/DomainIDs). RawSQL/DML bodies reference an opaque SQL blob
// (BlobID). Declared-inverse ops embed their inverse body (Down) so the down is
// a pure function of the up payload and edge identity stays total
// (edge_format.md TENSION 1).
type opBody struct {
	Kind        string   `json:"kind"`
	Schema      string   `json:"schema,omitempty"`
	Table       string   `json:"table,omitempty"` // owning table (triggers/policies)
	Name        string   `json:"name,omitempty"`
	PGVersion   int      `json:"pg_version,omitempty"`
	Replace     bool     `json:"replace,omitempty"`      // create_or_replace_{view,function}
	DefID       string   `json:"def_id,omitempty"`       // encoded def object id
	EnumIDs     []string `json:"enum_ids,omitempty"`     // create_table type closure
	DomainIDs   []string `json:"domain_ids,omitempty"`   // create_table type closure
	ParentTable string   `json:"parent_table,omitempty"` // create_partition parent
	BlobID      string   `json:"blob_id,omitempty"`      // raw/dml opaque SQL blob id
	Seq         int      `json:"seq,omitempty"`          // edge-seq for dml/raw pseudo-targets
	Down        *opBody  `json:"down,omitempty"`         // embedded inverse (declared-inverse)
}

// canonicalJSON marshals v with the same byte discipline as enc/rev/chain:
// compact, HTML escaping disabled, trailing newline stripped. Struct fields
// encode in declaration order and map keys sort, so the bytes are deterministic
// and content-addressing is stable.
func canonicalOpJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// putBody stores an op body and returns its content id.
func putBody(store *objstore.Store, b opBody) (string, error) {
	data, err := canonicalOpJSON(b)
	if err != nil {
		return "", fmt.Errorf("migrate: encode op body: %w", err)
	}
	return store.Put(data)
}

// loadBody resolves an op body by content id. A missing payload is a HARD
// ERROR: a self-contained op whose payload does not resolve cannot render its
// true SQL and is therefore unrepresentable.
func loadBody(store *objstore.Store, id string) (opBody, error) {
	data, err := store.Get(id)
	if err != nil {
		return opBody{}, fmt.Errorf("migrate: op payload %s does not resolve: %w", id, err)
	}
	var b opBody
	if err := json.Unmarshal(data, &b); err != nil {
		return opBody{}, fmt.Errorf("migrate: decode op payload %s: %w", id, err)
	}
	return b, nil
}

// invClassForKind is the DECLARED L4 invertibility of each op family (roadmap
// L4's three-way typing). "create" ops whose inverse is a structural "drop" are
// mechanically-invertible; "or_replace" and "alter" ops need the prior state
// and are declared-inverse; RawSQL/partman ops carry a recorded inverse and are
// declared-inverse; DML inverses are vacuous (declared-inverse).
func invClassForKind(kind string) chain.InvertibilityClass {
	switch kind {
	case "create_table", "create_view", "create_materialized_view",
		"create_sequence", "create_composite_type", "create_domain",
		"create_function", "create_trigger", "create_policy",
		"create_partition":
		return chain.MechanicallyInvertible
	case "create_or_replace_view", "create_or_replace_function",
		"alter_sequence",
		// RawSQL-bodied families carry a recorded inverse.
		"create_sm_trigger_function", "create_sm_trigger",
		"create_partman_parent", "update_partman_retention",
		"update_partman_premake",
		// DML inverses are vacuous (data is not restored).
		"backfill", "transform":
		return chain.DeclaredInverse
	default:
		// Down/leaf ops (drop_*, etc.) carry no further recorded inverse here;
		// their reversal is the journal's job (roadmap 5.6), not the edge's.
		return chain.NonInvertible
	}
}

// rawTargeted reports whether a kind's target is a raw:<seq> pseudo-key (its
// body is an opaque SQL blob) rather than a schema-object manifest key.
func rawTargeted(kind string) bool {
	switch kind {
	case "create_sm_trigger_function", "create_sm_trigger",
		"create_partman_parent", "update_partman_retention",
		"update_partman_premake":
		return true
	default:
		return false
	}
}

// dmlTargeted reports whether a kind's target is a dml:<seq> pseudo-key.
func dmlTargeted(kind string) bool {
	return kind == "backfill" || kind == "transform"
}

// dropKindFor returns the mechanical-inverse op kind for a create op.
func dropKindFor(kind string) string {
	switch kind {
	case "create_table", "create_partition":
		return "drop_table"
	case "create_view":
		return "drop_view"
	case "create_materialized_view":
		return "drop_materialized_view"
	case "create_sequence":
		return "drop_sequence"
	case "create_composite_type":
		return "drop_composite_type"
	case "create_domain":
		return "drop_domain"
	case "create_function":
		return "drop_function"
	case "create_trigger":
		return "drop_trigger"
	case "create_policy":
		return "drop_policy"
	default:
		return ""
	}
}
