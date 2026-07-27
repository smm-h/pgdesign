package migrate

// Journal-driven rollback on the chain (roadmap 5.6, L5+L4).
//
// RollbackChain reverses applied chain edges by executing the RECORDED down-ops
// from the journal (pgdesign_migration_ops.down_op) in REVERSE JOURNAL ORDER —
// the edge files are never consulted for the inverse content. This is the L5/L4
// correctness the subphase targets: rollback inverts RECORDED reality (the
// journal), never file-derived assumed intent (the legacy .toml-down data-loss
// path). Only journaled invertible ops (down_op NOT NULL) have an inverse to run;
// a non-invertible op in the rollback range is a hard refusal BEFORE any effect.
//
// SCOPE (5.6): rollback is guaranteed from the upgrade/baseline boundary forward.
// The pre-upgrade prefix and baselines are ROLLBACK-FROZEN — crossing the
// chain_position boundary_revision is a hard error naming the boundary. (The 5.2
// as-built fold makes prefix ops non-invertible with NULL downs, so the
// journal-based pre-check catches them naturally; the boundary error is the
// named, better message and fires first.)
//
// TOPOLOGY NOTE (as-built): the landed journal schema (tracking_schema.sql)
// stores no per-edge revision, and an edge_id is content-derived from its ops, so
// mapping the database's current_revision back through the applied history to the
// parent revisions requires the on-disk chain endpoints (LoadAllEdges). The
// INVERSE CONTENT and the reversibility decision remain journal-sourced — the
// on-disk chain provides only the parent/target revisions the position steps
// through. Payloads referenced by a journaled down-op resolve via the object
// store.
//
// JOURNAL DISPOSITION (as-built): rolled-back op rows are DELETED. The
// pgdesign_migration_ops CHECK constraints admit only status IN
// ('intended','confirmed') — there is no 'reverted' state to transition to — so
// DELETE is the sole disposition the schema permits while keeping the applied
// view coherent (a fully rolled-back edge disappears from the view because its
// rows are gone).

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// chainRollbackStep is one edge to reverse and the revision current_revision
// becomes once it is reversed. inProgress marks the abort of a partially-applied
// edge (chain_position.in_progress_edge): only its confirmed ops carry recorded
// effects, and its unconfirmed non-transactional intents are undone by their
// idempotent down-op (e.g. an unconfirmed CREATE INDEX CONCURRENTLY intent → DROP
// INDEX CONCURRENTLY IF EXISTS the possibly-invalid index).
type chainRollbackStep struct {
	edge        Edge
	targetAfter string // current_revision after this edge is reversed
	inProgress  bool
}

// journalOpRow is one pgdesign_migration_ops row projected for rollback: the op
// identity, its L4 class, its intent/confirm status, and its serialized down-op
// (NULL iff non-invertible).
type journalOpRow struct {
	seq           int
	opKind        string
	invertibility string
	status        string
	downOp        *string
}

// RollbackChain reverses applied chain edges against conn. toRevision is empty for
// a single-step rollback (reverse the most-recent edge, or abort the in-progress
// edge) or a target REVISION string for `rollback --to` (reverse every edge down
// to, but not including, toRevision). It returns the reversed edges' display ids.
func RollbackChain(ctx context.Context, conn *pgx.Conn, p *ChainProject, toRevision, lockTimeout string) ([]string, error) {
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

	exists, err := ChainStructuresExist(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("migrate rollback: no chain migrations have been applied to this database — nothing to roll back")
	}
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("migrate rollback: chain position row is missing — nothing to roll back")
	}

	allEdges, err := p.LoadAllEdges()
	if err != nil {
		return nil, err
	}
	byTarget := map[string][]Edge{}
	byID := map[string]Edge{}
	for _, e := range allEdges {
		byTarget[e.Target.String()] = append(byTarget[e.Target.String()], e)
		byID[e.ID()] = e
	}

	steps, err := planChainRollback(pos, toRevision, byTarget, byID)
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, nil
	}

	// Reversibility PRE-CHECK against JOURNALED ops (not files): any op in the
	// rollback range whose down_op is NULL (non-invertible) is a refusal naming it,
	// BEFORE executing anything.
	for _, s := range steps {
		rows, err := loadEdgeJournalOps(ctx, conn, s.edge.ID())
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !reversibleRow(s, r) {
				continue
			}
			if r.downOp == nil {
				return nil, fmt.Errorf("migrate rollback: op %s/%d (%s) is non-invertible (no recorded down-op) — refusing to roll back", s.edge.ID()[:12], r.seq, r.opKind)
			}
		}
	}

	if err := setLockTimeout(ctx, conn, lockTimeout); err != nil {
		return nil, err
	}

	var rolled []string
	for _, s := range steps {
		if err := reverseEdge(ctx, conn, p, s); err != nil {
			return rolled, fmt.Errorf("migrate rollback: edge %s: %w", s.edge.ID()[:12], err)
		}
		rolled = append(rolled, s.edge.ID()[:12]+" "+s.edge.Slug)
	}
	return rolled, nil
}

// reversibleRow reports whether a journal row represents an effect rollback must
// reverse: for a fully-applied edge every confirmed row; for an in-progress edge
// its confirmed rows plus its lingering (intended) non-transactional intents.
func reversibleRow(s chainRollbackStep, r journalOpRow) bool {
	if s.inProgress {
		return r.status == "confirmed" || r.status == "intended"
	}
	return r.status == "confirmed"
}

// planChainRollback resolves the ordered edges to reverse. It walks the applied
// history backward from chain_position.current_revision using the on-disk chain
// endpoints (the parent/target revisions the journal does not store), refusing to
// cross the boundary. An in-progress edge is aborted first (its target is not yet
// current_revision, so it is addressed by id, not by walking).
func planChainRollback(pos chainPosition, toRevision string, byTarget map[string][]Edge, byID map[string]Edge) ([]chainRollbackStep, error) {
	var steps []chainRollbackStep
	current := pos.CurrentRevision

	// 1. Abort an in-progress edge first (partially applied; not at current_revision).
	if pos.InProgressEdge != nil {
		e, ok := byID[*pos.InProgressEdge]
		if !ok {
			return nil, fmt.Errorf("migrate rollback: in-progress edge %s not found on the chain", (*pos.InProgressEdge)[:min12(*pos.InProgressEdge)])
		}
		steps = append(steps, chainRollbackStep{edge: e, targetAfter: current, inProgress: true})
	}

	// Single-step rollback (no --to).
	if toRevision == "" {
		if pos.InProgressEdge != nil {
			// Aborting the in-progress edge IS the single step.
			return steps, nil
		}
		e, err := edgeEndingAt(byTarget, current, pos)
		if err != nil {
			return nil, err
		}
		steps = append(steps, chainRollbackStep{edge: e, targetAfter: revString(e.Parent)})
		return steps, nil
	}

	// rollback --to <revision>: reverse full edges until current reaches toRevision.
	if current == toRevision && pos.InProgressEdge == nil {
		return nil, fmt.Errorf("migrate rollback: the database is already at revision %s — nothing to roll back", toRevision)
	}
	guard := 0
	for current != toRevision {
		guard++
		if guard > len(byID)+1 {
			return nil, fmt.Errorf("migrate rollback: target revision %q is not reachable on the applied chain from %q", toRevision, pos.CurrentRevision)
		}
		e, err := edgeEndingAt(byTarget, current, pos)
		if err != nil {
			return nil, err
		}
		steps = append(steps, chainRollbackStep{edge: e, targetAfter: revString(e.Parent)})
		current = revString(e.Parent)
	}
	return steps, nil
}

// edgeEndingAt returns the applied edge whose target is the revision current,
// enforcing the boundary freeze: an edge whose target IS the boundary_revision
// anchors the boundary, so reversing it would cross below the frozen floor — a
// hard error naming the boundary. Ambiguity (parallel edges sharing a target) is
// an explicit error rather than a silent guess.
func edgeEndingAt(byTarget map[string][]Edge, current string, pos chainPosition) (Edge, error) {
	if current != "" && current == pos.BoundaryRevision {
		return Edge{}, fmt.Errorf("migrate rollback: would cross the %s boundary at revision %s — the pre-upgrade prefix and baselines are rollback-frozen", pos.BoundaryKind, pos.BoundaryRevision)
	}
	cands := byTarget[current]
	switch len(cands) {
	case 0:
		return Edge{}, fmt.Errorf("migrate rollback: no applied edge ends at revision %q — nothing to roll back", current)
	case 1:
		return cands[0], nil
	default:
		ids := make([]string, len(cands))
		for i, c := range cands {
			ids[i] = c.ID()[:12]
		}
		return Edge{}, fmt.Errorf("migrate rollback: revision %q is the target of %d edges %v — an ambiguous (parallel/endomorphic) history that scoped rollback does not resolve", current, len(cands), ids)
	}
}

// reverseEdge executes an edge's recorded down-ops (from the journal) in reverse
// journal order, then DELETEs the edge's journal rows and steps chain_position
// back to targetAfter — the last two atomic in the final segment transaction, so
// the applied view is coherent the instant the edge disappears from it.
//
// Transactional down-ops run inside the segment; non-transactional down-ops
// (DROP INDEX CONCURRENTLY, from a create-index inverse) break out and run on the
// autocommit connection, exactly as apply mirrors them.
func reverseEdge(ctx context.Context, conn *pgx.Conn, p *ChainProject, s chainRollbackStep) error {
	store := p.Store()
	edgeID := s.edge.ID()
	rows, err := loadEdgeJournalOps(ctx, conn, edgeID)
	if err != nil {
		return err
	}

	var tx pgx.Tx
	defer func() {
		if tx != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	beginSeg := func() error {
		if tx == nil {
			t, err := conn.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin: %w", err)
			}
			tx = t
		}
		return nil
	}
	commitSeg := func() error {
		if tx != nil {
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			tx = nil
		}
		return nil
	}

	// Reverse journal order: descending seq.
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if !reversibleRow(s, r) {
			continue
		}
		if r.downOp == nil {
			continue // pre-checked; defensive
		}
		down, err := UnmarshalOp(store, []byte(*r.downOp))
		if err != nil {
			return fmt.Errorf("parse recorded down-op for seq %d (%s): %w", r.seq, r.opKind, err)
		}
		nonTx, err := down.isNonTransactional(store)
		if err != nil {
			return err
		}
		sqlStmt, err := down.RenderSQL(store)
		if err != nil {
			return err
		}
		stmts, err := sqlparse.SplitStatements(sqlStmt)
		if err != nil {
			return fmt.Errorf("parse down SQL for seq %d (%s): %w", r.seq, r.opKind, err)
		}
		if nonTx {
			if err := commitSeg(); err != nil {
				return fmt.Errorf("commit before non-transactional down-op seq %d: %w", r.seq, err)
			}
			for _, stmt := range stmts {
				if _, err := conn.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("non-transactional down-op seq %d (%s): %w", r.seq, r.opKind, err)
				}
			}
			continue
		}
		if err := beginSeg(); err != nil {
			return err
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("down-op seq %d (%s): %w\n  SQL: %s", r.seq, r.opKind, err, stmt)
			}
		}
	}

	// Finalize: delete the edge's journal rows and step the position back, atomic.
	// Both writes route through the tracking_chain.go writers (single write path).
	if err := beginSeg(); err != nil {
		return err
	}
	if err := deleteRolledBackOps(ctx, tx, edgeID); err != nil {
		return err
	}
	if err := advanceChainPosition(ctx, tx, s.targetAfter); err != nil {
		return err
	}
	return commitSeg()
}

// loadEdgeJournalOps reads an edge's op rows ordered by seq (ascending).
func loadEdgeJournalOps(ctx context.Context, conn *pgx.Conn, edgeID string) ([]journalOpRow, error) {
	rows, err := conn.Query(ctx,
		"SELECT seq, op_kind, invertibility, status, down_op FROM pgdesign_migration_ops WHERE edge_id = $1 ORDER BY seq",
		edgeID)
	if err != nil {
		return nil, fmt.Errorf("migrate rollback: load journal for %s: %w", edgeID[:12], err)
	}
	defer rows.Close()
	var out []journalOpRow
	for rows.Next() {
		var r journalOpRow
		if err := rows.Scan(&r.seq, &r.opKind, &r.invertibility, &r.status, &r.downOp); err != nil {
			return nil, fmt.Errorf("migrate rollback: scan journal row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
