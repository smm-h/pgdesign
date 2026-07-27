-- pgdesign phase-5 tracking schema (design gate 5.0, reviewed fixture).
--
-- Three managed structures replace today's single pgdesign_migrations table.
-- Rationale, the applied_at derivation, and the intent/confirm state machine
-- are in tracking_schema.md. These objects are MANAGED: introspection filters
-- them (0.4 predicate), so upgrade's own reconcile does not refuse itself on the
-- tables it creates in the same transaction (5.2).
--
-- All three are created inside the single `migrate upgrade` transaction (5.2),
-- after which the old pgdesign_migrations table is dropped.

-- 1. pgdesign_migration_ops -- the per-op journal.
--
-- Op identity is (edge_id, seq). Edge-level attributes (version_label,
-- description, checksum) are functionally dependent on edge_id and stored on
-- every op row: this is a deliberate denormalization (see the TENSION note in
-- README.md and tracking_schema.md) that lets the VIEW below be built from this
-- one table, honoring the roadmap's THREE-STRUCTURE [%%] naming without a fourth
-- per-edge table. The columns the roadmap enumerates for this table (migration
-- ref, phase, sequence, op kind, target, down-op, intent/confirm status) are all
-- present; the edge-level trio is the minimum addition the view provably needs.
CREATE TABLE pgdesign_migration_ops (
    edge_id       text        NOT NULL,          -- migration ref: chain.Edge.ID() (content hash)
    seq           integer     NOT NULL,          -- op position in the edge's op-list (0-based, apply order)
    phase         text        NOT NULL DEFAULT '' -- '' | 'expand' | 'migrate' | 'contract'
                              CHECK (phase IN ('', 'expand', 'migrate', 'contract')),
    op_kind       text        NOT NULL,          -- op family, e.g. 'create_table', 'add_column', 'dml'
    target        text        NOT NULL,          -- enc.Key.String() of the op's target (pseudo-key for dml/raw)
    invertibility text        NOT NULL           -- L4 class of the UP op
                              CHECK (invertibility IN ('mechanically-invertible', 'declared-inverse', 'non-invertible')),
    down_op       jsonb,                         -- serialized down-op {kind,target,invertibility,payload_id}; NULL iff non-invertible

    -- intent/confirm protocol (L8). See the state machine in tracking_schema.md.
    status        text        NOT NULL           -- 'intended' (written before a non-transactional op) | 'confirmed'
                              CHECK (status IN ('intended', 'confirmed')),
    intended_at   timestamptz NOT NULL DEFAULT now(),
    confirmed_at  timestamptz,                   -- NULL until confirmed; MAX over an edge = edge-completion time

    -- edge-level attributes (denormalized; constant per edge_id).
    version_label text        NOT NULL,          -- the VIEW's "version": semver for prefix rows; edge_id for post-upgrade edges
    description   text,                          -- edge slug (post-upgrade) or preserved old description (prefix)
    checksum      text        NOT NULL,          -- edge-file checksum (post-upgrade) or preserved old checksum (prefix)

    -- integrity: confirmed rows must carry a confirm time; intended rows must not.
    CONSTRAINT pgdesign_migration_ops_confirm_time
        CHECK ((status = 'confirmed') = (confirmed_at IS NOT NULL)),
    -- integrity: non-invertible ops have no down; all others do.
    CONSTRAINT pgdesign_migration_ops_down_presence
        CHECK ((invertibility = 'non-invertible') = (down_op IS NULL)),

    PRIMARY KEY (edge_id, seq)
);

COMMENT ON TABLE pgdesign_migration_ops IS
    'pgdesign managed: per-op migration journal (op identity, serialized down-op, intent/confirm status).';

-- 2. pgdesign_applied_migrations -- the VIEW (one SQL definition of
--    "applied + status" for four readers: serve, status, AppliedVersions,
--    the upgrade ASSERT step). An edge is APPLIED iff every one of its op rows
--    is confirmed (bool_and); applied_at is the edge-completion time
--    (max confirmed_at = the final op's confirm). Partially-applied (in-progress)
--    edges are excluded by the HAVING clause.
--
--    applied_at derivation:
--      * post-upgrade edges: max(confirmed_at) = the edge's final op confirm time.
--      * prefix rows: `migrate upgrade` inserts ONE synthetic confirmed op per
--        old row with confirmed_at := old.applied_at and checksum := old.checksum,
--        so max(confirmed_at) = old.applied_at VERBATIM and checksum is verbatim.
--        The view needs no prefix special-case -- verbatim preservation happens
--        at fold time, not in the view. This is what lets the upgrade's
--        ASSERT-view-reproduces-snapshot step pass on its own columns.
CREATE VIEW pgdesign_applied_migrations AS
    SELECT
        version_label            AS version,
        max(confirmed_at)        AS applied_at,
        description              AS description,
        checksum                 AS checksum
    FROM pgdesign_migration_ops
    GROUP BY edge_id, version_label, description, checksum
    HAVING bool_and(status = 'confirmed');

COMMENT ON VIEW pgdesign_applied_migrations IS
    'pgdesign managed: applied migrations (fully-confirmed edges) with version, applied_at, description, checksum.';

-- 3. pgdesign_chain_position -- the per-database position (singleton row).
--    "per-database" is structural: this table lives IN the database, so it holds
--    exactly one row for THIS database (the id-boolean CHECK enforces the
--    singleton). The rebase revision-remap table is NOT here -- it is a
--    REBASE-ONLY on-disk chain artifact (store_layout.md); apply consults it to
--    serve a database whose current_revision was rebased away.
CREATE TABLE pgdesign_chain_position (
    id                boolean     PRIMARY KEY DEFAULT true CHECK (id),   -- singleton guard
    current_revision  text        NOT NULL,   -- the revision this DB is at (class:hex); advances with each edge's final-op confirm
    in_progress_edge  text,                   -- edge_id mid-apply; NULL when idle
    boundary_revision text        NOT NULL,   -- upgrade/baseline floor; rollback refuses to cross below it (5.6)
    boundary_kind     text        NOT NULL CHECK (boundary_kind IN ('upgrade', 'baseline')),
    codec_epoch       integer     NOT NULL,   -- the codec epoch of this DB's chain (mixed-epoch guard)
    updated_at        timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE pgdesign_chain_position IS
    'pgdesign managed: this database''s chain position (current revision, in-progress edge, upgrade/baseline boundary).';
