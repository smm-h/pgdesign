package migrate

// Generate-to-edge (roadmap 5.2, item 1).
//
// GenerateEdge converts a generated Migration (a diff lowered to DDL/DML ops)
// into a self-contained chain edge and writes it — with its object payloads and
// its to-revision manifest — into the chain project. This is the on-disk-chain
// replacement for WriteMigrationFile's semver TOML: one content-addressed edge in
// migrations/chain/, its objects in migrations/objects/, and the to-revision
// manifest in migrations/revisions/.
//
// The from-revision is the CURRENT CHAIN HEAD; a genesis edge (null parent) is
// produced when the chain is empty (prev == nil). The to-revision is rev.Compute
// over the desired POST-STATE model. Each DDL op converts through the 5.1b shim
// (DDLOpToSelfContained); each DML op converts through BuildDMLOp (opaque SQL
// blob, dml:<seq> pseudo-target). A SCHEMA-META op is prepended whenever the
// schema-global header (extensions / pg_version / groups) differs between the
// prev and desired models — this covers the manifest's schema:<name> entry so
// endpoint simulation reproduces the to-manifest exactly.
//
// The zero-op guard is preserved: a migration with no DDL and no DML ops is a
// hard error (generate never mints an empty edge). The schema-meta op does not
// count toward the guard — an edge that only re-stamps the schema header with no
// real change is not a migration.

import (
	"bytes"
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// ErrNoEdgeOps is returned by GenerateEdge when the migration lowers to zero DDL
// and zero DML ops (the zero-op guard). Callers treat it as "nothing to
// generate", mirroring the semver path's no-op behavior.
var ErrNoEdgeOps = fmt.Errorf("migrate: migration has no operations; nothing to generate")

// GenerateEdge builds and writes the chain edge for migration m against the
// desired POST-STATE model. parent is the current chain-head revision (zero for a
// genesis edge); prev is the model AT that head (nil for genesis). class is the
// endpoints' model class. slug is the edge's human display name. It returns the
// written edge filename.
func GenerateEdge(p *ChainProject, m *Migration, desired *model.Schema, prev *model.Schema, parent rev.Revision, class rev.ModelClass, slug string) (string, error) {
	if len(m.DDLOps) == 0 && len(m.DMLOps) == 0 {
		return "", ErrNoEdgeOps
	}
	if !validModelClass(class) {
		return "", fmt.Errorf("migrate: generate edge: unknown model class %q", class)
	}
	store := p.Store()

	// To-manifest + object payloads for the whole desired model (this also seeds
	// the store with every object the manifest and the ops reference).
	to, err := chain.BuildManifestInto(desired, store)
	if err != nil {
		return "", fmt.Errorf("migrate: generate edge: build manifest: %w", err)
	}
	target, err := rev.Compute(desired, class)
	if err != nil {
		return "", fmt.Errorf("migrate: generate edge: compute revision: %w", err)
	}
	if parent.IsZero() != (prev == nil) {
		return "", fmt.Errorf("migrate: generate edge: parent revision and prev model must both be set or both be genesis")
	}

	var ops []SelfContainedOp
	seq := 0

	// Prepend a schema-meta op when the schema header changed (always for genesis).
	metaChanged, err := schemaMetaChanged(prev, desired)
	if err != nil {
		return "", err
	}
	if metaChanged {
		metaOp, err := BuildSchemaMeta(store, desired, prev)
		if err != nil {
			return "", fmt.Errorf("migrate: generate edge: schema-meta op: %w", err)
		}
		ops = append(ops, metaOp)
		seq++
	}

	for _, op := range m.DDLOps {
		sc, err := DDLOpToSelfContained(store, op, desired, seq)
		if err != nil {
			return "", fmt.Errorf("migrate: generate edge: op %d (%s): %w", seq, op.Op, err)
		}
		ops = append(ops, sc)
		seq++
	}
	for _, op := range m.DMLOps {
		sc, err := BuildDMLOp(store, op.Op, seq, op.SQL, seq, dmlDownSQL(op))
		if err != nil {
			return "", fmt.Errorf("migrate: generate edge: dml op %d (%s): %w", seq, op.Op, err)
		}
		ops = append(ops, sc)
		seq++
	}

	e := Edge{
		Parent: parent,
		Target: target,
		Slug:   slug,
		Class:  class,
		Ops:    ops,
	}

	if err := p.WriteRevisionManifest(target, class, to); err != nil {
		return "", err
	}
	return p.WriteEdge(e)
}

// schemaMetaChanged reports whether the schema-global header (name, extensions,
// pg_version, groups — everything EncodeSchemaMeta captures) differs between prev
// and desired. A nil prev (genesis) is always a change.
func schemaMetaChanged(prev, desired *model.Schema) (bool, error) {
	if prev == nil {
		return true, nil
	}
	pb, err := enc.EncodeSchemaMeta(prev)
	if err != nil {
		return false, fmt.Errorf("migrate: generate edge: encode prev schema meta: %w", err)
	}
	db, err := enc.EncodeSchemaMeta(desired)
	if err != nil {
		return false, fmt.Errorf("migrate: generate edge: encode desired schema meta: %w", err)
	}
	return !bytes.Equal(pb, db), nil
}

// dmlDownSQL returns a DML op's reverse SQL. Generate records DML downs as
// structural DDLOps (or Irreversible), never as reverse-DML text, so the DML
// inverse is VACUOUS today (data is not restored) — a marker preserves that
// semantics explicitly. When 5.6's journal-driven rollback records real reverse
// DML this becomes the extraction point.
func dmlDownSQL(op DMLOp) string {
	return "-- vacuous DML inverse: data is not restored"
}
