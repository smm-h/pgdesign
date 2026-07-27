package migrate

// One-time legacy -> chain upgrade (roadmap 5.2, item `migrate upgrade`).
//
// A pre-upgrade database has the single legacy pgdesign_migrations table and no
// chain position. `migrate upgrade` adopts it onto the on-disk chain in a
// VERIFY-THEN-STAMP choreography:
//
//  1. Preflight (pure, no writes): refuse if already upgraded (chain_position
//     exists); refuse if there is nothing to upgrade (no legacy table — fresh
//     databases just use apply); refuse a dirty working tree for the schema files
//     when inside a git repo (a mid-edit TOML must not stamp a boundary).
//  2. Advisory lock (THE shared session lock, held for the whole upgrade) so a
//     concurrent apply/rollback/baseline cannot interleave.
//  3. RECONCILE TOML<->DB: the caller's introspected `actual` model is diffed
//     against the desired TOML model via DiffLive (with the live round-trip
//     normalizer). A non-empty diff is a REFUSAL carrying the drift report —
//     adoption of a genuinely drifted database is baseline's job (5.10), not the
//     upgrade's. The three managed tables (and the legacy one) are introspect-
//     filtered by the pgdesign_ prefix, so the reconcile never flags them.
//  4. writeChainFiles: fold the reconciled model into the on-disk chain BEFORE
//     the DB transaction (content-addressed, idempotent — a re-run writes byte-
//     identical files). VerifyChainConsistency then proves the written store is
//     Merkle-closed, endpoint-consistent, and epoch-homogeneous.
//  5. CHECKSUM AMNESTY: each legacy row's recorded checksum is compared against
//     its on-disk semver file's current bytes; mismatches produce a NAMED report
//     (historical post-apply edits are a known legitimate state — the fold
//     proceeds by content, the report preserves the evidence, never silent,
//     never blocking).
//  6. runUpgradeTxn: ONE transaction — snapshot the old applied set, create the
//     three managed structures, fold one synthetic confirmed op per old row
//     (version_label/description/applied_at/checksum VERBATIM), ASSERT the view
//     reproduces the snapshot exactly, DROP the legacy table, stamp
//     chain_position with the per-database upgrade boundary, then COMMIT (the
//     sole commit point). A crash before COMMIT rolls the whole transaction back
//     (PG rolls back on disconnect); the files already landed idempotently, so a
//     re-run completes. A crash after COMMIT leaves chain_position present, so a
//     re-run is a no-op ("already upgraded").
//
// The writeChainFiles / runUpgradeTxn split IS the crash-injection seam; hooks
// carry a BeforeCommit test seam.
//
// PREFIX REPRESENTATION (design decision — flagged in the tranche report): the
// on-disk prefix is a SINGLE genesis edge from empty to the reconciled model's
// revision (rN). A per-old-row LINEAR chain of real intermediate edges is not
// reconstructable from semver TOML files (WriteMigrationFile never serialized the
// structured object defs, so DDLOpToSelfContained on a parsed legacy op hard-
// errors on every whole-object create; and post-content-identity the head MUST be
// the registry-present TOML model so post-upgrade generation produces deltas, not
// a full-schema re-create). The per-old-row DISTINCTNESS the view requires (A1)
// lives in the DB journal — one synthetic op per row with a distinct synthetic
// edge_id — decoupled from the single on-disk genesis edge. Because the reconcile
// gate refuses any database not at the reconciled model, every upgraded database
// is at rN; the boundary is a verified fact.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// AmnestyEntry names one legacy migration file whose current bytes no longer
// hash to the checksum the database recorded when it was applied (a historical
// post-apply edit). The fold proceeds by content; this preserves the evidence.
type AmnestyEntry struct {
	File     string // migration file path
	Recorded string // checksum stored in pgdesign_migrations
	Actual   string // checksum of the file's current bytes
}

// UpgradeReport summarizes an upgrade for the CLI/tests.
type UpgradeReport struct {
	AlreadyUpgraded bool           // true when the database was already on the chain (no-op)
	PrefixEdgeFile  string         // the written genesis prefix edge filename
	Boundary        string         // the stamped boundary revision (rN)
	PrefixRows      int            // number of legacy rows folded into the journal
	Amnesty         []AmnestyEntry // checksum-mismatch report (never blocking)
}

// UpgradeHooks carries optional test seams. BeforeCommit runs inside
// runUpgradeTxn after the fold + assert + drop, immediately before COMMIT; a
// non-nil error aborts the transaction (the in-process crash-before-commit
// equivalent — PG rolls back on disconnect). It is nil in production.
type UpgradeHooks struct {
	BeforeCommit func() error
}

// legacyRow mirrors a pgdesign_migrations row (the snapshot the view must
// reproduce). Description is nullable in the legacy schema.
type legacyRow struct {
	Version     string
	AppliedAt   time.Time
	Description *string
	Checksum    string
}

// Upgrade runs the one-time legacy -> chain upgrade against conn. desired is the
// TOML model (pg_version already resolved); actual is the caller's introspected
// model of the same database; ln is the live round-trip normalizer (may be nil).
// migrationsDir is the on-disk chain/migrations root; schemaFiles are the schema
// TOML paths guarded for a clean working tree. hooks may be nil.
func Upgrade(ctx context.Context, conn *pgx.Conn, p *ChainProject, desired, actual *model.Schema, ln diff.LiveNormalizer, migrationsDir string, schemaFiles []string, hooks *UpgradeHooks) (*UpgradeReport, error) {
	if hooks == nil {
		hooks = &UpgradeHooks{}
	}

	// Preflight (pure): already-upgraded is a clean no-op; no-legacy is a hard
	// error pointing at the fresh-apply path.
	if exists, err := ChainStructuresExist(ctx, conn); err != nil {
		return nil, err
	} else if exists {
		return &UpgradeReport{AlreadyUpgraded: true}, nil
	}
	if legacy, err := LegacyTrackingExists(ctx, conn); err != nil {
		return nil, err
	} else if !legacy {
		return nil, fmt.Errorf("migrate upgrade: no legacy pgdesign_migrations table found — this database has never used pgdesign migrations; a fresh database uses `migrate apply` directly (nothing to upgrade)")
	}

	// Dirty-tree guard: a mid-edit schema TOML must not stamp a boundary.
	if err := checkCleanSchemaFiles(schemaFiles); err != nil {
		return nil, err
	}

	// THE shared session advisory lock, held for the whole upgrade so a
	// concurrent apply/rollback/baseline cannot interleave.
	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("migrate upgrade: another migration operation is in progress (could not acquire the advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	// Re-check under the lock (a racing upgrade may have won).
	if exists, err := ChainStructuresExist(ctx, conn); err != nil {
		return nil, err
	} else if exists {
		return &UpgradeReport{AlreadyUpgraded: true}, nil
	}

	// RECONCILE: TOML must match the live database exactly, else refuse with the
	// drift report (adoption of drift is baseline's job, 5.10).
	d := diff.DiffLive(desired, actual, ln)
	if !d.IsEmpty() {
		return nil, fmt.Errorf("migrate upgrade: the schema TOML does not match the live database — refusing to stamp a boundary over drift. Reconcile the schema, or adopt the drift with `migrate baseline` (5.10).\n\nDrift:\n%s", diff.FormatTerminal(d))
	}

	// writeChainFiles: fold the reconciled model into the on-disk chain BEFORE the
	// DB transaction. Idempotent (content-addressed).
	edgeFile, target, err := writeChainFiles(p, desired, extregistry.NewBuiltinRegistry())
	if err != nil {
		return nil, err
	}
	if err := VerifyChainConsistency(p); err != nil {
		return nil, fmt.Errorf("migrate upgrade: chain consistency check failed after writing prefix files: %w", err)
	}

	// CHECKSUM AMNESTY (informational; never blocks).
	amnesty, err := computeAmnesty(ctx, conn, migrationsDir)
	if err != nil {
		return nil, err
	}

	// Snapshot the old applied set (the fold source of truth and the assert oracle).
	snapshot, err := snapshotLegacy(ctx, conn)
	if err != nil {
		return nil, err
	}

	// runUpgradeTxn: the one transaction (sole commit point).
	if err := runUpgradeTxn(ctx, conn, target, snapshot, hooks); err != nil {
		return nil, err
	}

	return &UpgradeReport{
		PrefixEdgeFile: edgeFile,
		Boundary:       target.String(),
		PrefixRows:     len(snapshot),
		Amnesty:        amnesty,
	}, nil
}

// writeChainFiles folds the reconciled model into a single genesis prefix edge
// (empty -> rN) plus its objects and to-revision manifest, and returns the edge
// filename and the target revision rN. It is idempotent: an identical model
// yields byte-identical files (content-addressed writes). A model that lowers to
// zero ops (an empty schema) is a hard error — there is nothing to fold.
func writeChainFiles(p *ChainProject, desired *model.Schema, extReg *extregistry.Registry) (string, rev.Revision, error) {
	base := &model.Schema{Name: desired.Name, PGVersion: desired.PGVersion}
	if collErr := diff.CheckTruncationCollisions(desired); collErr != nil {
		return "", rev.Revision{}, fmt.Errorf("migrate upgrade: %w", collErr)
	}
	d := diff.Diff(desired, base)
	if d.IsEmpty() {
		return "", rev.Revision{}, fmt.Errorf("migrate upgrade: the reconciled schema is empty; there is nothing to fold into a prefix edge")
	}
	m, _ := GenerateMigration(d, desired, "", nil, 0, 0, extReg)
	if len(m.DDLOps) == 0 && len(m.DMLOps) == 0 {
		return "", rev.Revision{}, fmt.Errorf("migrate upgrade: the reconciled schema produced no operations; there is nothing to fold into a prefix edge")
	}
	name, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "upgrade-prefix")
	if err != nil {
		return "", rev.Revision{}, fmt.Errorf("migrate upgrade: write prefix edge: %w", err)
	}
	target, err := rev.Compute(desired, rev.RegistryPresent)
	if err != nil {
		return "", rev.Revision{}, fmt.Errorf("migrate upgrade: compute boundary revision: %w", err)
	}
	return name, target, nil
}

// runUpgradeTxn is the sole commit point. In ONE transaction it snapshots-in the
// old applied set (already read as `snapshot`), creates the three managed
// structures, folds one synthetic confirmed op per old row, ASSERTS the view
// reproduces the snapshot exactly, drops the legacy table, and stamps the
// per-database upgrade boundary. The BeforeCommit hook fires just before COMMIT.
func runUpgradeTxn(ctx context.Context, conn *pgx.Conn, target rev.Revision, snapshot []legacyRow, hooks *UpgradeHooks) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate upgrade: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := CreateTrackingStructures(ctx, tx); err != nil {
		return err
	}

	// Fold: one synthetic confirmed op per old row, verbatim. Each row gets a
	// DISTINCT synthetic edge_id (derived from its version) so the view's GROUP BY
	// edge_id yields exactly one row per prefix migration (tracking_schema.md A1).
	for _, r := range snapshot {
		if err := insertSyntheticPrefixOp(ctx, tx, r); err != nil {
			return err
		}
	}

	// ASSERT the view reproduces the snapshot exactly (symmetric difference == 0),
	// on its own columns, before the legacy table is dropped.
	if err := assertViewReproducesSnapshot(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, "DROP TABLE pgdesign_migrations"); err != nil {
		return fmt.Errorf("migrate upgrade: drop legacy tracking table: %w", err)
	}

	// Stamp the per-database boundary: current == boundary == rN, kind 'upgrade'.
	if err := insertChainPosition(ctx, tx, chainPosition{
		CurrentRevision:  target.String(),
		BoundaryRevision: target.String(),
		BoundaryKind:     "upgrade",
		CodecEpoch:       int(enc.CodecVersion),
	}); err != nil {
		return err
	}

	if hooks.BeforeCommit != nil {
		if err := hooks.BeforeCommit(); err != nil {
			return err // defer tx.Rollback undoes the whole upgrade
		}
	}
	return tx.Commit(ctx)
}

// insertSyntheticPrefixOp folds one legacy row into a single confirmed
// pgdesign_migration_ops row. The op is non-invertible (prefix migrations are
// rollback-frozen, 5.6), so down_op is NULL; version_label/description/checksum
// and confirmed_at are the old row's values VERBATIM.
func insertSyntheticPrefixOp(ctx context.Context, tx pgx.Tx, r legacyRow) error {
	edgeID := syntheticPrefixEdgeID(r.Version)
	_, err := tx.Exec(ctx,
		`INSERT INTO pgdesign_migration_ops
		 (edge_id, seq, phase, op_kind, target, invertibility, down_op, status, confirmed_at, version_label, description, checksum)
		 VALUES ($1, 0, '', 'upgrade_prefix', $2, 'non-invertible', NULL, 'confirmed', $3, $4, $5, $6)`,
		edgeID, "upgrade:"+r.Version, r.AppliedAt, r.Version, r.Description, r.Checksum)
	if err != nil {
		return fmt.Errorf("migrate upgrade: fold legacy row %q: %w", r.Version, err)
	}
	return nil
}

// syntheticPrefixEdgeID mints a distinct, deterministic synthetic edge_id for a
// prefix migration from its version label. It is stable across databases (a
// per-database stamp derived only from the version), so two databases folding the
// same version produce the same identifier.
func syntheticPrefixEdgeID(version string) string {
	sum := sha256.Sum256([]byte("pgdesign-upgrade-prefix\x00" + version))
	return fmt.Sprintf("%s:%x", rev.RegistryPresent, sum)
}

// assertViewReproducesSnapshot verifies the applied-migrations view reproduces
// the legacy table's applied set exactly (symmetric difference of the two column
// projections is empty). A mismatch is a hard error that rolls the transaction
// back — the boundary is never stamped over a fold that lost or altered a row.
func assertViewReproducesSnapshot(ctx context.Context, tx pgx.Tx) error {
	var diffCount int
	err := tx.QueryRow(ctx, `
WITH a AS (SELECT version, applied_at, description, checksum FROM pgdesign_migrations),
     b AS (SELECT version, applied_at, description, checksum FROM pgdesign_applied_migrations)
SELECT
  (SELECT count(*) FROM (SELECT * FROM a EXCEPT SELECT * FROM b) d1)
+ (SELECT count(*) FROM (SELECT * FROM b EXCEPT SELECT * FROM a) d2)`).Scan(&diffCount)
	if err != nil {
		return fmt.Errorf("migrate upgrade: assert view reproduces snapshot: %w", err)
	}
	if diffCount != 0 {
		return fmt.Errorf("migrate upgrade: the applied-migrations view does not reproduce the legacy applied set (%d differing row(s)) — refusing to complete the upgrade", diffCount)
	}
	return nil
}

// snapshotLegacy reads the legacy pgdesign_migrations rows (the fold source and
// assert oracle).
func snapshotLegacy(ctx context.Context, conn *pgx.Conn) ([]legacyRow, error) {
	rows, err := conn.Query(ctx, "SELECT version, applied_at, description, checksum FROM pgdesign_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("migrate upgrade: snapshot legacy table: %w", err)
	}
	defer rows.Close()
	var out []legacyRow
	for rows.Next() {
		var r legacyRow
		if err := rows.Scan(&r.Version, &r.AppliedAt, &r.Description, &r.Checksum); err != nil {
			return nil, fmt.Errorf("migrate upgrade: scan legacy row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate upgrade: iterate legacy rows: %w", err)
	}
	return out, nil
}

// computeAmnesty compares each legacy row's recorded checksum against its on-disk
// semver file's current bytes. Rows with no discoverable file (e.g. baseline rows
// with checksum 'baseline', or a deleted file) are skipped — amnesty is only for
// a file that EXISTS but whose bytes have drifted from the recorded checksum.
func computeAmnesty(ctx context.Context, conn *pgx.Conn, migrationsDir string) ([]AmnestyEntry, error) {
	rows, err := conn.Query(ctx, "SELECT version, checksum FROM pgdesign_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("migrate upgrade: read checksums for amnesty: %w", err)
	}
	defer rows.Close()
	type vc struct{ version, checksum string }
	var recorded []vc
	for rows.Next() {
		var v vc
		if err := rows.Scan(&v.version, &v.checksum); err != nil {
			return nil, fmt.Errorf("migrate upgrade: scan checksum row: %w", err)
		}
		recorded = append(recorded, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate upgrade: iterate checksum rows: %w", err)
	}

	var out []AmnestyEntry
	for _, v := range recorded {
		path := filepath.Join(migrationsDir, v.version+".toml")
		data, err := os.ReadFile(path)
		if err != nil {
			continue // no file to compare (deleted, or a baseline record)
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(data))
		if actual != v.checksum {
			out = append(out, AmnestyEntry{File: path, Recorded: v.checksum, Actual: actual})
		}
	}
	return out, nil
}

// checkCleanSchemaFiles refuses when any schema TOML has uncommitted changes
// inside a git repository. Outside a git repository the check is skipped (the
// stated caveat: without version control there is no clean-tree fact to assert).
func checkCleanSchemaFiles(schemaFiles []string) error {
	if len(schemaFiles) == 0 {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil // git unavailable: caveat, proceed
	}
	dir := filepath.Dir(schemaFiles[0])
	if err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return nil // not a git repo: caveat, proceed
	}
	args := append([]string{"-C", dir, "status", "--porcelain", "--"}, schemaFiles...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return fmt.Errorf("migrate upgrade: git status on schema files: %w", err)
	}
	if dirty := strings.TrimSpace(string(out)); dirty != "" {
		return fmt.Errorf("migrate upgrade: the schema files have uncommitted changes — commit or revert them before upgrading so the stamped boundary reflects committed source:\n%s", dirty)
	}
	return nil
}
