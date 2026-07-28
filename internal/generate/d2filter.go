package generate

import (
	"path"

	"github.com/smm-h/pgdesign/internal/model"
)

// filteringActive reports whether any table-filtering knob is set. When false,
// GenerateD2 renders every table (the includedTableKeys sentinel is nil).
func (o D2Options) filteringActive() bool {
	return len(o.Include) > 0 || len(o.Exclude) > 0 || o.IncludeDependencies > 0
}

// includedTableKeys computes the set of owned-table keys (model.TableKey) that
// survive filtering. It returns nil when no filter is active, which callers read
// as "everything is included".
//
// Selection: a table is in the base set when it matches any Include glob (or
// Include is empty) and no Exclude glob. Globs match the bare table name via
// path.Match (*, ?, [class]). With IncludeDependencies > 0 the set is then
// expanded along FK dependencies (the depth-bounded 0.3 walker, TowardReferenced
// direction — the tables each surviving table references), so a focused include
// still shows what its rows point at. Exclude stays authoritative: a dependency
// that matches an Exclude glob is never pulled back in, which is what keeps
// edges to excluded tables danglingless.
func (o D2Options) includedTableKeys(schema *model.Schema) map[string]bool {
	if !o.filteringActive() {
		return nil
	}

	base := make(map[string]bool)
	for i := range schema.Tables {
		t := &schema.Tables[i]
		if matchAnyGlob(o.Include, t.Name, true) && !matchAnyGlob(o.Exclude, t.Name, false) {
			base[model.TableKey(t.Schema, t.Name)] = true
		}
	}

	if o.IncludeDependencies > 0 && schema.FKGraph != nil {
		// Snapshot the seed keys; the walk adds to base as it discovers deps.
		seeds := make([]string, 0, len(base))
		for k := range base {
			seeds = append(seeds, k)
		}
		excluded := func(schemaName, name string) bool {
			return matchAnyGlob(o.Exclude, name, false)
		}
		for _, seed := range seeds {
			schema.FKGraph.WalkCascade(seed, model.TowardReferenced, o.IncludeDependencies,
				func(edge model.FKEdge, _ bool) bool {
					// Do not traverse into excluded tables (keeps them out and
					// prevents their onward edges from resurfacing).
					return !excluded(edge.ToSchema, edge.ToTable)
				},
				func(pathEdges []model.FKEdge) {
					last := pathEdges[len(pathEdges)-1]
					base[model.TableKey(last.ToSchema, last.ToTable)] = true
				})
		}
	}

	return base
}

// matchAnyGlob reports whether name matches any of the patterns. When patterns
// is empty it returns emptyResult (true for an empty Include set = "match all",
// false for an empty Exclude set = "exclude none"). Malformed patterns are
// treated as non-matching.
func matchAnyGlob(patterns []string, name string, emptyResult bool) bool {
	if len(patterns) == 0 {
		return emptyResult
	}
	for _, p := range patterns {
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}
