package rev

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// TestParseRevisionRoundTrip pins ParseRevision as the string inverse of
// String(), and the empty-string genesis case.
func TestParseRevisionRoundTrip(t *testing.T) {
	s := &model.Schema{Name: "shop", Tables: []model.Table{{Name: "users", Comment: "u"}}}
	s.Canonicalize()
	r, err := Compute(s, RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRevision(r.String())
	if err != nil {
		t.Fatal(err)
	}
	eq, err := got.Equal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !eq {
		t.Fatalf("ParseRevision(%q) != original", r.String())
	}
	if got.Class() != RegistryPresent {
		t.Fatalf("class not preserved: %s", got.Class())
	}

	// Empty string -> zero revision (genesis null parent).
	z, err := ParseRevision("")
	if err != nil {
		t.Fatal(err)
	}
	if !z.IsZero() {
		t.Fatalf("ParseRevision(\"\") is not zero: %s", z)
	}
}

// TestParseRevisionRejectsMalformed pins the hard-error paths.
func TestParseRevisionRejectsMalformed(t *testing.T) {
	bad := []string{
		"no-colon-hex",
		"bogus_class:" + strings.Repeat("a", 64),
		"registry_present:zzzz",
		"registry_present:abcd", // too short
	}
	for _, s := range bad {
		if _, err := ParseRevision(s); err == nil {
			t.Errorf("ParseRevision(%q) = nil error, want error", s)
		}
	}
}
