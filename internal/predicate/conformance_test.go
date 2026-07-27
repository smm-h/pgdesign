package predicate

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/catalog"
	"github.com/smm-h/pgdesign/internal/testdb"
)

func setupDB(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	url := os.Getenv("PGDESIGN_DB")
	if url == "" {
		url = "postgres://localhost:5432/postgres?sslmode=disable"
	}
	mgr, err := testdb.NewManager(url)
	if err != nil {
		t.Skipf("no manager: %v", err)
	}
	db := mgr.SetupForTest(t, testdb.CreateOptions{})
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const ddl = `
CREATE TYPE status AS ENUM ('active', 'closed');
CREATE DOMAIN email AS text CHECK (VALUE ~ '@');
CREATE TABLE users (
    id integer PRIMARY KEY,
    name text NOT NULL,
    age integer,
    note text DEFAULT 'hi',
    CONSTRAINT users_age_chk CHECK (age >= 0)
);
CREATE INDEX ix_users_name ON users (name);
CREATE VIEW active_users AS SELECT id FROM users;
CREATE SEQUENCE ticket_seq;
CREATE FUNCTION bump(x integer) RETURNS integer LANGUAGE sql AS $$ SELECT x + 1 $$;
`
	if _, err := conn.Exec(ctx, ddl); err != nil {
		t.Fatalf("fixture ddl: %v", err)
	}
	return ctx, conn
}

// sqlVerdict executes the SQL-rendered assertion; the precondition HOLDS (ok=true)
// when the DO block runs without raising, and is VIOLATED (ok=false) when it raises.
func sqlVerdict(ctx context.Context, t *testing.T, conn *pgx.Conn, p Precondition) bool {
	t.Helper()
	_, err := conn.Exec(ctx, RenderAssert(p))
	return err == nil
}

func b(v bool) *bool     { return &v }
func s(v string) *string { return &v }

// TestConformanceAndMatrix is the conformance matrix: for every (precondition,
// state) case, the Go executor and the SQL renderer must return IDENTICAL
// verdicts, and that verdict must equal the expected one. This pins the two
// computations of the predicate against each other and against the world under the
// matching-strategy resolution: OID probes for types, in-DB round-trip for
// definitional bodies (constraint def / column default), booleans for not-null and
// index validity.
func TestConformanceAndMatrix(t *testing.T) {
	ctx, conn := setupDB(t)
	v, err := catalog.Version(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}

	// Catalog-canonical constraint def, to show the canonical spelling also matches.
	usersAgeDef := func() string {
		def, _, err := catalog.ConstraintDef(ctx, conn, "public", "users", "users_age_chk")
		if err != nil {
			t.Fatal(err)
		}
		return def
	}()

	cases := []struct {
		name string
		p    Precondition
		want bool // true = precondition holds
	}{
		// Creates: object must be absent.
		{"create table absent", Precondition{Existence: MustBeAbsent, Class: ClassTable, Schema: "public", Name: "orders"}, true},
		{"create table already present (drift)", Precondition{Existence: MustBeAbsent, Class: ClassTable, Schema: "public", Name: "users"}, false},
		{"create index absent", Precondition{Existence: MustBeAbsent, Class: ClassIndex, Schema: "public", Name: "ix_new"}, true},
		{"create index present (drift)", Precondition{Existence: MustBeAbsent, Class: ClassIndex, Schema: "public", Name: "ix_users_name"}, false},
		{"create enum value absent", Precondition{Existence: MustBeAbsent, Class: ClassEnumValue, Schema: "public", Name: "status", Value: "pending"}, true},

		// Drops/alters: object must be present.
		{"drop table present", Precondition{Existence: MustBePresent, Class: ClassTable, Schema: "public", Name: "users"}, true},
		{"drop table missing", Precondition{Existence: MustBePresent, Class: ClassTable, Schema: "public", Name: "ghost"}, false},
		{"drop view present", Precondition{Existence: MustBePresent, Class: ClassView, Schema: "public", Name: "active_users"}, true},
		{"drop sequence present", Precondition{Existence: MustBePresent, Class: ClassSequence, Schema: "public", Name: "ticket_seq"}, true},
		{"drop enum present", Precondition{Existence: MustBePresent, Class: ClassEnum, Schema: "public", Name: "status"}, true},
		{"drop domain present", Precondition{Existence: MustBePresent, Class: ClassDomain, Schema: "public", Name: "email"}, true},
		{"drop function present", Precondition{Existence: MustBePresent, Class: ClassFunction, Schema: "public", Name: "bump", ArgSig: "(integer)"}, true},
		{"drop function wrong sig missing", Precondition{Existence: MustBePresent, Class: ClassFunction, Schema: "public", Name: "bump", ArgSig: "(text)"}, false},
		{"drop column present", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v}, true},
		{"drop column missing", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "missing", PGVersion: v}, false},
		{"drop constraint present", Precondition{Existence: MustBePresent, Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk"}, true},

		// Column TYPE — OID probe (alias-robust). int4/int MUST NOT false-drift vs integer.
		{"column type matches canonical", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnType: "integer"}}, true},
		{"column type matches alias int4 (no false drift)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnType: "int4"}}, true},
		{"column type matches alias int (no false drift)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnType: "int"}}, true},
		{"column type mismatch (genuine wrong type)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnType: "text"}}, false},
		{"column type unparseable expected (mismatch)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnType: "no_such_type"}}, false},

		// Column NOT NULL — boolean.
		{"column notnull matches", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "name", PGVersion: v, Match: &Match{ColumnNotNull: b(true)}}, true},
		{"column notnull mismatch", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnNotNull: b(true)}}, false},

		// Column DEFAULT — "no default" boolean case + round-trip case.
		{"column default matches (none)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnDefault: s("")}}, true},
		{"column default round-trip matches (model spelling)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "note", PGVersion: v, Match: &Match{ColumnDefault: s("'hi'")}}, true},
		{"column default round-trip mismatch", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "note", PGVersion: v, Match: &Match{ColumnDefault: s("'bye'")}}, false},
		{"column default expected-none but present (drift)", Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "note", PGVersion: v, Match: &Match{ColumnDefault: s("")}}, false},

		// Constraint DEF — round-trip. Equivalent spellings must NOT false-drift.
		{"constraint def matches canonical", Precondition{Existence: MustBePresent, Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk", Match: &Match{ConstraintDef: usersAgeDef}}, true},
		{"constraint def matches model spelling (no false drift)", Precondition{Existence: MustBePresent, Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk", Match: &Match{ConstraintDef: "CHECK (age >= 0)"}}, true},
		{"constraint def genuine mismatch", Precondition{Existence: MustBePresent, Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk", Match: &Match{ConstraintDef: "CHECK (age > 100)"}}, false},

		// Index — existence + validity boolean (definition body is existence-only).
		{"index present and valid", Precondition{Existence: MustBePresent, Class: ClassIndex, Schema: "public", Name: "ix_users_name", Match: &Match{IndexMustBeValid: true}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Check(ctx, conn, c.p)
			if err != nil {
				t.Fatalf("Go executor error: %v", err)
			}
			goVerdict := r.OK
			sqlV := sqlVerdict(ctx, t, conn, c.p)
			if goVerdict != sqlV {
				t.Errorf("CONFORMANCE FAILURE: Go=%v SQL=%v for %+v", goVerdict, sqlV, c.p)
			}
			if goVerdict != c.want {
				t.Errorf("verdict %v, want %v (Result: %+v)", goVerdict, c.want, r)
			}
		})
	}
}

// TestInvalidIndexPrecondition verifies a present-but-INVALID index (interrupted
// CIC) is a MISMATCH under IndexMustBeValid — both backends agree.
func TestInvalidIndexPrecondition(t *testing.T) {
	ctx, conn := setupDB(t)
	if _, err := conn.Exec(ctx, "CREATE TABLE dups (v integer NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO dups VALUES (1),(1),(2)"); err != nil {
		t.Fatal(err)
	}
	if err := testdb.CreateInvalidIndex(ctx, conn, "ix_dups_v", "dups", "v"); err != nil {
		t.Fatal(err)
	}
	p := Precondition{Existence: MustBePresent, Class: ClassIndex, Schema: "public", Name: "ix_dups_v", Match: &Match{IndexMustBeValid: true}}
	r, err := Check(ctx, conn, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.OK {
		t.Error("expected invalid index to FAIL IndexMustBeValid")
	}
	if sqlVerdict(ctx, t, conn, p) {
		t.Error("SQL renderer accepted invalid index under IndexMustBeValid")
	}
}

// TestPreciseError checks the structured object/expected/found diagnostic.
func TestPreciseError(t *testing.T) {
	ctx, conn := setupDB(t)
	v, _ := catalog.Version(ctx, conn)
	p := Precondition{Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age", PGVersion: v, Match: &Match{ColumnType: "text"}}
	r, err := Check(ctx, conn, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.OK {
		t.Fatal("expected mismatch")
	}
	e := r.Err().Error()
	for _, want := range []string{"column public.users.age type", "expected text", "found integer"} {
		if !contains(e, want) {
			t.Errorf("error %q missing %q", e, want)
		}
	}
}

// TestConstraintDefPreciseError checks the round-trip mismatch names the canonical
// expected and found constraint bodies.
func TestConstraintDefPreciseError(t *testing.T) {
	ctx, conn := setupDB(t)
	p := Precondition{Existence: MustBePresent, Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk", Match: &Match{ConstraintDef: "CHECK (age > 100)"}}
	r, err := Check(ctx, conn, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.OK {
		t.Fatal("expected constraint mismatch")
	}
	e := r.Err().Error()
	// Expected is PG's canonical round-trip of the model text; found is the live def.
	for _, want := range []string{"age > 100", "age >= 0"} {
		if !contains(e, want) {
			t.Errorf("error %q missing %q", e, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
