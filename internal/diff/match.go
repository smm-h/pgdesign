package diff

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

// matchObjectsTrunc is matchObjects with NAMEDATALEN truncation awareness for
// content-derived constraint/index names: when a desired key has no exact match
// in actual, its 63-byte truncation is tried, since the introspected side is
// truncated. Used only for constraint/index collections (checks, indexes,
// uniques, FKs, exclusions), whose names are content-derived and can exceed 63
// bytes — never for user-named collections, where a shared 63-byte prefix could
// cause a spurious match.
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
