package enc

import "testing"

// TestParseKeyRoundTrip pins ParseKey as the exact inverse of Key.String() for
// every object kind that appears in a manifest.
func TestParseKeyRoundTrip(t *testing.T) {
	cases := []Key{
		{Kind: KindSchemaMeta, Name: "shop"},
		{Kind: KindRegistrySnap},
		{Kind: KindTable, Schema: "public", Name: "users"},
		{Kind: KindTable, Name: "users"}, // unqualified
		{Kind: KindView, Schema: "public", Name: "active_users"},
		{Kind: KindMatView, Name: "daily_totals"},
		{Kind: KindSequence, Schema: "app", Name: "order_seq"},
		{Kind: KindEnum, Name: "status"},
		{Kind: KindDomain, Schema: "public", Name: "email"},
		{Kind: KindComposite, Name: "address"},
		{Kind: KindSMType, Schema: "app", Name: "order_state"},
		{Kind: KindFunction, Schema: "public", Name: "f", ArgSig: "(int4,text)"},
		{Kind: KindFunction, Name: "g", ArgSig: "()"},
	}
	for _, k := range cases {
		s := k.String()
		got, err := ParseKey(s)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", s, err)
		}
		if got != k {
			t.Errorf("ParseKey(%q) = %+v, want %+v", s, got, k)
		}
		if got.String() != s {
			t.Errorf("round-trip String mismatch: %q -> %+v -> %q", s, got, got.String())
		}
	}
}

// TestParseKeyRejectsPseudoAndMalformed pins the hard-error paths: dml/raw
// pseudo-targets never resolve in a manifest, and malformed strings error.
func TestParseKeyRejectsPseudoAndMalformed(t *testing.T) {
	bad := []string{
		"dml:0",
		"raw:3",
		"no-colon",
		"boguskind:public.x",
		"function:public.f(unterminated",
	}
	for _, s := range bad {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("ParseKey(%q) = nil error, want error", s)
		}
	}
}
