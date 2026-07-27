package enc

import "github.com/smm-h/pgdesign/internal/semtype"

// The registry snapshot is the channel for semtype registry state that has NO
// representation in the model-level collections. After the state-machine
// escape-hatch fix (roadmap 1.1), there is NO such residue: every
// identity-bearing piece of registry state now has a first-class model home —
//
//   - enum values          -> model.Enum.Values
//   - composite fields      -> model.CompositeType.Fields
//   - scalar-type CHECKs     -> model.Domain.Check (builtin-derived too, so a
//     change to the builtin email regex flips identity via Domain.Check with
//     no special case)
//   - state-machine graphs   -> model.StateMachine (KindSMType objects): states
//     with comments/terminal, transitions with comments/requires, initial
//     state, enforce-trigger flag, and type comment — the full identity
//     content, including the nested transition comments that previously had no
//     model home.
//
// Consequently the snapshot is GENUINELY EMPTY for identity over ALL models,
// including state-machine-bearing ones — verified by
// TestRegistrySnapshotEmptyForFlatModels (now unconditional). It is NOT an
// identity input: EncodeObjects does not include it, and identity never
// consumes it. The channel is retained for its import-surface reconstruction
// role (roadmap 7.2 vendors the transitive type-definition closure as
// first-class KindSMType/enum/domain/composite objects, so import surfaces are
// reconstructed from those objects; this snapshot remains the designated home
// for any FUTURE registry state that lacks a model home). If a future check
// finds such state, the fix is to add it to the model — as the SM fix did —
// not to widen identity through this snapshot.

type registrySnapshotForm struct {
	Codec int  `json:"codec"`
	Kind  Kind `json:"kind"`
}

func registrySnapshotToForm(reg *semtype.Registry) registrySnapshotForm {
	// No residue: all identity-bearing registry state has a model home. The
	// registry argument is retained for the import-surface reconstruction role
	// and so this signature is stable when a future no-model-home residue field
	// is added.
	_ = reg
	return registrySnapshotForm{Codec: CodecVersion, Kind: KindRegistrySnap}
}

// EncodeRegistrySnapshot returns the canonical bytes for the registry residue
// channel (see the package comment above). It is empty for every registry and
// is NOT part of schema identity.
func EncodeRegistrySnapshot(reg *semtype.Registry) ([]byte, error) {
	return canonicalJSON(registrySnapshotToForm(reg))
}

// RegistrySnapshotEmpty reports whether the registry snapshot contributes
// nothing to identity. It is unconditionally true: all identity-bearing
// registry state has a model home, so there is no residue for ANY registry —
// including state-machine-bearing ones. This is the escape-hatch invariant the
// verification tests assert.
func RegistrySnapshotEmpty(reg *semtype.Registry) bool {
	_ = reg
	return true
}
