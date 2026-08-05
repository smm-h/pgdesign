package generate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"oss.terrastruct.com/d2/d2compiler"
)

// filterSchema is a FK chain a -> b -> c, plus a self-referencing users table
// and an audit_log table, for exercising include/exclude globs and
// include-dependencies depth.
func filterSchema() *model.Schema {
	col := func(name, typ string) model.Column {
		return model.Column{Name: name, PGType: typeinfo.MustParse(typ), NotNull: true}
	}
	s := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{Name: "c", Schema: "app", Columns: []model.Column{col("id", "uuid")}, PK: []string{"id"}},
			{
				Name: "b", Schema: "app",
				Columns: []model.Column{col("id", "uuid"), col("c_id", "uuid")}, PK: []string{"id"},
				FKs: []model.FK{{Name: "fk_b_c", Columns: []string{"c_id"}, RefSchema: "app", RefTable: "c", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			},
			{
				Name: "a", Schema: "app",
				Columns: []model.Column{col("id", "uuid"), col("b_id", "uuid")}, PK: []string{"id"},
				FKs: []model.FK{{Name: "fk_a_b", Columns: []string{"b_id"}, RefSchema: "app", RefTable: "b", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			},
			{
				Name: "users", Schema: "app",
				Columns: []model.Column{col("id", "uuid"), col("manager_id", "uuid")}, PK: []string{"id"},
				FKs: []model.FK{{Name: "fk_users_mgr", Columns: []string{"manager_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
			},
			{
				Name: "audit_log", Schema: "app",
				Columns: []model.Column{col("id", "uuid"), col("user_id", "uuid")}, PK: []string{"id"},
				FKs: []model.FK{{Name: "fk_audit_user", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			},
		},
	}
	s.Canonicalize()
	return s
}

func mustCompileD2(t *testing.T, src string) {
	t.Helper()
	if _, _, err := d2compiler.Compile("", strings.NewReader(src), nil); err != nil {
		t.Fatalf("filtered d2 failed to compile: %v\nsource:\n%s", err, src)
	}
}

func TestD2FilterExclude(t *testing.T) {
	testenv.Isolate(t)
	s := filterSchema()
	opts := DefaultD2Options()
	opts.Exclude = []string{"audit_*"}
	out := GenerateD2(s, nil, opts)

	if strings.Contains(out, "audit_log: {") {
		t.Errorf("excluded audit_log should not be rendered:\n%s", out)
	}
	// No dangling: the audit_log -> users edge must be gone.
	if strings.Contains(out, "audit_log.user_id") {
		t.Errorf("edge from excluded table should be skipped:\n%s", out)
	}
	// Non-excluded tables survive.
	for _, tbl := range []string{"a: {", "b: {", "c: {", "users: {"} {
		if !strings.Contains(out, tbl) {
			t.Errorf("expected %q in output:\n%s", tbl, out)
		}
	}
	mustCompileD2(t, out)
}

func TestD2FilterIncludeAndSelfFK(t *testing.T) {
	testenv.Isolate(t)
	s := filterSchema()
	opts := DefaultD2Options()
	opts.Include = []string{"users"}
	out := GenerateD2(s, nil, opts)

	if !strings.Contains(out, "users: {") {
		t.Errorf("included users should render:\n%s", out)
	}
	for _, tbl := range []string{"a: {", "b: {", "c: {", "audit_log: {"} {
		if strings.Contains(out, tbl) {
			t.Errorf("non-included %q should be absent:\n%s", tbl, out)
		}
	}
	// Self-FK preserved (both endpoints are the kept table).
	if !strings.Contains(out, "users.manager_id -> users.id") {
		t.Errorf("self-FK should be preserved:\n%s", out)
	}
	mustCompileD2(t, out)
}

func TestD2FilterIncludeDependenciesDepth(t *testing.T) {
	testenv.Isolate(t)
	s := filterSchema()

	// Depth 1: a plus its direct dependency b (not c).
	opts := DefaultD2Options()
	opts.Include = []string{"a"}
	opts.IncludeDependencies = 1
	out := GenerateD2(s, nil, opts)
	if !strings.Contains(out, "a: {") || !strings.Contains(out, "b: {") {
		t.Errorf("depth 1: expected a and b:\n%s", out)
	}
	if strings.Contains(out, "c: {") {
		t.Errorf("depth 1: c is two hops away, should be absent:\n%s", out)
	}
	if !strings.Contains(out, "a.b_id -> b.id") {
		t.Errorf("depth 1: a->b edge expected:\n%s", out)
	}
	mustCompileD2(t, out)

	// Depth 2: transitive dependency c is pulled in.
	opts.IncludeDependencies = 2
	out = GenerateD2(s, nil, opts)
	if !strings.Contains(out, "c: {") {
		t.Errorf("depth 2: transitive dep c should be present:\n%s", out)
	}
	if !strings.Contains(out, "b.c_id -> c.id") {
		t.Errorf("depth 2: b->c edge expected:\n%s", out)
	}
	mustCompileD2(t, out)
}

func TestD2FilterExcludeAuthoritativeOverDeps(t *testing.T) {
	testenv.Isolate(t)
	s := filterSchema()
	// Include a with deps, but exclude b: the dependency must not resurface, and
	// the transitive c behind it must also stay out.
	opts := DefaultD2Options()
	opts.Include = []string{"a"}
	opts.IncludeDependencies = 5
	opts.Exclude = []string{"b"}
	out := GenerateD2(s, nil, opts)
	if strings.Contains(out, "b: {") {
		t.Errorf("excluded b should not resurface as a dependency:\n%s", out)
	}
	if strings.Contains(out, "c: {") {
		t.Errorf("c is only reachable through excluded b, should stay out:\n%s", out)
	}
	if strings.Contains(out, "a.b_id -> b.id") {
		t.Errorf("edge to excluded b should be skipped:\n%s", out)
	}
	mustCompileD2(t, out)
}

func TestD2FilterPreservesCanonicalOrder(t *testing.T) {
	testenv.Isolate(t)
	s := filterSchema()
	full := GenerateD2(s, nil, DefaultD2Options())

	opts := DefaultD2Options()
	opts.Exclude = []string{"audit_log"}
	filtered := GenerateD2(s, nil, opts)

	// The relative order of surviving table shapes must match the full diagram
	// (canonical TableOrder preserved).
	order := func(src string, names []string) []string {
		var seen []string
		for _, line := range strings.Split(src, "\n") {
			for _, n := range names {
				if strings.HasPrefix(line, n+": {") {
					seen = append(seen, n)
				}
			}
		}
		return seen
	}
	names := []string{"a", "b", "c", "users"}
	fo := order(full, names)
	flo := order(filtered, names)
	if strings.Join(fo, ",") != strings.Join(flo, ",") {
		t.Errorf("filtered order %v differs from full order %v", flo, fo)
	}
}
