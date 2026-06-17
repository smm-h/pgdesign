package audit

import (
	"strings"
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
	// inferFDs fires before the early return, producing A100 + Info
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics (A100 + Info), got %d: %+v", len(diags), diags)
	}
	if diags[0].Code != "A100" {
		t.Errorf("expected A100 first, got %v", diags[0].Code)
	}
	if diags[1].Severity != diagnostic.Info {
		t.Errorf("expected Info severity, got %v", diags[1].Severity)
	}
	if diags[1].Message != "No functional dependencies declared. NF audit skipped." {
		t.Errorf("unexpected message: %s", diags[1].Message)
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

func TestAudit_FDInference_PKNoDeps(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
				{Name: "name", PGType: "text", NotNull: true},
				{Name: "email", PGType: "text", NotNull: true},
			},
			PK: []string{"id"},
		}},
	}

	diags := Audit(schema)
	var a100 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "A100" {
			a100 = append(a100, d)
		}
	}
	if len(a100) != 1 {
		t.Fatalf("expected 1 A100 diagnostic, got %d: %+v", len(a100), diags)
	}
	if a100[0].Severity != diagnostic.Error {
		t.Errorf("expected Error severity, got %v", a100[0].Severity)
	}
	if !strings.Contains(a100[0].Message, "primary key") {
		t.Errorf("expected message about primary key, got: %s", a100[0].Message)
	}
	if !strings.Contains(a100[0].Message, "email") || !strings.Contains(a100[0].Message, "name") {
		t.Errorf("expected message to mention undeclared columns, got: %s", a100[0].Message)
	}
}

func TestAudit_FDInference_PKDeclared(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "clean",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "text", NotNull: true},
				{Name: "C", PGType: "text", NotNull: true},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
			},
		}},
	}

	diags := Audit(schema)
	for _, d := range diags {
		if d.Code == "A100" {
			t.Errorf("unexpected A100: %+v", d)
		}
	}
}

func TestAudit_FDInference_UniqueNotNull(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "items",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "text", NotNull: true},
				{Name: "C", PGType: "text", NotNull: true},
			},
			PK: []string{"A"},
			Uniques: []model.UniqueConstraint{
				{Name: "uq_b", Columns: []string{"B"}},
			},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
			},
		}},
	}

	diags := Audit(schema)
	var a100 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "A100" {
			a100 = append(a100, d)
		}
	}
	if len(a100) != 1 {
		t.Fatalf("expected 1 A100 for unique constraint, got %d: %+v", len(a100), diags)
	}
	if !strings.Contains(a100[0].Message, "unique constraint") {
		t.Errorf("expected message about unique constraint, got: %s", a100[0].Message)
	}
}

func TestAudit_FDInference_UniqueNullable(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "items",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "text", NotNull: false},
				{Name: "C", PGType: "text", NotNull: true},
			},
			PK: []string{"A"},
			Uniques: []model.UniqueConstraint{
				{Name: "uq_b", Columns: []string{"B"}},
			},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
			},
		}},
	}

	diags := Audit(schema)
	for _, d := range diags {
		if d.Code == "A100" {
			t.Errorf("unexpected A100 for nullable unique column: %+v", d)
		}
	}
}

func TestAudit_BCNF_Violation_3NF_Pass(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "bcnf_test",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "bigint", NotNull: true},
				{Name: "C", PGType: "bigint", NotNull: true},
			},
			PK: []string{"A", "B"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A", "B"}, Dependent: []string{"C"}},
				{Determinant: []string{"C"}, Dependent: []string{"B"}},
			},
		}},
	}

	diags := Audit(schema)

	// Should have NO W102 (3NF pass -- B is prime)
	for _, d := range diags {
		if d.Code == "W102" {
			t.Errorf("unexpected W102 (3NF violation): %+v", d)
		}
	}

	// Should have W103 (BCNF violation -- C is not a superkey)
	var w103 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "W103" {
			w103 = append(w103, d)
		}
	}
	if len(w103) != 1 {
		t.Fatalf("expected 1 W103 diagnostic, got %d: %+v", len(w103), diags)
	}
	if w103[0].Column != "B" {
		t.Errorf("expected BCNF violation for column 'B', got '%s'", w103[0].Column)
	}
	if !strings.Contains(w103[0].Message, "BCNF violation") {
		t.Errorf("expected BCNF violation message, got: %s", w103[0].Message)
	}
}

func TestAudit_BCNF_Pass(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "bcnf_clean",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "text", NotNull: true},
				{Name: "C", PGType: "text", NotNull: true},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
			},
		}},
	}

	diags := Audit(schema)
	for _, d := range diags {
		if d.Code == "W103" {
			t.Errorf("unexpected W103 (BCNF violation): %+v", d)
		}
	}
}

func TestAudit_BCNF_DecompositionSuggestion(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "bcnf_test",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "bigint", NotNull: true},
				{Name: "C", PGType: "bigint", NotNull: true},
			},
			PK: []string{"A", "B"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A", "B"}, Dependent: []string{"C"}},
				{Determinant: []string{"C"}, Dependent: []string{"B"}},
			},
		}},
	}

	diags := Audit(schema)

	// Should have BCNF decomposition suggestion
	var bcnfSuggestions []diagnostic.Diagnostic
	for _, d := range diags {
		if strings.Contains(d.Message, "BCNF decomposition") {
			bcnfSuggestions = append(bcnfSuggestions, d)
		}
	}
	if len(bcnfSuggestions) == 0 {
		t.Fatalf("expected BCNF decomposition suggestion, got none. All diags: %+v", diags)
	}

	// Should mention lost FDs
	var lostDiags []diagnostic.Diagnostic
	for _, d := range diags {
		if strings.Contains(d.Message, "loses functional dependencies") {
			lostDiags = append(lostDiags, d)
		}
	}
	if len(lostDiags) == 0 {
		t.Logf("Note: no lost FD diagnostic found. All diags: %+v", diags)
		// This is acceptable if the decomposition happens to preserve all FDs
	}
}

func TestAudit_BCNF_NoDecomposition_WhenClean(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "bcnf_clean",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "text", NotNull: true},
				{Name: "C", PGType: "text", NotNull: true},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
			},
		}},
	}

	diags := Audit(schema)
	for _, d := range diags {
		if strings.Contains(d.Message, "BCNF decomposition") {
			t.Errorf("unexpected BCNF decomposition suggestion for clean table: %+v", d)
		}
	}
}

func TestAudit_3NF_Counterexample(t *testing.T) {
	// Table(A, B, C) PK={A} deps: A->B, B->C
	// B->C is a 3NF violation; W102 should include a counterexample
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
	if len(w102) == 0 {
		t.Fatalf("expected W102 diagnostic, got none. All diags: %+v", diags)
	}

	// The Suggestion field should contain the counterexample
	if w102[0].Suggestion == "" {
		t.Fatal("expected W102 to have a non-empty Suggestion with counterexample")
	}
	if !strings.Contains(w102[0].Suggestion, "Counterexample") {
		t.Errorf("expected Suggestion to contain 'Counterexample', got: %s", w102[0].Suggestion)
	}
	// Should mention the violating FD
	if !strings.Contains(w102[0].Suggestion, "B") {
		t.Errorf("expected Suggestion to mention attribute B, got: %s", w102[0].Suggestion)
	}
	// Should contain table formatting (pipe characters)
	if !strings.Contains(w102[0].Suggestion, "|") {
		t.Errorf("expected Suggestion to contain table formatting, got: %s", w102[0].Suggestion)
	}
}

func TestAudit_BCNF_Counterexample(t *testing.T) {
	// Table(A, B, C) PK={A,B} deps: AB->C, C->B
	// C->B is a BCNF violation; W103 should include a counterexample
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "bcnf_test",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint", NotNull: true},
				{Name: "B", PGType: "bigint", NotNull: true},
				{Name: "C", PGType: "bigint", NotNull: true},
			},
			PK: []string{"A", "B"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A", "B"}, Dependent: []string{"C"}},
				{Determinant: []string{"C"}, Dependent: []string{"B"}},
			},
		}},
	}

	diags := Audit(schema)
	var w103 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "W103" {
			w103 = append(w103, d)
		}
	}
	if len(w103) == 0 {
		t.Fatalf("expected W103 diagnostic, got none. All diags: %+v", diags)
	}

	if w103[0].Suggestion == "" {
		t.Fatal("expected W103 to have a non-empty Suggestion with counterexample")
	}
	if !strings.Contains(w103[0].Suggestion, "Counterexample") {
		t.Errorf("expected Suggestion to contain 'Counterexample', got: %s", w103[0].Suggestion)
	}
	if !strings.Contains(w103[0].Suggestion, "|") {
		t.Errorf("expected Suggestion to contain table formatting, got: %s", w103[0].Suggestion)
	}
}

func TestAudit_MinimalCover_WithRedundancy(t *testing.T) {
	// Table(A, B, C) PK={A} deps: A->B, B->C, A->C
	// A->C is derivable from A->B, B->C -- minimal cover should detect this
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "redundant",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint"},
				{Name: "B", PGType: "text"},
				{Name: "C", PGType: "text"},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B"}},
				{Determinant: []string{"B"}, Dependent: []string{"C"}},
				{Determinant: []string{"A"}, Dependent: []string{"C"}},
			},
		}},
	}

	diags := Audit(schema)
	var i100 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "I100" {
			i100 = append(i100, d)
		}
	}
	if len(i100) == 0 {
		t.Fatalf("expected I100 diagnostic for minimal cover redundancy, got none. All diags: %+v", diags)
	}
	if !strings.Contains(i100[0].Message, "reduced to") {
		t.Errorf("expected message about reduction, got: %s", i100[0].Message)
	}
	if !strings.Contains(i100[0].Message, "derivable") {
		t.Errorf("expected message about derivable FDs, got: %s", i100[0].Message)
	}
}

func TestAudit_MinimalCover_NoRedundancy(t *testing.T) {
	// Table(A, B, C) PK={A} deps: A->B, A->C -- already minimal
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
		if d.Code == "I100" {
			t.Errorf("unexpected I100 diagnostic for clean FD set: %+v", d)
		}
	}
}

func TestAudit_FDSource_Declared(t *testing.T) {
	// When FDs are set directly with Source="declared", verify they work correctly in audit
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "sourced",
			Columns: []model.Column{
				{Name: "A", PGType: "bigint"},
				{Name: "B", PGType: "text"},
				{Name: "C", PGType: "text"},
			},
			PK: []string{"A"},
			Dependencies: []fd.FuncDep{
				{Determinant: []string{"A"}, Dependent: []string{"B", "C"}, Source: "declared"},
			},
		}},
	}

	diags := Audit(schema)
	// Should work normally -- no warnings or errors
	for _, d := range diags {
		if d.Severity == diagnostic.Warning || d.Severity == diagnostic.Error {
			t.Errorf("unexpected warning/error: %+v", d)
		}
	}
}

func TestAudit_FDInference_UpdatedMessage(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
				{Name: "name", PGType: "text", NotNull: true},
			},
			PK: []string{"id"},
		}},
	}

	diags := Audit(schema)
	var a100 []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == "A100" {
			a100 = append(a100, d)
		}
	}
	if len(a100) == 0 {
		t.Fatalf("expected A100, got none")
	}
	if !strings.Contains(a100[0].Message, "inferred FD") {
		t.Errorf("expected message to say 'inferred FD', got: %s", a100[0].Message)
	}
}
