package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"
)

// TestBaselineTwoHeadsGuardRefusesPreExistingChain: checkBaselineEmptyChain must
// hard-error when the on-disk chain already carries a live head that is not the
// baseline target (a pre-existing chain cannot be baselined — that would fork it
// into two cross-class heads). The remediation names regenerating the chain.
func TestBaselineTwoHeadsGuardRefusesPreExistingChain(t *testing.T) {
	testenv.Isolate(t)
	p, e, _ := fixtureProject(t)
	if _, err := p.WriteEdge(e); err != nil {
		t.Fatalf("write live edge: %v", err)
	}

	// A baseline target distinct from the existing live head.
	other := revAt(t, 0xBADCAFE)
	err := checkBaselineEmptyChain(p, other)
	if err == nil {
		t.Fatal("baseline must refuse when a live head already exists in the chain")
	}
	if !strings.Contains(err.Error(), "already has a chain") ||
		!strings.Contains(err.Error(), "regenerate the chain") {
		t.Errorf("error should name the real remediation, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), e.Target.String()) {
		t.Errorf("error should name the existing live head %s, got: %s", e.Target, err.Error())
	}
}

// TestBaselineTwoHeadsGuardAllowsEmptyChain: an empty chain (no live edges) has no
// head, so the guard passes.
func TestBaselineTwoHeadsGuardAllowsEmptyChain(t *testing.T) {
	testenv.Isolate(t)
	p, e, _ := fixtureProject(t)
	if err := checkBaselineEmptyChain(p, e.Target); err != nil {
		t.Fatalf("empty chain must pass the two-heads guard, got: %v", err)
	}
}

// TestBaselineTwoHeadsGuardAllowsTargetHead: when the sole live head IS the
// baseline target (an idempotent re-run whose edge was written but whose stamp did
// not complete), the guard passes.
func TestBaselineTwoHeadsGuardAllowsTargetHead(t *testing.T) {
	testenv.Isolate(t)
	p, e, _ := fixtureProject(t)
	if _, err := p.WriteEdge(e); err != nil {
		t.Fatalf("write live edge: %v", err)
	}
	if err := checkBaselineEmptyChain(p, e.Target); err != nil {
		t.Fatalf("a live head equal to the baseline target must pass, got: %v", err)
	}
}
