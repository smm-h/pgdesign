package migrate

import (
	"github.com/smm-h/pgdesign/internal/risk"
)

// Phase constants for expand/migrate/contract annotation.
const (
	PhaseExpand   = "expand"
	PhaseMigrate  = "migrate"
	PhaseContract = "contract"
)

// classifyPhase maps an op type and its risk level to an expand/migrate/contract phase.
func classifyPhase(op string, riskLevel risk.RiskLevel) string {
	// Migrate-phase ops: always migrate regardless of risk.
	switch op {
	case "validate_constraint":
		return PhaseMigrate
	}

	// Always-contract ops: destructive by nature, regardless of risk level.
	switch op {
	case "drop_table", "drop_column", "alter_column_type", "set_not_null",
		"rename_table", "rename_column", "drop_fk", "drop_index", "drop_index_concurrently",
		"drop_unique", "drop_view", "drop_materialized_view", "refresh_materialized_view",
		"drop_sequence", "drop_composite_type", "drop_domain", "drop_function", "drop_trigger",
		"drop_enum":
		return PhaseContract
	}

	// For remaining ops, use risk level.
	// Safe -> expand, Caution/Dangerous -> contract.
	if riskLevel == risk.Safe {
		return PhaseExpand
	}
	return PhaseContract
}

// classifyDMLPhase returns the phase for a DML operation. All DML ops
// (backfill, transform) are migrate-phase.
func classifyDMLPhase(_ string) string {
	return PhaseMigrate
}

// AnnotatePhases sets the Phase field on all DDLOps and DMLOps in a migration.
// It re-derives the risk level for each DDLOp using a minimal OpContext built
// from the op's own fields and the target PG version. After annotation, it
// collapses single-phase migrations (all expand) to empty phases.
func AnnotatePhases(m *Migration, pgVersion int) {
	for i := range m.DDLOps {
		op := &m.DDLOps[i]
		ctx := risk.OpContext{
			PGVersion:  pgVersion,
			IsNullable: !op.NotNull,
			HasDefault: op.Default != nil,
		}
		riskOp := risk.OpType(op.Op)
		c := risk.Classify(riskOp, ctx)
		op.Phase = classifyPhase(op.Op, c.RiskLevel)
	}
	for i := range m.DMLOps {
		m.DMLOps[i].Phase = classifyDMLPhase(m.DMLOps[i].Op)
	}
	collapseSinglePhase(m)
}

// collapseSinglePhase clears all Phase fields if every op in the migration
// is in the expand phase. A pure-expand migration does not need phase
// separation and can run as a single step.
func collapseSinglePhase(m *Migration) {
	allExpand := true
	for i := range m.DDLOps {
		if m.DDLOps[i].Phase != PhaseExpand {
			allExpand = false
			break
		}
	}
	if allExpand {
		for i := range m.DMLOps {
			if m.DMLOps[i].Phase != PhaseExpand {
				allExpand = false
				break
			}
		}
	}
	if allExpand {
		for i := range m.DDLOps {
			m.DDLOps[i].Phase = ""
		}
		for i := range m.DMLOps {
			m.DMLOps[i].Phase = ""
		}
	}
}

// HasPhases returns true if any DDLOp or DMLOp has a non-empty Phase.
func HasPhases(m *Migration) bool {
	for i := range m.DDLOps {
		if m.DDLOps[i].Phase != "" {
			return true
		}
	}
	for i := range m.DMLOps {
		if m.DMLOps[i].Phase != "" {
			return true
		}
	}
	return false
}
