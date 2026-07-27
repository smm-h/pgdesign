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
	"github.com/smm-h/pgdesign/internal/catalog"
	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/predicate"
	"github.com/smm-h/pgdesign/internal/sql"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// indexQualified renders a schema-qualified (or bare) quoted index name.
func indexQualified(schema, name string) string {
	if schema == "" {
		return sql.QuoteIdent(name)
	}
	return sql.QualifiedName(schema, name)
}

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

// applyEdge executes one edge's ops as the single pass PRECONDITION -> EXECUTE ->
// JOURNAL (roadmap 5.5+5.7). Per op:
//
//   - PRECONDITION: the op's domain check runs against the current DB state via the
//     predicate Go executor (creates require their object absent; drops/alters
//     require it present). Unexpected state is a hard error naming
//     object/expected/found — drift is loud, never absorbed (L5).
//   - EXECUTE: the op's rendered SQL.
//   - JOURNAL: a pgdesign_migration_ops row. TRANSACTIONAL ops journal a confirmed
//     row INSIDE the op's segment transaction (atomic with the effect).
//     NON-TRANSACTIONAL ops (create/drop index concurrently; pre-12 enum-add) use
//     an INTENT-then-CONFIRM protocol whose resume is idempotent in Postgres's own
//     state model (pg_index.indisvalid; IF EXISTS).
//
// RESUME (mid-edge): already-confirmed ops are SKIPPED; a lingering intent row
// drives the non-transactional resume protocol. chain_position advances in the
// transaction that confirms the edge's FINAL op.
func applyEdge(ctx context.Context, conn *pgx.Conn, store *objstore.Store, e Edge, hooks *ApplyHooks) error {
	edgeID := e.ID()
	checksum, err := edgeFileChecksum(e)
	if err != nil {
		return err
	}
	slug := e.Slug
	finalSeq := len(e.Ops) - 1

	confirmed, intents, err := loadEdgeOpStatus(ctx, conn, edgeID)
	if err != nil {
		return err
	}
	if err := setInProgressEdge(ctx, conn, &edgeID); err != nil {
		return err
	}

	var tx pgx.Tx // the current transactional segment; nil when none is open
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

	for seq, op := range e.Ops {
		if confirmed[seq] {
			continue // mid-edge resume: a confirmed op does not re-run its precondition
		}
		isFinal := seq == finalSeq
		row, err := journalRowFor(op, seq, slug, edgeID, checksum)
		if err != nil {
			return err
		}
		nonTx, err := op.isNonTransactional(store)
		if err != nil {
			return err
		}

		if nonTx {
			resume := intents[seq]
			if !resume {
				// Write the intent row and commit the (possibly preceding) segment so
				// it durably lands BEFORE the effect runs — the applied view never
				// shows a partial edge as applied, and resume has a durable marker.
				if err := beginSeg(); err != nil {
					return err
				}
				if err := journalIntentOp(ctx, tx, row); err != nil {
					return err
				}
			}
			if err := commitSeg(); err != nil {
				return fmt.Errorf("commit before non-transactional op %d (%s): %w", seq, op.kind, err)
			}
			if err := executeNonTransactionalOp(ctx, conn, store, op, resume); err != nil {
				return fmt.Errorf("op %d (%s): %w", seq, op.kind, err)
			}
			// Confirm (+ advance if final) atomically in a small transaction.
			t2, err := conn.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin confirm for op %d: %w", seq, err)
			}
			if err := confirmIntentOp(ctx, t2, edgeID, seq); err != nil {
				_ = t2.Rollback(ctx)
				return err
			}
			if isFinal {
				if err := advanceChainPosition(ctx, t2, e.Target.String()); err != nil {
					_ = t2.Rollback(ctx)
					return err
				}
			}
			if err := t2.Commit(ctx); err != nil {
				return fmt.Errorf("commit confirm for op %d: %w", seq, err)
			}
			if hooks.AfterOp != nil {
				if err := hooks.AfterOp(edgeID, seq); err != nil {
					return err
				}
			}
			continue
		}

		// Transactional op: precondition + execute + journal, all in the segment tx.
		if err := beginSeg(); err != nil {
			return err
		}
		if err := checkPreconditions(ctx, tx, store, op); err != nil {
			return fmt.Errorf("op %d (%s): %w", seq, op.kind, err)
		}
		sqlStmt, err := op.RenderSQL(store)
		if err != nil {
			return err
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
				return err // defer tx.Rollback undoes this segment's uncommitted work
			}
		}
		if isFinal {
			if err := advanceChainPosition(ctx, tx, e.Target.String()); err != nil {
				return err
			}
			if err := commitSeg(); err != nil {
				return err
			}
		}
	}

	// Defensive: a residual open segment (never expected, since the final op commits).
	return commitSeg()
}

// checkPreconditions evaluates every precondition an op declares against the
// current DB state (q is the segment tx for transactional ops, the conn for
// non-transactional ops). A violated precondition is a hard error naming
// object/expected/found.
func checkPreconditions(ctx context.Context, q catalog.Querier, store *objstore.Store, op SelfContainedOp) error {
	pres, err := op.preconditions(store)
	if err != nil {
		return err
	}
	for _, p := range pres {
		r, err := predicate.Check(ctx, q, p)
		if err != nil {
			return err
		}
		if err := r.Err(); err != nil {
			return err
		}
	}
	return nil
}

// executeNonTransactionalOp runs a non-transactional op idempotently. On a fresh
// run it checks the op's precondition then executes; on resume it applies the
// class-specific protocol in Postgres's own state model (L8).
func executeNonTransactionalOp(ctx context.Context, conn *pgx.Conn, store *objstore.Store, op SelfContainedOp, resume bool) error {
	switch op.kind {
	case "create_index_concurrently":
		return resumeCreateIndexConcurrently(ctx, conn, store, op, resume)
	case "drop_index_concurrently":
		// DROP INDEX CONCURRENTLY IF EXISTS is idempotent; its precondition (index
		// present) is checked only on a fresh run — on resume the index may already
		// be gone, which IF EXISTS handles.
		if !resume {
			if err := checkPreconditions(ctx, conn, store, op); err != nil {
				return err
			}
		}
		return execNonTxStatements(ctx, conn, store, op)
	default:
		// Version-conditional enum-add (pre-12) and any other non-transactional op:
		// ADD VALUE IF NOT EXISTS is idempotent, so resume simply re-runs.
		if !resume {
			if err := checkPreconditions(ctx, conn, store, op); err != nil {
				return err
			}
		}
		return execNonTxStatements(ctx, conn, store, op)
	}
}

// resumeCreateIndexConcurrently implements the create-index resume protocol
// (roadmap L8): an interrupted CREATE INDEX CONCURRENTLY leaves an INVALID index
// of the target name that IF NOT EXISTS would skip forever. On resume, if the
// index is present AND valid it was built before the crash (nothing to do); if it
// is absent or INVALID, DROP INDEX CONCURRENTLY IF EXISTS then rebuild.
func resumeCreateIndexConcurrently(ctx context.Context, conn *pgx.Conn, store *objstore.Store, op SelfContainedOp, resume bool) error {
	if !resume {
		if err := checkPreconditions(ctx, conn, store, op); err != nil {
			return err
		}
		return execNonTxStatements(ctx, conn, store, op)
	}
	schema, name, err := indexTargetName(store, op)
	if err != nil {
		return err
	}
	info, present, err := catalog.Index(ctx, conn, schema, name)
	if err != nil {
		return err
	}
	if present && info.Valid {
		return nil // built valid before the crash; the confirm step finishes the edge
	}
	dropStmt := fmt.Sprintf("DROP INDEX CONCURRENTLY IF EXISTS %s", indexQualified(schema, name))
	if _, err := conn.Exec(ctx, dropStmt); err != nil {
		return fmt.Errorf("resume: drop invalid index %s: %w", name, err)
	}
	return execNonTxStatements(ctx, conn, store, op)
}

// execNonTxStatements renders an op and executes its (split) statements via the
// autocommit connection.
func execNonTxStatements(ctx context.Context, conn *pgx.Conn, store *objstore.Store, op SelfContainedOp) error {
	sqlStmt, err := op.RenderSQL(store)
	if err != nil {
		return err
	}
	stmts, err := sqlparse.SplitStatements(sqlStmt)
	if err != nil {
		return fmt.Errorf("parse non-transactional op (%s): %w", op.kind, err)
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("non-transactional op (%s): %w", op.kind, err)
		}
	}
	return nil
}

// indexTargetName resolves the (schema, index-name) of a concurrent-index op from
// its stored delta.
func indexTargetName(store *objstore.Store, op SelfContainedOp) (string, string, error) {
	body, err := loadBody(store, op.payload)
	if err != nil {
		return "", "", err
	}
	if body.Delta == nil {
		return "", "", fmt.Errorf("migrate: concurrent-index op %s has no delta", op.kind)
	}
	schema, _ := splitQualifiedName(body.Delta.Table)
	return schema, body.Delta.Name, nil
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
	data, err := canonicalOpJSON(edgeToJSON(e))
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
