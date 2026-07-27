package diff

import "github.com/smm-h/pgdesign/internal/model"

// liveNormalizeDesired returns a copy of desired in which every table-scoped
// BOOLEAN PREDICATE (CHECK expressions, partial-index and exclusion WHERE
// predicates, policy USING/WITH CHECK) is round-tripped through the target DB
// via ln. Only predicates are round-tripped: they fit a throwaway CHECK, which
// is how ln makes PostgreSQL materialize catalog-dependent casts. Column
// defaults and generated expressions are left as their N-canonical form (they
// are not boolean predicates and do not fit the CHECK round-trip).
//
// The caller's schema is never mutated: the Tables slice and every
// expression-bearing sub-slice are copied before normalization.
func liveNormalizeDesired(desired *model.Schema, ln LiveNormalizer) *model.Schema {
	out := *desired // shallow copy; only Tables (and its expr sub-slices) are deep-copied
	out.Tables = make([]model.Table, len(desired.Tables))
	for i := range desired.Tables {
		t := desired.Tables[i] // struct copy
		schema, name := t.Schema, t.Name

		if len(t.Checks) > 0 {
			checks := make([]model.CheckConstraint, len(t.Checks))
			copy(checks, t.Checks)
			for j := range checks {
				checks[j].Expr = ln.NormalizeExprForTable(schema, name, checks[j].Expr)
			}
			t.Checks = checks
		}
		if len(t.Indexes) > 0 {
			idxs := make([]model.Index, len(t.Indexes))
			copy(idxs, t.Indexes)
			for j := range idxs {
				if idxs[j].Where != "" {
					idxs[j].Where = ln.NormalizeExprForTable(schema, name, idxs[j].Where)
				}
			}
			t.Indexes = idxs
		}
		if len(t.Exclusions) > 0 {
			excs := make([]model.ExclusionConstraint, len(t.Exclusions))
			copy(excs, t.Exclusions)
			for j := range excs {
				if excs[j].Where != "" {
					excs[j].Where = ln.NormalizeExprForTable(schema, name, excs[j].Where)
				}
			}
			t.Exclusions = excs
		}
		if len(t.Policies) > 0 {
			pols := make([]model.Policy, len(t.Policies))
			copy(pols, t.Policies)
			for j := range pols {
				if pols[j].Using != "" {
					pols[j].Using = ln.NormalizeExprForTable(schema, name, pols[j].Using)
				}
				if pols[j].WithCheck != "" {
					pols[j].WithCheck = ln.NormalizeExprForTable(schema, name, pols[j].WithCheck)
				}
			}
			t.Policies = pols
		}

		out.Tables[i] = t
	}
	return &out
}
