package imports

import (
	"testing"
)

// TestLoadSurface_DecodesVendoredReferenceTables verifies the 7.3 loader decodes
// the vendored surface back into REFERENCE tables stamped into the target schema.
func TestLoadSurface_DecodesVendoredReferenceTables(t *testing.T) {
	projectDir := lockAlias(t, []string{"users"})

	surface, err := LoadSurface(projectDir, "framework")
	if err != nil {
		t.Fatalf("LoadSurface: %v", err)
	}
	if len(surface.Tables) != 1 {
		t.Fatalf("expected 1 imported table, got %d", len(surface.Tables))
	}
	users := surface.Tables[0]
	if users.Name != "users" || users.Schema != "app" {
		t.Errorf("imported table mis-stamped: schema=%q name=%q (want app.users)", users.Schema, users.Name)
	}
}

// TestLoadAllSurfaces_SkipsUnlockedAggregatesLocked verifies aggregation across
// aliases and that an alias without a lockfile is silently skipped.
func TestLoadAllSurfaces_SkipsUnlockedAggregatesLocked(t *testing.T) {
	projectDir := lockAlias(t, []string{"users"})

	// "framework" is locked; "other" is declared but never locked.
	agg, err := LoadAllSurfaces(projectDir, []string{"framework", "other"})
	if err != nil {
		t.Fatalf("LoadAllSurfaces: %v", err)
	}
	if len(agg.Tables) != 1 {
		t.Fatalf("expected 1 aggregated table (only locked alias), got %d", len(agg.Tables))
	}
}
