package introspect

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestApplyDomainTypes pins the roundtrip hardening for domain-backed columns
// (roadmap 5.10 hardening item c): a column whose introspected Base is the domain
// name (bare in the search path, schema-qualified outside it) is rewritten to the
// domain's base type with the BARE domain name in DomainName, matching what the
// TOML build produces.
func TestApplyDomainTypes(t *testing.T) {
	schema := &model.Schema{
		Name: "public",
		Domains: []model.Domain{
			{Name: "email", Schema: "public", BaseType: typeinfo.T("text"), Check: "VALUE ~ '@'"},
			{Name: "amount", Schema: "billing", BaseType: typeinfo.T("numeric"), Check: "VALUE > 0"},
		},
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
			Columns: []model.Column{
				// Bare (in search path).
				{Name: "e", PGType: typeinfo.Parse("email")},
				// Schema-qualified (outside search path).
				{Name: "a", PGType: typeinfo.Parse("billing.amount")},
				// A non-domain column must be untouched.
				{Name: "n", PGType: typeinfo.T("int8")},
			},
		}},
	}
	applyDomainTypes(schema)

	cols := schema.Tables[0].Columns
	if cols[0].PGType.Base != "text" || cols[0].PGType.DomainName != "email" {
		t.Fatalf("bare domain col: Base=%q DomainName=%q, want text/email", cols[0].PGType.Base, cols[0].PGType.DomainName)
	}
	if cols[1].PGType.Base != "numeric" || cols[1].PGType.DomainName != "amount" {
		t.Fatalf("schema-qualified domain col: Base=%q DomainName=%q, want numeric/amount (bare)", cols[1].PGType.Base, cols[1].PGType.DomainName)
	}
	if cols[2].PGType.Base != "int8" || cols[2].PGType.DomainName != "" {
		t.Fatalf("non-domain col must be untouched: Base=%q DomainName=%q", cols[2].PGType.Base, cols[2].PGType.DomainName)
	}
}
