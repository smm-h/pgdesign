package model

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

func TestBuild_MaintenanceRequiresRangeStrategy(t *testing.T) {
	testenv.Isolate(t)
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	raw.Tables[0].Partitioning.Strategy = "list"

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error when maintenance is used with non-RANGE partitioning")
	}
	if !hasErrorContaining(diags, "RANGE") {
		t.Errorf("expected error mentioning RANGE, got: %v", diags)
	}
}

func TestBuild_MaintenanceRequiresPartitioning(t *testing.T) {
	testenv.Isolate(t)
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	raw.Tables[0].Partitioning = nil

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error when maintenance is used without partitioning")
	}
}

func TestBuild_MaintenanceRequiresPartmanExtension(t *testing.T) {
	testenv.Isolate(t)
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	raw.Meta.Extensions = nil // pg_partman undeclared

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error when maintenance is used without pg_partman declared")
	}
	if !hasErrorContaining(diags, "pg_partman") {
		t.Errorf("expected error mentioning pg_partman, got: %v", diags)
	}
}

func TestBuild_MaintenanceConflictsManualChildren(t *testing.T) {
	testenv.Isolate(t)
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	raw.Tables[0].Partitioning.Partitions = []parse.RawPartitioning{
		{Name: "events_2024_01", Bound: "FROM ('2024-01-01') TO ('2024-02-01')"},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error when maintenance is combined with manual partition children")
	}
}
