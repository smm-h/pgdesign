// Package diff defines the generic schema differ interfaces. The PG-specific
// implementation lives in internal/diff; this package provides the abstract
// contracts that make the differ engine reusable for non-PG schema models.
//
// # Interface design
//
// The differ algorithm is parameterized over three interfaces:
//
//   - Model: abstracts the schema/table/column/index concepts so the differ
//     operates on any schema model, not just the PG-specific model.Table.
//
//   - TypeComparer: abstracts type equality. The PG implementation uses
//     typeinfo.Parse/Reconstruct with default-precision awareness. Other
//     backends provide their own type normalization.
//
//   - Classifier: abstracts risk classification for changes. The PG
//     implementation delegates to internal/risk with pgcap version gates.
//
// # Current status
//
// The genericization is deferred. The interfaces below define the target
// abstractions; internal/diff still uses concrete PG types directly. The
// migration path is:
//
//  1. Wrap internal/diff's concrete functions behind these interfaces.
//  2. Parameterize the differ algorithm over Model/TypeComparer/Classifier.
//  3. Move the parameterized algorithm to pkg/diff.
//  4. internal/diff becomes a thin adapter providing PG-specific implementations.
//
// Until step 3, consumers that need the differ import internal/diff directly.
package diff

import (
	"github.com/smm-h/pgdesign/pkg/diagnostic"
)

// RiskLevel represents the safety level of a schema change operation.
// This mirrors internal/risk.RiskLevel but is defined here so pkg/diff
// does not import internal/risk.
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

// Classification is the risk assessment result for a schema change.
type Classification struct {
	RiskLevel  RiskLevel
	Reversible bool
	DataLoss   bool
	Suggestion string
}

// ColumnRef identifies a column in a schema model.
type ColumnRef struct {
	Table  string
	Column string
}

// TypeComparer abstracts type equality for the differ. The PG implementation
// uses typeinfo.Parse/Reconstruct with default-precision awareness (e.g.,
// timestamp vs timestamp(6) are semantically identical in PG).
type TypeComparer interface {
	// TypesEqual returns true if two type strings represent the same type
	// after normalization (e.g., alias resolution, default precision).
	TypesEqual(a, b string) bool

	// Reconstruct produces a canonical SQL type string for display in diffs.
	Reconstruct(typeStr string) string

	// IsWidening returns true if oldType -> newType is a safe widening
	// conversion (e.g., int4 -> int8, varchar(50) -> text).
	IsWidening(oldType, newType string) bool
}

// Classifier assigns risk classifications to schema change operations.
// The PG implementation delegates to internal/risk with pgcap version gates.
type Classifier interface {
	// ClassifyColumnChange returns the risk classification for a column change.
	// The implementation inspects the change details (type change, nullability,
	// etc.) and returns the highest risk among all sub-changes.
	ClassifyColumnChange(change ColumnChange) Classification
}

// ColumnChange describes a change to a single column, as seen by the Classifier.
// This is the generic representation; the PG implementation maps from the
// concrete internal/diff.ColumnChange.
type ColumnChange struct {
	Name            string
	TypeChanged     bool
	OldType         string
	NewType         string
	NullableChanged bool
	OldNotNull      bool
	NewNotNull      bool
	DefaultChanged  bool
	StoredChanged   bool
	CollationChanged bool
}

// Model abstracts schema, table, column, and index concepts for the differ.
// The PG implementation wraps internal/model types.
//
// TODO: Define the full Model interface once genericization proceeds.
// The interface should provide:
//   - Tables() returning a list of table-like objects with columns, FKs, indexes
//   - Enums() returning enum types
//   - Views(), MaterializedViews(), Functions(), etc.
//   - Each sub-object should be matchable by a string key (name or qualified name)
type Model interface {
	// TableNames returns all table names (schema-qualified where applicable).
	TableNames() []string
}

// DiffResult holds the output of a diff operation. This is the generic
// counterpart to internal/diff.SchemaDiff.
//
// TODO: Populate with generic diff fields once the algorithm is extracted.
type DiffResult struct {
	// Diagnostics collected during diffing.
	Diagnostics []diagnostic.Diagnostic

	// HasChanges is true if any schema objects differ.
	HasChanges bool
}
