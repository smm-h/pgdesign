// Package model provides the resolved intermediate representation (IR) for pgdesign.
// It is the canonical in-memory schema that all downstream packages consume.
package model

import (
	"github.com/smm-h/pgdesign/internal/fd"
)

// Schema is the top-level resolved schema.
type Schema struct {
	Name        string     `json:"name"`
	Extensions  []string   `json:"extensions"`
	Enums       []Enum     `json:"enums"`
	Tables      []Table    `json:"tables"`
	Views              []View              `json:"views,omitempty"`
	MaterializedViews  []MaterializedView  `json:"materialized_views,omitempty"`
	CycleGroups        [][]string          `json:"cycle_groups,omitempty"`
	PGVersion   int        `json:"pg_version"`
}

// View represents a resolved view definition.
type View struct {
	Name      string   `json:"name"`
	Schema    string   `json:"schema,omitempty"`
	Query     string   `json:"query"`
	Comment   string   `json:"comment,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// MaterializedView represents a resolved materialized view definition.
type MaterializedView struct {
	Name      string  `json:"name"`
	Schema    string  `json:"schema,omitempty"`
	Query     string  `json:"query"`
	Comment   string  `json:"comment,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	WithData  bool    `json:"with_data"`
	Indexes   []Index `json:"indexes,omitempty"`
}

// TableOrder returns tables in dependency order (topo-sorted).
// Cycle group tables appear after their non-cyclic dependencies.
func (s *Schema) TableOrder() []Table {
	return s.Tables
}

// TableByName looks up a table by schema and name.
func (s *Schema) TableByName(schema, name string) *Table {
	for i := range s.Tables {
		if s.Tables[i].Schema == schema && s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	return nil
}

// Table represents a resolved table definition.
type Table struct {
	Name         string             `json:"name"`
	Schema       string             `json:"schema"`
	Comment      string             `json:"comment"`
	Columns      []Column           `json:"columns"`
	PK           []string           `json:"pk"`
	FKs          []FK               `json:"fks"`
	Indexes      []Index            `json:"indexes"`
	Uniques      []UniqueConstraint `json:"uniques"`
	Checks       []CheckConstraint  `json:"checks"`
	Partitioning *PartitionSpec     `json:"partitioning,omitempty"`
	Dependencies []fd.FuncDep       `json:"dependencies,omitempty"`
	Maintenance  *MaintenanceConfig `json:"maintenance,omitempty"`
	Owner        string             `json:"owner,omitempty"`
	Policies     []Policy           `json:"policies,omitempty"`
	EnableRLS    bool               `json:"enable_rls,omitempty"`
	AppendOnly   bool               `json:"append_only,omitempty"`

	candidateKeys [][]string // cached result of CandidateKeys()
}

// HasIndexCovering returns true if any index's leading columns cover all of the
// given columns (prefix coverage).
func (t *Table) HasIndexCovering(columns []string) bool {
	for _, idx := range t.Indexes {
		if prefixCovers(idx.Columns, columns) {
			return true
		}
	}
	return false
}

// prefixCovers returns true if the leading elements of indexCols contain all of targets.
func prefixCovers(indexCols []string, targets []string) bool {
	if len(indexCols) < len(targets) {
		return false
	}
	prefix := indexCols[:len(targets)]
	targetSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		targetSet[t] = true
	}
	for _, col := range prefix {
		delete(targetSet, col)
	}
	return len(targetSet) == 0
}

// CandidateKeys computes candidate keys from the table's functional dependencies.
// The result is cached after the first call.
func (t *Table) CandidateKeys() [][]string {
	if t.candidateKeys != nil {
		return t.candidateKeys
	}
	allCols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		allCols[i] = c.Name
	}
	t.candidateKeys = fd.CandidateKeys(allCols, t.Dependencies)
	return t.candidateKeys
}

// Column represents a resolved column definition.
type Column struct {
	Name             string `json:"name"`
	PGType           string `json:"pg_type"`
	NotNull          bool   `json:"not_null"`
	Default          *string `json:"default,omitempty"`
	DefaultExpr      string `json:"default_expr,omitempty"`
	Generated        string `json:"generated,omitempty"`
	Stored           bool   `json:"stored,omitempty"`
	Identity         string `json:"identity,omitempty"` // "ALWAYS" or "BY DEFAULT" for identity columns
	Comment          string `json:"comment,omitempty"`
	SemanticTypeName string `json:"semantic_type_name,omitempty"`
	Array            bool   `json:"array,omitempty"`
	JSONSchema       string `json:"json_schema,omitempty"`
}

// FK represents a resolved foreign key constraint.
type FK struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema,omitempty"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
	OnDelete   string   `json:"on_delete"`
}

// Index represents a resolved index definition.
type Index struct {
	Name      string            `json:"name"`
	Columns   []string          `json:"columns"`
	Desc      []bool            `json:"desc,omitempty"` // parallel to Columns; true if DESC
	Method    string            `json:"method,omitempty"`
	Opclasses map[string]string `json:"opclasses,omitempty"`
	Where     string            `json:"where,omitempty"`
	Include   []string          `json:"include,omitempty"`
	With      map[string]string `json:"with,omitempty"`
	Unique    bool              `json:"unique"`
	IsAutoFK  bool              `json:"is_auto_fk"`
}

// UniqueConstraint represents a unique constraint.
type UniqueConstraint struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// CheckConstraint represents a check constraint.
type CheckConstraint struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

// Policy represents a row-level security (RLS) policy.
type Policy struct {
	Name         string `json:"name"`
	Operation    string `json:"operation"`               // SELECT, INSERT, UPDATE, DELETE, ALL
	Role         string `json:"role,omitempty"`           // PG role the policy applies to (e.g., "game_app")
	Using        string `json:"using,omitempty"`          // SQL expression for existing rows (SELECT/UPDATE/DELETE)
	WithCheck    string `json:"with_check,omitempty"`     // SQL expression for new rows (INSERT/UPDATE)
	ErrorCode    string `json:"error_code,omitempty"`     // Application-level error code (e.g., "chat_disabled")
	ErrorMessage string `json:"error_message,omitempty"` // Human-readable error message
}

// Enum represents a resolved enum type.
type Enum struct {
	Schema  string   `json:"schema,omitempty"`
	Name    string   `json:"name"`
	Values  []string `json:"values"`
	Comment string   `json:"comment,omitempty"`
}

// PartitionSpec represents partitioning configuration.
type PartitionSpec struct {
	Strategy string          `json:"strategy"`
	Column   string          `json:"column"`
	Name     string          `json:"name,omitempty"`  // child partition table name
	Bound    string          `json:"bound,omitempty"` // bound expression, e.g. "FROM ('2024-01-01') TO ('2024-02-01')"
	Children []PartitionSpec `json:"children"`
}

// MaintenanceConfig represents maintenance configuration for a table.
type MaintenanceConfig struct {
	Premake            int    `json:"premake"`
	Retention          string `json:"retention"`
	RetentionKeepTable bool   `json:"retention_keep_table"`
}

// StrPtr returns a pointer to the given string. Used for constructing
// struct literals with *string fields.
func StrPtr(s string) *string {
	return &s
}
