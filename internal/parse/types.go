// Package parse provides a lenient TOML parser for pgdesign schema files.
// It extracts structure without enforcing semantic rules, producing a RawSchema
// and diagnostics. Column order is preserved via AST walking (not map iteration).
package parse

// RawSchema is the top-level result of parsing one or more TOML schema files.
type RawSchema struct {
	Meta   RawMeta
	Types  []RawType
	Tables            []RawTable
	Views             []RawView
	MaterializedViews []RawMaterializedView
	Sequences         []RawSequence
	Functions         []RawFunction
	Groups            map[string][]string
}

// RawMeta holds the [meta] section values.
type RawMeta struct {
	Version    int
	Schema     string
	Extensions []string
}

// RawSMState holds a state in a state machine type from [types.*.states.*].
type RawSMState struct {
	Terminal *bool
	Comment  *string
}

// RawSMTransition holds a transition in a state machine type from [[types.*.transitions]].
type RawSMTransition struct {
	Name     string
	From     []string
	To       string
	Requires map[string]string
	Comment  *string
}

// RawType holds a user-defined type from [types.*].
type RawType struct {
	Name       string
	Kind       string
	BaseType   string
	Values     []string
	Fields     map[string]string // composite fields: field name -> PG type
	States         map[string]RawSMState  // state machine states: state name -> definition
	Transitions    []RawSMTransition      // state machine transitions
	InitialState   *string                // state machine initial state
	EnforceTrigger *bool                  // state machine: generate enforcement trigger
	NotNull    *bool
	Default    *string
	DefaultExpr *string
	Check      *string
	Unique     *bool
	Array      *bool
	Comment    *string
}

// RawView holds a view definition from [views.*].
type RawView struct {
	Name      string
	Query     string
	Comment   *string
	DependsOn []string
}

// RawMaterializedView holds a materialized view definition from [materialized_views.*].
type RawMaterializedView struct {
	Name      string
	Query     string
	Comment   *string
	DependsOn []string
	WithData  *bool
	Indexes   map[string]RawIndex
}

// RawSequence holds a sequence definition from [sequences.*].
type RawSequence struct {
	Name      string
	Start     *int64
	Increment *int64
	MinValue  *int64
	MaxValue  *int64
	Cache     *int64
	Cycle     *bool
	OwnedBy   *string
	Comment   *string
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
	Exclusions   map[string]RawExclusion
	Policies     map[string]RawPolicy
	Triggers     map[string]RawTrigger
	EnableRLS    bool
	ForceRLS     bool
	Partitioning *RawPartitioning
	Dependencies []RawDependency
	Maintenance  *RawMaintenance
	AppendOnly   *bool
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
	Collation  *string
	Statistics *int
	Comment    *string
	JSONSchema        *string
	JSONSchemaContent []byte // populated during File() parse when json_schema file is read successfully
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
	Opclass      *string            // single opclass (applied to all columns)
	OpclassMap   map[string]string  // per-column opclass map
	Collation    *string            // single collation (applied to all columns)
	CollationMap map[string]string  // per-column collation map
	Where        *string
	Include    []string
	Unique     *bool
	With       map[string]string  // storage parameters, e.g. { m = "16", ef_construction = "200" }
}

// RawUnique holds a unique constraint from [tables.*.unique.*].
type RawUnique struct {
	Name              string
	Columns           []string
	Deferrable        *bool
	InitiallyDeferred *bool
}

// RawCheck holds a check constraint from [tables.*.checks.*].
type RawCheck struct {
	Name string
	Expr string
}

// RawExclusion holds an exclusion constraint from [tables.*.exclusions.*].
type RawExclusion struct {
	Name              string
	Columns           []string
	Operators         []string
	Method            *string
	Where             *string
	Deferrable        *bool
	InitiallyDeferred *bool
}

// RawPolicy holds a row-level security policy from [tables.*.policies.*].
type RawPolicy struct {
	Name         string
	Type         string // PERMISSIVE or RESTRICTIVE
	For          string // SELECT, INSERT, UPDATE, DELETE, ALL
	To           string // role name
	Using        string // SQL expr for existing rows
	WithCheck    string // SQL expr for new rows
	ErrorCode    string // application error code
	ErrorMessage string // human-readable error message
}

// RawTrigger holds a trigger definition from [tables.*.triggers.*].
type RawTrigger struct {
	Name              string
	Function          string
	Events            []string
	Timing            string
	ForEach           *string
	When              *string
	Constraint        *bool
	Deferrable        *bool
	InitiallyDeferred *bool
	ReferencingOld    *string
	ReferencingNew    *string
	Comment           *string
}

// RawPartitioning holds partition configuration from [tables.*.partitioning].
type RawPartitioning struct {
	Strategy   string
	Column     string   // single column (backward compat): column = "x"
	Columns    []string // multi-column: columns = ["x", "y"]
	Name       string   // child partition table name
	Bound      string   // bound expression, e.g. "FROM ('2024-01-01') TO ('2024-02-01')"
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

// RawFunctionArg holds a function argument from [[functions.*.args]].
type RawFunctionArg struct {
	Name    string
	Type    string
	Default *string
}

// RawFunction holds a function or procedure definition from [functions.*].
type RawFunction struct {
	Name            string
	Language        *string
	Returns         *string
	Body            *string
	File            *string
	Comment         *string
	Volatility      *string
	Parallel        *string
	SecurityDefiner *bool
	Procedure       *bool
	Cost            *float64
	Rows            *float64
	DependsOn       []string
	Args            []RawFunctionArg
}
