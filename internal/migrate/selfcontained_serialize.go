package migrate

// Edge-file op serialization for self-contained ops (roadmap 5.1, edge_format.md).
//
// The on-disk op schema is EXACTLY the design's edge-file op entry
// (kind/target/payload_id/invertibility/down), so roadmap 5.2 wraps a slice of
// these into a full chain-edge file without reworking the op schema. The down
// field is a DERIVED CACHE of the up payload (edge_format.md TENSION 1):
// VerifyDown re-derives the down from the up op and rejects a stored down that
// does not match — corruption/tamper is caught at LOAD, before any apply.

import (
	"encoding/json"
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// OpJSON is the serialized edge-file op entry.
type OpJSON struct {
	Kind          string  `json:"kind"`
	Target        keyJSON `json:"target"`
	Invertibility string  `json:"invertibility"`
	PayloadID     string  `json:"payload_id"`
	Down          *OpJSON `json:"down,omitempty"`
}

// keyJSON is the serialized enc.Key (edge_format.md: the structured key so the
// manifest key round-trips exactly, overload signatures included, without
// re-parsing Key.String()).
type keyJSON struct {
	Kind   string `json:"kind"`
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name,omitempty"`
	ArgSig string `json:"arg_sig,omitempty"`
}

func keyToJSON(k enc.Key) keyJSON {
	return keyJSON{Kind: string(k.Kind), Schema: k.Schema, Name: k.Name, ArgSig: k.ArgSig}
}

func keyFromJSON(k keyJSON) enc.Key {
	return enc.Key{Kind: enc.Kind(k.Kind), Schema: k.Schema, Name: k.Name, ArgSig: k.ArgSig}
}

// Serialize projects a self-contained op to its edge-file op entry.
func (o SelfContainedOp) Serialize() OpJSON {
	j := OpJSON{
		Kind:          o.kind,
		Target:        keyToJSON(o.target),
		Invertibility: o.inv.String(),
		PayloadID:     o.payload,
	}
	if o.down != nil {
		d := o.down.Serialize()
		j.Down = &d
	}
	return j
}

// MarshalOp serializes an op to canonical JSON bytes (the edge-file op entry).
func MarshalOp(o SelfContainedOp) ([]byte, error) {
	return canonicalOpJSON(o.Serialize())
}

// parseInvClass parses the L4 class string back to its enum.
func parseInvClass(s string) (chain.InvertibilityClass, error) {
	switch s {
	case chain.MechanicallyInvertible.String():
		return chain.MechanicallyInvertible, nil
	case chain.DeclaredInverse.String():
		return chain.DeclaredInverse, nil
	case chain.NonInvertible.String():
		return chain.NonInvertible, nil
	default:
		return 0, fmt.Errorf("migrate: unknown invertibility class %q", s)
	}
}

// ParseOp reconstructs a self-contained op from its edge-file entry, RESOLVING
// its payload against the store. A payload id that does not resolve is a HARD
// ERROR: the op cannot render its true SQL, so it is unrepresentable — never a
// silent degraded op. The down reference is parsed as a cache; callers verify
// it against the up payload via VerifyDown.
func ParseOp(store *objstore.Store, j OpJSON) (SelfContainedOp, error) {
	inv, err := parseInvClass(j.Invertibility)
	if err != nil {
		return SelfContainedOp{}, err
	}
	if _, err := store.Get(j.PayloadID); err != nil {
		return SelfContainedOp{}, fmt.Errorf("migrate: op %q payload %s does not resolve: %w", j.Kind, j.PayloadID, err)
	}
	op := SelfContainedOp{
		kind:    j.Kind,
		target:  keyFromJSON(j.Target),
		inv:     inv,
		payload: j.PayloadID,
	}
	if j.Down != nil {
		down, err := ParseOp(store, *j.Down)
		if err != nil {
			return SelfContainedOp{}, fmt.Errorf("migrate: op %q down: %w", j.Kind, err)
		}
		op.down = &down
	}
	return op, nil
}

// UnmarshalOp parses an op from canonical JSON bytes and resolves its payload.
func UnmarshalOp(store *objstore.Store, data []byte) (SelfContainedOp, error) {
	var j OpJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return SelfContainedOp{}, fmt.Errorf("migrate: decode op entry: %w", err)
	}
	return ParseOp(store, j)
}

// VerifyDown is the LOAD-time down-cache verifier (edge_format.md TENSION 1,
// amendment A3). The edge-file down is never independently trusted: it must be a
// pure function of the up payload. VerifyDown re-derives the down from the up
// op (structurally for mechanically-invertible ops; from the inverse embedded in
// the up payload for declared-inverse ops) and asserts the re-derivation matches
// the stored down on every identity facet (kind, target, payload id). A mismatch
// is a HARD ERROR — corruption or tamper is caught at read time, before any
// apply, and never fed to rollback. Non-invertible ops must carry no down.
//
// Roadmap 5.2's edge reader calls VerifyDown on every op as it loads a chain
// edge.
func VerifyDown(store *objstore.Store, up SelfContainedOp) error {
	body, err := loadBody(store, up.payload)
	if err != nil {
		return err
	}
	derived, err := deriveDown(store, up, body)
	if err != nil {
		return fmt.Errorf("migrate: re-deriving down for %q: %w", up.kind, err)
	}
	switch {
	case derived == nil && up.down == nil:
		return nil
	case derived == nil && up.down != nil:
		return fmt.Errorf("migrate: op %q is non-invertible but carries a down (%q)", up.kind, up.down.kind)
	case derived != nil && up.down == nil:
		return fmt.Errorf("migrate: op %q is invertible but carries no down cache", up.kind)
	}
	if derived.kind != up.down.kind ||
		derived.target.String() != up.down.target.String() ||
		derived.payload != up.down.payload {
		return fmt.Errorf("migrate: op %q down cache does not match its re-derivation "+
			"(cached kind=%q target=%q payload=%s; derived kind=%q target=%q payload=%s) — corruption or tamper",
			up.kind, up.down.kind, up.down.target.String(), up.down.payload,
			derived.kind, derived.target.String(), derived.payload)
	}
	return nil
}
