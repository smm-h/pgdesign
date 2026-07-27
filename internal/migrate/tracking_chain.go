package migrate

// Chain-era tracking structures (roadmap 5.2, design/tracking_schema.sql).
//
// Three managed structures replace the single legacy pgdesign_migrations table:
//
//   - pgdesign_migration_ops      -- per-op journal (op identity, down-op, status)
//   - pgdesign_applied_migrations -- the "applied + status" VIEW (four readers)
//   - pgdesign_chain_position     -- this database's chain position (singleton)
//
// CreateTrackingStructures runs the exact reviewed DDL. It is called by BOTH
// `migrate upgrade` (the legacy-table adoption path, later subphase) and
// chain-mode apply on a FRESH database (no legacy table): apply creates the
// structures directly, no upgrade needed. The pre-upgrade guard (GuardNotPreUpgrade)
// fires only when the OLD table exists WITHOUT chain_position.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The three managed structures, verbatim per design/tracking_schema.sql. Executed
// together (single call) so the view's dependency on the ops table is satisfied.
const (
	createMigrationOpsDDL = `CREATE TABLE pgdesign_migration_ops (
    edge_id       text        NOT NULL,
    seq           integer     NOT NULL,
    phase         text        NOT NULL DEFAULT ''
                              CHECK (phase IN ('', 'expand', 'migrate', 'contract')),
    op_kind       text        NOT NULL,
    target        text        NOT NULL,
    invertibility text        NOT NULL
                              CHECK (invertibility IN ('mechanically-invertible', 'declared-inverse', 'non-invertible')),
    down_op       jsonb,
    status        text        NOT NULL
                              CHECK (status IN ('intended', 'confirmed')),
    intended_at   timestamptz NOT NULL DEFAULT now(),
    confirmed_at  timestamptz,
    version_label text        NOT NULL,
    description   text,
    checksum      text        NOT NULL,
    CONSTRAINT pgdesign_migration_ops_confirm_time
        CHECK ((status = 'confirmed') = (confirmed_at IS NOT NULL)),
    CONSTRAINT pgdesign_migration_ops_down_presence
        CHECK ((invertibility = 'non-invertible') = (down_op IS NULL)),
    PRIMARY KEY (edge_id, seq)
);`

	commentMigrationOpsDDL = `COMMENT ON TABLE pgdesign_migration_ops IS
    'pgdesign managed: per-op migration journal (op identity, serialized down-op, intent/confirm status).';`

	createAppliedViewDDL = `CREATE VIEW pgdesign_applied_migrations AS
    SELECT
        version_label            AS version,
        max(confirmed_at)        AS applied_at,
        description              AS description,
        checksum                 AS checksum
    FROM pgdesign_migration_ops
    GROUP BY edge_id, version_label, description, checksum
    HAVING bool_and(status = 'confirmed');`

	commentAppliedViewDDL = `COMMENT ON VIEW pgdesign_applied_migrations IS
    'pgdesign managed: applied migrations (fully-confirmed edges) with version, applied_at, description, checksum.';`

	createChainPositionDDL = `CREATE TABLE pgdesign_chain_position (
    id                boolean     PRIMARY KEY DEFAULT true CHECK (id),
    current_revision  text        NOT NULL,
    in_progress_edge  text,
    boundary_revision text        NOT NULL,
    boundary_kind     text        NOT NULL CHECK (boundary_kind IN ('upgrade', 'baseline')),
    codec_epoch       integer     NOT NULL,
    updated_at        timestamptz NOT NULL DEFAULT now()
);`

	commentChainPositionDDL = `COMMENT ON TABLE pgdesign_chain_position IS
    'pgdesign managed: this database''s chain position (current revision, in-progress edge, upgrade/baseline boundary).';`
)

// CreateTrackingStructures creates the three managed structures in tx. It runs
// the exact reviewed DDL; a failure leaves tx to roll them all back atomically.
func CreateTrackingStructures(ctx context.Context, tx pgx.Tx) error {
	for _, stmt := range []string{
		createMigrationOpsDDL, commentMigrationOpsDDL,
		createAppliedViewDDL, commentAppliedViewDDL,
		createChainPositionDDL, commentChainPositionDDL,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: create tracking structures: %w", err)
		}
	}
	return nil
}

// relationExists reports whether a relation (table or view) named rel exists in
// the current search path, via to_regclass (NULL when absent).
func relationExists(ctx context.Context, conn *pgx.Conn, rel string) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", rel).Scan(&exists); err != nil {
		return false, fmt.Errorf("migrate: probe relation %q: %w", rel, err)
	}
	return exists, nil
}

// ChainStructuresExist reports whether the chain-era tracking structures are
// present (probed via pgdesign_chain_position, the singleton position table).
func ChainStructuresExist(ctx context.Context, conn *pgx.Conn) (bool, error) {
	return relationExists(ctx, conn, "pgdesign_chain_position")
}

// LegacyTrackingExists reports whether the pre-upgrade pgdesign_migrations table
// is present.
func LegacyTrackingExists(ctx context.Context, conn *pgx.Conn) (bool, error) {
	return relationExists(ctx, conn, "pgdesign_migrations")
}

// GuardNotPreUpgrade is the shared pre-upgrade preflight for EVERY migrate
// subcommand that takes --db (roadmap 5.2). A PRE-UPGRADE database has the old
// pgdesign_migrations table AND lacks pgdesign_chain_position; running any
// subcommand against it is a hard error naming `migrate upgrade`. A fresh
// database (neither present) proceeds — apply creates the chain structures; a
// post-upgrade database (chain present) proceeds normally.
func GuardNotPreUpgrade(ctx context.Context, conn *pgx.Conn) error {
	chainOK, err := ChainStructuresExist(ctx, conn)
	if err != nil {
		return err
	}
	if chainOK {
		return nil
	}
	legacy, err := LegacyTrackingExists(ctx, conn)
	if err != nil {
		return err
	}
	if legacy {
		return fmt.Errorf("migrate: this database has a pre-upgrade tracking table (pgdesign_migrations) and no chain position — run `migrate upgrade` before any other migrate subcommand")
	}
	return nil
}

// chainPosition mirrors the pgdesign_chain_position singleton row.
type chainPosition struct {
	CurrentRevision  string
	InProgressEdge   *string
	BoundaryRevision string
	BoundaryKind     string
	CodecEpoch       int
}

// readChainPosition reads the singleton position row. The bool is false when the
// table exists but holds no row (never happens once apply seeds it, but the read
// is defensive).
func readChainPosition(ctx context.Context, conn *pgx.Conn) (chainPosition, bool, error) {
	var p chainPosition
	err := conn.QueryRow(ctx,
		"SELECT current_revision, in_progress_edge, boundary_revision, boundary_kind, codec_epoch FROM pgdesign_chain_position WHERE id = true").
		Scan(&p.CurrentRevision, &p.InProgressEdge, &p.BoundaryRevision, &p.BoundaryKind, &p.CodecEpoch)
	if err == pgx.ErrNoRows {
		return chainPosition{}, false, nil
	}
	if err != nil {
		return chainPosition{}, false, fmt.Errorf("migrate: read chain position: %w", err)
	}
	return p, true, nil
}

// insertChainPosition seeds the singleton position row (fresh apply). boundaryKind
// must be 'upgrade' or 'baseline'.
func insertChainPosition(ctx context.Context, tx pgx.Tx, p chainPosition) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO pgdesign_chain_position
		 (id, current_revision, in_progress_edge, boundary_revision, boundary_kind, codec_epoch)
		 VALUES (true, $1, $2, $3, $4, $5)`,
		p.CurrentRevision, p.InProgressEdge, p.BoundaryRevision, p.BoundaryKind, p.CodecEpoch)
	if err != nil {
		return fmt.Errorf("migrate: seed chain position: %w", err)
	}
	return nil
}

// setInProgressEdge marks (or clears, with edgeID nil) the mid-apply edge on the
// position row.
func setInProgressEdge(ctx context.Context, conn *pgx.Conn, edgeID *string) error {
	_, err := conn.Exec(ctx,
		"UPDATE pgdesign_chain_position SET in_progress_edge = $1, updated_at = now() WHERE id = true", edgeID)
	if err != nil {
		return fmt.Errorf("migrate: set in-progress edge: %w", err)
	}
	return nil
}

// advanceChainPosition advances current_revision to the edge target and clears
// in_progress_edge, in the edge's final transaction (roadmap 5.2/5.5: the edge's
// completion advances the position atomically with its final-op confirm).
func advanceChainPosition(ctx context.Context, tx pgx.Tx, currentRevision string) error {
	_, err := tx.Exec(ctx,
		"UPDATE pgdesign_chain_position SET current_revision = $1, in_progress_edge = NULL, updated_at = now() WHERE id = true",
		currentRevision)
	if err != nil {
		return fmt.Errorf("migrate: advance chain position: %w", err)
	}
	return nil
}

// journalRow is one pgdesign_migration_ops row. For 5.2 every op is journaled
// as 'confirmed' (the intent/confirm state machine for non-transactional ops is
// 5.5); downOp is nil exactly for non-invertible ops (CHECK-enforced).
type journalRow struct {
	EdgeID        string
	Seq           int
	Phase         string
	OpKind        string
	Target        string
	Invertibility string
	DownOp        *string // serialized {kind,target,invertibility,payload_id}; nil iff non-invertible
	VersionLabel  string
	Description   *string
	Checksum      string
}

// journalConfirmedOp writes a confirmed op row (status='confirmed', confirmed_at=now())
// via exec (a tx for transactional ops, the conn for non-transactional ops).
func journalConfirmedOp(ctx context.Context, exec sqlExecer, r journalRow) error {
	_, err := exec.Exec(ctx,
		`INSERT INTO pgdesign_migration_ops
		 (edge_id, seq, phase, op_kind, target, invertibility, down_op, status, confirmed_at, version_label, description, checksum)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'confirmed', now(), $8, $9, $10)`,
		r.EdgeID, r.Seq, r.Phase, r.OpKind, r.Target, r.Invertibility, r.DownOp,
		r.VersionLabel, r.Description, r.Checksum)
	if err != nil {
		return fmt.Errorf("migrate: journal op %s/%d (%s): %w", r.EdgeID, r.Seq, r.OpKind, err)
	}
	return nil
}
