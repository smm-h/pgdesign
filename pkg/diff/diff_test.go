package diff

import (
	"testing"
)

func TestRiskLevelString(t *testing.T) {
	tests := []struct {
		r    RiskLevel
		want string
	}{
		{Safe, "safe"},
		{Caution, "caution"},
		{Dangerous, "dangerous"},
		{RiskLevel(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.r.String(); got != tt.want {
			t.Errorf("RiskLevel(%d).String() = %q, want %q", tt.r, got, tt.want)
		}
	}
}

func TestRiskLevelOrdering(t *testing.T) {
	if Safe >= Caution {
		t.Error("Safe should be < Caution")
	}
	if Caution >= Dangerous {
		t.Error("Caution should be < Dangerous")
	}
}

func TestClassificationFields(t *testing.T) {
	c := Classification{
		RiskLevel:  Dangerous,
		Reversible: false,
		DataLoss:   true,
		Suggestion: "backup first",
	}
	if c.RiskLevel != Dangerous {
		t.Errorf("RiskLevel = %v, want Dangerous", c.RiskLevel)
	}
	if c.Reversible {
		t.Error("expected Reversible = false")
	}
	if !c.DataLoss {
		t.Error("expected DataLoss = true")
	}
	if c.Suggestion != "backup first" {
		t.Errorf("Suggestion = %q, want %q", c.Suggestion, "backup first")
	}
}

func TestColumnChangeFields(t *testing.T) {
	cc := ColumnChange{
		Name:             "email",
		TypeChanged:      true,
		OldType:          "varchar(50)",
		NewType:          "text",
		NullableChanged:  true,
		OldNotNull:       false,
		NewNotNull:       true,
		DefaultChanged:   false,
		StoredChanged:    false,
		CollationChanged: false,
	}
	if cc.Name != "email" {
		t.Errorf("Name = %q, want %q", cc.Name, "email")
	}
	if !cc.TypeChanged {
		t.Error("expected TypeChanged = true")
	}
	if !cc.NullableChanged {
		t.Error("expected NullableChanged = true")
	}
}

func TestDiffResultDefault(t *testing.T) {
	var r DiffResult
	if r.HasChanges {
		t.Error("expected HasChanges = false by default")
	}
	if len(r.Diagnostics) != 0 {
		t.Error("expected empty Diagnostics by default")
	}
}

// TestTypeComparerInterface verifies the TypeComparer interface is implementable.
type mockTypeComparer struct{}

func (m *mockTypeComparer) TypesEqual(a, b string) bool     { return a == b }
func (m *mockTypeComparer) Reconstruct(typeStr string) string { return typeStr }
func (m *mockTypeComparer) IsWidening(old, new string) bool  { return false }

func TestTypeComparerInterface(t *testing.T) {
	var tc TypeComparer = &mockTypeComparer{}
	if !tc.TypesEqual("int4", "int4") {
		t.Error("expected equal types")
	}
	if tc.TypesEqual("int4", "int8") {
		t.Error("expected unequal types")
	}
	if tc.Reconstruct("text") != "text" {
		t.Error("expected identity reconstruct")
	}
}

// TestClassifierInterface verifies the Classifier interface is implementable.
type mockClassifier struct{}

func (m *mockClassifier) ClassifyColumnChange(cc ColumnChange) Classification {
	if cc.TypeChanged {
		return Classification{RiskLevel: Dangerous}
	}
	return Classification{RiskLevel: Safe}
}

func TestClassifierInterface(t *testing.T) {
	var cl Classifier = &mockClassifier{}
	c := cl.ClassifyColumnChange(ColumnChange{TypeChanged: true})
	if c.RiskLevel != Dangerous {
		t.Errorf("expected Dangerous, got %v", c.RiskLevel)
	}
	c = cl.ClassifyColumnChange(ColumnChange{TypeChanged: false})
	if c.RiskLevel != Safe {
		t.Errorf("expected Safe, got %v", c.RiskLevel)
	}
}
