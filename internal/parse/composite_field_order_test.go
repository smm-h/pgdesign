package parse

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/semtype"
)

// compositeFieldOrderTOML declares composite type fields in a deliberately
// non-alphabetical order. Declaration order is the semantic order: PostgreSQL
// composite field order affects ROW(...) construction, tuple comparison, and
// the CREATE TYPE ... AS (...) DDL.
const compositeFieldOrderTOML = `
[meta]
version = 1
schema = "public"

[types.mailing_address]
kind = "composite"
comment = "Postal address"

[types.mailing_address.fields]
street = "text"
city = "text"
zip = "text"
country = "text"
region = "text"
building = "integer"
apartment = "integer"
`

// declaredFieldOrder is the exact document order of the keys in the
// [types.mailing_address.fields] section above.
var declaredFieldOrder = []string{
	"street", "city", "zip", "country", "region", "building", "apartment",
}

// compositeExtendsOrderTOML adds a child composite that extends the parent.
// Child additions are declared in non-alphabetical order; the merged type
// must list parent fields first (in parent declaration order), then child
// fields (in child declaration order).
const compositeExtendsOrderTOML = compositeFieldOrderTOML + `
[types.geo_address]
kind = "composite"
extends = "mailing_address"

[types.geo_address.fields]
longitude = "float8"
latitude = "float8"
`

var declaredExtendedFieldOrder = []string{
	"street", "city", "zip", "country", "region", "building", "apartment",
	"longitude", "latitude",
}

// TestCompositeFieldOrder_DeclarationOrder verifies that a single fresh parse
// preserves TOML declaration order for composite type fields, end to end
// through CollectUserTypes and the semtype registry's resolved TypeDef.Fields.
func TestCompositeFieldOrder_DeclarationOrder(t *testing.T) {
	names, resolved := parseCompositeFieldOrder(t)
	assertOrder(t, "CollectUserTypes fields", names, declaredFieldOrder)
	assertOrder(t, "semtype TypeDef.Fields", resolved, declaredFieldOrder)
}

// TestCompositeFieldOrder_RebuildDeterminism parses and loads the same TOML
// from scratch N times. Every rebuild must yield the identical field order.
// Map-backed parsing randomizes the order across builds, which flaps
// freshness checks and silently reorders composite fields (a semantic change).
func TestCompositeFieldOrder_RebuildDeterminism(t *testing.T) {
	const runs = 20
	for i := 0; i < runs; i++ {
		names, resolved := parseCompositeFieldOrder(t)
		assertOrder(t, "CollectUserTypes fields", names, declaredFieldOrder)
		assertOrder(t, "semtype TypeDef.Fields", resolved, declaredFieldOrder)
		if t.Failed() {
			t.Fatalf("order diverged on rebuild %d of %d", i+1, runs)
		}
	}
}

// TestCompositeFieldOrder_ExtendsMerge verifies that a composite type
// extending another lists parent fields first in parent declaration order,
// followed by child additions in child declaration order.
func TestCompositeFieldOrder_ExtendsMerge(t *testing.T) {
	raw, diags := Bytes([]byte(compositeExtendsOrderTOML))
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	userTypes := CollectUserTypes(raw)
	if len(userTypes) != 2 {
		t.Fatalf("expected 2 user types, got %d", len(userTypes))
	}

	reg := semtype.NewBuiltinRegistry()
	if loadDiags := reg.LoadUserTypes(userTypes); loadDiags.HasErrors() {
		t.Fatalf("LoadUserTypes errors: %v", loadDiags)
	}
	td, err := reg.Resolve("geo_address")
	if err != nil {
		t.Fatalf("Resolve(geo_address): %v", err)
	}
	var got []string
	for _, f := range td.Fields {
		got = append(got, f.Name)
	}
	assertOrder(t, "extended TypeDef.Fields", got, declaredExtendedFieldOrder)
}

// parseCompositeFieldOrder does one fresh parse+load cycle and returns the
// field name order as seen by CollectUserTypes and by the semtype registry's
// resolved TypeDef.Fields.
func parseCompositeFieldOrder(t *testing.T) (names, resolved []string) {
	t.Helper()

	raw, diags := Bytes([]byte(compositeFieldOrderTOML))
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	userTypes := CollectUserTypes(raw)
	if len(userTypes) != 1 {
		t.Fatalf("expected 1 user type, got %d", len(userTypes))
	}
	for _, f := range userTypes[0].Fields {
		names = append(names, f.Name)
	}

	reg := semtype.NewBuiltinRegistry()
	if loadDiags := reg.LoadUserTypes(userTypes); loadDiags.HasErrors() {
		t.Fatalf("LoadUserTypes errors: %v", loadDiags)
	}
	td, err := reg.Resolve("mailing_address")
	if err != nil {
		t.Fatalf("Resolve(mailing_address): %v", err)
	}
	for _, f := range td.Fields {
		resolved = append(resolved, f.Name)
	}
	return names, resolved
}
