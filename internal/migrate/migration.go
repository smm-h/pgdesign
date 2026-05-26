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

// DDLOp represents a single DDL operation in a migration.
type DDLOp struct {
	Op       string      // "create_table", "add_column", "drop_table", etc.
	Table    string      // schema-qualified table name
	Column   string      // for column ops
	Type     string      // for add_column
	Default  interface{} // for add_column
	NotNull  bool
	Name     string   // for constraints/indexes
	Columns  []string // for indexes, FKs
	RefTable string   // for FKs
	RefCols  []string // for FKs
	OnDelete string   // for FKs
	Method    string            // for indexes
	Where     string            // for partial indexes
	Opclasses map[string]string // per-column opclass
	Include   []string
	Comment  string   // for tables
	PK       []string // for create_table
	Values   []string // for create_enum, alter_enum_add_value
	Schema   string   // for enums (schema-qualified ops)
	Expr     string   // for check constraints

	TableDef *model.Table // full table def for create_table (not serialized)

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
