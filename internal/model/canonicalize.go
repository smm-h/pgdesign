package model

import (
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/graph"
)

// Canonicalize is the shared finalize routine that puts a resolved Schema into
// canonical form. It is invoked by Build, BuildMulti, Introspect, and the
// FilterByGroups/FilterBySource filters so that every schema — regardless of
// origin (TOML declaration order or introspect ORDER BY) — serializes to the
// same bytes.
//
// It performs three kinds of work:
//
//  1. Ordering. Per-table collections (FKs, indexes, uniques, checks,
//     exclusions, policies, triggers), materialized-view indexes, top-level
//     type collections (enums, domains, composite types, sequences), and
//     Extensions are sorted alphabetically. Tables, views, materialized views,
//     and functions are ordered topologically with an alphabetical tie-break.
//     Columns, enum values, composite-type fields, function args, partition
//     key columns, FK column correspondence, and index key-column order are
//     SOURCE-ORDERED and never sorted — their order is semantic.
//
//  2. Derived structures. TablesByName and the FKGraph are rebuilt from the
//     (now canonical) tables. Callers that mutate the table set — the group
//     and source filters — rely on this to avoid carrying a stale graph.
//
//  3. Extension point. Once expression normalization lands (roadmap 1.2),
//     Canonicalize will N-normalize expression fields (CHECK/policy/index
//     predicates, defaults) into the IR here, activating full L1(a). Until
//     then, identity is structural with opaque expression leaves.
//
// Canonicalize is idempotent: running it twice yields the same result.
func (s *Schema) Canonicalize() {
	// 1a. Per-table collections: alphabetical by name.
	for i := range s.Tables {
		t := &s.Tables[i]
		sort.Slice(t.FKs, func(a, b int) bool { return t.FKs[a].Name < t.FKs[b].Name })
		sort.Slice(t.Indexes, func(a, b int) bool { return t.Indexes[a].Name < t.Indexes[b].Name })
		sort.Slice(t.Uniques, func(a, b int) bool { return t.Uniques[a].Name < t.Uniques[b].Name })
		sort.Slice(t.Checks, func(a, b int) bool { return t.Checks[a].Name < t.Checks[b].Name })
		sort.Slice(t.Exclusions, func(a, b int) bool { return t.Exclusions[a].Name < t.Exclusions[b].Name })
		sort.Slice(t.Policies, func(a, b int) bool { return t.Policies[a].Name < t.Policies[b].Name })
		sort.Slice(t.Triggers, func(a, b int) bool { return t.Triggers[a].Name < t.Triggers[b].Name })
	}

	// 1b. Materialized-view indexes: alphabetical by name.
	for i := range s.MaterializedViews {
		idxs := s.MaterializedViews[i].Indexes
		sort.Slice(idxs, func(a, b int) bool { return idxs[a].Name < idxs[b].Name })
	}

	// 1c. Top-level type collections and Extensions: alphabetical by name.
	sort.Slice(s.Enums, func(a, b int) bool { return s.Enums[a].Name < s.Enums[b].Name })
	sort.Slice(s.Domains, func(a, b int) bool { return s.Domains[a].Name < s.Domains[b].Name })
	sort.Slice(s.CompositeTypes, func(a, b int) bool { return s.CompositeTypes[a].Name < s.CompositeTypes[b].Name })
	sort.Slice(s.Sequences, func(a, b int) bool { return s.Sequences[a].Name < s.Sequences[b].Name })
	sort.Strings(s.Extensions)

	// 1d. DependsOn is a dependency SET, not a positional list: two models that
	// declare the same dependencies in different orders are ≈_syn-equal and must
	// encode identically. topoSort consumes DependsOn as an edge set
	// (order-insensitive), so sorting it here is purely canonical and cannot
	// change the resulting object order.
	for i := range s.Views {
		sort.Strings(s.Views[i].DependsOn)
	}
	for i := range s.MaterializedViews {
		sort.Strings(s.MaterializedViews[i].DependsOn)
	}
	for i := range s.Functions {
		sort.Strings(s.Functions[i].DependsOn)
	}

	// 1e. Partition children are a CANONICAL-ONLY set (each child is
	// bound-distinguished, so PostgreSQL does not observe their declaration
	// order); sort recursively by child name so permutations converge.
	for i := range s.Tables {
		canonicalizePartition(s.Tables[i].Partitioning)
	}

	// 1f2. State-machine type definitions: name-sorted collection. Within each,
	// transitions are a CANONICAL-ONLY set (sorted by name, then target, then
	// source set) and each transition's From is a source-state SET (sorted).
	// States are NOT sorted — their declaration order is SEMANTIC (it becomes the
	// enum label order in the generated CREATE TYPE ... AS ENUM).
	sort.Slice(s.StateMachines, func(a, b int) bool { return s.StateMachines[a].Name < s.StateMachines[b].Name })
	for i := range s.StateMachines {
		sm := &s.StateMachines[i]
		for j := range sm.Transitions {
			sort.Strings(sm.Transitions[j].From)
		}
		sort.SliceStable(sm.Transitions, func(a, b int) bool {
			return smTransitionKey(sm.Transitions[a]) < smTransitionKey(sm.Transitions[b])
		})
	}

	// 1f. Topological ordering with alphabetical tie-break. Tables carry
	// CycleGroups (used for cycle-safe DDL); views/matviews/functions fall
	// back to a deterministic order even under a cycle (which PostgreSQL
	// rejects at apply time anyway).
	s.Tables, s.CycleGroups = topoSort(s.Tables)
	s.Views = topoSortViewsStable(s.Views)
	s.MaterializedViews = topoSortMatviewsStable(s.MaterializedViews)
	s.Functions = topoSortFunctionsStable(s.Functions)

	// 2. Rebuild derived structures from the canonical tables.
	s.buildTablesByName()
	s.BuildFKGraph()
}

// smTransitionKey builds a TOTAL, deterministic sort key for a state-machine
// transition. Transitions are a canonical-only set, so their order must be a
// pure function of their content. The earlier key was (Name, To, joined-From)
// with an unstable sort — non-total, because two transitions sharing all three
// (differing only in Requires or Comment) sorted nondeterministically. The key
// now includes every field: Name, To, From (a sorted set), Requires (a map,
// serialized in sorted key order), and Comment. \x1f (unit separator) delimits
// components so field boundaries cannot be forged by content containing the
// delimiter's textual form.
func smTransitionKey(t SMTransition) string {
	reqKeys := make([]string, 0, len(t.Requires))
	for k := range t.Requires {
		reqKeys = append(reqKeys, k)
	}
	sort.Strings(reqKeys)
	var req strings.Builder
	for _, k := range reqKeys {
		req.WriteString(k)
		req.WriteByte('=')
		req.WriteString(t.Requires[k])
		req.WriteByte('\x1e')
	}
	return strings.Join([]string{
		t.Name,
		t.To,
		strings.Join(t.From, ","),
		req.String(),
		t.Comment,
	}, "\x1f")
}

// canonicalizePartition recursively sorts a partition subtree's children by
// child name. Children are a CANONICAL-ONLY set (bound-distinguished), so their
// declaration order is not observable and must not affect identity.
func canonicalizePartition(p *PartitionSpec) {
	if p == nil {
		return
	}
	sort.Slice(p.Children, func(a, b int) bool { return p.Children[a].Name < p.Children[b].Name })
	for i := range p.Children {
		canonicalizePartition(&p.Children[i])
	}
}

// topoSortViewsStable orders views by their DependsOn edges with an
// alphabetical tie-break. Cycle members remain in the deterministic order
// TopoSortStable produces.
func topoSortViewsStable(views []View) []View {
	sorted, _ := graph.TopoSortStable(views,
		func(v View) string { return v.Name },
		func(v View) []string { return v.DependsOn },
	)
	return sorted
}

// topoSortMatviewsStable orders materialized views by DependsOn with an
// alphabetical tie-break.
func topoSortMatviewsStable(mvs []MaterializedView) []MaterializedView {
	sorted, _ := graph.TopoSortStable(mvs,
		func(mv MaterializedView) string { return mv.Name },
		func(mv MaterializedView) []string { return mv.DependsOn },
	)
	return sorted
}

// topoSortFunctionsStable orders functions by DependsOn with an alphabetical
// tie-break. Introspected functions carry no DependsOn, so they fall back to
// pure alphabetical order.
func topoSortFunctionsStable(funcs []Function) []Function {
	sorted, _ := graph.TopoSortStable(funcs,
		func(f Function) string { return f.Name },
		func(f Function) []string { return f.DependsOn },
	)
	return sorted
}
