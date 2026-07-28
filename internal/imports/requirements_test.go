package imports

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// TestCheckRequirements_MissingExtension verifies E241 when the consumer does not
// re-declare an extension the imported surface requires.
func TestCheckRequirements_MissingExtension(t *testing.T) {
	projectDir := t.TempDir()
	lf := &Lockfile{
		Alias: "framework", URL: "file:///fw", Ref: "v1", Commit: "abc",
		Schema: "app", Extensions: []string{"pgcrypto", "vector"}, PGVersion: 15,
	}
	if err := WriteLockfile(projectDir, lf); err != nil {
		t.Fatal(err)
	}
	consumer := &model.Schema{Extensions: []string{"pgcrypto"}, PGVersion: 16}

	diags := CheckRequirements(projectDir, []string{"framework"}, consumer)
	if !diagHasCode(diags, "E241") {
		t.Fatalf("expected E241 (missing extension vector), got: %v", diags)
	}
}

// TestCheckRequirements_PGVersionBelowFloor verifies E242 when the consumer's
// pg_version is below the imported floor.
func TestCheckRequirements_PGVersionBelowFloor(t *testing.T) {
	projectDir := t.TempDir()
	lf := &Lockfile{
		Alias: "framework", URL: "file:///fw", Ref: "v1", Commit: "abc",
		Schema: "app", Extensions: nil, PGVersion: 16,
	}
	if err := WriteLockfile(projectDir, lf); err != nil {
		t.Fatal(err)
	}
	consumer := &model.Schema{PGVersion: 14}

	diags := CheckRequirements(projectDir, []string{"framework"}, consumer)
	if !diagHasCode(diags, "E242") {
		t.Fatalf("expected E242 (pg_version below floor), got: %v", diags)
	}
}

// TestCheckRequirements_AllSatisfied verifies no diagnostics when requirements met.
func TestCheckRequirements_AllSatisfied(t *testing.T) {
	projectDir := t.TempDir()
	lf := &Lockfile{
		Alias: "framework", URL: "file:///fw", Ref: "v1", Commit: "abc",
		Schema: "app", Extensions: []string{"pgcrypto"}, PGVersion: 15,
	}
	if err := WriteLockfile(projectDir, lf); err != nil {
		t.Fatal(err)
	}
	consumer := &model.Schema{Extensions: []string{"pgcrypto", "vector"}, PGVersion: 16}

	diags := CheckRequirements(projectDir, []string{"framework"}, consumer)
	if diags.HasErrors() {
		t.Fatalf("expected no requirement errors, got: %v", diags)
	}
}
