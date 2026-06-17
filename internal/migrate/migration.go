// Package migrate provides migration generation, application, and rollback.
package migrate

import (
	"github.com/smm-h/pgdesign/internal/model"
)

// Migration represents a parsed migration file.
type Migration struct {
	Version     string
	Description string
	DDLOps      []DDLOp
	DMLOps      []DMLOp
}

// Design decision: DDLOp is intentionally a flat struct.
//
// Each operation type uses a subset of fields; unused fields stay at zero values.
// This was considered against a tagged-union approach (interface + type-specific
// sub-structs) and kept flat because:
//
//  1. parse_migration.go uses matching flat TOML structs — both layers would
//     need to change in lockstep.
//  2. Go lacks sum types; the alternatives (interface + type switch, embedded
//     sub-structs) add indirection without compile-time safety.
//  3. DDLOp is internal-only — it never crosses an API boundary.
//  4. Pointer fields (*model.Table, etc.) already handle complex payloads;
//     nil means "not applicable to this op."

// DDLOp represents a single DDL operation in a migration.
type DDLOp struct {
	Op       string      // "create_table", "add_column", "drop_table", etc.
	Table    string      // schema-qualified table name
	Column   string      // for column ops
	Type       string      // for add_column
	Collation  string      // for column collation (ALTER COLUMN TYPE ... COLLATE)
	Statistics *int        // for set_statistics ops
	Default    interface{} // for add_column
	NotNull    bool
	Generated  string // for add_column: GENERATED ALWAYS AS (expr)
	Stored     bool   // for add_column: true=STORED, false=VIRTUAL
	PGVersion  int    // target PG version for version-gated DDL
	Name     string   // for constraints/indexes
	Columns  []string // for indexes, FKs
	RefTable string   // for FKs
	RefCols  []string // for FKs
	OnDelete string   // for FKs
	Method    string            // for indexes
	Where     string            // for partial indexes
	Opclasses  map[string]string // per-column opclass
	Collations map[string]string // per-column collation for indexes
	Desc       []bool            // per-column DESC (parallel to Columns)
	Include   []string
	With      map[string]string // index storage parameters (WITH clause)
	Comment  string   // for tables
	PK       []string // for create_table
	Values   []string // for create_enum, alter_enum_add_value
	Schema   string   // for enums (schema-qualified ops)
	Expr              string   // for check constraints
	Operators         []string // for exclusion constraints
	Deferrable        bool     // for exclusion and unique constraints
	InitiallyDeferred bool     // for exclusion and unique constraints

	TableDef          *model.Table          // full table def for create_table (not serialized)
	PartitionChildSpec *model.PartitionSpec // child spec for create_partition (not serialized)
	ParentTable       string               // parent table for create_partition
	ViewDef              *model.View              // full view def for create_view/drop_view (not serialized)
	MaterializedViewDef  *model.MaterializedView  // full matview def for create/drop materialized view (not serialized)
	SequenceDef          *model.Sequence          // full sequence def for create/alter sequence (not serialized)
	CompositeTypeDef     *model.CompositeType         // full composite type def (not serialized)
	DomainDef            *model.Domain                // full domain def (not serialized)
	FunctionDef          *model.Function              // full function def for create/drop function (not serialized)
	TriggerDef           *model.Trigger               // full trigger def for create/drop trigger (not serialized)
	PolicyDef            *model.Policy                // full policy def for create/drop policy (not serialized)

	Down *DownOp
}

// DMLOp represents a DML operation in a migration.
type DMLOp struct {
	Op   string // "backfill", "transform"
	SQL  string
	Down *DownOp
}

// DownOp represents the rollback operation(s) for a DDL or DML op.
type DownOp struct {
	Irreversible bool
	Ops          []DDLOp
}
