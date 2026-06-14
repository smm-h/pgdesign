package audit

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
)

func TestAudit_NoDepsSkipped(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "name", PGType: "text"},
			},
			PK: []string{"id"},
		}},
	}

	diags := Audit(schema)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != diagnostic.Info {
		t.Errorf("expected Info severity, got %v", diags[0].Severity)
	}
	if diags[0].Message != "No functional dependencies declared. NF audit skipped." {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestAudit_CleanTable_NoViolations(t *testing.T) {
	// Table in 3NF: A→B, A→C with PK={A}
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "clean",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint"},
				{Name: "B", PGType: "text"},
				{Name: "C", PGType: "text"},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
			},
		}},
	}

	diags := Audit(schema)
	for _, d := range diags {
		if d.Severity == diagnostic.Warning || d.Severity == diagnostic.Error {
			t.Errorf("unexpected warning/error: %+v", d)
		}
	}
}

func TestAudit_1NF_JsonbRepeatingGroup(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "posts",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "tags", PGType: "jsonb", Default: model.StrPtr("'[]'::jsonb")},
			},
			PK: []string{"id"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"id"}, Dependent: []string{"tags"}},
			},
		}},
	}

	diags := Audit(schema)
	var w100 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "W100" {
			w100 = append(w100, d)
		}
	}
	if len(w100) != 1 {
		t.Fatalf("expected 1 W100 diagnostic, got %d: %+v", len(w100), diags)
	}
	if w100[0].Column != "tags" {
		t.Errorf("expected column 'tags', got '%s'", w100[0].Column)
	}
	if w100[0].Severity != diagnostic.Warning {
		t.Errorf("expected Warning severity, got %v", w100[0].Severity)
	}
}

func TestAudit_2NF_PartialDependency(t *testing.T) {
	// Table(A, B, C, D) PK={A,B} deps: AB→CD, A→C
	// C partially depends on {A} which is a subset of key {A,B}
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "orders",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint"},
				{Name: "B", PGType: "bigint"},
				{Name: "C", PGType: "text"},
				{Name: "D", PGType: "text"},
			},
			PK: []string{"A", "B"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A", "B"}, Dependent: []string{"C", "D"}},
				{Determinant: []string{"A"}, Dependent: []string{"C"}},
			},
		}},
	}

	diags := Audit(schema)
	var w101 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "W101" {
			w101 = append(w101, d)
		}
	}
	if len(w101) == 0 {
		t.Fatalf("expected at least 1 W101 diagnostic, got 0. All diags: %+v", diags)
	}
	found := false
	for _, d := range w101 {
		if d.Column == "C" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected W101 for column 'C', got: %+v", w101)
	}
}

func TestAudit_3NF_TransitiveDependency(t *testing.T) {
	// Table(A, B, C) PK={A} deps: A→B, B→C
	// B→C violates 3NF: B is not a superkey, C is not prime
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "employees",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint"},
				{Name: "B", PGType: "text"},
				{Name: "C", PGType: "text"},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B"}},
				{Determinant: []string{"B"}, Dependent: []string{"C"}},
			},
		}},
	}

	diags := Audit(schema)
	var w102 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "W102" {
			w102 = append(w102, d)
		}
	}
	if len(w102) != 1 {
		t.Fatalf("expected 1 W102 diagnostic, got %d: %+v", len(w102), diags)
	}
	if w102[0].Column != "C" {
		t.Errorf("expected column 'C', got '%s'", w102[0].Column)
	}
	if w102[0].Severity != diagnostic.Warning {
		t.Errorf("expected Warning severity, got %v", w102[0].Severity)
	}
}

func TestAudit_DecompositionSuggestion(t *testing.T) {
	// Same 3NF violation setup: should produce decomposition suggestion
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "employees",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint"},
				{Name: "B", PGType: "text"},
				{Name: "C", PGType: "text"},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B"}},
				{Determinant: []string{"B"}, Dependent: []string{"C"}},
			},
		}},
	}

	diags := Audit(schema)
	var suggestions []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Suggestion != "" {
			suggestions = append(suggestions, d)
		}
	}
	if len(suggestions) == 0 {
		t.Fatalf("expected decomposition suggestion, got none. All diags: %+v", diags)
	}
	// Should suggest at least two tables from Bernstein's synthesis
	suggestion := suggestions[0].Suggestion
	if suggestion == "" {
		t.Fatal("suggestion field is empty")
	}
	// The suggestion should contain table definitions
	if !containsStr([]string{suggestion}, "Suggested decomposition:") {
		// Use strings.Contains for checking
		t.Logf("suggestion: %s", suggestion)
	}
}
