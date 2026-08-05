package introspect

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"
)

// TestManagedNamePredicates pins the distinction the I201 filtering relies on:
// isManagedObjectName recognizes the whole reserved namespace, while
// isManagedMachineryName recognizes only pgdesign's OWN generated
// trigger-machinery objects. A name that is managed-reserved but NOT machinery
// is a user collision that must surface an I201 (managed && !machinery); a
// machinery name is filtered silently.
func TestManagedNamePredicates(t *testing.T) {
	testenv.Isolate(t)
	cases := []struct {
		name      string
		managed   bool // matches the reserved pattern (filtered)
		machinery bool // pgdesign's own generated object (silent)
	}{
		// pgdesign's own machinery: filtered silently.
		{"_pgdesign_sm_order_status", true, true},
		{"pgdesign_deny_mutation", true, true},

		// pgdesign's own managed relations (tracking table / views): reserved,
		// but not trigger machinery. As relations they are filtered by the
		// table/view/matview paths; the machinery predicate correctly reports
		// false (they are not SM/deny-mutation functions).
		{"pgdesign_migration_ops", true, false},
		{"pgdesign_applied_migrations", true, false},

		// User objects colliding with the reserved pattern: must be visible.
		{"pgdesign_my_helper", true, false},
		{"pgdesign_audit", true, false},

		// Ordinary user objects: not reserved at all.
		{"users", false, false},
		{"order_status_check", false, false},
		{"sm_orders", false, false},
	}

	for _, c := range cases {
		if got := isManagedObjectName(c.name); got != c.managed {
			t.Errorf("isManagedObjectName(%q) = %v, want %v", c.name, got, c.managed)
		}
		if got := isManagedMachineryName(c.name); got != c.machinery {
			t.Errorf("isManagedMachineryName(%q) = %v, want %v", c.name, got, c.machinery)
		}
		// Invariant: machinery names are always a subset of managed names.
		if c.machinery && !c.managed {
			t.Errorf("%q machinery implies managed, but managed=false", c.name)
		}
	}
}

// TestReservedNameDiagIsVisible pins that a user collision produces a warning-
// severity I201 (not silently dropped), so the filtering never goes unnoticed.
func TestReservedNameDiagIsVisible(t *testing.T) {
	testenv.Isolate(t)
	d := reservedNameDiag("function", "public", "pgdesign_my_helper")
	if d.Code != "I201" {
		t.Errorf("code = %q, want I201", d.Code)
	}
	if d.Message == "" {
		t.Error("expected a non-empty message")
	}
}
