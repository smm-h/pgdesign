package migrate

import (
	"context"
	"testing"

	"github.com/smm-h/pgdesign/internal/catalog"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// attrDriftStore builds a scratch object store for these unit-style DB tests.
func attrDriftStore(t *testing.T) *objstore.Store {
	t.Helper()
	store, err := objstore.New(t.TempDir(), uint32(enc.CodecVersion))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// fromModelColumn builds a one-table parent model whose single column carries the
// given base type — the recorded pre-state an alter/drop precondition expects.
func fromModelColumn(colType string) *model.Schema {
	return &model.Schema{Tables: []model.Table{{
		Schema:  "public",
		Name:    "t",
		Columns: []model.Column{{Name: "a", PGType: typeinfo.Type{Base: colType}}},
	}}}
}

// TestPreconditionColumnTypeFromManifest exercises the from-manifest attribute
// precondition for a column TYPE alter: the pre-state type comes from the parent
// model and is OID-probed, so alias spellings (int4 vs integer) do NOT false-drift
// while a genuinely wrong live type is caught precisely.
func TestPreconditionColumnTypeFromManifest(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	v, err := catalog.Version(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(ctx, "CREATE TABLE public.t (a integer)"); err != nil {
		t.Fatal(err)
	}
	store := attrDriftStore(t)
	// alter_column_type carries the NEW type (bigint); the parent model records the
	// OLD type the live column must currently have.
	op, err := DDLOpToSelfContained(store, DDLOp{
		Op: "alter_column_type", Table: "public.t", Column: "a", Type: "bigint", PGVersion: v,
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Parent recorded the column as int4 (alias of integer): OID probe must NOT drift.
	if err := checkPreconditions(ctx, conn, store, fromModelColumn("int4"), op); err != nil {
		t.Errorf("alias int4 vs live integer must not false-drift: %v", err)
	}
	// Canonical spelling also matches.
	if err := checkPreconditions(ctx, conn, store, fromModelColumn("integer"), op); err != nil {
		t.Errorf("canonical integer must match: %v", err)
	}

	// Now drift the live column to text: the recorded pre-state (int4) no longer holds.
	if _, err := conn.Exec(ctx, "ALTER TABLE public.t ALTER COLUMN a TYPE text USING a::text"); err != nil {
		t.Fatal(err)
	}
	err = checkPreconditions(ctx, conn, store, fromModelColumn("int4"), op)
	if err == nil {
		t.Fatal("expected a type-drift precondition error")
	}
	for _, want := range []string{"column public.t.a type", "expected int4", "found text"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestPreconditionConstraintDefFromManifest exercises the from-manifest CHECK
// definition precondition on a drop_check: the parent CHECK expression is
// round-tripped, so equivalent spellings do NOT false-drift and a genuine
// definition mismatch is caught.
func TestPreconditionConstraintDefFromManifest(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE public.t (a integer, CONSTRAINT c CHECK (a >= 0))"); err != nil {
		t.Fatal(err)
	}
	store := attrDriftStore(t)
	op, err := DDLOpToSelfContained(store, DDLOp{
		Op: "drop_check", Table: "public.t", Name: "c",
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	fromWith := func(expr string) *model.Schema {
		return &model.Schema{Tables: []model.Table{{
			Schema: "public", Name: "t",
			Columns: []model.Column{{Name: "a", PGType: typeinfo.Type{Base: "integer"}}},
			Checks:  []model.CheckConstraint{{Name: "c", Expr: expr}},
		}}}
	}

	// Model spelling "a >= 0" round-trips to the same canonical form as the live
	// "CHECK ((a >= 0))" — no false drift.
	if err := checkPreconditions(ctx, conn, store, fromWith("a >= 0"), op); err != nil {
		t.Errorf("equivalent CHECK spelling must not false-drift: %v", err)
	}

	// A genuinely different recorded CHECK is caught.
	err = checkPreconditions(ctx, conn, store, fromWith("a > 100"), op)
	if err == nil {
		t.Fatal("expected a CHECK definition drift error")
	}
	if !contains(err.Error(), "constraint c on public.t") {
		t.Errorf("error %q missing object identity", err.Error())
	}
}

// TestPreconditionMissingTableFromManifest confirms a drop against an absent object
// still hard-errors (existence check precedes any attribute match).
func TestPreconditionMissingTableFromManifest(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	store := attrDriftStore(t)
	op, err := DDLOpToSelfContained(store, DDLOp{Op: "drop_table", Table: "public.gone"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A from-model is irrelevant here; the table simply does not exist.
	err = checkPreconditions(ctx, conn, store, fromModelColumn("integer"), op)
	if err == nil {
		t.Fatal("expected a missing-table precondition error")
	}
	for _, want := range []string{"table public.gone", "expected present", "found absent"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}
