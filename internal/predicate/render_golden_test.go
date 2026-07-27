package predicate

import "testing"

// TestRenderAssertGolden pins the exact DO-block SQL the renderer emits for
// representative preconditions. This is the "golden idempotent DO-block SQL" the
// idempotent generation path builds on: a RAISE-on-mismatch guard whose text is
// stable across refactors. A change to the emitted SQL (identity-adjacent) turns
// this test red on purpose.
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
			name: "column present and matching type",
			p: Precondition{
				Existence: MustBePresent, Class: ClassColumn, Schema: "public", Table: "users", Name: "age",
				Match: &Match{ColumnType: "integer"},
			},
			want: `DO $pgdpred$
BEGIN
    IF NOT ((EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = to_regclass('"public"."users"') AND a.attname = 'age' AND a.attnum > 0 AND NOT a.attisdropped)) AND (((SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a WHERE a.attrelid = to_regclass('"public"."users"') AND a.attname = 'age' AND a.attnum > 0 AND NOT a.attisdropped) = 'integer'))) THEN
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
    IF NOT (((SELECT true FROM pg_index i WHERE i.indexrelid = to_regclass('"public"."ix_users_name"')) IS TRUE) AND (((SELECT i.indisvalid FROM pg_index i WHERE i.indexrelid = to_regclass('"public"."ix_users_name"')) IS TRUE))) THEN
        RAISE EXCEPTION 'pgdesign precondition violated: index public.ix_users_name (expected present)';
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
