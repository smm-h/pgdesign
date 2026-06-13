// Package parse provides a lenient TOML parser for pgdesign schema files.
// It extracts structure without enforcing semantic rules, producing a RawSchema
// and diagnostics. Column order is preserved via AST walking (not map iteration).
package parse

// RawSchema is the top-level result of parsing one or more TOML schema files.
type RawSchema struct {
	Meta   RawMeta
	Types  []RawType
	Tables []RawTable
}

// RawMeta holds the [meta] section values.
type RawMeta struct {
	Version    int
	Schema     string
	Extensions []string
}

// RawType holds a user-defined type from [types.*].
type RawType struct {
	Name       string
	Kind       string
	BaseType   string
	Values     []string
	NotNull    *bool
	Default    *string
	DefaultExpr *string
	Check      *string
	Unique     *bool
	Array      *bool
	Comment    *string
}

// RawTable holds a table definition from [tables.*].
type RawTable struct {
	Name         string
	Comment      *string
	PK           []string
	Columns      []RawColumn
	FKs          map[string]RawFK
	Indexes      map[string]RawIndex
	Uniques      map[string]RawUnique
	Checks       map[string]RawCheck
	Policies     map[string]RawPolicy
	EnableRLS    bool
	Partitioning *RawPartitioning
	Dependencies []RawDependency
	Maintenance  *RawMaintenance
}

// RawColumn holds a column definition from [tables.*.columns.*].
type RawColumn struct {
	Name       string
	Type       string
	Nullable   *bool
	Default    *string
	DefaultExpr *string
	Generated  *string
	Stored     *bool
	Array      *bool
	Comment    *string
}

// RawFK holds a foreign key constraint from [tables.*.fks.*].
type RawFK struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   string
}

// RawIndex holds an index definition from [tables.*.indexes.*].
type RawIndex struct {
	Name       string
	Columns    []string
	Method     *string
	Opclass    *string            // single opclass (applied to all columns)
	OpclassMap map[string]string  // per-column opclass map
	Where      *string
	Include    []string
	Unique     *bool
}

// RawUnique holds a unique constraint from [tables.*.unique.*].
type RawUnique struct {
	Name    string
	Columns []string
}

// RawCheck holds a check constraint from [tables.*.checks.*].
type RawCheck struct {
	Name string
	Expr string
}

// RawPolicy holds a row-level security policy from [tables.*.policies.*].
type RawPolicy struct {
	Name         string
	For          string // SELECT, INSERT, UPDATE, DELETE, ALL
	To           string // role name
	Using        string // SQL expr for existing rows
	WithCheck    string // SQL expr for new rows
	ErrorCode    string // application error code
	ErrorMessage string // human-readable error message
}

// RawPartitioning holds partition configuration from [tables.*.partitioning].
type RawPartitioning struct {
	Strategy   string
	Column     string
	Name       string             // child partition table name
	Bound      string             // bound expression, e.g. "FROM ('2024-01-01') TO ('2024-02-01')"
	Partitions []RawPartitioning
}

// RawDependency holds a functional dependency from [[tables.*.dependencies]].
type RawDependency struct {
	Determinant []string
	Dependent   []string
}

// RawMaintenance holds maintenance configuration from [tables.*.maintenance].
type RawMaintenance struct {
	Premake            *int
	Retention          *string
	RetentionKeepTable *bool
}
