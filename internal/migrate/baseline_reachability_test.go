package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/rev"
)

// TestBaselineReachabilityDivergence: the divergence guard fires for an off-chain
// stamped position (a position that is not any edge endpoint) and passes for a
// chain-reachable one.
func TestBaselineReachabilityDivergence(t *testing.T) {
	testenv.Isolate(t)
	r0, r1, r2 := revAt(t, 0), revAt(t, 1), revAt(t, 2)
	all := []Edge{edgeOf(rev.Revision{}, r0, "genesis"), edgeOf(r0, r1, "e1"), edgeOf(r1, r2, "e2")}
	remap := RemapTable{}

	// Off-chain stamped position -> divergence.
	off := revAt(t, 0xDEAD)
	err := baselineReachability(all, remap, off.String(), r2)
	if err == nil || !strings.Contains(err.Error(), "divergence") {
		t.Fatalf("off-chain position must be a divergence error, got: %v", err)
	}

	// Chain-reachable stamped position (r0) with a reachable target (r2): no
	// divergence, target reachable -> passes.
	if err := baselineReachability(all, remap, r0.String(), r2); err != nil {
		t.Fatalf("a chain-reachable position with a reachable target must pass, got: %v", err)
	}
}

// TestBaselineReachabilityOutOfOrder: the out-of-order guard fires when the
// baseline target is not reachable FROM the stamped position (cannot baseline
// backward), and passes when it is forward-reachable.
func TestBaselineReachabilityOutOfOrder(t *testing.T) {
	testenv.Isolate(t)
	r0, r1, r2 := revAt(t, 0), revAt(t, 1), revAt(t, 2)
	all := []Edge{edgeOf(rev.Revision{}, r0, "genesis"), edgeOf(r0, r1, "e1"), edgeOf(r1, r2, "e2")}
	remap := RemapTable{}

	// Stamped at r2 (advanced), target r0 is not forward-reachable from r2 ->
	// out-of-order (cannot baseline backward). r2 is a valid chain node, so the
	// divergence guard passes first, isolating the out-of-order guard.
	err := baselineReachability(all, remap, r2.String(), r0)
	if err == nil || !strings.Contains(err.Error(), "out-of-order") {
		t.Fatalf("an unreachable-forward target must be an out-of-order error, got: %v", err)
	}

	// Stamped at r0, target r2 forward-reachable -> passes.
	if err := baselineReachability(all, remap, r0.String(), r2); err != nil {
		t.Fatalf("a forward-reachable target must pass, got: %v", err)
	}
}
