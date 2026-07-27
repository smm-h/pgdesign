package diff

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/model"
)

// namedatalen is PostgreSQL's NAMEDATALEN. An identifier is truncated to
// NAMEDATALEN-1 = 63 bytes when stored in the catalog.
const maxIdentBytes = 63

// Pair holds a matched desired/actual pair for further comparison.
type Pair[T any] struct {
	Desired T
	Actual  T
}

// truncateIdent truncates an identifier to NAMEDATALEN-1 bytes, mirroring
// PostgreSQL's pg_truncate_identifier. Content-derived constraint and index
// names can exceed this on the desired side while the introspected (actual)
// side comes back truncated. Truncation is by byte for ASCII identifiers (the
// case for auto-generated constraint/index names); PostgreSQL additionally
// backs up to avoid splitting a multibyte character, a refinement not needed
// for the ASCII names this matcher pairs.
func truncateIdent(s string) string {
	if len(s) <= maxIdentBytes {
		return s
	}
	return s[:maxIdentBytes]
}

// truncationCollisions reports every group of two-or-more DISTINCT names in one
// collection that collapse to the same NAMEDATALEN truncation. Such names are
// indistinguishable once PostgreSQL stores them, so matchObjectsTrunc's
// truncation fallback would AMBIGUOUSLY pair more than one desired name against
// the single truncated actual — a silent mis-match. Each returned string names
// the truncation and all the colliding full names. The caller
// (CheckTruncationCollisions) turns a non-empty result into a hard error before
// any matching runs.
func truncationCollisions(names []string) []string {
	// Group every name by its truncation. A group of two-or-more DISTINCT names
	// is a collision: the truncation fallback cannot tell them apart. This
	// naturally also catches the mixed case where one name is <= 63 bytes and
	// another > 63 bytes truncates to it.
	byTrunc := make(map[string][]string)
	for _, n := range names {
		t := truncateIdent(n)
		byTrunc[t] = append(byTrunc[t], n)
	}
	var out []string
	// Deterministic order for stable error messages and tests.
	truncs := make([]string, 0, len(byTrunc))
	for t := range byTrunc {
		truncs = append(truncs, t)
	}
	sort.Strings(truncs)
	for _, t := range truncs {
		group := byTrunc[t]
		// Distinct names only (a name repeated verbatim is a different problem,
		// not a truncation collision).
		distinct := dedupeSorted(group)
		if len(distinct) >= 2 {
			out = append(out, fmt.Sprintf("%q <- %v", t, distinct))
		}
	}
	return out
}

// dedupeSorted returns the sorted, de-duplicated set of a string slice.
func dedupeSorted(s []string) []string {
	seen := make(map[string]bool, len(s))
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// CheckTruncationCollisions is the NAMEDATALEN collision guard for the diff
// path. Content-derived constraint/index names can exceed 63 bytes; when two
// DISTINCT such names in one table collection truncate to the same 63 bytes,
// PostgreSQL stores them identically and matchObjectsTrunc's truncation-aware
// fallback can no longer tell them apart — it would silently pair both desired
// names against the one truncated actual. This guard runs on the DESIRED schema
// before any diff and returns a HARD ERROR naming the truncation and every
// colliding name, per-collection and per-table, so the ambiguity is surfaced
// loudly instead of producing a wrong migration. It is a pure function of the
// desired schema (the collision is a property of desired names alone).
func CheckTruncationCollisions(s *model.Schema) error {
	if s == nil {
		return nil
	}
	var problems []string
	report := func(where string, names []string) {
		for _, c := range truncationCollisions(names) {
			problems = append(problems, fmt.Sprintf("%s: %s", where, c))
		}
	}
	for i := range s.Tables {
		t := &s.Tables[i]
		tk := t.Name
		if t.Schema != "" {
			tk = t.Schema + "." + t.Name
		}
		report(fmt.Sprintf("table %s indexes", tk), names(t.Indexes, func(x model.Index) string { return x.Name }))
		report(fmt.Sprintf("table %s uniques", tk), names(t.Uniques, func(x model.UniqueConstraint) string { return x.Name }))
		report(fmt.Sprintf("table %s checks", tk), names(t.Checks, func(x model.CheckConstraint) string { return x.Name }))
		report(fmt.Sprintf("table %s foreign keys", tk), names(t.FKs, func(x model.FK) string { return x.Name }))
		report(fmt.Sprintf("table %s exclusions", tk), names(t.Exclusions, func(x model.ExclusionConstraint) string { return x.Name }))
	}
	for i := range s.MaterializedViews {
		mv := &s.MaterializedViews[i]
		mk := mv.Name
		if mv.Schema != "" {
			mk = mv.Schema + "." + mv.Name
		}
		report(fmt.Sprintf("materialized view %s indexes", mk), names(mv.Indexes, func(x model.Index) string { return x.Name }))
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("NAMEDATALEN identifier collision (names truncate to the same 63 bytes and become indistinguishable):\n  %s", joinLines(problems))
}

// names extracts the key of each element of a slice.
func names[T any](items []T, key func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = key(it)
	}
	return out
}

// joinLines joins problem lines with a two-space-indented newline.
func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}

// matchObjectsTrunc is matchObjects with NAMEDATALEN truncation awareness for
// content-derived constraint/index names: when a desired key has no exact match
// in actual, its 63-byte truncation is tried, since the introspected side is
// truncated. Used only for constraint/index collections (checks, indexes,
// uniques, FKs, exclusions), whose names are content-derived and can exceed 63
// bytes — never for user-named collections, where a shared 63-byte prefix could
// cause a spurious match. CheckTruncationCollisions runs first on the desired
// schema in production, so the ambiguous two-desired-to-one-actual case is
// rejected before this function is reached.
func matchObjectsTrunc[T any](desired, actual []T, key func(T) string) (added []T, removed []T, matched []Pair[T]) {
	actualByKey := make(map[string]T, len(actual))
	for _, a := range actual {
		actualByKey[key(a)] = a
	}

	matchedActual := make(map[string]bool, len(actual))
	for _, d := range desired {
		k := key(d)
		a, found := actualByKey[k]
		matchKey := k
		if !found {
			if tk := truncateIdent(k); tk != k {
				if a2, ok := actualByKey[tk]; ok {
					a, found, matchKey = a2, true, tk
				}
			}
		}
		if found {
			matched = append(matched, Pair[T]{Desired: d, Actual: a})
			matchedActual[matchKey] = true
		} else {
			added = append(added, d)
		}
	}

	for _, a := range actual {
		if !matchedActual[key(a)] {
			removed = append(removed, a)
		}
	}

	return added, removed, matched
}

// matchObjects classifies items into added (in desired but not actual),
// removed (in actual but not desired), and matched pairs (in both).
// The key function extracts a unique string identifier from each item.
// Order of results follows the order of the input slices:
// added and matched follow desired order; removed follows actual order.
func matchObjects[T any](desired, actual []T, key func(T) string) (added []T, removed []T, matched []Pair[T]) {
	actualByKey := make(map[string]T, len(actual))
	for _, a := range actual {
		actualByKey[key(a)] = a
	}

	desiredKeys := make(map[string]bool, len(desired))
	for _, d := range desired {
		k := key(d)
		desiredKeys[k] = true
		if a, found := actualByKey[k]; found {
			matched = append(matched, Pair[T]{Desired: d, Actual: a})
		} else {
			added = append(added, d)
		}
	}

	for _, a := range actual {
		if !desiredKeys[key(a)] {
			removed = append(removed, a)
		}
	}

	return added, removed, matched
}
