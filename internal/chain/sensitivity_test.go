package chain_test

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// sensitivityBase is a hand-built model carrying every aspect the sensitivity
// tests perturb: an extension, an enum type, a table with a comment and typed
// columns, table groups, and a pg_version. It is hand-built (not modelgen) so
// each perturbation is surgical and readable — modelgen increment A produces no
// extensions/enums, which these tests specifically need.
func sensitivityBase() *model.Schema {
	s := &model.Schema{
		Name:       "shop",
		Extensions: []string{"pgcrypto"},
		Enums: []model.Enum{
			{Schema: "shop", Name: "role", Values: []string{"admin", "user"}, Comment: "user role"},
		},
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "shop",
				Comment: "all users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "role", PGType: typeinfo.MustParse("role"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name:    "orders",
				Schema:  "shop",
				Comment: "orders",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true, DefaultExpr: "gen_random_uuid()"},
				},
				PK: []string{"id"},
			},
		},
		Groups:    map[string][]string{"core": {"users"}},
		PGVersion: 16,
	}
	s.Canonicalize()
	return s
}

// tableIdx returns the index of the table named name (Canonicalize reorders
// tables, so positional access is unsafe).
func tableIdx(t *testing.T, s *model.Schema, name string) int {
	t.Helper()
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return i
		}
	}
	t.Fatalf("table %q not found", name)
	return -1
}

func mustRev(t *testing.T, s *model.Schema) rev.Revision {
	t.Helper()
	r, err := rev.Compute(s, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return r
}

func mustDiffer(t *testing.T, what string, a, b rev.Revision) {
	t.Helper()
	eq, err := a.Equal(b)
	if err != nil {
		t.Fatalf("%s: Equal: %v", what, err)
	}
	if eq {
		t.Errorf("%s: revision did not change (expected a flip): %s", what, a)
	}
}

func mustSame(t *testing.T, what string, a, b rev.Revision) {
	t.Helper()
	eq, err := a.Equal(b)
	if err != nil {
		t.Fatalf("%s: Equal: %v", what, err)
	}
	if !eq {
		t.Errorf("%s: revision changed but should not have: %s vs %s", what, a, b)
	}
}

// TestRevisionSensitivity walks every semantic aspect and asserts a change flips
// the revision; a no-op rebuild does not (roadmap 1.4 verify: comment / column /
// type / pg_version / extension / GROUPS changes flip revisions; no-op rebuilds
// don't).
func TestRevisionSensitivity(t *testing.T) {
	base := mustRev(t, sensitivityBase())

	// Comment change.
	{
		s := sensitivityBase()
		s.Tables[tableIdx(t, s, "users")].Comment = "all the users"
		s.Canonicalize()
		mustDiffer(t, "table comment", base, mustRev(t, s))
	}

	// Column change (add a column).
	{
		s := sensitivityBase()
		u := tableIdx(t, s, "users")
		s.Tables[u].Columns = append(s.Tables[u].Columns, model.Column{
			Name: "email", PGType: typeinfo.MustParse("text"), NotNull: true,
		})
		s.Canonicalize()
		mustDiffer(t, "column added", base, mustRev(t, s))
	}

	// Column type change.
	{
		s := sensitivityBase()
		u := tableIdx(t, s, "users")
		s.Tables[u].Columns[1].PGType = typeinfo.MustParse("text")
		s.Canonicalize()
		mustDiffer(t, "column type", base, mustRev(t, s))
	}

	// Type change (enum values).
	{
		s := sensitivityBase()
		s.Enums[0].Values = []string{"admin", "user", "guest"}
		s.Canonicalize()
		mustDiffer(t, "enum values", base, mustRev(t, s))
	}

	// pg_version change.
	{
		s := sensitivityBase()
		s.PGVersion = 17
		s.Canonicalize()
		mustDiffer(t, "pg_version", base, mustRev(t, s))
	}

	// Extension change.
	{
		s := sensitivityBase()
		s.Extensions = append(s.Extensions, "btree_gist")
		s.Canonicalize()
		mustDiffer(t, "extension added", base, mustRev(t, s))
	}

	// GROUPS change.
	{
		s := sensitivityBase()
		s.Groups["core"] = []string{"users", "orders"}
		s.Canonicalize()
		mustDiffer(t, "groups membership", base, mustRev(t, s))
	}
}

// TestRevisionStableOnNoOpRebuild confirms a rebuild that changes nothing
// semantic does NOT flip the revision: recanonicalizing, and permuting a
// canonical-only collection (table declaration order), both preserve identity.
func TestRevisionStableOnNoOpRebuild(t *testing.T) {
	base := mustRev(t, sensitivityBase())

	// Recanonicalize the same model.
	{
		s := sensitivityBase()
		s.Canonicalize()
		s.Canonicalize()
		mustSame(t, "double canonicalize", base, mustRev(t, s))
	}

	// Permute table declaration order (tables are a canonical-only collection,
	// so order is not semantic): the revision must be unchanged.
	{
		s := sensitivityBase()
		s.Tables[0], s.Tables[1] = s.Tables[1], s.Tables[0]
		s.Canonicalize()
		mustSame(t, "table order permuted", base, mustRev(t, s))
	}
}
