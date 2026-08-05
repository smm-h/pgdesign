package rev

import (
	"bytes"
	"encoding/json"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// sampleSchema builds a small resolved schema for the tests. It is intentionally
// hand-built (not Built from TOML) so the tests stay pure and DB-free; the enc
// encoder tolerates a hand-built model and DecodeModel re-Canonicalizes.
func sampleSchema() *model.Schema {
	s := &model.Schema{
		Name:       "shop",
		Extensions: []string{"pgcrypto"},
		Enums: []model.Enum{
			{Schema: "shop", Name: "role", Values: []string{"admin", "user"}, Comment: "user role"},
		},
		Tables: []model.Table{
			{
				Name:    "users",
				Schema:  "shop",
				Comment: "all users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true, DefaultExpr: "gen_random_uuid()"},
					{Name: "role", PGType: typeinfo.MustParse("role"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		PGVersion: 16,
	}
	s.Canonicalize()
	return s
}

func TestComputeDeterministic(t *testing.T) {
	testenv.Isolate(t)
	s := sampleSchema()
	r1, err := Compute(s, RegistryPresent)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	r2, err := Compute(sampleSchema(), RegistryPresent)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	eq, err := r1.Equal(r2)
	if err != nil {
		t.Fatalf("Equal: %v", err)
	}
	if !eq {
		t.Errorf("revision not deterministic: %s vs %s", r1, r2)
	}
}

func TestUnknownClassErrors(t *testing.T) {
	testenv.Isolate(t)
	if _, err := Compute(sampleSchema(), ModelClass("")); err == nil {
		t.Fatal("expected error for empty model class")
	}
	if _, err := CanonicalBytes(sampleSchema(), ModelClass("bogus")); err == nil {
		t.Fatal("expected error for unknown model class")
	}
}

// TestCrossClassComparisonErrors is the L7 guarantee: comparing revisions of
// different model classes is a type error, not a silent false.
func TestCrossClassComparisonErrors(t *testing.T) {
	testenv.Isolate(t)
	s := sampleSchema()
	present, err := Compute(s, RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := Compute(s, RegistryAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := present.Equal(absent); err == nil {
		t.Fatal("expected cross-class Equal to error, got nil")
	}
	// The class marker is inside the hashed bytes, so the two classes produce
	// different digests even for the identical structural model.
	if present.Hex() == absent.Hex() {
		t.Error("expected different digests across model classes (marker not inside hashed bytes?)")
	}
}

// TestSameClassComparison confirms Equal works within a class.
func TestSameClassComparison(t *testing.T) {
	testenv.Isolate(t)
	a, _ := Compute(sampleSchema(), RegistryPresent)
	b, _ := Compute(sampleSchema(), RegistryPresent)
	eq, err := a.Equal(b)
	if err != nil {
		t.Fatalf("Equal: %v", err)
	}
	if !eq {
		t.Error("expected equal revisions within a class")
	}
}

// TestEnvelopeRevisionVerifiesAgainstEmbeddedBytes is the core 1.5 verify item:
// the envelope's revision hashes the embedded model bytes exactly.
func TestEnvelopeRevisionVerifiesAgainstEmbeddedBytes(t *testing.T) {
	testenv.Isolate(t)
	s := sampleSchema()
	body, err := Marshal(s, RegistryPresent, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	env, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse (revision verification failed): %v", err)
	}
	// Independently recompute the revision from the model bytes and compare.
	want, err := Compute(s, RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	eq, err := env.Revision.Equal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !eq {
		t.Errorf("envelope revision %s != computed %s", env.Revision, want)
	}
}

// TestEnvelopeTamperDetected confirms Parse rejects an envelope whose embedded
// model bytes were altered after the revision was computed.
func TestEnvelopeTamperDetected(t *testing.T) {
	testenv.Isolate(t)
	body, err := Marshal(sampleSchema(), RegistryPresent, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte inside the embedded model by decoding, mutating, re-encoding
	// the envelope frame (but keeping the old revision string).
	var f envelopeForm
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	f.Model = json.RawMessage(bytes.Replace(f.Model, []byte("shop"), []byte("shoq"), 1))
	tampered, err := compactJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(tampered); err == nil {
		t.Fatal("expected Parse to reject tampered model bytes")
	}
}

// TestParseRejectsForgedClass is the forged-envelope rider (1.5 audit): the
// class named in the revision STRING must match the class marker baked inside
// the embedded model BYTES. An envelope whose string claims one class over
// bytes of another must be rejected — otherwise the returned Revision would
// carry a class tag its bytes do not, corrupting future cross-class Equal.
func TestParseRejectsForgedClass(t *testing.T) {
	testenv.Isolate(t)
	body, err := Marshal(sampleSchema(), RegistryAbsent, nil)
	if err != nil {
		t.Fatal(err)
	}
	var f envelopeForm
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	// Forge: keep the registry_absent MODEL BYTES verbatim, but relabel the
	// revision STRING's class to registry_present (same hex digest). The digest
	// still matches the bytes (the class is not folded into the hash), so only
	// the explicit in-bytes-vs-string class cross-check can catch this.
	_, sum, err := splitRevision(f.Revision)
	if err != nil {
		t.Fatal(err)
	}
	forged := Revision{class: RegistryPresent, sum: sum}
	f.Revision = forged.String()
	tampered, err := compactJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(tampered); err == nil {
		t.Fatal("expected Parse to reject an envelope whose revision-string class does not match the embedded model-bytes class")
	}
}

// TestParseRejectsWrongFormatVersion is the outer-format_version rider (1.5
// audit): an envelope framed under a different serializer generation is
// rejected before its bytes are trusted.
func TestParseRejectsWrongFormatVersion(t *testing.T) {
	testenv.Isolate(t)
	body, err := Marshal(sampleSchema(), RegistryPresent, nil)
	if err != nil {
		t.Fatal(err)
	}
	var f envelopeForm
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	f.FormatVersion = FormatVersion + 1
	tampered, err := compactJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(tampered); err == nil {
		t.Fatal("expected Parse to reject an envelope with a mismatched outer format_version")
	}
}

// TestOneSerializerIdenticalBodies is the 1.5 verify item: `generate json` and
// serve's /schema response call THE SAME function, so identical inputs yield
// byte-identical bodies. Both paths ultimately call Marshal; here we assert
// Marshal is a pure function of its inputs.
func TestOneSerializerIdenticalBodies(t *testing.T) {
	testenv.Isolate(t)
	s := sampleSchema()
	a, err := Marshal(s, RegistryPresent, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(sampleSchema(), RegistryPresent, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("Marshal not byte-stable:\n%s\n%s", a, b)
	}
}

// TestDiagnosticsPreserved confirms diagnostics survive into the envelope.
func TestDiagnosticsPreserved(t *testing.T) {
	testenv.Isolate(t)
	diags := []diagnostic.Diagnostic{
		{Severity: diagnostic.Warning, Code: "W001", Message: "heads up", Table: "users"},
	}
	body, err := Marshal(sampleSchema(), RegistryAbsent, diags)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Diagnostics) != 1 || env.Diagnostics[0].Code != "W001" {
		t.Errorf("diagnostics not preserved: %+v", env.Diagnostics)
	}
	// Diagnostics live OUTSIDE the hashed model bytes: adding them must not
	// change the revision.
	plain, err := Marshal(sampleSchema(), RegistryAbsent, nil)
	if err != nil {
		t.Fatal(err)
	}
	plainEnv, err := Parse(plain)
	if err != nil {
		t.Fatal(err)
	}
	eq, err := env.Revision.Equal(plainEnv.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !eq {
		t.Error("diagnostics leaked into the hashed model bytes (revision changed)")
	}
}

// TestDecodeModelRoundTrip is decode∘enc = id at the whole-model level:
// encoding a canonical model, decoding it, and re-encoding yields byte-identical
// canonical bytes.
func TestDecodeModelRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	s := sampleSchema()
	canonical, err := CanonicalBytes(s, RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	decoded, class, err := DecodeModel(canonical)
	if err != nil {
		t.Fatalf("DecodeModel: %v", err)
	}
	if class != RegistryPresent {
		t.Errorf("expected registry-present, got %s", class)
	}
	reencoded, err := CanonicalBytes(decoded, RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, reencoded) {
		t.Errorf("decode∘enc != id at whole-model level:\n%s\n%s", canonical, reencoded)
	}
}

// TestClassMarkerInBytes confirms the model class is carried inside the
// canonical bytes (the L7 marker), not only on the Revision wrapper.
func TestClassMarkerInBytes(t *testing.T) {
	testenv.Isolate(t)
	present, err := CanonicalBytes(sampleSchema(), RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := CanonicalBytes(sampleSchema(), RegistryAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(present, absent) {
		t.Fatal("class marker not present inside hashed bytes")
	}
	if !bytes.Contains(present, []byte(`"class":"registry_present"`)) {
		t.Error("registry-present marker missing from canonical bytes")
	}
	if !bytes.Contains(absent, []byte(`"class":"registry_absent"`)) {
		t.Error("registry-absent marker missing from canonical bytes")
	}
}
