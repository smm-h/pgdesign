// Package livenorm implements LIVE ROUND-TRIP NORMALIZATION (roadmap 1.2,
// boundary item 4): the concrete diff.LiveNormalizer that resolves the ≈_pg
// RESIDUE — catalog-dependent cast materialization — that no pure normalizer
// can reach.
//
// A desired-side boolean predicate is round-tripped through the TARGET database
// itself: a throwaway TEMP table cloned (LIKE) from the real table carries a
// temporary CHECK constraint holding the expression; PostgreSQL parses, type-
// resolves, and stores it, then pg_get_constraintdef renders PG's OWN canonical
// form (e.g. materializing `status = 'active'` to `status = 'active'::text`).
// The temp object is always dropped.
//
// Identity NEVER consumes this output — livenorm is used only on the diff --live
// path, which has a database; the pure/encoding path never touches it.
//
// The MINIMAL FORWARD-SIMULATION rule set is N: where a round-trip cannot reach
// (the real table or a referenced column is absent, so the temp DDL cannot be
// built), normalization falls to sqlparse.NormalizeExpr — the catalog-
// independent foldings that survive without a database. This is reachability-
// determined (a fixed property of the expression against the live schema), not
// silent degradation: the same expression against the same database always
// takes the same path.
package livenorm

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// Normalizer holds a session-scoped connection to the target database. Temp
// objects live in that session's pg_temp schema and vanish when Close is called
// (or the session ends), in addition to being explicitly dropped per call.
type Normalizer struct {
	ctx     context.Context
	conn    *pgx.Conn
	counter int
}

// New opens a connection to the target database for round-trip normalization.
func New(ctx context.Context, dbURL string) (*Normalizer, error) {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	return &Normalizer{ctx: ctx, conn: conn}, nil
}

// Close releases the connection (and, with it, all session temp objects).
func (n *Normalizer) Close() {
	if n.conn != nil {
		_ = n.conn.Close(n.ctx)
		n.conn = nil
	}
}

// NormalizeExprForTable returns the target DB's canonical form of a boolean
// predicate expr in the context of schema.table. On any reason the round-trip
// cannot reach (absent table/column, parse error, or a dead connection), it
// returns the forward-simulation form: sqlparse.NormalizeExpr(expr).
func (n *Normalizer) NormalizeExprForTable(schema, table, expr string) string {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return ""
	}
	base := sqlparse.NormalizeExpr(trimmed)
	if n.conn == nil {
		return base
	}

	pgForm, ok := n.roundTrip(schema, table, trimmed)
	if !ok {
		return base // forward-simulation: N is the rule set where round-trip can't reach
	}
	return sqlparse.NormalizeExpr(pgForm)
}

// roundTrip builds a throwaway temp table cloned from schema.table, adds a
// temporary CHECK holding expr, reads PG's rendered form, and drops the temp
// table. Returns (renderedInnerExpr, true) on success. Any SQL error yields
// (_, false) — the caller then forward-simulates.
func (n *Normalizer) roundTrip(schema, table, expr string) (string, bool) {
	if schema == "" {
		schema = "public"
	}
	n.counter++
	tmp := fmt.Sprintf("_pgd_rt_%d", n.counter)
	qtmp := quoteIdent(tmp)
	src := quoteIdent(schema) + "." + quoteIdent(table)

	// Always attempt cleanup, even on partial failure.
	defer func() {
		_, _ = n.conn.Exec(n.ctx, "DROP TABLE IF EXISTS "+qtmp)
	}()

	if _, err := n.conn.Exec(n.ctx, fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s)", qtmp, src)); err != nil {
		return "", false
	}
	if _, err := n.conn.Exec(n.ctx, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT _pgd_c CHECK (%s)", qtmp, expr)); err != nil {
		return "", false
	}

	// Scope the lookup to THIS session's own temp namespace. The temp table
	// name (_pgd_rt_<counter>) and the constraint name (_pgd_c) are per-session
	// but NOT globally unique: a concurrent session's counter starts at 0 too,
	// so two sessions can both hold a _pgd_rt_1 with a _pgd_c constraint (each in
	// its own pg_temp_N schema, both visible in pg_class). Without the namespace
	// scope this query could match — or ambiguously pick between — the OTHER
	// session's constraint. pg_my_temp_schema() is the OID of the current
	// session's temp namespace, so it isolates the lookup to our own temp object.
	var def string
	err := n.conn.QueryRow(n.ctx,
		`SELECT pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		   JOIN pg_class r ON r.oid = c.conrelid
		  WHERE r.relname = $1 AND c.conname = '_pgd_c'
		    AND r.relnamespace = pg_my_temp_schema()`,
		tmp,
	).Scan(&def)
	if err != nil {
		return "", false
	}

	inner := strings.TrimSpace(strings.TrimPrefix(def, "CHECK "))
	if inner == "" {
		return "", false
	}
	return inner, true
}

// quoteIdent double-quotes a PostgreSQL identifier, escaping embedded quotes.
func quoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}
