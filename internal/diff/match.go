package diff

// Pair holds a matched desired/actual pair for further comparison.
type Pair[T any] struct {
	Desired T
	Actual  T
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
