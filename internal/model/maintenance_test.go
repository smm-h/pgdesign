package model

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// hasErrorContaining reports whether any error-severity diagnostic message
// contains the substring.
func hasErrorContaining(diags diagnostic.Diagnostics, substr string) bool {
	for _, d := range diags {
		if d.Severity == diagnostic.Error && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

// maintenanceRawSchema builds a valid partman-managed RawSchema that can be
// mutated per-test. It has a RANGE-partitioned table with pg_partman declared
// and a complete maintenance block (interval + premake).
func maintenanceRawSchema() *parse.RawSchema {
	interval := "1 month"
	premake := 4
	return &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public", Extensions: []string{"pg_partman"}},
		Tables: []parse.RawTable{
			{
				Name: "events",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "created_at", Type: "timestamp"},
				},
				Partitioning: &parse.RawPartitioning{
					Strategy: "range",
					Column:   "created_at",
				},
				Maintenance: &parse.RawMaintenance{
					Interval: &interval,
					Premake:  &premake,
				},
			},
		},
	}
}

func TestBuild_MaintenanceValidBaseline(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	_, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("baseline maintenance schema should build cleanly, got: %v", diags)
	}
}

func TestBuild_MaintenancePremakeRequired(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	// Omit premake: silent zero would disable partman premaking.
	raw.Tables[0].Maintenance.Premake = nil

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error when premake is omitted from [tables.*.maintenance]")
	}
	if !hasErrorContaining(diags, "premake") {
		t.Errorf("expected error mentioning premake, got: %v", diags)
	}
}

func TestBuild_MaintenanceScheduleRequiresPgCron(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	sched := "*/30 * * * *"
	raw.Tables[0].Maintenance.Schedule = &sched
	// pg_cron NOT declared (only pg_partman).

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected error when schedule is set without pg_cron declared")
	}
	if !hasErrorContaining(diags, "pg_cron") {
		t.Errorf("expected error mentioning pg_cron, got: %v", diags)
	}
}

func TestBuild_MaintenanceScheduleWithPgCron(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	raw := maintenanceRawSchema()
	raw.Meta.Extensions = []string{"pg_partman", "pg_cron"}
	sched := "*/30 * * * *"
	raw.Tables[0].Maintenance.Schedule = &sched

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("schedule with pg_cron should build cleanly, got: %v", diags)
	}
	if schema.Tables[0].Maintenance.Schedule != "*/30 * * * *" {
		t.Errorf("Schedule = %q, want %q", schema.Tables[0].Maintenance.Schedule, "*/30 * * * *")
	}
}
