package catalog

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// setupDB creates a fresh ephemeral database with a small fixture schema and
// returns the connection (which is both a Querier and exposes Exec).
func setupDB(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	db := testdb.RequireEphemeralDB(t)
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const ddl = `
CREATE TYPE status AS ENUM ('active', 'closed');
CREATE DOMAIN email AS text CHECK (VALUE ~ '@');
CREATE TYPE addr AS (street text, city text);
CREATE TABLE users (
    id integer PRIMARY KEY,
    name text NOT NULL,
    kind status NOT NULL DEFAULT 'active',
    age integer,
    CONSTRAINT users_age_chk CHECK (age >= 0)
);
CREATE INDEX ix_users_name ON users (name);
CREATE VIEW active_users AS SELECT id FROM users WHERE kind = 'active';
CREATE SEQUENCE ticket_seq;
CREATE FUNCTION bump(x integer) RETURNS integer LANGUAGE sql AS $$ SELECT x + 1 $$;
`
	if _, err := conn.Exec(ctx, ddl); err != nil {
		t.Fatalf("fixture ddl: %v", err)
	}
	return ctx, conn
}

func TestExistenceChecks(t *testing.T) {
	testenv.Isolate(t)
	ctx, q := setupDB(t)

	checks := []struct {
		name string
		got  func() (bool, error)
		want bool
	}{
		{"table present", func() (bool, error) { return TableExists(ctx, q, "public", "users") }, true},
		{"table absent", func() (bool, error) { return TableExists(ctx, q, "public", "nope") }, false},
		{"table search-path", func() (bool, error) { return TableExists(ctx, q, "", "users") }, true},
		{"view present", func() (bool, error) { return ViewExists(ctx, q, "public", "active_users") }, true},
		{"view not a table", func() (bool, error) { return TableExists(ctx, q, "public", "active_users") }, false},
		{"sequence present", func() (bool, error) { return SequenceExists(ctx, q, "public", "ticket_seq") }, true},
		{"enum present", func() (bool, error) { return EnumExists(ctx, q, "public", "status") }, true},
		{"domain present", func() (bool, error) { return DomainExists(ctx, q, "public", "email") }, true},
		{"composite present", func() (bool, error) { return CompositeExists(ctx, q, "public", "addr") }, true},
		{"enum not domain", func() (bool, error) { return DomainExists(ctx, q, "public", "status") }, false},
		{"function present", func() (bool, error) { return FunctionExists(ctx, q, "public", "bump", "(integer)") }, true},
		{"function wrong args", func() (bool, error) { return FunctionExists(ctx, q, "public", "bump", "(text)") }, false},
		{"enum value present", func() (bool, error) { return EnumHasValue(ctx, q, "public", "status", "active") }, true},
		{"enum value absent", func() (bool, error) { return EnumHasValue(ctx, q, "public", "status", "pending") }, false},
		{"extension plpgsql", func() (bool, error) { return ExtensionExists(ctx, q, "plpgsql") }, true},
		{"extension absent", func() (bool, error) { return ExtensionExists(ctx, q, "nope_ext") }, false},
	}
	for _, c := range checks {
		got, err := c.got()
		if err != nil {
			t.Errorf("%s: error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestColumn(t *testing.T) {
	testenv.Isolate(t)
	ctx, q := setupDB(t)
	v, err := Version(ctx, q)
	if err != nil {
		t.Fatal(err)
	}

	name, ok, err := Column(ctx, q, v, "public", "users", "name")
	if err != nil || !ok {
		t.Fatalf("column name: ok=%v err=%v", ok, err)
	}
	if name.Type != "text" || !name.NotNull || name.Default != "" {
		t.Errorf("name col: %+v", name)
	}

	kind, ok, err := Column(ctx, q, v, "public", "users", "kind")
	if err != nil || !ok {
		t.Fatalf("column kind: ok=%v err=%v", ok, err)
	}
	if !name.NotNull || kind.Default == "" {
		t.Errorf("kind col expected a default: %+v", kind)
	}

	age, ok, err := Column(ctx, q, v, "public", "users", "age")
	if err != nil || !ok {
		t.Fatalf("column age: ok=%v err=%v", ok, err)
	}
	if age.NotNull {
		t.Errorf("age should be nullable: %+v", age)
	}

	if _, ok, _ := Column(ctx, q, v, "public", "users", "missing"); ok {
		t.Error("missing column reported present")
	}
	if _, ok, _ := Column(ctx, q, v, "public", "nope", "id"); ok {
		t.Error("column of missing table reported present")
	}
}

func TestConstraintAndIndex(t *testing.T) {
	testenv.Isolate(t)
	ctx, q := setupDB(t)

	def, ok, err := ConstraintDef(ctx, q, "public", "users", "users_age_chk")
	if err != nil || !ok {
		t.Fatalf("constraint: ok=%v err=%v", ok, err)
	}
	if def == "" {
		t.Error("empty constraint def")
	}
	if _, ok, _ := ConstraintDef(ctx, q, "public", "users", "no_such_chk"); ok {
		t.Error("missing constraint reported present")
	}

	ix, ok, err := Index(ctx, q, "public", "ix_users_name")
	if err != nil || !ok {
		t.Fatalf("index: ok=%v err=%v", ok, err)
	}
	if !ix.Valid || ix.Def == "" {
		t.Errorf("index expected valid with def: %+v", ix)
	}
	if _, ok, _ := Index(ctx, q, "public", "no_such_index"); ok {
		t.Error("missing index reported present")
	}
}

func TestIndexInvalid(t *testing.T) {
	testenv.Isolate(t)
	ctx, c := setupDB(t)
	if _, err := c.Exec(ctx, "CREATE TABLE dups (v integer NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, "INSERT INTO dups VALUES (1),(1),(2)"); err != nil {
		t.Fatal(err)
	}
	if err := testdb.CreateInvalidIndex(ctx, c, "ix_dups_v", "dups", "v"); err != nil {
		t.Fatal(err)
	}
	ix, ok, err := Index(ctx, c, "public", "ix_dups_v")
	if err != nil || !ok {
		t.Fatalf("invalid index: ok=%v err=%v", ok, err)
	}
	if ix.Valid {
		t.Error("expected indisvalid=false for interrupted-CIC index")
	}
}
