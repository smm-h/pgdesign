package generate

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func col(name, typ string) model.Column {
	return model.Column{Name: name, PGType: typeinfo.MustParse(typ), NotNull: true}
}

func TestD2CardinalityOneToMany(t *testing.T) {
	s := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{Name: "users", Schema: "app", Columns: []model.Column{col("id", "uuid")}, PK: []string{"id"}},
			{
				Name: "posts", Schema: "app",
				Columns: []model.Column{col("id", "uuid"), col("author_id", "uuid")}, PK: []string{"id"},
				FKs: []model.FK{{Name: "fk", Columns: []string{"author_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			},
		},
	}
	s.Canonicalize()
	out := GenerateD2(s, nil, DefaultD2Options())

	if !strings.Contains(out, "posts.author_id -> users.id: CASCADE {") {
		t.Errorf("expected edge block, got:\n%s", out)
	}
	if !strings.Contains(out, "source-arrowhead: {shape: cf-many}") {
		t.Errorf("1:N expects cf-many on child end, got:\n%s", out)
	}
	if !strings.Contains(out, "target-arrowhead: {shape: cf-one}") {
		t.Errorf("1:N expects cf-one on parent end, got:\n%s", out)
	}
	mustCompileD2(t, out)
}

func TestD2CardinalityOneToOne(t *testing.T) {
	s := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{Name: "users", Schema: "app", Columns: []model.Column{col("id", "uuid")}, PK: []string{"id"}},
			{
				Name: "profiles", Schema: "app",
				Columns: []model.Column{col("id", "uuid"), col("user_id", "uuid")}, PK: []string{"id"},
				// user_id is UNIQUE -> at most one profile per user -> 1:1.
				Uniques: []model.UniqueConstraint{{Name: "uq_profiles_user", Columns: []string{"user_id"}}},
				FKs:     []model.FK{{Name: "fk", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			},
		},
	}
	s.Canonicalize()
	out := GenerateD2(s, nil, DefaultD2Options())

	if !strings.Contains(out, "profiles.user_id -> users.id: CASCADE {") {
		t.Errorf("expected edge block, got:\n%s", out)
	}
	// Both ends "one".
	if !strings.Contains(out, "source-arrowhead: {shape: cf-one}") {
		t.Errorf("1:1 expects cf-one on child end, got:\n%s", out)
	}
	if strings.Contains(out, "source-arrowhead: {shape: cf-many}") {
		t.Errorf("1:1 should not have a many end, got:\n%s", out)
	}
	mustCompileD2(t, out)
}

// junctionSchema links users and roles via a junction table. extraCol adds a
// non-key column when true, which must defeat the strict-junction collapse.
func junctionSchema(extraCol bool) *model.Schema {
	jt := model.Table{
		Name: "user_roles", Schema: "app",
		Columns: []model.Column{col("user_id", "uuid"), col("role_id", "uuid")},
		PK:      []string{"user_id", "role_id"},
		FKs: []model.FK{
			{Name: "fk_ur_user", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
			{Name: "fk_ur_role", Columns: []string{"role_id"}, RefSchema: "app", RefTable: "roles", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
	}
	if extraCol {
		jt.Columns = append(jt.Columns, model.Column{Name: "assigned_at", PGType: typeinfo.MustParse("timestamptz"), NotNull: true})
	}
	s := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{Name: "users", Schema: "app", Columns: []model.Column{col("id", "uuid")}, PK: []string{"id"}},
			{Name: "roles", Schema: "app", Columns: []model.Column{col("id", "uuid")}, PK: []string{"id"}},
			jt,
		},
	}
	s.Canonicalize()
	return s
}

func TestD2CardinalityManyToManyCollapse(t *testing.T) {
	s := junctionSchema(false)
	out := GenerateD2(s, nil, DefaultD2Options())

	// The junction table shape is gone.
	if strings.Contains(out, "user_roles: {") {
		t.Errorf("strict junction should collapse (no table shape), got:\n%s", out)
	}
	// The two per-FK edges are gone.
	if strings.Contains(out, "user_roles.user_id ->") || strings.Contains(out, "user_roles.role_id ->") {
		t.Errorf("junction FK edges should be replaced by one M:N edge, got:\n%s", out)
	}
	// One M:N edge labeled with the junction, many on both ends. Endpoint order
	// follows canonical FK order (fk_ur_role sorts before fk_ur_user).
	if !strings.Contains(out, "roles -> users: user_roles {") {
		t.Errorf("expected collapsed M:N edge, got:\n%s", out)
	}
	if strings.Count(out, "shape: cf-many") < 2 {
		t.Errorf("M:N edge expects cf-many on both ends, got:\n%s", out)
	}
	mustCompileD2(t, out)
}

func TestD2CardinalityJunctionWithExtraColumnNotCollapsed(t *testing.T) {
	s := junctionSchema(true)
	out := GenerateD2(s, nil, DefaultD2Options())

	// The junction with an extra column stays a first-class table.
	if !strings.Contains(out, "user_roles: {") {
		t.Errorf("junction with extra column must NOT collapse, got:\n%s", out)
	}
	if strings.Contains(out, "users -> roles: user_roles") {
		t.Errorf("junction with extra column must not become an M:N edge, got:\n%s", out)
	}
	// Its two 1:N edges remain.
	if !strings.Contains(out, "user_roles.user_id -> users.id") || !strings.Contains(out, "user_roles.role_id -> roles.id") {
		t.Errorf("expected the two junction FK edges, got:\n%s", out)
	}
	mustCompileD2(t, out)
}

func TestD2CardinalityDisabled(t *testing.T) {
	s := junctionSchema(false)
	opts := DefaultD2Options()
	opts.Cardinality = false
	out := GenerateD2(s, nil, opts)

	// With cardinality off, no crow's-foot and no junction collapse.
	if strings.Contains(out, "arrowhead") {
		t.Errorf("cardinality off: no arrowheads, got:\n%s", out)
	}
	if !strings.Contains(out, "user_roles: {") {
		t.Errorf("cardinality off: junction rendered as a table, got:\n%s", out)
	}
	mustCompileD2(t, out)
}
