package predicate

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestRenderIdempotentCreateConstraintDB is the DB-gated matrix for the idempotent
// CHECK-constraint create: applied onto a matching DB it is a no-op; onto an empty
// (constraint-absent) DB it creates; onto a DRIFTED DB (same name, different
// definition) it RAISEs precisely; and an alias-spelled but semantically-equal
// clause does NOT raise. This is the consumer-visible semantic the breaking change
// ships: re-applying idempotent DDL now fails loudly on definition drift instead of
// silently skipping.
func TestRenderIdempotentCreateConstraintDB(t *testing.T) {
	testenv.Isolate(t)
	base := Precondition{
		Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk",
		Match: &Match{ConstraintDef: "CHECK (age >= 0)"},
	}
	const createSQL = `ALTER TABLE "public"."users" ADD CONSTRAINT "users_age_chk" CHECK (age >= 0);`
	const canonical = "CHECK ((age >= 0))"

	t.Run("matching definition is a no-op", func(t *testing.T) {
		ctx, conn := setupDB(t)
		if _, err := conn.Exec(ctx, RenderIdempotentCreate(base, createSQL)); err != nil {
			t.Fatalf("matching def must be a no-op, got error: %v", err)
		}
		if got := constraintDef(ctx, t, conn, "users_age_chk"); got != canonical {
			t.Fatalf("constraint changed: got %q want %q", got, canonical)
		}
	})

	t.Run("absent constraint is created", func(t *testing.T) {
		ctx, conn := setupDB(t)
		mustExec(ctx, t, conn, "ALTER TABLE users DROP CONSTRAINT users_age_chk")
		if _, err := conn.Exec(ctx, RenderIdempotentCreate(base, createSQL)); err != nil {
			t.Fatalf("absent constraint must be created, got error: %v", err)
		}
		if got := constraintDef(ctx, t, conn, "users_age_chk"); got != canonical {
			t.Fatalf("constraint not created correctly: got %q want %q", got, canonical)
		}
	})

	t.Run("drifted definition RAISEs precisely", func(t *testing.T) {
		ctx, conn := setupDB(t)
		drift := base
		drift.Match = &Match{ConstraintDef: "CHECK (age >= 18)"}
		driftSQL := `ALTER TABLE "public"."users" ADD CONSTRAINT "users_age_chk" CHECK (age >= 18);`
		_, err := conn.Exec(ctx, RenderIdempotentCreate(drift, driftSQL))
		if err == nil {
			t.Fatal("drifted definition must RAISE, got nil")
		}
		if !strings.Contains(err.Error(), "definition mismatch") {
			t.Fatalf("expected a definition-mismatch RAISE, got: %v", err)
		}
		// The live constraint is unchanged (the create branch never ran).
		if got := constraintDef(ctx, t, conn, "users_age_chk"); got != canonical {
			t.Fatalf("drift check must not mutate: got %q want %q", got, canonical)
		}
	})

	t.Run("alias-spelled equivalent does not RAISE", func(t *testing.T) {
		ctx, conn := setupDB(t)
		alias := base
		// Extra parentheses that PG canonicalizes away to the identical form.
		alias.Match = &Match{ConstraintDef: "CHECK (((age)) >= 0)"}
		aliasSQL := `ALTER TABLE "public"."users" ADD CONSTRAINT "users_age_chk" CHECK (((age)) >= 0);`
		if _, err := conn.Exec(ctx, RenderIdempotentCreate(alias, aliasSQL)); err != nil {
			t.Fatalf("semantically-equal spelling must not RAISE, got: %v", err)
		}
	})
}

// TestRenderIdempotentCreateDefaultDB exercises the column-default round-trip of the
// idempotent-create primitive (not wired into generate — generate folds defaults
// into ADD COLUMN IF NOT EXISTS — but consumed by the apply-loop executor path).
func TestRenderIdempotentCreateDefaultDB(t *testing.T) {
	testenv.Isolate(t)
	// The fixture column users.note has DEFAULT 'hi'.
	const noopSQL = `ALTER TABLE "public"."users" ALTER COLUMN "note" SET DEFAULT 'hi';`

	t.Run("matching default is a no-op", func(t *testing.T) {
		ctx, conn := setupDB(t)
		p := Precondition{Class: ClassColumn, Schema: "public", Table: "users", Name: "note", Match: &Match{ColumnDefault: sp("'hi'")}}
		if _, err := conn.Exec(ctx, RenderIdempotentCreate(p, noopSQL)); err != nil {
			t.Fatalf("matching default must be a no-op, got error: %v", err)
		}
	})

	t.Run("drifted default RAISEs", func(t *testing.T) {
		ctx, conn := setupDB(t)
		p := Precondition{Class: ClassColumn, Schema: "public", Table: "users", Name: "note", Match: &Match{ColumnDefault: sp("'bye'")}}
		_, err := conn.Exec(ctx, RenderIdempotentCreate(p, noopSQL))
		if err == nil {
			t.Fatal("drifted default must RAISE, got nil")
		}
		if !strings.Contains(err.Error(), "default mismatch") {
			t.Fatalf("expected a default-mismatch RAISE, got: %v", err)
		}
	})
}

func sp(s string) *string { return &s }

func mustExec(ctx context.Context, t *testing.T, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func constraintDef(ctx context.Context, t *testing.T, conn *pgx.Conn, name string) string {
	t.Helper()
	var def string
	err := conn.QueryRow(ctx,
		"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1 AND conrelid = 'public.users'::regclass",
		name).Scan(&def)
	if err != nil {
		t.Fatalf("read constraintdef %q: %v", name, err)
	}
	return def
}
