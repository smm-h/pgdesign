// Package risk provides shared risk classification for schema change operations.
// Used by diff/ and migrate/ to assess operation safety.
package risk

// RiskLevel represents the safety level of a schema operation.
type RiskLevel int

const (
	Safe      RiskLevel = iota
	Caution   RiskLevel = iota
	Dangerous RiskLevel = iota
)

func (r RiskLevel) String() string {
	switch r {
	case Safe:
		return "safe"
	case Caution:
		return "caution"
	case Dangerous:
		return "dangerous"
	default:
		return "unknown"
	}
}

// LockType represents the PostgreSQL lock level required by an operation.
type LockType int

const (
	LockNone                 LockType = iota
	LockShareLock            LockType = iota
	LockShareRowExclusive    LockType = iota
	LockShareUpdateExclusive LockType = iota
	LockAccessExclusive      LockType = iota
)

func (l LockType) String() string {
	switch l {
	case LockNone:
		return "none"
	case LockShareLock:
		return "ShareLock"
	case LockShareRowExclusive:
		return "ShareRowExclusive"
	case LockShareUpdateExclusive:
		return "ShareUpdateExclusive"
	case LockAccessExclusive:
		return "AccessExclusive"
	default:
		return "unknown"
	}
}

// OpType identifies a schema change operation.
type OpType string

const (
	OpCreateTable             OpType = "create_table"
	OpDropTable               OpType = "drop_table"
	OpAddColumn               OpType = "add_column"
	OpDropColumn              OpType = "drop_column"
	OpAlterColumnType         OpType = "alter_column_type"
	OpSetNotNull              OpType = "set_not_null"
	OpDropNotNull             OpType = "drop_not_null"
	OpAddFK                   OpType = "add_fk"
	OpAddFKNotValid           OpType = "add_fk_not_valid"
	OpValidateConstraint      OpType = "validate_constraint"
	OpCreateIndex             OpType = "create_index"
	OpCreateIndexConcurrently OpType = "create_index_concurrently"
	OpDropIndex               OpType = "drop_index"
	OpDropIndexConcurrently   OpType = "drop_index_concurrently"
	OpRenameTable             OpType = "rename_table"
	OpRenameColumn            OpType = "rename_column"
	OpAlterEnumAddValue       OpType = "alter_enum_add_value"
	OpAddUnique               OpType = "add_unique"
	OpDropUnique              OpType = "drop_unique"
	OpAddCheck                OpType = "add_check"
	OpDropCheck               OpType = "drop_check"
	OpAddExclusion            OpType = "add_exclusion"
	OpDropExclusion           OpType = "drop_exclusion"
	OpAlterIndexSet           OpType = "alter_index_set"
	OpCreateView              OpType = "create_view"
	OpDropView                OpType = "drop_view"
	OpCreateOrReplaceView     OpType = "create_or_replace_view"
	OpCreateMaterializedView  OpType = "create_materialized_view"
	OpDropMaterializedView    OpType = "drop_materialized_view"
	OpRefreshMaterializedView OpType = "refresh_materialized_view"
	OpCreateSequence          OpType = "create_sequence"
	OpDropSequence            OpType = "drop_sequence"
	OpAlterSequence           OpType = "alter_sequence"
	OpCreateCompositeType     OpType = "create_composite_type"
	OpDropCompositeType       OpType = "drop_composite_type"
)

// OpContext provides context about the operation environment for risk assessment.
type OpContext struct {
	EstimatedRows int64
	PGVersion     int
	HasDefault    bool
	IsNullable    bool
	IsWidening    bool
}

// Classification is the risk assessment result for a schema operation.
type Classification struct {
	RiskLevel   RiskLevel
	LockType    LockType
	Reversible  bool
	DataLoss    bool
	RequiresDML bool
	Suggestion  string
}

// Classify assesses the risk of a schema operation given its context.
func Classify(op OpType, ctx OpContext) Classification {
	c := classifyBase(op, ctx)
	c = applyTableSizeEscalation(c, ctx)
	return c
}

func classifyBase(op OpType, ctx OpContext) Classification {
	switch op {
	case OpCreateTable:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockNone,
			Reversible: true,
		}

	case OpDropTable:
		return Classification{
			RiskLevel:  Dangerous,
			LockType:   LockAccessExclusive,
			Reversible: false,
			DataLoss:   true,
			Suggestion: "Consider marking as deprecated first; data will be lost",
		}

	case OpAddColumn:
		return classifyAddColumn(ctx)

	case OpDropColumn:
		return Classification{
			RiskLevel:  Dangerous,
			LockType:   LockAccessExclusive,
			Reversible: false,
			DataLoss:   true,
			Suggestion: "Consider marking as deprecated first; data will be lost",
		}

	case OpAlterColumnType:
		if ctx.IsWidening {
			return Classification{
				RiskLevel:  Caution,
				LockType:   LockAccessExclusive,
				Reversible: true,
			}
		}
		return Classification{
			RiskLevel:  Dangerous,
			LockType:   LockAccessExclusive,
			Reversible: false,
			DataLoss:   true,
		}

	case OpSetNotNull:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: true,
		}

	case OpDropNotNull:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockAccessExclusive,
			Reversible: true,
		}

	case OpAddFK:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockShareRowExclusive,
			Reversible: true,
			Suggestion: "Add with NOT VALID, then VALIDATE CONSTRAINT separately",
		}

	case OpAddFKNotValid:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockShareRowExclusive,
			Reversible: true,
		}

	case OpValidateConstraint:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockShareUpdateExclusive,
			Reversible: false,
		}

	case OpCreateIndex:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockShareLock,
			Reversible: true,
			Suggestion: "Use CONCURRENTLY to avoid blocking writes",
		}

	case OpCreateIndexConcurrently:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockShareUpdateExclusive,
			Reversible: true,
		}

	case OpDropIndex:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: false,
		}

	case OpDropIndexConcurrently:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockShareUpdateExclusive,
			Reversible: false,
		}

	case OpRenameTable:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: true,
			Suggestion: "Renaming breaks existing client queries referencing the old name",
		}

	case OpRenameColumn:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: true,
			Suggestion: "Renaming breaks existing client queries referencing the old name",
		}

	case OpAlterEnumAddValue:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockNone,
			Reversible: false,
		}

	case OpAddUnique:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockShareLock,
			Reversible: true,
		}

	case OpDropUnique:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: false,
		}

	case OpAddCheck:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockShareRowExclusive,
			Reversible: true,
		}

	case OpDropCheck:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockAccessExclusive,
			Reversible: false,
		}

	case OpAddExclusion:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockShareLock,
			Reversible: true,
			Suggestion: "EXCLUDE constraint may fail if existing data violates the constraint",
		}

	case OpDropExclusion:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockAccessExclusive,
			Reversible: false,
		}

	case OpAlterIndexSet:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockShareUpdateExclusive,
			Reversible: true,
		}

	case OpCreateView:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockNone,
			Reversible: true,
		}

	case OpDropView:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: false,
			Suggestion: "Dependents (other views, functions) may break",
		}

	case OpCreateOrReplaceView:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockAccessExclusive,
			Reversible: true,
		}

	case OpCreateMaterializedView:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockNone,
			Reversible: true,
			Suggestion: "Initial data population may be slow on large datasets",
		}

	case OpDropMaterializedView:
		return Classification{
			RiskLevel:  Dangerous,
			LockType:   LockAccessExclusive,
			Reversible: false,
			DataLoss:   true,
			Suggestion: "Materialized view data will be lost",
		}

	case OpRefreshMaterializedView:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: false,
			Suggestion: "REFRESH locks the materialized view; consider CONCURRENTLY",
		}

	case OpCreateSequence:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockNone,
			Reversible: true,
		}

	case OpDropSequence:
		return Classification{
			RiskLevel:  Caution,
			LockType:   LockAccessExclusive,
			Reversible: false,
			Suggestion: "Dependents (columns using nextval) may break",
		}

	case OpAlterSequence:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockNone,
			Reversible: true,
		}

	case OpCreateCompositeType:
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockNone,
			Reversible: true,
		}

	case OpDropCompositeType:
		return Classification{
			RiskLevel:  Dangerous,
			LockType:   LockAccessExclusive,
			Reversible: false,
			DataLoss:   true,
			Suggestion: "Dropping a composite type with CASCADE affects all columns using this type",
		}

	default:
		return Classification{
			RiskLevel: Dangerous,
			LockType:  LockAccessExclusive,
			Suggestion: "Unknown operation type",
		}
	}
}

func classifyAddColumn(ctx OpContext) Classification {
	// Nullable with no default: metadata-only, safe.
	if ctx.IsNullable {
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockAccessExclusive,
			Reversible: true,
		}
	}

	// NOT NULL with default on PG11+: metadata-only, safe.
	if ctx.HasDefault && ctx.PGVersion >= 11 {
		return Classification{
			RiskLevel:  Safe,
			LockType:   LockAccessExclusive,
			Reversible: true,
		}
	}

	// NOT NULL with volatile default (HasDefault but pre-PG11): table rewrite.
	if ctx.HasDefault {
		return Classification{
			RiskLevel:   Dangerous,
			LockType:    LockAccessExclusive,
			Reversible:  true,
			RequiresDML: true,
			Suggestion:  "Add as nullable, backfill, then SET NOT NULL",
		}
	}

	// NOT NULL without default: fails on non-empty tables.
	return Classification{
		RiskLevel:  Dangerous,
		LockType:   LockAccessExclusive,
		Reversible: true,
		Suggestion: "Add as nullable, backfill, then SET NOT NULL",
	}
}

func applyTableSizeEscalation(c Classification, ctx OpContext) Classification {
	// Escalate Caution to Dangerous for large tables with AccessExclusive locks.
	if ctx.EstimatedRows > 1_000_000 && c.LockType == LockAccessExclusive && c.RiskLevel == Caution {
		c.RiskLevel = Dangerous
		if c.Suggestion == "" {
			c.Suggestion = "Table has >1M rows; AccessExclusive lock will block all access"
		}
	}

	// Add lock_timeout suggestion for very large tables.
	if ctx.EstimatedRows > 10_000_000 {
		timeout := "Consider using SET lock_timeout to avoid long waits on a table with >10M rows"
		if c.Suggestion == "" {
			c.Suggestion = timeout
		} else {
			c.Suggestion = c.Suggestion + "; " + timeout
		}
	}

	return c
}
