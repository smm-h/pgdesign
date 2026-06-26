package pgcap

import "testing"

// allCapabilities enumerates every Capability constant so tests stay
// exhaustive when new capabilities are added.
var allCapabilities = []Capability{
	IdentityColumns,
	RestrictiveRLS,
	SequenceIfNotExists,
	MetadataOnlyDefault,
	AttGeneratedColumn,
	TransactionalEnumAdd,
	DropDBForce,
	CreateOrReplaceTrigger,
	CreateOrReplacePolicy,
	VirtualGeneratedCols,
}

func TestRegistryCompleteness(t *testing.T) {
	for _, cap := range allCapabilities {
		info, ok := registry[cap]
		if !ok {
			t.Errorf("capability %d missing from registry", cap)
			continue
		}
		if info.MinVersion <= 0 {
			t.Errorf("capability %s has MinVersion %d, want > 0", info.Name, info.MinVersion)
		}
		if info.Name == "" {
			t.Errorf("capability %d has empty Name", cap)
		}
		if info.Description == "" {
			t.Errorf("capability %s has empty Description", info.Name)
		}
	}
}

func TestNoDuplicateCapabilities(t *testing.T) {
	seen := make(map[Capability]bool)
	for _, cap := range allCapabilities {
		if seen[cap] {
			t.Errorf("duplicate capability constant: %d", cap)
		}
		seen[cap] = true
	}
	if len(registry) != len(allCapabilities) {
		t.Errorf("registry has %d entries but allCapabilities has %d; they must match",
			len(registry), len(allCapabilities))
	}
}

func TestHasBoundary(t *testing.T) {
	for _, cap := range allCapabilities {
		info := registry[cap]
		min := info.MinVersion

		if Has(min-1, cap) {
			t.Errorf("Has(%d, %s) = true, want false (below minimum %d)",
				min-1, info.Name, min)
		}
		if !Has(min, cap) {
			t.Errorf("Has(%d, %s) = false, want true (at minimum)",
				min, info.Name)
		}
		if !Has(min+1, cap) {
			t.Errorf("Has(%d, %s) = false, want true (above minimum)",
				min+1, info.Name)
		}
	}
}

func TestHasVersionZero(t *testing.T) {
	for _, cap := range allCapabilities {
		if Has(0, cap) {
			info := registry[cap]
			t.Errorf("Has(0, %s) = true, want false (version 0 means unknown)",
				info.Name)
		}
	}
}

func TestMinVersion(t *testing.T) {
	expected := map[Capability]int{
		IdentityColumns:        10,
		RestrictiveRLS:         10,
		SequenceIfNotExists:    10,
		MetadataOnlyDefault:    11,
		AttGeneratedColumn:     12,
		TransactionalEnumAdd:   12,
		DropDBForce:            13,
		CreateOrReplaceTrigger: 14,
		CreateOrReplacePolicy:  15,
		VirtualGeneratedCols:   18,
	}
	for cap, want := range expected {
		got := MinVersion(cap)
		if got != want {
			t.Errorf("MinVersion(%d) = %d, want %d", cap, got, want)
		}
	}
}

func TestAllSortedByMinVersion(t *testing.T) {
	all := All()
	if len(all) != len(registry) {
		t.Fatalf("All() returned %d items, want %d", len(all), len(registry))
	}
	for i := 1; i < len(all); i++ {
		prev := all[i-1]
		curr := all[i]
		if curr.MinVersion < prev.MinVersion {
			t.Errorf("All() not sorted: %s (v%d) comes after %s (v%d)",
				curr.Name, curr.MinVersion, prev.Name, prev.MinVersion)
		}
		if curr.MinVersion == prev.MinVersion && curr.Name < prev.Name {
			t.Errorf("All() not sorted by name within same version: %s before %s (both v%d)",
				curr.Name, prev.Name, curr.MinVersion)
		}
	}
}
