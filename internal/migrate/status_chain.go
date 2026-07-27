package migrate

// Chain-aware status (roadmap 5.2, command_surface.md `status`).
//
// ComputeChainStatus reports a database's position on the on-disk chain: the
// CONFIRMED edges (read from the pgdesign_applied_migrations view) and the
// PENDING edges the path-finder selects from this database's chain_position to
// the single live head. It is a pure READ: it NEVER creates any managed
// structure. This is the anti-resurrection guarantee — the pre-chain status
// handler called EnsureMigrationsTable, recreating the legacy pgdesign_migrations
// table on an upgraded database.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AppliedChainEdge is one confirmed edge from pgdesign_applied_migrations. For
// post-upgrade prefix rows Version is the legacy semver label; for chain edges it
// is the edge_id.
type AppliedChainEdge struct {
	Version     string
	AppliedAt   time.Time
	Description string
}

// ChainStatus is a database's chain position: the confirmed edges (from the
// applied-migrations view) and the pending edges (from the path-finder).
type ChainStatus struct {
	Position string // chain_position.current_revision ("" = genesis, never stamped)
	Applied  []AppliedChainEdge
	Pending  []Edge
}

// ComputeChainStatus reads conn's chain position and applied view, then asks the
// path-finder for the pending edges to the live head — WITHOUT creating any
// managed structure. A database with no chain structures is reported as genesis
// (nothing applied; every edge pending). A pre-upgrade database is a hard error
// (the shared guard names `migrate upgrade`).
func ComputeChainStatus(ctx context.Context, conn *pgx.Conn, p *ChainProject) (*ChainStatus, error) {
	if err := GuardNotPreUpgrade(ctx, conn); err != nil {
		return nil, err
	}
	st := &ChainStatus{}
	exists, err := ChainStructuresExist(ctx, conn)
	if err != nil {
		return nil, err
	}
	if exists {
		if cp, ok, err := readChainPosition(ctx, conn); err != nil {
			return nil, err
		} else if ok {
			st.Position = cp.CurrentRevision
		}
		applied, err := queryAppliedChainEdges(ctx, conn)
		if err != nil {
			return nil, err
		}
		st.Applied = applied
	}
	pending, err := findApplyPath(p, st.Position)
	if err != nil {
		return nil, err
	}
	st.Pending = pending
	return st, nil
}

// queryAppliedChainEdges reads the confirmed edges from the applied-migrations
// view, oldest first.
func queryAppliedChainEdges(ctx context.Context, conn *pgx.Conn) ([]AppliedChainEdge, error) {
	rows, err := conn.Query(ctx,
		"SELECT version, applied_at, COALESCE(description, '') FROM pgdesign_applied_migrations ORDER BY applied_at, version")
	if err != nil {
		return nil, fmt.Errorf("migrate: query applied migrations: %w", err)
	}
	defer rows.Close()
	var out []AppliedChainEdge
	for rows.Next() {
		var e AppliedChainEdge
		if err := rows.Scan(&e.Version, &e.AppliedAt, &e.Description); err != nil {
			return nil, fmt.Errorf("migrate: scan applied migration: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
