// Package model provides the resolved intermediate representation (IR) for pgdesign.
// It is the canonical in-memory schema that all downstream packages consume.
package model

import (
	"github.com/smm-h/pgdesign/internal/fd"
)

// Schema is the top-level resolved schema.
type Schema struct {
	Name        string
	Extensions  []string
	Enums       []Enum
	Tables      []Table
	CycleGroups [][]string
	PGVersion   int
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
	Name         string
	Schema       string
	Comment      string
	Columns      []Column
	PK           []string
	FKs          []FK
	Indexes      []Index
	Uniques      []UniqueConstraint
	Checks       []CheckConstraint
	Partitioning *PartitionSpec
	Dependencies []fd.FuncDep
	Maintenance  *MaintenanceConfig
	Owner        string
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
func (t *Table) CandidateKeys() [][]string {
	allCols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		allCols[i] = c.Name
	}
	return fd.CandidateKeys(allCols, t.Dependencies)
}

// Column represents a resolved column definition.
type Column struct {
	Name             string
	PGType           string
	NotNull          bool
	Default          string
	DefaultExpr      string
	Generated        string
	Stored           bool
	Identity         string // "ALWAYS" or "BY DEFAULT" for identity columns
	Comment          string
	SemanticTypeName string
}

// FK represents a resolved foreign key constraint.
type FK struct {
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
	OnDelete   string
}

// Index represents a resolved index definition.
type Index struct {
	Name     string
	Columns  []string
	Method   string
	Opclass  string
	Where    string
	Include  []string
	IsAutoFK bool
}

// UniqueConstraint represents a unique constraint.
type UniqueConstraint struct {
	Name    string
	Columns []string
}

// CheckConstraint represents a check constraint.
type CheckConstraint struct {
	Name string
	Expr string
}

// Enum represents a resolved enum type.
type Enum struct {
	Schema  string
	Name    string
	Values  []string
	Comment string
}

// PartitionSpec represents partitioning configuration.
type PartitionSpec struct {
	Strategy string
	Column   string
	Children []PartitionSpec
}

// MaintenanceConfig represents maintenance configuration for a table.
type MaintenanceConfig struct {
	Premake            int
	Retention          string
	RetentionKeepTable bool
}
