package predicate

import "testing"

// TestRenderAssertGolden pins the exact DO-block SQL the renderer emits for
// representative preconditions. This is the "golden idempotent DO-block SQL" the
// idempotent generation path builds on: a RAISE-on-mismatch guard whose text is
// stable across refactors. A change to the emitted SQL (identity-adjacent) turns
// this test red on purpose.
//
// The forms encode the matching-strategy resolution: existence via to_regclass;
// column TYPE via to_regtype OID equality (alias-robust, NOT format_type text);
// index validity via pg_index.indisvalid; and definitional bodies (constraint def)
// via a temp-object round-trip DECLARE block that canonicalizes the MODEL text
// through the live DB and compares PG's own pg_get_constraintdef.
func TestRenderAssertGolden(t *testing.T) {
	cases := []struct {
		name string
		p    Precondition
		want string
	}{
		{
			name: "table must be absent (a create's guard)",
			p:    Precondition{Existence: MustBeAbsent, Class: ClassTable, Schema: "public", Name: "users"},
			want: `DO $pgdpred$
BEGIN
    IF NOT (NOT ((SELECT c.relkind IN ('r','p') FROM pg_class c WHERE c.oid = to_regclass('"public"."users"')) IS TRUE)) THEN
        RAISE EXCEPTION 'pgdesign precondition violated: table public.users (expected absent)';
    END IF;
END
$pgdpred$;`,
		},
		{
			name: "column present and matching type (OID probe via to_regtype)",
			p: Precondition{
				Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age",
				Match: &Match{ColumnType: "integer"},
			},
			want: `DO $pgdpred$
BEGIN
    IF NOT ((EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = to_regclass('"public"."users"') AND a.attname = 'age' AND a.attnum > 0 AND NOT a.attisdropped)) AND (((SELECT COALESCE(a.atttypid = to_regtype('integer')::oid, false) FROM pg_attribute a WHERE a.attrelid = to_regclass('"public"."users"') AND a.attname = 'age' AND a.attnum > 0 AND NOT a.attisdropped) IS TRUE))) THEN
        RAISE EXCEPTION 'pgdesign precondition violated: column public.users.age (expected present)';
    END IF;
END
$pgdpred$;`,
		},
		{
			name: "index must be present and valid (CIC resume guard)",
			p: Precondition{
				Existence: MustBePresent, Class: ClassIndex, Schema: "public", Name: "ix_users_name",
				Match: &Match{IndexMustBeValid: true},
			},
			want: `DO $pgdpred$
BEGIN
    IF NOT (((SELECT true FROM pg_index i WHERE i.indexrelid = to_regclass('"public"."ix_users_name"')) IS TRUE) AND ((SELECT i.indisvalid FROM pg_index i WHERE i.indexrelid = to_regclass('"public"."ix_users_name"')) IS TRUE)) THEN
        RAISE EXCEPTION 'pgdesign precondition violated: index public.ix_users_name (expected present)';
    END IF;
END
$pgdpred$;`,
		},
		{
			name: "constraint present and matching def (temp-object round-trip)",
			p: Precondition{
				Existence: MustBePresent, Class: ClassConstraint, Schema: "public", Table: "users", Name: "users_age_chk",
				Match: &Match{ConstraintDef: "CHECK (age >= 0)"},
			},
			want: `DO $pgdpred$
DECLARE
    found_def text;
    expected_def text;
BEGIN
    IF NOT ((EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conrelid = to_regclass('"public"."users"') AND con.conname = 'users_age_chk')) AND (true)) THEN
        RAISE EXCEPTION 'pgdesign precondition violated: constraint users_age_chk on public.users (expected present)';
    END IF;
    SELECT pg_get_constraintdef(con.oid) INTO found_def
        FROM pg_constraint con
        WHERE con.conrelid = to_regclass('"public"."users"') AND con.conname = 'users_age_chk';
    DROP TABLE IF EXISTS "_pgd_pre_rt";
    CREATE TEMP TABLE "_pgd_pre_rt" (LIKE "public"."users");
    ALTER TABLE "_pgd_pre_rt" ADD CONSTRAINT "_pgd_c" CHECK (age >= 0);
    SELECT pg_get_constraintdef(con.oid) INTO expected_def
        FROM pg_constraint con
        JOIN pg_class r ON r.oid = con.conrelid
        WHERE r.relname = '_pgd_pre_rt' AND con.conname = '_pgd_c' AND r.relnamespace = pg_my_temp_schema();
    DROP TABLE IF EXISTS "_pgd_pre_rt";
    IF found_def IS DISTINCT FROM expected_def THEN
        RAISE EXCEPTION 'pgdesign precondition violated: constraint users_age_chk on public.users (definition mismatch): expected %, found %', expected_def, found_def;
    END IF;
END
$pgdpred$;`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderAssert(c.p)
			if got != c.want {
				t.Errorf("RenderAssert mismatch\n--- got ---\n%s\n--- want ---\n%s", got, c.want)
			}
		})
	}
}
