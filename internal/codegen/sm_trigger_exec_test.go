package codegen

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// TestSMTrigger_RuntimeBehavior applies the exec-matrix DDL (including the
// generated state-machine BEFORE UPDATE trigger) to a live PostgreSQL server
// and exercises BOTH runtime branches of the trigger:
//   - a legal transition succeeds;
//   - an illegal transition is rejected with the trigger's P0001 error;
//   - a requires-bearing transition raises when the required column is null,
//     and succeeds once it is populated.
//
// All existing SM coverage asserts generated SQL text; this is the only test
// that executes the trigger. It skips cleanly without local Postgres.
func TestSMTrigger_RuntimeBehavior(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	schema, reg := loadExecMatrixSchema(t)
	ctx := context.Background()
	m := execMatrixManager(t)

	out, diags, err := generate.Generate(schema, generate.Options{
		Idempotent:      false,
		IncludeComments: true,
		Format:          "sql",
		TypeRegistry:    reg,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			t.Fatalf("generate error: [%s] %s", d.Code, d.Message)
		}
	}

	db := m.SetupForTest(t, testdb.CreateOptions{})
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	execSQLBlock(t, ctx, conn, "exec-matrix DDL", out)

	// Seed an account (FK target for orders).
	var accountID string
	err = conn.QueryRow(ctx, `
		INSERT INTO exec_matrix.accounts (name, email, role, created_at)
		VALUES ('Acme', 'a@b.com', 'admin', now())
		RETURNING id
	`).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// Seed an order in the initial state 'pending'. tracking_number left NULL.
	var orderID string
	err = conn.QueryRow(ctx, `
		INSERT INTO exec_matrix.orders (account_id, status, total_cents, busy)
		VALUES ($1, 'pending', 100, '[2024-01-01,2024-02-01)')
		RETURNING id
	`, accountID).Scan(&orderID)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}

	// 1. Legal transition pending -> processing succeeds.
	if _, err := conn.Exec(ctx,
		`UPDATE exec_matrix.orders SET status = 'processing' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("legal transition pending->processing should succeed, got: %v", err)
	}

	// 2. Illegal transition processing -> delivered is rejected with P0001.
	_, err = conn.Exec(ctx,
		`UPDATE exec_matrix.orders SET status = 'delivered' WHERE id = $1`, orderID)
	if err == nil {
		t.Fatal("illegal transition processing->delivered should be rejected")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a PgError for the illegal transition, got: %v", err)
	}
	if pgErr.Code != "P0001" {
		t.Errorf("illegal transition error code = %q, want P0001", pgErr.Code)
	}
	if !strings.Contains(pgErr.Message, "invalid state transition") {
		t.Errorf("illegal transition message = %q, want it to mention 'invalid state transition'", pgErr.Message)
	}

	// 3. Requires branch: ship (processing -> shipped) needs a non-null
	// tracking_number. Attempting to ship without it raises P0001.
	_, err = conn.Exec(ctx,
		`UPDATE exec_matrix.orders SET status = 'shipped' WHERE id = $1`, orderID)
	if err == nil {
		t.Fatal("ship without tracking_number should be rejected")
	}
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a PgError for the requires violation, got: %v", err)
	}
	if pgErr.Code != "P0001" {
		t.Errorf("requires-violation error code = %q, want P0001", pgErr.Code)
	}
	if !strings.Contains(pgErr.Message, "requires non-null tracking_number") {
		t.Errorf("requires-violation message = %q, want it to mention 'requires non-null tracking_number'", pgErr.Message)
	}

	// 4. Ship succeeds once tracking_number is populated in the same UPDATE.
	if _, err := conn.Exec(ctx,
		`UPDATE exec_matrix.orders SET status = 'shipped', tracking_number = 'TRK123' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("ship with tracking_number should succeed, got: %v", err)
	}

	// Sanity: the row is now shipped.
	var status string
	if err := conn.QueryRow(ctx,
		`SELECT status FROM exec_matrix.orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read final status: %v", err)
	}
	if status != "shipped" {
		t.Errorf("final status = %q, want shipped", status)
	}
}
