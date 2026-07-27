package main

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// inSyncSchemas returns a matching desired/actual pair for the migrate diff
// path: an identical single-table schema where desired has an UNPINNED
// pg_version (0) and actual is an introspected schema reporting the live server
// version (17) with no semantic type names (introspection carries none).
func inSyncSchemas() (desired, actual *model.Schema) {
	mkTable := func(semName string) model.Table {
		return model.Table{
			Name:    "users",
			Comment: "users table",
			Columns: []model.Column{{
				Name:             "id",
				PGType:           typeinfo.Type{Base: "int4"},
				NotNull:          true,
				SemanticTypeName: semName,
			}},
			PK: []string{"id"},
		}
	}
	desired = &model.Schema{PGVersion: 0, Tables: []model.Table{mkTable("int")}}
	actual = &model.Schema{PGVersion: 17, Tables: []model.Table{mkTable("")}}
	return desired, actual
}

// TestMigrationDiff_UnpinnedPGVersionNoSpuriousDrift is the regression for the
// 1.4 live-regression bug: migrate plan/generate diffed BEFORE applying the
// live PG version, so an in-sync project with an unpinned pg_version got a
// spurious PGVersionChanged (plan lost "No changes detected"; generate wrote a
// zero-op migration file).
func TestMigrationDiff_UnpinnedPGVersionNoSpuriousDrift(t *testing.T) {
	// RED: diffing before applying the live version reports spurious drift.
	desired, actual := inSyncSchemas()
	if d := diff.DiffLive(desired, actual, nil); d.IsEmpty() {
		t.Fatalf("precondition: expected spurious PGVersionChanged when diffing before applying the live version, got empty")
	}

	// GREEN: migrationDiff applies the live version first, so no drift.
	desired, actual = inSyncSchemas()
	if d := migrationDiff(desired, actual); !d.IsEmpty() {
		t.Fatalf("migrationDiff reported spurious drift for an in-sync unpinned schema: %s", d.Summary())
	}
}

// TestLiveReportDiff_UnpinnedPGVersionNoSpuriousDrift is the regression for the
// diff --live foot-gun: the report path called diff.DiffLive directly (without
// resolving the live server version onto the desired model first), so an in-sync
// project with an unpinned/stale [meta].version surfaced a spurious
// "pg_version changed".
func TestLiveReportDiff_UnpinnedPGVersionNoSpuriousDrift(t *testing.T) {
	// RED: diffing before applying the live version reports spurious drift.
	desired, actual := inSyncSchemas()
	if d := diff.DiffLive(desired, actual, nil); d.IsEmpty() {
		t.Fatalf("precondition: expected spurious PGVersionChanged when diffing before applying the live version, got empty")
	}

	// GREEN: liveReportDiff (the diff --live seam) applies the live version first.
	desired, actual = inSyncSchemas()
	if d := liveReportDiff(desired, actual, nil); !d.IsEmpty() {
		t.Fatalf("liveReportDiff reported spurious drift for an in-sync unpinned schema: %s", d.Summary())
	}
}

// TestMigrationDiff_SuppressesSemanticTypeName confirms the introspected diff
// path does not false-drift on semantic type names (the actual side, being
// introspected, carries none). This is the reverse of the model-to-model case
// tested in the diff package.
func TestMigrationDiff_SuppressesSemanticTypeName(t *testing.T) {
	desired, actual := inSyncSchemas() // desired id has semantic name "int", actual ""
	if d := migrationDiff(desired, actual); !d.IsEmpty() {
		t.Fatalf("introspected diff false-drifted on semantic type name: %s", d.Summary())
	}
}
