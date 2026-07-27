package migrate

// Apply on the chain (roadmap 5.2, item 2).
//
// ApplyChain drives a database forward over the on-disk chain: from its recorded
// chain_position.current_revision (genesis "" when fresh), the path-finder selects
// the ordered edges to the single live head; each edge's ops are RENDERED VIA THE
// SELF-CONTAINED RENDERER (RenderSQL against the object store) and executed with
// the LEGACY execution semantics preserved exactly — one transaction per edge with
// non-transactional breakouts (per IsNonTransactional, mapped by op kind), the
// shared session advisory lock, and a validated lock_timeout via set_config.
//
// Per op, a confirmed pgdesign_migration_ops row is written (op identity, phase,
// serialized down-op, edge-level version_label/description/checksum); the edge's
// final transaction advances chain_position.current_revision to the edge target.
//
// DOUBLE-RENDER RESOLUTION (verified): generate emits create_table plus SEPARATE
// add_fk / create_index ops (cycle-safe DDL). sql.CreateTable renders columns +
// inline PK + PARTITION BY only — never FKs, indexes, checks, or uniques — and the
// self-contained create_table body deliberately carries an EMPTY enum/domain
// closure (selfcontained_shim.go), so RenderSQL reproduces the legacy OpToSQL
// output byte-for-byte, op by op. RenderedEdgeSQL exposes that sequence for the
// byte-identity test (chain-mode == legacy for the same Migration).
//
// For 5.2, every op is journaled as 'confirmed'; the intent/confirm state machine
// for non-transactional ops (crash-window resume) is 5.5. ApplyHooks.AfterOp is a
// minimal per-op seam for crash-injection tests (nil by default).

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// ApplyHooks carries optional test seams. AfterOp runs after an op executes and
// journals, before the edge's transaction commits; a non-nil error aborts the
// edge (the transactional path rolls back — the in-process crash-before-commit
// equivalent). It is nil in production.
type ApplyHooks struct {
	AfterOp func(edgeID string, seq int) error
}

// ApplyChain applies pending chain edges to conn and returns the applied edges'
// display ids (edge-id prefix + slug). A fresh database (no chain structures) is
// seeded here: the three managed structures are created and chain_position is set
// to genesis before path-finding. lockTimeout sets the session lock_timeout.
func ApplyChain(ctx context.Context, conn *pgx.Conn, p *ChainProject, lockTimeout string, hooks *ApplyHooks) ([]string, error) {
	if hooks == nil {
		hooks = &ApplyHooks{}
	}
	if err := GuardNotPreUpgrade(ctx, conn); err != nil {
		return nil, err
	}

	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("another migration is in progress (could not acquire advisory lock)")
	}
	defer ReleaseAdvisoryLock(ctx, conn)

	if err := ensureChainStructures(ctx, conn); err != nil {
		return nil, err
	}
	if err := setLockTimeout(ctx, conn, lockTimeout); err != nil {
		return nil, err
	}

	pos, _, err := readChainPosition(ctx, conn)
	if err != nil {
		return nil, err
	}

	path, err := findApplyPath(p, pos.CurrentRevision)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return nil, nil
	}

	var applied []string
	for _, e := range path {
		if err := applyEdge(ctx, conn, p.Store(), e, hooks); err != nil {
			return applied, fmt.Errorf("edge %s: %w", e.ID()[:12], err)
		}
		applied = append(applied, e.ID()[:12]+" "+e.Slug)
	}
	return applied, nil
}

// findApplyPath loads the live + archive edges and asks the path-finder for the
// ordered edges from pos to the single live head.
func findApplyPath(p *ChainProject, pos string) ([]Edge, error) {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return nil, err
	}
	all, err := p.LoadAllEdges()
	if err != nil {
		return nil, err
	}
	return FindPath(pos, RemapTable{}, live, all)
}

// ensureChainStructures creates the three managed structures and seeds
// chain_position at genesis when the database is fresh (no chain_position). A
// database already carrying the structures is left untouched.
func ensureChainStructures(ctx context.Context, conn *pgx.Conn) error {
	exists, err := ChainStructuresExist(ctx, conn)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin (create tracking): %w", err)
	}
	defer tx.Rollback(ctx)
	if err := CreateTrackingStructures(ctx, tx); err != nil {
		return err
	}
	// Fresh chain-native database: genesis position, genesis boundary. A fresh
	// apply is neither an upgrade nor an explicit baseline; boundary_kind is a
	// CHECK-constrained enum ('upgrade'|'baseline'), so 'baseline' labels the
	// genesis floor (rollback may reach empty). See the DECISION note in the
	// tranche report — reversible.
	if err := insertChainPosition(ctx, tx, chainPosition{
		CurrentRevision:  "",
		BoundaryRevision: "",
		BoundaryKind:     "baseline",
		CodecEpoch:       int(enc.CodecVersion),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applyEdge executes one edge's ops with the legacy execution semantics preserved
// (single transaction, non-transactional breakouts) and journals each op as
// confirmed. Stale rows from a prior crashed attempt at this edge are cleared
// first (the position was not advanced, so those rows never represented a
// completed edge). The edge's final transaction advances chain_position.
func applyEdge(ctx context.Context, conn *pgx.Conn, store *objstore.Store, e Edge, hooks *ApplyHooks) error {
	edgeID := e.ID()
	checksum, err := edgeFileChecksum(e)
	if err != nil {
		return err
	}
	slug := e.Slug
	// Clear any partial rows from a prior crashed attempt and mark this edge
	// in-progress (both autocommit; safe because chain_position still points at
	// the parent — the edge is not applied).
	if err := setInProgressEdge(ctx, conn, &edgeID); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "DELETE FROM pgdesign_migration_ops WHERE edge_id = $1", edgeID); err != nil {
		return fmt.Errorf("migrate: clear stale journal rows for edge %s: %w", edgeID[:12], err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for seq, op := range e.Ops {
		sqlStmt, err := op.RenderSQL(store)
		if err != nil {
			return err
		}
		nonTx, err := op.isNonTransactional(store)
		if err != nil {
			return err
		}
		row, err := journalRowFor(op, seq, slug, edgeID, checksum)
		if err != nil {
			return err
		}

		if nonTx {
			// Commit the current transaction, execute outside, journal via the conn,
			// then start a fresh transaction (mirrors legacy applyOne).
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit before non-transactional op %d (%s): %w", seq, op.kind, err)
			}
			stmts, err := sqlparse.SplitStatements(sqlStmt)
			if err != nil {
				return fmt.Errorf("parse non-transactional op %d (%s): %w", seq, op.kind, err)
			}
			for _, stmt := range stmts {
				if _, err := conn.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("non-transactional op %d (%s): %w", seq, op.kind, err)
				}
			}
			if err := journalConfirmedOp(ctx, conn, row); err != nil {
				return err
			}
			if hooks.AfterOp != nil {
				if err := hooks.AfterOp(edgeID, seq); err != nil {
					return err
				}
			}
			tx, err = conn.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin after non-transactional op %d: %w", seq, err)
			}
			defer tx.Rollback(ctx)
			continue
		}

		stmts, err := sqlparse.SplitStatements(sqlStmt)
		if err != nil {
			return fmt.Errorf("parse op %d (%s): %w", seq, op.kind, err)
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("op %d (%s): %w\n  SQL: %s", seq, op.kind, err, stmt)
			}
		}
		if err := journalConfirmedOp(ctx, tx, row); err != nil {
			return err
		}
		if hooks.AfterOp != nil {
			if err := hooks.AfterOp(edgeID, seq); err != nil {
				return err // defer tx.Rollback undoes this edge's uncommitted work
			}
		}
	}

	// The edge's final transaction advances the position atomically with the
	// last op's confirm.
	if err := advanceChainPosition(ctx, tx, e.Target.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// journalRowFor builds the confirmed journal row for one op. The down-op is the
// serialized inverse (nil exactly for non-invertible ops, matching the
// down_presence CHECK). Phase is '' — chain edges strip phase tags (roadmap 5.3).
func journalRowFor(op SelfContainedOp, seq int, slug, edgeID, checksum string) (journalRow, error) {
	var downJSON *string
	if op.inv != chain.NonInvertible && op.down != nil {
		b, err := canonicalOpJSON(op.down.Serialize())
		if err != nil {
			return journalRow{}, fmt.Errorf("migrate: serialize down-op for %s/%d: %w", edgeID[:12], seq, err)
		}
		s := string(b)
		downJSON = &s
	}
	desc := slug
	return journalRow{
		EdgeID:        edgeID,
		Seq:           seq,
		Phase:         "",
		OpKind:        op.kind,
		Target:        op.target.String(),
		Invertibility: op.inv.String(),
		DownOp:        downJSON,
		VersionLabel:  edgeID, // version_label = edge_id for post-upgrade edges (tracking_schema.md)
		Description:   &desc,
		Checksum:      checksum,
	}, nil
}

// edgeFileChecksum returns the sha256 of the edge's on-disk file bytes (the
// apply-surface checksum, distinct from the edge-content identity edge_id). For
// an in-memory edge (no File) the checksum is over its encoded form so the value
// is stable and non-empty (the checksum column is NOT NULL).
func edgeFileChecksum(e Edge) (string, error) {
	if e.File != "" {
		data, err := os.ReadFile(e.File)
		if err != nil {
			return "", fmt.Errorf("migrate: read edge file for checksum: %w", err)
		}
		return fmt.Sprintf("%x", sha256.Sum256(data)), nil
	}
	f := edgeFileJSON{
		FormatVersion: rev.FormatVersion,
		Codec:         enc.CodecVersion,
		Class:         string(e.Class),
		Parent:        revString(e.Parent),
		Target:        e.Target.String(),
		Slug:          e.Slug,
		Ops:           serializeOps(e.Ops),
	}
	data, err := canonicalOpJSON(f)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// RenderedEdgeSQL returns the ordered rendered SQL for an edge's ops (the exact
// sequence apply executes). It underpins the byte-identity test comparing
// chain-mode apply against legacy apply for the same Migration.
func RenderedEdgeSQL(store *objstore.Store, e Edge) ([]string, error) {
	out := make([]string, 0, len(e.Ops))
	for i, op := range e.Ops {
		s, err := op.RenderSQL(store)
		if err != nil {
			return nil, fmt.Errorf("migrate: render edge op %d: %w", i, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// PlanApplyChain previews the path-finder's chosen edges and their rendered SQL
// without executing (apply --dry-run). It reads the position when the chain
// structures exist, else treats the database as genesis. It never writes.
func PlanApplyChain(ctx context.Context, conn *pgx.Conn, p *ChainProject) ([]EdgePlan, error) {
	if err := GuardNotPreUpgrade(ctx, conn); err != nil {
		return nil, err
	}
	pos := ""
	if exists, err := ChainStructuresExist(ctx, conn); err != nil {
		return nil, err
	} else if exists {
		cp, ok, err := readChainPosition(ctx, conn)
		if err != nil {
			return nil, err
		}
		if ok {
			pos = cp.CurrentRevision
		}
	}
	path, err := findApplyPath(p, pos)
	if err != nil {
		return nil, err
	}
	plans := make([]EdgePlan, 0, len(path))
	for _, e := range path {
		sqls, err := RenderedEdgeSQL(p.Store(), e)
		if err != nil {
			return nil, err
		}
		plans = append(plans, EdgePlan{Edge: e, SQL: sqls})
	}
	return plans, nil
}

// EdgePlan is a previewed edge and its rendered per-op SQL.
type EdgePlan struct {
	Edge Edge
	SQL  []string
}

// isNonTransactional maps a self-contained op to the legacy IsNonTransactional
// decision by kind (and, for alter_enum_add_value, the recorded PGVersion).
func (o SelfContainedOp) isNonTransactional(store *objstore.Store) (bool, error) {
	switch o.kind {
	case "create_index_concurrently", "drop_index_concurrently":
		return true, nil
	case "alter_enum_add_value":
		body, err := loadBody(store, o.payload)
		if err != nil {
			return false, err
		}
		return IsNonTransactional(DDLOp{Op: o.kind, PGVersion: body.PGVersion}), nil
	default:
		return false, nil
	}
}
