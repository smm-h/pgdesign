package enc

import (
	"bytes"
	"testing"

	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// richSchema builds a canonical schema exercising fields the increment-A
// generator does not reach (indexes with map fields, uniques, checks,
// exclusions, policies, triggers, partitioning, dependencies, maintenance,
// functions with args, sequences, domains, composites, views, matviews). It is
// canonicalized before return so it is a valid decode∘enc = id input.
func richSchema() *model.Schema {
	prec := 10
	scale := 2
	cost := 100.0
	rows := 1000.0
	start := int64(1)
	stat := 500

	s := &model.Schema{
		Name:       "public",
		Extensions: []string{"btree_gist", "pgcrypto"},
		PGVersion:  16,
		Groups:     map[string][]string{"core": {"child", "parent"}, "aux": {"parent"}},
		Enums: []model.Enum{
			{Name: "color", Schema: "public", Values: []string{"red", "green", "blue"}, Comment: "a color"},
		},
		Domains: []model.Domain{
			{Name: "positive", Schema: "public", BaseType: typeinfo.Type{Base: "int4"}, NotNull: true,
				Check: "VALUE > 0", Comment: "a positive int"},
		},
		CompositeTypes: []model.CompositeType{
			{Name: "addr", Schema: "public", Comment: "an address", Fields: []model.CompositeField{
				{Name: "street", PGType: typeinfo.Type{Base: "text"}},
				{Name: "zip", PGType: typeinfo.Type{Base: "varchar", Params: typeinfo.Params{Length: intptr(10)}}},
			}},
		},
		Sequences: []model.Sequence{
			{Name: "counter_seq", Schema: "public", Start: &start, Cycle: true, Comment: "a sequence"},
		},
		Functions: []model.Function{
			{Name: "add", Schema: "public", Language: "sql", ReturnType: "int4", Body: "SELECT $1 + $2",
				Comment: "adds", Volatility: "IMMUTABLE", Cost: &cost, Rows: &rows,
				Args: []model.FunctionArg{
					{Name: "a", Type: typeinfo.Type{Base: "int4"}},
					{Name: "b", Type: typeinfo.Type{Base: "int4"}, Default: "0"},
				}},
		},
		Tables: []model.Table{
			{
				Name: "parent", Schema: "public", Comment: "the parent",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.Type{Base: "int4"}, NotNull: true, Identity: "ALWAYS"},
					{Name: "label", PGType: typeinfo.Type{Base: "text"}, NotNull: true, Statistics: &stat},
				},
				PK: []string{"id"},
				Uniques: []model.UniqueConstraint{
					{Name: "parent_label_key", Columns: []string{"label"}, Deferrable: true},
				},
			},
			{
				Name: "child", Schema: "public", Comment: "the child",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.Type{Base: "int4"}, NotNull: true},
					{Name: "parent_id", PGType: typeinfo.Type{Base: "int4"}, NotNull: true},
					{Name: "amount", PGType: typeinfo.Type{Base: "numeric",
						Params: typeinfo.Params{Precision: &prec, Scale: &scale}}, NotNull: true},
					{Name: "created", PGType: typeinfo.Type{Base: "timestamptz"}, NotNull: true,
						Default: model.StrPtr("now()")},
				},
				PK: []string{"id"},
				FKs: []model.FK{
					{Name: "child_parent_fk", Columns: []string{"parent_id"}, RefTable: "parent",
						RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
				Indexes: []model.Index{
					{Name: "child_amount_idx", Columns: []string{"amount"}, Method: "btree",
						With: map[string]string{"fillfactor": "80"}, Include: []string{"created", "parent_id"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "child_amount_chk", Expr: "amount > 0"},
				},
				Exclusions: []model.ExclusionConstraint{
					{Name: "child_excl", Method: "gist", Elements: []model.ExclusionElement{
						{Column: "parent_id", Operator: "="},
						{Column: "amount", Operator: "&&"},
					}},
				},
				Dependencies: []fd.FuncDep{
					{Determinant: []string{"id"}, Dependent: []string{"parent_id", "amount"}, Source: "declared"},
				},
				Maintenance: &model.MaintenanceConfig{Interval: "1 month", Premake: 4, Retention: "12 months"},
				Policies: []model.Policy{
					{Name: "child_sel", Operation: "SELECT", Using: "true", Role: "app"},
				},
				Triggers: []model.Trigger{
					{Name: "child_audit", Function: "audit_fn", Events: []string{"UPDATE", "INSERT", "DELETE"},
						Timing: "BEFORE", ForEach: "ROW", Comment: "audits"},
				},
				AppendOnly: true,
			},
		},
		Views: []model.View{
			{Name: "child_view", Schema: "public", Query: "SELECT * FROM child", Comment: "a view"},
		},
		MaterializedViews: []model.MaterializedView{
			{Name: "child_mv", Schema: "public", Query: "SELECT id FROM child", WithData: true,
				Indexes: []model.Index{{Name: "child_mv_idx", Columns: []string{"id"}, Unique: true}}},
		},
	}
	s.Canonicalize()
	return s
}

func intptr(i int) *int { return &i }

// TestRichRoundTrip exercises decode∘enc = id on a canonical model that
// populates fields far beyond the increment-A generator. It also confirms every
// object kind gets a manifest key.
func TestRichRoundTrip(t *testing.T) {
	s := richSchema()
	objs1, err := EncodeObjects(s)
	if err != nil {
		t.Fatalf("EncodeObjects: %v", err)
	}
	decoded, err := DecodeObjects(objs1)
	if err != nil {
		t.Fatalf("DecodeObjects: %v", err)
	}
	objs2, err := EncodeObjects(decoded)
	if err != nil {
		t.Fatalf("re-EncodeObjects: %v", err)
	}
	if len(objs1) != len(objs2) {
		t.Fatalf("object count changed on round-trip: %d -> %d", len(objs1), len(objs2))
	}
	for k, b1 := range objs1 {
		b2, ok := objs2[k]
		if !ok {
			t.Fatalf("key %s lost on round-trip", k)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("key %s bytes differ on round-trip:\n%s\n%s", k, b1, b2)
		}
	}

	// Every expected kind produced a key.
	wantKinds := map[Kind]bool{
		KindSchemaMeta: false, KindTable: false, KindView: false, KindMatView: false,
		KindSequence: false, KindFunction: false, KindEnum: false, KindDomain: false, KindComposite: false,
	}
	for k := range objs1 {
		wantKinds[k.Kind] = true
	}
	for kind, seen := range wantKinds {
		if !seen {
			t.Errorf("no object of kind %q was encoded", kind)
		}
	}
}

// TestExclusionElementsAreSemantic: the exclusion element order is preserved
// (SEMANTIC), never sorted. Reversing the elements yields different bytes.
func TestExclusionElementsAreSemantic(t *testing.T) {
	base := model.Table{Name: "t", Schema: "public", Comment: "c",
		Columns: []model.Column{{Name: "id", PGType: typeinfo.Type{Base: "int4"}, NotNull: true}},
		PK:      []string{"id"},
		Exclusions: []model.ExclusionConstraint{
			{Name: "e", Method: "gist", Elements: []model.ExclusionElement{
				{Column: "a", Operator: "="}, {Column: "b", Operator: "&&"},
			}},
		}}
	reversed := base
	reversed.Exclusions = []model.ExclusionConstraint{
		{Name: "e", Method: "gist", Elements: []model.ExclusionElement{
			{Column: "b", Operator: "&&"}, {Column: "a", Operator: "="},
		}},
	}
	b1, _ := EncodeTable(base)
	b2, _ := EncodeTable(reversed)
	if bytes.Equal(b1, b2) {
		t.Fatalf("exclusion element order was NOT preserved (must be semantic)")
	}
}

// TestTriggerEventsAreCanonical: trigger events are a SET, so the encoder sorts
// them — two event orderings converge to the same bytes.
func TestTriggerEventsAreCanonical(t *testing.T) {
	mk := func(events []string) model.Trigger {
		return model.Trigger{Name: "tr", Function: "f", Events: events, Timing: "BEFORE", ForEach: "ROW"}
	}
	b1, _ := canonicalJSON(triggerToForm(mk([]string{"INSERT", "UPDATE", "DELETE"})))
	b2, _ := canonicalJSON(triggerToForm(mk([]string{"DELETE", "INSERT", "UPDATE"})))
	if !bytes.Equal(b1, b2) {
		t.Fatalf("trigger events not canonicalized:\n%s\n%s", b1, b2)
	}
}
