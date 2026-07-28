// Package diff compares two resolved schemas or a schema against a live database and produces a structured diff with risk annotations on each change.
package diff

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/risk"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// SchemaDiff describes the differences between a desired and actual schema.
type SchemaDiff struct {
	TablesAdded              []string               `json:"tables_added"`
	TablesRemoved            []string               `json:"tables_removed"`
	TablesChanged            []TableDiff            `json:"tables_changed"`
	EnumsAdded               []string               `json:"enums_added"`
	EnumsRemoved             []string               `json:"enums_removed"`
	EnumsChanged             []EnumDiff             `json:"enums_changed"`
	ExtensionsAdded          []string               `json:"extensions_added"`
	ExtensionsRemoved        []string               `json:"extensions_removed"`
	ViewsAdded               []string               `json:"views_added,omitempty"`
	ViewsRemoved             []string               `json:"views_removed,omitempty"`
	ViewsChanged             []ViewDiff             `json:"views_changed,omitempty"`
	MaterializedViewsAdded   []string               `json:"materialized_views_added,omitempty"`
	MaterializedViewsRemoved []string               `json:"materialized_views_removed,omitempty"`
	MaterializedViewsChanged []MaterializedViewDiff `json:"materialized_views_changed,omitempty"`
	SequencesAdded           []string               `json:"sequences_added,omitempty"`
	SequencesRemoved         []string               `json:"sequences_removed,omitempty"`
	SequencesChanged         []SequenceDiff         `json:"sequences_changed,omitempty"`
	CompositeTypesAdded      []string               `json:"composite_types_added,omitempty"`
	CompositeTypesRemoved    []string               `json:"composite_types_removed,omitempty"`
	CompositeTypesChanged    []CompositeTypeDiff    `json:"composite_types_changed,omitempty"`
	DomainsAdded             []string               `json:"domains_added,omitempty"`
	DomainsRemoved           []string               `json:"domains_removed,omitempty"`
	DomainsChanged           []DomainDiff           `json:"domains_changed,omitempty"`
	FunctionsAdded           []string               `json:"functions_added,omitempty"`
	FunctionsRemoved         []string               `json:"functions_removed,omitempty"`
	FunctionsChanged         []FunctionDiff         `json:"functions_changed,omitempty"`
	SMTransitionsChanged     []SMTransitionDiff     `json:"sm_transitions_changed,omitempty"`
	// PGVersionChanged and GroupsChanged report schema-global identity fields
	// that the differ historically ignored despite their being part of the
	// revision (Part III). They exist to satisfy the REVERSE conformance
	// direction (diff-empty implies revision-equal): under-reporting either one
	// would let two models with distinct revisions diff empty. PGVersionChanged
	// is [old, new]; GroupsChanged is set (a bool wrapper) when the group->table
	// map differs.
	PGVersionChanged *[2]int `json:"pg_version_changed,omitempty"`
	GroupsChanged    bool    `json:"groups_changed,omitempty"`
}

// SMTransitionDiff describes changes to a state machine type's transitions.
// Enum value changes (states added/removed) are tracked separately in EnumsChanged.
type SMTransitionDiff struct {
	TypeName           string            `json:"type_name"`
	TransitionsAdded   []SMTransitionRef `json:"transitions_added,omitempty"`
	TransitionsRemoved []SMTransitionRef `json:"transitions_removed,omitempty"`
}

// SMTransitionRef identifies a single directed transition edge (from -> to).
type SMTransitionRef struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TableDiff describes the differences within a single table.
type TableDiff struct {
	Name                string                      `json:"name"`
	ColumnsAdded        []model.Column              `json:"columns_added"`
	ColumnsRemoved      []string                    `json:"columns_removed"`
	ColumnsChanged      []ColumnChange              `json:"columns_changed"`
	FKsAdded            []model.FK                  `json:"fks_added"`
	FKsRemoved          []string                    `json:"fks_removed"`
	FKsChanged          []FKChange                  `json:"fks_changed"`
	IndexesAdded        []model.Index               `json:"indexes_added"`
	IndexesRemoved      []string                    `json:"indexes_removed"`
	IndexesChanged      []IndexChange               `json:"indexes_changed"`
	UniquesAdded        []model.UniqueConstraint    `json:"uniques_added"`
	UniquesRemoved      []string                    `json:"uniques_removed"`
	ChecksAdded         []model.CheckConstraint     `json:"checks_added"`
	ChecksRemoved       []string                    `json:"checks_removed"`
	ExclusionsAdded     []model.ExclusionConstraint `json:"exclusions_added"`
	ExclusionsRemoved   []string                    `json:"exclusions_removed"`
	TriggersAdded       []model.Trigger             `json:"triggers_added,omitempty"`
	TriggersRemoved     []string                    `json:"triggers_removed,omitempty"`
	TriggersChanged     []TriggerChange             `json:"triggers_changed,omitempty"`
	PoliciesAdded       []model.Policy              `json:"policies_added,omitempty"`
	PoliciesRemoved     []string                    `json:"policies_removed,omitempty"`
	PoliciesChanged     []PolicyDiff                `json:"policies_changed,omitempty"`
	EnableRLSChanged    *[2]bool                    `json:"enable_rls_changed,omitempty"`
	ForceRLSChanged     *[2]bool                    `json:"force_rls_changed,omitempty"`
	CommentChanged      *[2]string                  `json:"comment_changed"` // [old, new]
	PKChanged           *[2][]string                `json:"pk_changed"`      // [old, new]
	OwnerChanged        *[2]string                  `json:"owner_changed"`
	PartitioningChanged  *PartitionDiff              `json:"partitioning_changed,omitempty"`
	MaintenanceChanged   *MaintenanceDiff            `json:"maintenance_changed,omitempty"`
	AppendOnlyChanged    *[2]bool                    `json:"append_only_changed,omitempty"`
}

// ColumnChange describes a change to a single column, with risk classification.
type ColumnChange struct {
	Name              string              `json:"name"`
	TypeChanged       *[2]string          `json:"type_changed"`                // [old, new]
	NullableChanged   *[2]bool            `json:"nullable_changed"`            // [old, new]
	DefaultChanged    *[2]string          `json:"default_changed"`             // [old, new]
	CommentChanged    *[2]string          `json:"comment_changed"`             // [old, new]
	GeneratedChanged  *[2]string          `json:"generated_changed,omitempty"` // [old, new]
	StoredChanged     *[2]bool            `json:"stored_changed,omitempty"`    // [old, new]
	IdentityChanged   *[2]string          `json:"identity_changed,omitempty"`  // [old, new]
	ArrayChanged      *[2]bool            `json:"array_changed,omitempty"`
	CollationChanged  *[2]string          `json:"collation_changed,omitempty"`
	JSONSchemaChanged *[2]string          `json:"json_schema_changed,omitempty"`
	StatisticsChanged *[2]*int            `json:"statistics_changed,omitempty"`
	// SemanticTypeNameChanged reports a change in the column's declared semantic
	// type name (e.g. a pure-alias scalar `email` vs `text`). It is CLASS-AWARE:
	// compared only when both sides are registry-present models (diff --against,
	// diff --base, chain, conformance). An introspected `actual` carries no
	// semantic types, so comparing would false-drift; the introspected diff paths
	// (migrate, diff --live, serve) suppress it. Two DDL-identical models with
	// different semantic type names have DISTINCT revisions (SemanticTypeName is
	// encoded) yet produce different codegen — reporting it satisfies the reverse
	// conformance direction (diff-empty implies revision-equal).
	SemanticTypeNameChanged *[2]string          `json:"semantic_type_name_changed,omitempty"`
	// TypeNarrowing flags a type change that is NOT a safe widening (e.g.
	// bigint -> integer). It is set purely from the diff — there are no row
	// counts here — so downstream generation emits the EXPAND_CONTRACT_TYPE_NARROW
	// advisory on EVERY narrowing, not only on large tables (the 5.9 behavior
	// change: a manifest cannot gate on row count).
	TypeNarrowing bool                `json:"type_narrowing,omitempty"`
	Risk          risk.Classification `json:"risk"`
}

// EnumDiff describes changes to an enum type.
type EnumDiff struct {
	Name          string   `json:"name"`
	ValuesAdded   []string `json:"values_added"`
	ValuesRemoved []string `json:"values_removed"`

	// Position-aware fields.
	ValuesAddedAtEnd []string          `json:"values_added_at_end,omitempty"`
	ValuesInserted   []EnumValueInsert `json:"values_inserted,omitempty"`
	Reordered        bool              `json:"reordered,omitempty"`
}

// EnumValueInsert describes an enum value inserted in the middle of an existing
// enum, requiring BEFORE/AFTER syntax in ALTER TYPE.
type EnumValueInsert struct {
	Value string `json:"value"`
	After string `json:"after"` // the existing value it should go after
}

// ViewDiff describes changes to a view.
type ViewDiff struct {
	Name           string     `json:"name"`
	QueryChanged   *[2]string `json:"query_changed,omitempty"`
	CommentChanged *[2]string `json:"comment_changed,omitempty"`
}

// MaterializedViewDiff describes changes to a materialized view.
type MaterializedViewDiff struct {
	Name            string        `json:"name"`
	QueryChanged    *[2]string    `json:"query_changed,omitempty"`
	CommentChanged  *[2]string    `json:"comment_changed,omitempty"`
	WithDataChanged *[2]bool      `json:"with_data_changed,omitempty"`
	IndexesAdded    []model.Index `json:"indexes_added,omitempty"`
	IndexesRemoved  []string      `json:"indexes_removed,omitempty"`
	IndexesChanged  []IndexChange `json:"indexes_changed,omitempty"`
}

// SequenceDiff describes changes to a sequence.
type SequenceDiff struct {
	Name             string     `json:"name"`
	StartChanged     *[2]*int64 `json:"start_changed,omitempty"`
	IncrementChanged *[2]*int64 `json:"increment_changed,omitempty"`
	MinValueChanged  *[2]*int64 `json:"min_value_changed,omitempty"`
	MaxValueChanged  *[2]*int64 `json:"max_value_changed,omitempty"`
	CacheChanged     *[2]*int64 `json:"cache_changed,omitempty"`
	CycleChanged     *[2]bool   `json:"cycle_changed,omitempty"`
	OwnedByChanged   *[2]string `json:"owned_by_changed,omitempty"`
	CommentChanged   *[2]string `json:"comment_changed,omitempty"`
}

// CompositeTypeDiff describes changes to a composite type.
// Composite type changes are destructive (DROP + CREATE CASCADE).
type CompositeTypeDiff struct {
	Name           string                 `json:"name"`
	FieldsAdded    []model.CompositeField `json:"fields_added,omitempty"`
	FieldsRemoved  []string               `json:"fields_removed,omitempty"`
	FieldsChanged  []CompositeFieldChange `json:"fields_changed,omitempty"`
	CommentChanged *[2]string             `json:"comment_changed,omitempty"`
}

// CompositeFieldChange describes a change to a composite type field.
type CompositeFieldChange struct {
	Name        string     `json:"name"`
	TypeChanged *[2]string `json:"type_changed,omitempty"` // [old, new]
}

// FunctionDiff describes changes to a function/procedure.
type FunctionDiff struct {
	Name              string       `json:"name"`
	BodyChanged       *[2]string   `json:"body_changed,omitempty"`
	ReturnTypeChanged *[2]string   `json:"return_type_changed,omitempty"`
	ArgsChanged       bool         `json:"args_changed,omitempty"`
	SignatureChanged  bool         `json:"signature_changed,omitempty"` // true when arg types/count or return type changed (requires DROP+CREATE)
	LanguageChanged   *[2]string   `json:"language_changed,omitempty"`
	VolatilityChanged *[2]string   `json:"volatility_changed,omitempty"`
	ParallelChanged   *[2]string   `json:"parallel_changed,omitempty"`
	SecurityChanged   *[2]bool     `json:"security_changed,omitempty"`
	CommentChanged    *[2]string   `json:"comment_changed,omitempty"`
	CostChanged       *[2]*float64 `json:"cost_changed,omitempty"`
	RowsChanged       *[2]*float64 `json:"rows_changed,omitempty"`
	IsProcChanged     *[2]bool     `json:"is_proc_changed,omitempty"`
}

// DomainDiff describes changes to a domain type.
type DomainDiff struct {
	Name            string     `json:"name"`
	BaseTypeChanged *[2]string `json:"base_type_changed,omitempty"`
	CheckChanged    *[2]string `json:"check_changed,omitempty"`
	DefaultChanged  *[2]string `json:"default_changed,omitempty"`
	NotNullChanged  *[2]bool   `json:"not_null_changed,omitempty"`
	CommentChanged  *[2]string `json:"comment_changed,omitempty"`
}

// FKChange describes a changed foreign key constraint.
type FKChange struct {
	Name string   `json:"name"`
	Old  model.FK `json:"old"`
	New  model.FK `json:"new"`
}

// IndexChange describes a changed index.
type IndexChange struct {
	Name string      `json:"name"`
	Old  model.Index `json:"old"`
	New  model.Index `json:"new"`
}

// TriggerChange describes a changed trigger.
type TriggerChange struct {
	Name string        `json:"name"`
	Old  model.Trigger `json:"old"`
	New  model.Trigger `json:"new"`
}

// PolicyDiff describes changes to a single RLS policy.
type PolicyDiff struct {
	Name                string     `json:"name"`
	TypeChanged         *[2]string `json:"type_changed,omitempty"`
	RoleChanged         *[2]string `json:"role_changed,omitempty"`
	UsingChanged        *[2]string `json:"using_changed,omitempty"`
	WithCheckChanged    *[2]string `json:"with_check_changed,omitempty"`
	ErrorCodeChanged    *[2]string `json:"error_code_changed,omitempty"`
	ErrorMessageChanged *[2]string `json:"error_message_changed,omitempty"`
}

// PartitionDiff describes changes to a table's partitioning configuration.
type PartitionDiff struct {
	StrategyChanged *[2]string `json:"strategy_changed,omitempty"`
	KeyChanged      *[2]string `json:"key_changed,omitempty"`
	ChildrenAdded   []string   `json:"children_added,omitempty"`
	ChildrenRemoved []string   `json:"children_removed,omitempty"`
}

// MaintenanceDiff describes changes to a table's partman maintenance configuration.
type MaintenanceDiff struct {
	// InitialSetup is true when the actual side has no maintenance config and
	// the desired side introduces one -- partman registration (create_parent),
	// not a per-field update. When set, IntervalChanged is left nil so the
	// migrate layer does not mistake first-time setup for an interval change
	// (which is a hard error requiring repartitioning).
	InitialSetup     bool       `json:"initial_setup,omitempty"`
	IntervalChanged  *[2]string `json:"interval_changed,omitempty"`
	PremakeChanged   *[2]int    `json:"premake_changed,omitempty"`
	RetentionChanged *[2]string `json:"retention_changed,omitempty"`
	RetentionKeepTableChanged *[2]bool `json:"retention_keep_table_changed,omitempty"`
}

// IsEmpty returns true if the diff contains no changes.
func (d *SchemaDiff) IsEmpty() bool {
	return len(d.TablesAdded) == 0 &&
		len(d.TablesRemoved) == 0 &&
		len(d.TablesChanged) == 0 &&
		len(d.EnumsAdded) == 0 &&
		len(d.EnumsRemoved) == 0 &&
		len(d.EnumsChanged) == 0 &&
		len(d.ExtensionsAdded) == 0 &&
		len(d.ExtensionsRemoved) == 0 &&
		len(d.ViewsAdded) == 0 &&
		len(d.ViewsRemoved) == 0 &&
		len(d.ViewsChanged) == 0 &&
		len(d.MaterializedViewsAdded) == 0 &&
		len(d.MaterializedViewsRemoved) == 0 &&
		len(d.MaterializedViewsChanged) == 0 &&
		len(d.SequencesAdded) == 0 &&
		len(d.SequencesRemoved) == 0 &&
		len(d.SequencesChanged) == 0 &&
		len(d.CompositeTypesAdded) == 0 &&
		len(d.CompositeTypesRemoved) == 0 &&
		len(d.CompositeTypesChanged) == 0 &&
		len(d.DomainsAdded) == 0 &&
		len(d.DomainsRemoved) == 0 &&
		len(d.DomainsChanged) == 0 &&
		len(d.FunctionsAdded) == 0 &&
		len(d.FunctionsRemoved) == 0 &&
		len(d.FunctionsChanged) == 0 &&
		len(d.SMTransitionsChanged) == 0 &&
		d.PGVersionChanged == nil &&
		!d.GroupsChanged
}

// Summary returns a human-readable summary of the diff.
func (d *SchemaDiff) Summary() string {
	if d.IsEmpty() {
		return "no changes"
	}

	var parts []string

	if n := len(d.TablesAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d table(s) added", n))
	}
	if n := len(d.TablesRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d table(s) removed", n))
	}
	if n := len(d.TablesChanged); n > 0 {
		// Count total column and policy changes across all changed tables.
		var totalCols, totalPolicies int
		for _, td := range d.TablesChanged {
			totalCols += len(td.ColumnsAdded) + len(td.ColumnsRemoved) + len(td.ColumnsChanged)
			totalPolicies += len(td.PoliciesAdded) + len(td.PoliciesRemoved) + len(td.PoliciesChanged)
		}
		s := fmt.Sprintf("%d table(s) changed", n)
		if totalCols > 0 {
			s += fmt.Sprintf(" (%d column(s) modified)", totalCols)
		}
		if totalPolicies > 0 {
			s += fmt.Sprintf(" (%d policy/policies modified)", totalPolicies)
		}
		parts = append(parts, s)
	}
	if n := len(d.EnumsAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d enum(s) added", n))
	}
	if n := len(d.EnumsRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d enum(s) removed", n))
	}
	if n := len(d.EnumsChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d enum(s) changed", n))
	}
	if n := len(d.ExtensionsAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d extension(s) added", n))
	}
	if n := len(d.ExtensionsRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d extension(s) removed", n))
	}
	if n := len(d.ViewsAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d view(s) added", n))
	}
	if n := len(d.ViewsRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d view(s) removed", n))
	}
	if n := len(d.ViewsChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d view(s) changed", n))
	}
	if n := len(d.MaterializedViewsAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d materialized view(s) added", n))
	}
	if n := len(d.MaterializedViewsRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d materialized view(s) removed", n))
	}
	if n := len(d.MaterializedViewsChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d materialized view(s) changed", n))
	}
	if n := len(d.SequencesAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d sequence(s) added", n))
	}
	if n := len(d.SequencesRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d sequence(s) removed", n))
	}
	if n := len(d.SequencesChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d sequence(s) changed", n))
	}
	if n := len(d.CompositeTypesAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d composite type(s) added", n))
	}
	if n := len(d.CompositeTypesRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d composite type(s) removed", n))
	}
	if n := len(d.CompositeTypesChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d composite type(s) changed", n))
	}
	if n := len(d.DomainsAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d domain(s) added", n))
	}
	if n := len(d.DomainsRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d domain(s) removed", n))
	}
	if n := len(d.DomainsChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d domain(s) changed", n))
	}
	if n := len(d.FunctionsAdded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d function(s) added", n))
	}
	if n := len(d.FunctionsRemoved); n > 0 {
		parts = append(parts, fmt.Sprintf("%d function(s) removed", n))
	}
	if n := len(d.FunctionsChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d function(s) changed", n))
	}
	if n := len(d.SMTransitionsChanged); n > 0 {
		parts = append(parts, fmt.Sprintf("%d state machine(s) transitions changed", n))
	}
	if d.PGVersionChanged != nil {
		parts = append(parts, fmt.Sprintf("pg_version changed (%d -> %d)", d.PGVersionChanged[0], d.PGVersionChanged[1]))
	}
	if d.GroupsChanged {
		parts = append(parts, "table groups changed")
	}

	return strings.Join(parts, ", ")
}

// Diff compares two registry-present models (desired vs actual) and returns a
// structured diff. Items in desired but not in actual are "added"; items in
// actual but not in desired are "removed". Both sides are the SAME model class
// (L7): semantic type names are compared. Use DiffLive when actual is an
// introspected (registry-absent) schema.
func Diff(desired, actual *model.Schema) *SchemaDiff {
	return diffSchemas(desired, actual, nil, false)
}

// LiveNormalizer resolves the ≈_pg RESIDUE that pure N cannot reach:
// catalog-dependent cast materialization (e.g. `status = 'active'` vs the
// PG-stored `status = 'active'::text`). It round-trips a desired-side
// expression through the target database — PG computes its own canonical form —
// so the desired side matches the introspected (already PG-canonical) side.
//
// It is used ONLY on the live diff path (diff --live). Identity NEVER consumes
// its output: the pure/encoding path has no database. Implementations are
// best-effort total — an expression a round-trip cannot reach (referencing an
// absent table/column) falls to the minimal forward-simulation rule set, which
// is N itself.
type LiveNormalizer interface {
	// NormalizeExprForTable returns the target DB's canonical form of expr in
	// the context of schema.table. schema may be "" for the default schema.
	NormalizeExprForTable(schema, table, expr string) string
}

// DiffLive compares a registry-present desired model against an INTROSPECTED
// (registry-absent) actual schema, with an optional LiveNormalizer. Because the
// introspected side carries no semantic type information (L7 — a different model
// class), class-aware fields such as Column.SemanticTypeName are NOT compared,
// so they never false-drift. When ln is non-nil (the diff --live path), the
// DESIRED side's table-scoped expressions are round-tripped through the target
// DB before comparison, resolving the catalog-dependent cast residue. The
// caller's desired schema is never mutated — a normalized copy is built.
func DiffLive(desired, actual *model.Schema, ln LiveNormalizer) *SchemaDiff {
	return diffSchemas(desired, actual, ln, true)
}

// diffSchemas is the shared implementation. actualIntrospected suppresses
// class-aware comparisons (semantic type names) that only make sense between two
// registry-present models.
func diffSchemas(desired, actual *model.Schema, ln LiveNormalizer, actualIntrospected bool) *SchemaDiff {
	if ln != nil {
		desired = liveNormalizeDesired(desired, ln)
	}
	d := &SchemaDiff{}

	diffTables(d, desired, actual, actualIntrospected)
	diffEnums(d, desired, actual)
	diffExtensions(d, desired, actual)
	diffViews(d, desired, actual)
	diffMaterializedViews(d, desired, actual)
	diffSequences(d, desired, actual)
	diffCompositeTypes(d, desired, actual)
	diffDomains(d, desired, actual)
	diffFunctions(d, desired, actual)
	diffSMTransitions(d, desired, actual)
	diffSchemaMeta(d, desired, actual)

	return d
}

// diffSchemaMeta compares the schema-global identity fields that are part of a
// model's revision but were historically invisible to the differ (Part III):
// the target PostgreSQL version (which silently alters emitted DDL) and the
// table groups. Reporting them is a REVERSE-conformance obligation
// (diff-empty implies revision-equal); both are encoded into canonical bytes,
// so a change to either flips the revision and must therefore be a non-empty
// diff.
func diffSchemaMeta(d *SchemaDiff, desired, actual *model.Schema) {
	if desired.PGVersion != actual.PGVersion {
		d.PGVersionChanged = &[2]int{actual.PGVersion, desired.PGVersion}
	}
	if !groupsEqual(desired.Groups, actual.Groups) {
		d.GroupsChanged = true
	}
}

// groupsEqual reports whether two group->table maps are equal. Group membership
// order is not semantic (groups are a canonical-only collection for filtering),
// so table lists are compared as SETS; the map itself is compared key-wise.
func groupsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, at := range a {
		bt, ok := b[name]
		if !ok {
			return false
		}
		if !stringSetEqual(at, bt) {
			return false
		}
	}
	return true
}

// stringSetEqual reports whether two string slices contain the same elements,
// ignoring order and duplicates.
func stringSetEqual(a, b []string) bool {
	as := make(map[string]struct{}, len(a))
	for _, v := range a {
		as[v] = struct{}{}
	}
	bs := make(map[string]struct{}, len(b))
	for _, v := range b {
		bs[v] = struct{}{}
	}
	if len(as) != len(bs) {
		return false
	}
	for v := range as {
		if _, ok := bs[v]; !ok {
			return false
		}
	}
	return true
}

// diffTables matches tables by schema-qualified name and diffs matched pairs.
// Partman-managed children (detected during introspection) are excluded from
// existence drift: they appear in the live DB but are created/dropped by partman,
// so they should not be flagged as added or removed.
func diffTables(d *SchemaDiff, desired, actual *model.Schema, actualIntrospected bool) {
	added, removed, matched := matchObjects(desired.Tables, actual.Tables, func(t model.Table) string {
		return tableKey(&t)
	})
	for _, t := range added {
		d.TablesAdded = append(d.TablesAdded, tableKey(&t))
	}
	for _, t := range removed {
		// Exclude partman-managed children from existence drift.
		if t.PartmanManaged {
			continue
		}
		d.TablesRemoved = append(d.TablesRemoved, tableKey(&t))
	}
	for _, p := range matched {
		td := diffTable(&p.Desired, &p.Actual, actualIntrospected)
		if !isTableDiffEmpty(&td) {
			d.TablesChanged = append(d.TablesChanged, td)
		}
	}
}

func tableKey(t *model.Table) string {
	if t.Schema == "" || t.Schema == "public" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// diffTable diffs two matched tables.
func diffTable(desired, actual *model.Table, actualIntrospected bool) TableDiff {
	td := TableDiff{Name: tableKey(desired)}

	diffColumns(&td, desired, actual, actualIntrospected)
	diffFKs(&td, desired, actual)
	diffIndexes(&td, desired, actual)
	diffUniques(&td, desired, actual)
	diffChecks(&td, desired, actual)
	diffExclusions(&td, desired, actual)
	diffTriggers(&td, desired, actual)
	diffPolicies(&td, desired, actual)

	// Comment
	if desired.Comment != actual.Comment {
		td.CommentChanged = &[2]string{actual.Comment, desired.Comment}
	}

	// PK
	if !sliceEqual(desired.PK, actual.PK) {
		td.PKChanged = &[2][]string{actual.PK, desired.PK}
	}

	// Owner
	if desired.Owner != actual.Owner {
		td.OwnerChanged = &[2]string{actual.Owner, desired.Owner}
	}

	// Partitioning
	diffPartitioning(&td, desired, actual)

	// Maintenance (partman config)
	td.MaintenanceChanged = diffMaintenance(desired.Maintenance, actual.Maintenance)

	// AppendOnly
	if desired.AppendOnly != actual.AppendOnly {
		td.AppendOnlyChanged = &[2]bool{actual.AppendOnly, desired.AppendOnly}
	}

	// EnableRLS
	if desired.EnableRLS != actual.EnableRLS {
		td.EnableRLSChanged = &[2]bool{actual.EnableRLS, desired.EnableRLS}
	}

	// ForceRLS
	if desired.ForceRLS != actual.ForceRLS {
		td.ForceRLSChanged = &[2]bool{actual.ForceRLS, desired.ForceRLS}
	}

	return td
}

func isTableDiffEmpty(td *TableDiff) bool {
	return len(td.ColumnsAdded) == 0 &&
		len(td.ColumnsRemoved) == 0 &&
		len(td.ColumnsChanged) == 0 &&
		len(td.FKsAdded) == 0 &&
		len(td.FKsRemoved) == 0 &&
		len(td.FKsChanged) == 0 &&
		len(td.IndexesAdded) == 0 &&
		len(td.IndexesRemoved) == 0 &&
		len(td.IndexesChanged) == 0 &&
		len(td.UniquesAdded) == 0 &&
		len(td.UniquesRemoved) == 0 &&
		len(td.ChecksAdded) == 0 &&
		len(td.ChecksRemoved) == 0 &&
		len(td.ExclusionsAdded) == 0 &&
		len(td.ExclusionsRemoved) == 0 &&
		len(td.TriggersAdded) == 0 &&
		len(td.TriggersRemoved) == 0 &&
		len(td.TriggersChanged) == 0 &&
		len(td.PoliciesAdded) == 0 &&
		len(td.PoliciesRemoved) == 0 &&
		len(td.PoliciesChanged) == 0 &&
		td.EnableRLSChanged == nil &&
		td.ForceRLSChanged == nil &&
		td.CommentChanged == nil &&
		td.PKChanged == nil &&
		td.OwnerChanged == nil &&
		td.PartitioningChanged == nil &&
		td.MaintenanceChanged == nil &&
		td.AppendOnlyChanged == nil
}

// diffColumns matches columns by name and classifies changes with risk.
func diffColumns(td *TableDiff, desired, actual *model.Table, actualIntrospected bool) {
	added, removed, matched := matchObjects(desired.Columns, actual.Columns, func(c model.Column) string {
		return c.Name
	})
	for _, c := range added {
		td.ColumnsAdded = append(td.ColumnsAdded, c)
	}
	for _, c := range removed {
		td.ColumnsRemoved = append(td.ColumnsRemoved, c.Name)
	}
	for _, p := range matched {
		cc := diffColumn(&p.Desired, &p.Actual, actualIntrospected)
		if cc != nil {
			td.ColumnsChanged = append(td.ColumnsChanged, *cc)
		}
	}
}

// diffColumn compares two matched columns and returns nil if identical.
func diffColumn(desired, actual *model.Column, actualIntrospected bool) *ColumnChange {
	cc := ColumnChange{Name: desired.Name}
	changed := false

	// Type comparison using default-precision-aware equality.
	// This avoids false positives where omitted precision matches the PG default
	// (e.g., timestamp vs timestamp(6) are semantically identical).
	if !typesEqualWithDefaults(desired.PGType, actual.PGType) {
		cc.TypeChanged = &[2]string{typeinfo.Reconstruct(actual.PGType), typeinfo.Reconstruct(desired.PGType)}
		changed = true
	}

	// Nullable comparison.
	// NotNull: true means NOT NULL, false means nullable.
	if desired.NotNull != actual.NotNull {
		cc.NullableChanged = &[2]bool{actual.NotNull, desired.NotNull}
		changed = true
	}

	// Default comparison (normalized).
	desiredDefault := normalizeDefault(desired.Default, desired.DefaultExpr)
	actualDefault := normalizeDefault(actual.Default, actual.DefaultExpr)
	if desiredDefault != actualDefault {
		cc.DefaultChanged = &[2]string{actualDefault, desiredDefault}
		changed = true
	}

	// Comment comparison.
	if desired.Comment != actual.Comment {
		cc.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	// Generated comparison (the generation expression compares via N).
	if !sqlparse.ExprEqual(desired.Generated, actual.Generated) {
		cc.GeneratedChanged = &[2]string{actual.Generated, desired.Generated}
		changed = true
	}

	// Stored comparison (STORED <-> VIRTUAL transition).
	// Only meaningful for generated columns: a change in storage strategy
	// requires DROP + recreate, which is destructive.
	if desired.Generated != "" && actual.Generated != "" && desired.Stored != actual.Stored {
		cc.StoredChanged = &[2]bool{actual.Stored, desired.Stored}
		changed = true
	}

	// Identity comparison.
	if desired.Identity != actual.Identity {
		cc.IdentityChanged = &[2]string{actual.Identity, desired.Identity}
		changed = true
	}

	// Array comparison.
	if desired.Array != actual.Array {
		cc.ArrayChanged = &[2]bool{actual.Array, desired.Array}
		changed = true
	}

	// JSONSchema comparison.
	if desired.JSONSchema != actual.JSONSchema {
		cc.JSONSchemaChanged = &[2]string{actual.JSONSchema, desired.JSONSchema}
		changed = true
	}

	// Collation comparison.
	if desired.Collation != actual.Collation {
		cc.CollationChanged = &[2]string{actual.Collation, desired.Collation}
		changed = true
	}

	// Statistics comparison.
	if !intPtrEqual(desired.Statistics, actual.Statistics) {
		cc.StatisticsChanged = &[2]*int{actual.Statistics, desired.Statistics}
		changed = true
	}

	// Semantic type name comparison (CLASS-AWARE). Only meaningful between two
	// registry-present models: an introspected actual carries no semantic types,
	// so a comparison would always false-drift. When both sides are model-class
	// (diff --against/--base, chain, conformance), a pure-alias scalar type change
	// (same PGType, different semantic name) is DDL-identical yet codegen-visible
	// and revision-distinct, so it must be reported.
	if !actualIntrospected && desired.SemanticTypeName != actual.SemanticTypeName {
		cc.SemanticTypeNameChanged = &[2]string{actual.SemanticTypeName, desired.SemanticTypeName}
		changed = true
	}

	if !changed {
		return nil
	}

	// Classify risk for the most significant change.
	cc.Risk = classifyColumnChange(&cc, desired)
	return &cc
}

// classifyColumnChange determines the risk classification for a column change.
// It picks the highest risk among all sub-changes.
func classifyColumnChange(cc *ColumnChange, desired *model.Column) risk.Classification {
	highest := risk.Classification{RiskLevel: risk.Safe}

	if cc.TypeChanged != nil {
		widening := IsWidening(cc.TypeChanged[0], cc.TypeChanged[1])
		// Narrowing-always advisory (5.9): flagged from the diff alone, no row
		// count. Downstream generation renders EXPAND_CONTRACT_TYPE_NARROW from
		// this flag.
		cc.TypeNarrowing = !widening
		c := risk.Classify(risk.OpAlterColumnType, risk.OpContext{
			IsWidening: widening,
		})
		if c.RiskLevel > highest.RiskLevel {
			highest = c
		}
	}

	if cc.NullableChanged != nil {
		oldNotNull := cc.NullableChanged[0]
		newNotNull := cc.NullableChanged[1]
		if !oldNotNull && newNotNull {
			// nullable -> NOT NULL
			c := risk.Classify(risk.OpSetNotNull, risk.OpContext{})
			if c.RiskLevel > highest.RiskLevel {
				highest = c
			}
		} else {
			// NOT NULL -> nullable
			c := risk.Classify(risk.OpDropNotNull, risk.OpContext{})
			if c.RiskLevel > highest.RiskLevel {
				highest = c
			}
		}
	}

	// Default changes are safe.
	if cc.DefaultChanged != nil {
		c := risk.Classification{RiskLevel: risk.Safe}
		if c.RiskLevel > highest.RiskLevel {
			highest = c
		}
	}

	// Comment changes are safe (no risk escalation needed).

	if cc.StoredChanged != nil {
		// STORED <-> VIRTUAL requires DROP + recreate of the generated column.
		c := risk.Classify(risk.OpDropColumn, risk.OpContext{})
		if c.RiskLevel > highest.RiskLevel {
			highest = c
		}
	}

	if cc.CollationChanged != nil {
		// Collation change requires ALTER COLUMN TYPE ... COLLATE.
		c := risk.Classify(risk.OpAlterColumnType, risk.OpContext{})
		if c.RiskLevel > highest.RiskLevel {
			highest = c
		}
	}

	return highest
}

// IsWidening returns true if oldType -> newType is a safe widening conversion.
// Arguments are SQL type strings (e.g., from typeinfo.Reconstruct output).
func IsWidening(oldType, newType string) bool {
	oldT := typeinfo.Parse(oldType)
	newT := typeinfo.Parse(newType)

	// int4 -> int8
	if oldT.Base == "int4" && newT.Base == "int8" {
		return true
	}

	// int2 -> int4 or int8
	if oldT.Base == "int2" && (newT.Base == "int4" || newT.Base == "int8") {
		return true
	}

	// varchar -> text
	if oldT.Base == "varchar" && newT.Base == "text" {
		return true
	}

	// varchar(N) -> varchar(M) where M > N
	if oldT.Base == "varchar" && newT.Base == "varchar" &&
		oldT.Params.Length != nil && newT.Params.Length != nil &&
		*newT.Params.Length > *oldT.Params.Length {
		return true
	}

	// float4 -> float8
	if oldT.Base == "float4" && newT.Base == "float8" {
		return true
	}

	return false
}

// defaultPrecision maps base type names to their PostgreSQL default precision.
// When a type is specified without explicit precision, PostgreSQL uses these
// defaults. A nil value means "no default" -- omitting the parameter gives
// arbitrary/unlimited semantics that differ from any specific value.
//
// Reference: https://www.postgresql.org/docs/current/datatype.html
var defaultPrecision = map[string]*int{
	"timestamp":   intPtr(6), // microsecond precision
	"timestamptz": intPtr(6), // microsecond precision
	"time":        intPtr(6), // microsecond precision
	"timetz":      intPtr(6), // microsecond precision
	"interval":    intPtr(6), // full range of fields
	"bit":         intPtr(1), // default length is 1
	// numeric (no params) = arbitrary precision (NOT same as 0) -- nil entry means no default
	// varchar (no length) = unlimited (same as text) -- nil entry means no default
}

// typesEqualWithDefaults compares two typeinfo.Type values, treating omitted
// precision/length as equivalent to the PostgreSQL default for that type.
// For example, timestamp (no precision) equals timestamp(6) because 6 is the
// PG default for timestamp precision.
func typesEqualWithDefaults(a, b typeinfo.Type) bool {
	if a.Base != b.Base || a.DomainName != b.DomainName || a.Params.RawModifier != b.Params.RawModifier {
		return false
	}
	if !intPtrEqualWithDefault(a.Params.Precision, b.Params.Precision, a.Base) {
		return false
	}
	if !intPtrEqual(a.Params.Scale, b.Params.Scale) {
		return false
	}
	if !intPtrEqualWithDefault(a.Params.Length, b.Params.Length, a.Base) {
		return false
	}
	return true
}

// intPtrEqualWithDefault compares two *int values, treating nil as the default
// value for the given base type. The defaultPrecision map stores the implicit
// default for each type (precision for timestamp/interval, length for bit).
// Each PG type uses exactly one parameterization dimension, so both Precision
// and Length can safely be checked against the same default value.
func intPtrEqualWithDefault(a, b *int, base string) bool {
	if intPtrEqual(a, b) {
		return true
	}
	// One is nil and the other is not. Check if the non-nil value matches
	// the default for this type.
	defPtr := defaultPrecision[base]
	if defPtr == nil {
		// No default -- nil and non-nil are genuinely different.
		return false
	}
	def := *defPtr

	if a == nil && b != nil {
		return *b == def
	}
	if b == nil && a != nil {
		return *a == def
	}
	return false
}

// normalizeDefault returns a normalized default value for comparison.
// DefaultExpr (a SQL expression) takes precedence over Default (a literal).
//
// Expression defaults route through N (the single ≈_syn computation). Literal
// defaults are compared EXACTLY (trim only) — never lowercased: the old
// ToLower was unsound, identifying the distinct literals 'Active' and 'active'
// as equal and MISSING real drift. A literal is an opaque value, not an
// expression, so routing it through N (which would parse a bare word as a
// case-folded column reference) is wrong; exact comparison is correct.
func normalizeDefault(literal *string, expr string) string {
	if expr != "" {
		// A cast-wrapped quoted literal (e.g. '{}'::jsonb, 'active'::text) is
		// semantically the bare literal. Introspection already reduces the live
		// default to the bare form (parseSimpleDefault), so a desired
		// expression-default carrying the explicit cast must reduce the same way
		// or it false-drifts against the introspected literal. Reduce it here so
		// both sides converge (fixes the ≈_pg cast-materialization residue for
		// literal defaults without a database round-trip).
		if lit, ok := reduceCastWrappedLiteral(expr); ok {
			return lit
		}
		return sqlparse.NormalizeExpr(expr)
	}
	if literal != nil {
		return strings.TrimSpace(*literal)
	}
	return ""
}

// reduceCastWrappedLiteral reduces a default expression that is a single quoted
// string literal followed by ::type cast(s) (optionally a COLLATE clause) to its
// bare inner value — mirroring introspection's parseSimpleDefault/parseQuotedDefault
// so a desired '{}'::jsonb reconciles with an introspected literal {}. It returns
// ("", false) for anything that is not a simple cast-wrapped quoted literal (a
// genuine expression, left to N).
func reduceCastWrappedLiteral(expr string) (string, bool) {
	s := strings.TrimSpace(expr)
	if s == "" || s[0] != '\'' {
		return "", false
	}
	var val strings.Builder
	i := 1
	closed := false
	for i < len(s) {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' { // '' escape
				val.WriteByte('\'')
				i += 2
				continue
			}
			i++ // consume the closing quote
			closed = true
			break
		}
		val.WriteByte(s[i])
		i++
	}
	if !closed {
		return "", false
	}
	// Everything after the closing quote must be only ::cast chains / COLLATE.
	if strings.TrimSpace(stripDefaultCastChain(s[i:])) != "" {
		return "", false
	}
	return val.String(), true
}

// stripDefaultCastChain removes a chain of ::type casts (and a trailing COLLATE
// clause) from the tail of a default expression. It mirrors introspection's
// stripCastChain so the two sides reduce identically.
func stripDefaultCastChain(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "::") {
		s = s[2:]
		j := 0
		for j < len(s) {
			c := s[j]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '[' || c == ']' {
				j++
				continue
			}
			if c == ' ' {
				after := strings.TrimLeft(s[j:], " ")
				if strings.HasPrefix(after, "::") || strings.HasPrefix(strings.ToUpper(after), "COLLATE") || after == "" {
					break
				}
				j++
				continue
			}
			break
		}
		s = strings.TrimSpace(s[j:])
	}
	return s
}

// domainDefaultKey normalizes a domain default (expression via N, literal
// exact) into a comparison key. DefaultExpr takes precedence over the literal.
// A cast-wrapped quoted literal reduces to its bare form (same residue fix as
// normalizeDefault) so an explicit '{}'::jsonb reconciles with the introspected
// literal {}.
func domainDefaultKey(literal, expr string) string {
	if expr != "" {
		if lit, ok := reduceCastWrappedLiteral(expr); ok {
			return lit
		}
		return sqlparse.NormalizeExpr(expr)
	}
	return strings.TrimSpace(literal)
}

// diffFKs matches foreign keys by name.
func diffFKs(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjectsTrunc(desired.FKs, actual.FKs, func(fk model.FK) string {
		return fk.Name
	})
	for _, fk := range added {
		td.FKsAdded = append(td.FKsAdded, fk)
	}
	for _, fk := range removed {
		td.FKsRemoved = append(td.FKsRemoved, fk.Name)
	}
	for _, p := range matched {
		if !fkEqual(&p.Desired, &p.Actual) {
			td.FKsChanged = append(td.FKsChanged, FKChange{
				Name: p.Desired.Name,
				Old:  p.Actual,
				New:  p.Desired,
			})
		}
	}
}

func fkEqual(a, b *model.FK) bool {
	return a.Name == b.Name &&
		sliceEqual(a.Columns, b.Columns) &&
		a.RefSchema == b.RefSchema &&
		a.RefTable == b.RefTable &&
		sliceEqual(a.RefColumns, b.RefColumns) &&
		a.OnDelete == b.OnDelete
}

// diffIndexes matches indexes by name.
func diffIndexes(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjectsTrunc(desired.Indexes, actual.Indexes, func(idx model.Index) string {
		return idx.Name
	})
	for _, idx := range added {
		td.IndexesAdded = append(td.IndexesAdded, idx)
	}
	for _, idx := range removed {
		td.IndexesRemoved = append(td.IndexesRemoved, idx.Name)
	}
	for _, p := range matched {
		if !indexEqual(&p.Desired, &p.Actual) {
			td.IndexesChanged = append(td.IndexesChanged, IndexChange{
				Name: p.Desired.Name,
				Old:  p.Actual,
				New:  p.Desired,
			})
		}
	}
}

// indexMethod returns an index's access method, defaulting an unspecified method
// to "btree" (PostgreSQL's default). A desired index that omits the method must
// reconcile with the introspected index whose method is the materialized "btree".
func indexMethod(m string) string {
	if m == "" {
		return "btree"
	}
	return m
}

func indexEqual(a, b *model.Index) bool {
	return a.Name == b.Name &&
		keyColumnsEqual(a.Columns, b.Columns) &&
		boolSliceEqual(a.Desc, b.Desc) &&
		indexMethod(a.Method) == indexMethod(b.Method) &&
		mapEqual(a.Opclasses, b.Opclasses) &&
		mapEqual(a.Collations, b.Collations) &&
		sqlparse.ExprEqual(a.Where, b.Where) &&
		sliceEqual(a.Include, b.Include) &&
		mapEqual(a.With, b.With)
}

// bareIdentifier matches an unquoted SQL identifier (a plain column name). A key
// column that is NOT a bare identifier is an EXPRESSION (e.g. lower(email),
// (a + b)) and must be compared via ≈_syn, not raw string equality.
var bareIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// isExpressionColumn reports whether an index/exclusion key column is an
// expression rather than a bare column reference. Anything containing a call,
// operator, cast, quoting, or qualification is an expression.
func isExpressionColumn(s string) bool {
	return !bareIdentifier.MatchString(strings.TrimSpace(s))
}

// keyColumnEqual compares one index/exclusion KEY column. Plain identifiers
// compare EXACTLY (a genuine column rename is a real change). Expression columns
// compare via sqlparse.ExprEqual (≈_syn), so catalog-independent spelling
// differences — function case, whitespace, cast-alias names — do not false-drift
// a desired expression key against PostgreSQL's deparsed form. (The remaining
// residue — catalog-dependent cast materialization like lower(email) vs
// lower((email)::text) — is the ≈_pg boundary resolved by live round-trip
// normalization on live paths, not by this pure comparison.)
func keyColumnEqual(a, b string) bool {
	if isExpressionColumn(a) || isExpressionColumn(b) {
		return sqlparse.ExprEqual(a, b)
	}
	// Both are bare (unquoted) identifiers. PostgreSQL folds unquoted
	// identifiers to lowercase, so `Email` and `email` name the SAME column;
	// a raw exact compare would false-drift a desired mixed-case unquoted name
	// against the introspected lowercased form. QUOTED identifiers never reach
	// here — a quote makes isExpressionColumn true, routing them through the
	// expression path above, which preserves case (a quoted "Email" is a
	// genuinely distinct, case-sensitive column).
	return strings.ToLower(a) == strings.ToLower(b)
}

// keyColumnsEqual compares two ordered lists of index/exclusion key columns
// element-wise. Key-column ORDER is semantic (it defines the index), so this is
// positional, not set comparison.
func keyColumnsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !keyColumnEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// boolSliceEqual returns true if two bool slices represent the same sort
// directions. nil and all-false are treated as equivalent (both mean all ASC).
func boolSliceEqual(a, b []bool) bool {
	aLen := len(a)
	bLen := len(b)
	maxLen := aLen
	if bLen > maxLen {
		maxLen = bLen
	}
	for i := 0; i < maxLen; i++ {
		av := i < aLen && a[i]
		bv := i < bLen && b[i]
		if av != bv {
			return false
		}
	}
	return true
}

// mapEqual returns true if two string maps are equal.
func mapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || v != bv {
			return false
		}
	}
	return true
}

// diffUniques matches unique constraints by name.
func diffUniques(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjectsTrunc(desired.Uniques, actual.Uniques, func(u model.UniqueConstraint) string {
		return u.Name
	})
	for _, u := range added {
		td.UniquesAdded = append(td.UniquesAdded, u)
	}
	for _, u := range removed {
		td.UniquesRemoved = append(td.UniquesRemoved, u.Name)
	}
	for _, p := range matched {
		if !uniqueEqual(p.Desired, p.Actual) {
			td.UniquesRemoved = append(td.UniquesRemoved, p.Actual.Name)
			td.UniquesAdded = append(td.UniquesAdded, p.Desired)
		}
	}
}

func uniqueEqual(a, b model.UniqueConstraint) bool {
	if a.Deferrable != b.Deferrable || a.InitiallyDeferred != b.InitiallyDeferred {
		return false
	}
	return sliceEqual(a.Columns, b.Columns)
}

// diffChecks matches check constraints by name.
func diffChecks(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjectsTrunc(desired.Checks, actual.Checks, func(c model.CheckConstraint) string {
		return c.Name
	})
	for _, c := range added {
		td.ChecksAdded = append(td.ChecksAdded, c)
	}
	for _, c := range removed {
		td.ChecksRemoved = append(td.ChecksRemoved, c.Name)
	}
	for _, p := range matched {
		if !sqlparse.ExprEqual(p.Desired.Expr, p.Actual.Expr) {
			td.ChecksRemoved = append(td.ChecksRemoved, p.Actual.Name)
			td.ChecksAdded = append(td.ChecksAdded, p.Desired)
		}
	}
}

// diffExclusions matches exclusion constraints by name.
func diffExclusions(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjectsTrunc(desired.Exclusions, actual.Exclusions, func(e model.ExclusionConstraint) string {
		return e.Name
	})
	for _, e := range added {
		td.ExclusionsAdded = append(td.ExclusionsAdded, e)
	}
	for _, e := range removed {
		td.ExclusionsRemoved = append(td.ExclusionsRemoved, e.Name)
	}
	for _, p := range matched {
		if !exclusionEqual(p.Desired, p.Actual) {
			td.ExclusionsRemoved = append(td.ExclusionsRemoved, p.Actual.Name)
			td.ExclusionsAdded = append(td.ExclusionsAdded, p.Desired)
		}
	}
}

func exclusionEqual(a, b model.ExclusionConstraint) bool {
	if a.Method != b.Method || !sqlparse.ExprEqual(a.Where, b.Where) || a.Deferrable != b.Deferrable || a.InitiallyDeferred != b.InitiallyDeferred {
		return false
	}
	if len(a.Elements) != len(b.Elements) {
		return false
	}
	for i := range a.Elements {
		// Exclusion element columns are index key columns and can be
		// expressions; compare via ≈_syn, not raw string (see keyColumnEqual).
		if !keyColumnEqual(a.Elements[i].Column, b.Elements[i].Column) || a.Elements[i].Operator != b.Elements[i].Operator {
			return false
		}
	}
	return true
}

// diffTriggers matches triggers by name, excluding pgdesign-managed state
// machine triggers (_pgdesign_sm_*) to prevent phantom diffs.
func diffTriggers(td *TableDiff, desired, actual *model.Table) {
	desiredFiltered := filterNonSMTriggers(desired.Triggers)
	actualFiltered := filterNonSMTriggers(actual.Triggers)

	added, removed, matched := matchObjects(desiredFiltered, actualFiltered, func(trig model.Trigger) string {
		return trig.Name
	})
	for _, trig := range added {
		td.TriggersAdded = append(td.TriggersAdded, trig)
	}
	for _, trig := range removed {
		td.TriggersRemoved = append(td.TriggersRemoved, trig.Name)
	}
	for _, p := range matched {
		if !triggerEqual(&p.Desired, &p.Actual) {
			td.TriggersChanged = append(td.TriggersChanged, TriggerChange{
				Name: p.Desired.Name,
				Old:  p.Actual,
				New:  p.Desired,
			})
		}
	}
}

// filterNonSMTriggers returns triggers that are not pgdesign-managed state
// machine triggers. SM triggers use the _pgdesign_sm_ prefix and are managed
// by the SM diff/migrate path, not the generic trigger diff.
func filterNonSMTriggers(triggers []model.Trigger) []model.Trigger {
	var result []model.Trigger
	for _, t := range triggers {
		if !strings.HasPrefix(t.Name, "_pgdesign_sm_") {
			result = append(result, t)
		}
	}
	return result
}

func triggerEqual(a, b *model.Trigger) bool {
	return a.Name == b.Name &&
		a.Function == b.Function &&
		sliceEqual(a.Events, b.Events) &&
		a.Timing == b.Timing &&
		a.ForEach == b.ForEach &&
		sqlparse.ExprEqual(a.When, b.When) &&
		a.Constraint == b.Constraint &&
		a.Deferrable == b.Deferrable &&
		a.InitiallyDeferred == b.InitiallyDeferred &&
		a.ReferencingOld == b.ReferencingOld &&
		a.ReferencingNew == b.ReferencingNew
}

// diffPolicies matches policies by name.
func diffPolicies(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjects(desired.Policies, actual.Policies, func(p model.Policy) string {
		return p.Name
	})
	for _, p := range added {
		td.PoliciesAdded = append(td.PoliciesAdded, p)
	}
	for _, p := range removed {
		td.PoliciesRemoved = append(td.PoliciesRemoved, p.Name)
	}
	for _, p := range matched {
		pd := diffPolicy(&p.Desired, &p.Actual)
		if pd != nil {
			td.PoliciesChanged = append(td.PoliciesChanged, *pd)
		}
	}
}

func diffPolicy(desired, actual *model.Policy) *PolicyDiff {
	pd := PolicyDiff{Name: desired.Name}
	changed := false

	if desired.Type != actual.Type {
		pd.TypeChanged = &[2]string{actual.Type, desired.Type}
		changed = true
	}
	if desired.Role != actual.Role {
		pd.RoleChanged = &[2]string{actual.Role, desired.Role}
		changed = true
	}
	if !sqlparse.ExprEqual(desired.Using, actual.Using) {
		pd.UsingChanged = &[2]string{actual.Using, desired.Using}
		changed = true
	}
	if !sqlparse.ExprEqual(desired.WithCheck, actual.WithCheck) {
		pd.WithCheckChanged = &[2]string{actual.WithCheck, desired.WithCheck}
		changed = true
	}
	if desired.ErrorCode != actual.ErrorCode {
		pd.ErrorCodeChanged = &[2]string{actual.ErrorCode, desired.ErrorCode}
		changed = true
	}
	if desired.ErrorMessage != actual.ErrorMessage {
		pd.ErrorMessageChanged = &[2]string{actual.ErrorMessage, desired.ErrorMessage}
		changed = true
	}

	if !changed {
		return nil
	}
	return &pd
}

// diffPartitioning compares partitioning configuration between two tables.
func diffPartitioning(td *TableDiff, desired, actual *model.Table) {
	dp := desired.Partitioning
	ap := actual.Partitioning

	// Both nil: no partitioning on either side.
	if dp == nil && ap == nil {
		return
	}

	pd := &PartitionDiff{}
	changed := false

	// Determine strategies (empty string for unpartitioned).
	desiredStrategy := ""
	actualStrategy := ""
	if dp != nil {
		desiredStrategy = dp.Strategy
	}
	if ap != nil {
		actualStrategy = ap.Strategy
	}

	if desiredStrategy != actualStrategy {
		pd.StrategyChanged = &[2]string{actualStrategy, desiredStrategy}
		changed = true
	}

	// Determine partition keys.
	desiredKey := ""
	actualKey := ""
	if dp != nil {
		desiredKey = strings.Join(dp.Columns, ", ")
	}
	if ap != nil {
		actualKey = strings.Join(ap.Columns, ", ")
	}

	if desiredKey != actualKey {
		pd.KeyChanged = &[2]string{actualKey, desiredKey}
		changed = true
	}

	// Compare first-level children by name.
	var desiredChildren []string
	var actualChildren []string
	if dp != nil {
		for _, child := range dp.Children {
			desiredChildren = append(desiredChildren, partitionChildKey(&child))
		}
	}
	if ap != nil {
		for _, child := range ap.Children {
			actualChildren = append(actualChildren, partitionChildKey(&child))
		}
	}

	pd.ChildrenAdded = stringDiff(desiredChildren, actualChildren)
	pd.ChildrenRemoved = stringDiff(actualChildren, desiredChildren)
	if len(pd.ChildrenAdded) > 0 || len(pd.ChildrenRemoved) > 0 {
		changed = true
	}

	if changed {
		td.PartitioningChanged = pd
	}
}

// diffMaintenance compares maintenance (partman) configuration between two tables.
// Returns nil if both are nil or identical.
func diffMaintenance(desired, actual *model.MaintenanceConfig) *MaintenanceDiff {
	if desired == nil && actual == nil {
		return nil
	}

	md := &MaintenanceDiff{}
	changed := false

	// Initial setup: actual has no partman config, desired introduces one.
	// This is create_parent registration, not a field-level interval change.
	initialSetup := actual == nil && desired != nil

	// Get effective values (zero values for nil configs).
	var dInterval, aInterval string
	var dPremake, aPremake int
	var dRetention, aRetention string
	var dKeepTable, aKeepTable bool

	if desired != nil {
		dInterval = desired.Interval
		dPremake = desired.Premake
		dRetention = desired.Retention
		dKeepTable = desired.RetentionKeepTable
	}
	if actual != nil {
		aInterval = actual.Interval
		aPremake = actual.Premake
		aRetention = actual.Retention
		aKeepTable = actual.RetentionKeepTable
	}

	if initialSetup {
		// Everything is "new" -- record the setup flag and the resulting
		// retention/premake so the migrate layer can emit create_parent plus
		// any config UPDATE. Do NOT set IntervalChanged: the interval is part
		// of the initial create_parent, not a repartitioning change.
		md.InitialSetup = true
		if dPremake != 0 {
			md.PremakeChanged = &[2]int{0, dPremake}
		}
		if dRetention != "" {
			md.RetentionChanged = &[2]string{"", dRetention}
		}
		if dKeepTable {
			md.RetentionKeepTableChanged = &[2]bool{false, dKeepTable}
		}
		return md
	}

	if dInterval != aInterval {
		md.IntervalChanged = &[2]string{aInterval, dInterval}
		changed = true
	}

	if dPremake != aPremake {
		md.PremakeChanged = &[2]int{aPremake, dPremake}
		changed = true
	}

	if dRetention != aRetention {
		md.RetentionChanged = &[2]string{aRetention, dRetention}
		changed = true
	}

	if dKeepTable != aKeepTable {
		md.RetentionKeepTableChanged = &[2]bool{aKeepTable, dKeepTable}
		changed = true
	}

	if !changed {
		return nil
	}
	return md
}

// partitionChildKey returns an identifier for a partition child.
// Combines Name and Bound to form a unique key for the child partition.
func partitionChildKey(ps *model.PartitionSpec) string {
	if ps.Name != "" && ps.Bound != "" {
		return ps.Name + ":" + ps.Bound
	}
	if ps.Name != "" {
		return ps.Name
	}
	return ps.Bound
}

// diffEnums matches enums by schema-qualified name.
func diffEnums(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.Enums, actual.Enums, func(e model.Enum) string {
		return enumKey(&e)
	})
	for _, e := range added {
		d.EnumsAdded = append(d.EnumsAdded, enumKey(&e))
	}
	for _, e := range removed {
		d.EnumsRemoved = append(d.EnumsRemoved, enumKey(&e))
	}
	for _, p := range matched {
		ed := diffEnum(&p.Desired, &p.Actual)
		if ed != nil {
			d.EnumsChanged = append(d.EnumsChanged, *ed)
		}
	}
}

func enumKey(e *model.Enum) string {
	if e.Schema == "" || e.Schema == "public" {
		return e.Name
	}
	return e.Schema + "." + e.Name
}

// diffEnum compares two matched enums and returns nil if identical.
// It performs position-aware comparison to distinguish safe appends from
// middle insertions and detect reordering.
func diffEnum(desired, actual *model.Enum) *EnumDiff {
	added := stringDiff(desired.Values, actual.Values)
	removed := stringDiff(actual.Values, desired.Values)
	reordered := enumReordered(desired.Values, actual.Values)

	if len(added) == 0 && len(removed) == 0 && !reordered {
		return nil
	}

	ed := &EnumDiff{
		Name:          enumKey(desired),
		ValuesAdded:   added,
		ValuesRemoved: removed,
		Reordered:     reordered,
	}

	// Classify added values as appended-at-end vs inserted-in-middle.
	if len(added) > 0 {
		classifyEnumInsertions(ed, desired.Values, actual.Values)
	}

	return ed
}

// classifyEnumInsertions splits added values into safe appends (at end) and
// middle insertions (requiring BEFORE/AFTER). A new value is "appended at end"
// if it appears after all old values in the desired list. Otherwise it is
// inserted in the middle and we record which existing value it follows.
func classifyEnumInsertions(ed *EnumDiff, desired, actual []string) {
	oldSet := make(map[string]bool, len(actual))
	for _, v := range actual {
		oldSet[v] = true
	}

	// Find the index of the last old value in the desired list.
	lastOldIdx := -1
	for i, v := range desired {
		if oldSet[v] {
			lastOldIdx = i
		}
	}

	addedSet := make(map[string]bool, len(ed.ValuesAdded))
	for _, v := range ed.ValuesAdded {
		addedSet[v] = true
	}

	for i, v := range desired {
		if !addedSet[v] {
			continue
		}
		if i > lastOldIdx {
			// This new value appears after all existing values.
			ed.ValuesAddedAtEnd = append(ed.ValuesAddedAtEnd, v)
		} else {
			// Inserted in the middle. Find the nearest preceding value
			// that exists in the old enum (the AFTER neighbor).
			after := ""
			for j := i - 1; j >= 0; j-- {
				if oldSet[desired[j]] {
					after = desired[j]
					break
				}
			}
			ed.ValuesInserted = append(ed.ValuesInserted, EnumValueInsert{
				Value: v,
				After: after, // empty string means "before the first old value"
			})
		}
	}
}

// enumReordered returns true if values present in both old and new lists have
// a different relative order. It extracts the common subsequence from each
// list and checks whether the orderings match.
func enumReordered(desired, actual []string) bool {
	oldSet := make(map[string]bool, len(actual))
	for _, v := range actual {
		oldSet[v] = true
	}
	newSet := make(map[string]bool, len(desired))
	for _, v := range desired {
		newSet[v] = true
	}

	// Extract values common to both, in the order they appear in each list.
	var commonInOld, commonInNew []string
	for _, v := range actual {
		if newSet[v] {
			commonInOld = append(commonInOld, v)
		}
	}
	for _, v := range desired {
		if oldSet[v] {
			commonInNew = append(commonInNew, v)
		}
	}

	return !sliceEqual(commonInOld, commonInNew)
}

// diffExtensions compares extension lists.
func diffExtensions(d *SchemaDiff, desired, actual *model.Schema) {
	d.ExtensionsAdded = stringDiff(desired.Extensions, actual.Extensions)
	d.ExtensionsRemoved = stringDiff(actual.Extensions, desired.Extensions)
}

// diffViews matches views by schema-qualified name.
func diffViews(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.Views, actual.Views, func(v model.View) string {
		return viewKey(&v)
	})
	for _, v := range added {
		d.ViewsAdded = append(d.ViewsAdded, viewKey(&v))
	}
	for _, v := range removed {
		d.ViewsRemoved = append(d.ViewsRemoved, viewKey(&v))
	}
	for _, p := range matched {
		vd := diffView(&p.Desired, &p.Actual)
		if vd != nil {
			d.ViewsChanged = append(d.ViewsChanged, *vd)
		}
	}
}

func viewKey(v *model.View) string {
	if v.Schema == "" || v.Schema == "public" {
		return v.Name
	}
	return v.Schema + "." + v.Name
}

// diffView compares two matched views and returns nil if identical.
func diffView(desired, actual *model.View) *ViewDiff {
	vd := &ViewDiff{Name: viewKey(desired)}
	changed := false

	if desired.Query != actual.Query {
		vd.QueryChanged = &[2]string{actual.Query, desired.Query}
		changed = true
	}

	if desired.Comment != actual.Comment {
		vd.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	if !changed {
		return nil
	}
	return vd
}

// diffMaterializedViews matches materialized views by schema-qualified name.
func diffMaterializedViews(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.MaterializedViews, actual.MaterializedViews, func(mv model.MaterializedView) string {
		return mvKey(&mv)
	})
	for _, mv := range added {
		d.MaterializedViewsAdded = append(d.MaterializedViewsAdded, mvKey(&mv))
	}
	for _, mv := range removed {
		d.MaterializedViewsRemoved = append(d.MaterializedViewsRemoved, mvKey(&mv))
	}
	for _, p := range matched {
		mvd := diffMaterializedView(&p.Desired, &p.Actual)
		if mvd != nil {
			d.MaterializedViewsChanged = append(d.MaterializedViewsChanged, *mvd)
		}
	}
}

func mvKey(mv *model.MaterializedView) string {
	if mv.Schema == "" || mv.Schema == "public" {
		return mv.Name
	}
	return mv.Schema + "." + mv.Name
}

// diffMaterializedView compares two matched materialized views and returns nil if identical.
func diffMaterializedView(desired, actual *model.MaterializedView) *MaterializedViewDiff {
	mvd := &MaterializedViewDiff{Name: mvKey(desired)}
	changed := false

	if desired.Query != actual.Query {
		mvd.QueryChanged = &[2]string{actual.Query, desired.Query}
		changed = true
	}

	if desired.Comment != actual.Comment {
		mvd.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	if desired.WithData != actual.WithData {
		mvd.WithDataChanged = &[2]bool{actual.WithData, desired.WithData}
		changed = true
	}

	// Diff indexes.
	idxAdded, idxRemoved, idxMatched := matchObjectsTrunc(desired.Indexes, actual.Indexes, func(idx model.Index) string {
		return idx.Name
	})
	for _, idx := range idxAdded {
		mvd.IndexesAdded = append(mvd.IndexesAdded, idx)
		changed = true
	}
	for _, idx := range idxRemoved {
		mvd.IndexesRemoved = append(mvd.IndexesRemoved, idx.Name)
		changed = true
	}
	for _, p := range idxMatched {
		if !indexEqual(&p.Desired, &p.Actual) {
			mvd.IndexesChanged = append(mvd.IndexesChanged, IndexChange{
				Name: p.Desired.Name,
				Old:  p.Actual,
				New:  p.Desired,
			})
			changed = true
		}
	}

	if !changed {
		return nil
	}
	return mvd
}

// stringDiff returns elements in a that are not in b.
func stringDiff(a, b []string) []string {
	bSet := make(map[string]bool, len(b))
	for _, s := range b {
		bSet[s] = true
	}
	var diff []string
	for _, s := range a {
		if !bSet[s] {
			diff = append(diff, s)
		}
	}
	return diff
}

// sliceEqual returns true if two string slices are equal.
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// intPtr returns a pointer to the given int value.
func intPtr(n int) *int {
	return &n
}

// intPtrEqual returns true if two *int pointers represent the same value.
// nil and nil are equal; nil and non-nil are not.
func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// diffSequences matches sequences by schema-qualified name.
func diffSequences(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.Sequences, actual.Sequences, func(s model.Sequence) string {
		return seqKey(&s)
	})
	for _, s := range added {
		d.SequencesAdded = append(d.SequencesAdded, seqKey(&s))
	}
	for _, s := range removed {
		d.SequencesRemoved = append(d.SequencesRemoved, seqKey(&s))
	}
	for _, p := range matched {
		sd := diffSequence(&p.Desired, &p.Actual)
		if sd != nil {
			d.SequencesChanged = append(d.SequencesChanged, *sd)
		}
	}
}

func seqKey(s *model.Sequence) string {
	if s.Schema == "" || s.Schema == "public" {
		return s.Name
	}
	return s.Schema + "." + s.Name
}

// diffSequence compares two matched sequences and returns nil if identical.
func diffSequence(desired, actual *model.Sequence) *SequenceDiff {
	sd := &SequenceDiff{Name: seqKey(desired)}
	changed := false

	if !int64PtrEqual(desired.Start, actual.Start) {
		sd.StartChanged = &[2]*int64{actual.Start, desired.Start}
		changed = true
	}
	if !int64PtrEqual(desired.Increment, actual.Increment) {
		sd.IncrementChanged = &[2]*int64{actual.Increment, desired.Increment}
		changed = true
	}
	if !int64PtrEqual(desired.MinValue, actual.MinValue) {
		sd.MinValueChanged = &[2]*int64{actual.MinValue, desired.MinValue}
		changed = true
	}
	if !int64PtrEqual(desired.MaxValue, actual.MaxValue) {
		sd.MaxValueChanged = &[2]*int64{actual.MaxValue, desired.MaxValue}
		changed = true
	}
	if !int64PtrEqual(desired.Cache, actual.Cache) {
		sd.CacheChanged = &[2]*int64{actual.Cache, desired.Cache}
		changed = true
	}
	if desired.Cycle != actual.Cycle {
		sd.CycleChanged = &[2]bool{actual.Cycle, desired.Cycle}
		changed = true
	}
	if desired.OwnedBy != actual.OwnedBy {
		sd.OwnedByChanged = &[2]string{actual.OwnedBy, desired.OwnedBy}
		changed = true
	}
	if desired.Comment != actual.Comment {
		sd.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	if !changed {
		return nil
	}
	return sd
}

// int64PtrEqual returns true if two *int64 pointers represent the same value.
func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// diffCompositeTypes matches composite types by schema-qualified name.
func diffCompositeTypes(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.CompositeTypes, actual.CompositeTypes, func(ct model.CompositeType) string {
		return compositeTypeKey(&ct)
	})
	for _, ct := range added {
		d.CompositeTypesAdded = append(d.CompositeTypesAdded, compositeTypeKey(&ct))
	}
	for _, ct := range removed {
		d.CompositeTypesRemoved = append(d.CompositeTypesRemoved, compositeTypeKey(&ct))
	}
	for _, p := range matched {
		ctd := diffCompositeType(&p.Desired, &p.Actual)
		if ctd != nil {
			d.CompositeTypesChanged = append(d.CompositeTypesChanged, *ctd)
		}
	}
}

func compositeTypeKey(ct *model.CompositeType) string {
	if ct.Schema == "" || ct.Schema == "public" {
		return ct.Name
	}
	return ct.Schema + "." + ct.Name
}

// diffCompositeType compares two matched composite types and returns nil if identical.
func diffCompositeType(desired, actual *model.CompositeType) *CompositeTypeDiff {
	ctd := &CompositeTypeDiff{Name: compositeTypeKey(desired)}
	changed := false

	// Match fields by name.
	added, removed, matched := matchObjects(desired.Fields, actual.Fields, func(f model.CompositeField) string {
		return f.Name
	})
	for _, f := range added {
		ctd.FieldsAdded = append(ctd.FieldsAdded, f)
		changed = true
	}
	for _, f := range removed {
		ctd.FieldsRemoved = append(ctd.FieldsRemoved, f.Name)
		changed = true
	}
	for _, p := range matched {
		if !p.Desired.PGType.Equal(p.Actual.PGType) {
			ctd.FieldsChanged = append(ctd.FieldsChanged, CompositeFieldChange{
				Name:        p.Desired.Name,
				TypeChanged: &[2]string{typeinfo.Reconstruct(p.Actual.PGType), typeinfo.Reconstruct(p.Desired.PGType)},
			})
			changed = true
		}
	}

	// Comment comparison.
	if desired.Comment != actual.Comment {
		ctd.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	if !changed {
		return nil
	}
	return ctd
}

func funcKey(f *model.Function) string {
	if f.Schema == "" || f.Schema == "public" {
		return f.Name
	}
	return f.Schema + "." + f.Name
}

// diffFunctions matches functions by schema-qualified name.
func diffFunctions(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.Functions, actual.Functions, func(f model.Function) string {
		return funcKey(&f)
	})
	for _, f := range added {
		d.FunctionsAdded = append(d.FunctionsAdded, funcKey(&f))
	}
	for _, f := range removed {
		d.FunctionsRemoved = append(d.FunctionsRemoved, funcKey(&f))
	}
	for _, p := range matched {
		fd := diffFunction(&p.Desired, &p.Actual)
		if fd != nil {
			d.FunctionsChanged = append(d.FunctionsChanged, *fd)
		}
	}
}

// diffFunction compares two matched functions and returns nil if identical.
func diffFunction(desired, actual *model.Function) *FunctionDiff {
	fd := &FunctionDiff{Name: funcKey(desired)}
	changed := false

	if desired.Body != actual.Body {
		fd.BodyChanged = &[2]string{actual.Body, desired.Body}
		changed = true
	}

	if desired.ReturnType != actual.ReturnType {
		fd.ReturnTypeChanged = &[2]string{actual.ReturnType, desired.ReturnType}
		changed = true
	}

	if !funcArgsEqual(desired.Args, actual.Args) {
		fd.ArgsChanged = true
		changed = true
	}

	// SignatureChanged: true when arg types/count or return type changed.
	// This is distinct from ArgsChanged which also fires for name/default changes.
	// Signature change requires DROP+CREATE; body-only can use CREATE OR REPLACE.
	if desired.ReturnType != actual.ReturnType || !funcArgSignatureEqual(desired.Args, actual.Args) {
		fd.SignatureChanged = true
	}

	if desired.Language != actual.Language {
		fd.LanguageChanged = &[2]string{actual.Language, desired.Language}
		changed = true
	}

	if desired.Volatility != actual.Volatility {
		fd.VolatilityChanged = &[2]string{actual.Volatility, desired.Volatility}
		changed = true
	}

	if desired.Parallel != actual.Parallel {
		fd.ParallelChanged = &[2]string{actual.Parallel, desired.Parallel}
		changed = true
	}

	if desired.SecurityDefiner != actual.SecurityDefiner {
		fd.SecurityChanged = &[2]bool{actual.SecurityDefiner, desired.SecurityDefiner}
		changed = true
	}

	if desired.Comment != actual.Comment {
		fd.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	if !float64PtrEqual(desired.Cost, actual.Cost) {
		fd.CostChanged = &[2]*float64{actual.Cost, desired.Cost}
		changed = true
	}

	if !float64PtrEqual(desired.Rows, actual.Rows) {
		fd.RowsChanged = &[2]*float64{actual.Rows, desired.Rows}
		changed = true
	}

	if desired.IsProc != actual.IsProc {
		fd.IsProcChanged = &[2]bool{actual.IsProc, desired.IsProc}
		changed = true
		// Kind change (function <-> procedure) always changes signature.
		fd.SignatureChanged = true
	}

	if !changed {
		return nil
	}
	return fd
}

// funcArgsEqual returns true if two arg slices are identical (name, type, default).
func funcArgsEqual(a, b []model.FunctionArg) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || !a[i].Type.Equal(b[i].Type) || a[i].Default != b[i].Default {
			return false
		}
	}
	return true
}

// funcArgSignatureEqual returns true if arg types and count match.
// Only compares types (not names or defaults) since PostgreSQL identifies
// function signatures by argument types, not names.
func funcArgSignatureEqual(a, b []model.FunctionArg) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Type.Equal(b[i].Type) {
			return false
		}
	}
	return true
}

// float64PtrEqual returns true if two *float64 pointers represent the same value.
func float64PtrEqual(a, b *float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// diffDomains matches domains by schema-qualified name.
func diffDomains(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.Domains, actual.Domains, func(dom model.Domain) string {
		return domainKey(&dom)
	})
	for _, dom := range added {
		d.DomainsAdded = append(d.DomainsAdded, domainKey(&dom))
	}
	for _, dom := range removed {
		d.DomainsRemoved = append(d.DomainsRemoved, domainKey(&dom))
	}
	for _, p := range matched {
		dd := diffDomain(&p.Desired, &p.Actual)
		if dd != nil {
			d.DomainsChanged = append(d.DomainsChanged, *dd)
		}
	}
}

func domainKey(d *model.Domain) string {
	if d.Schema == "" || d.Schema == "public" {
		return d.Name
	}
	return d.Schema + "." + d.Name
}

// diffDomain compares two matched domains and returns nil if identical.
func diffDomain(desired, actual *model.Domain) *DomainDiff {
	dd := &DomainDiff{Name: domainKey(desired)}
	changed := false

	if !desired.BaseType.Equal(actual.BaseType) {
		dd.BaseTypeChanged = &[2]string{typeinfo.Reconstruct(actual.BaseType), typeinfo.Reconstruct(desired.BaseType)}
		changed = true
	}

	if !sqlparse.ExprEqual(desired.Check, actual.Check) {
		dd.CheckChanged = &[2]string{actual.Check, desired.Check}
		changed = true
	}

	// Compare defaults via N: an expression default (DefaultExpr) routes through
	// N; a literal default (Default) compares exactly. DefaultExpr takes
	// precedence over the literal.
	desiredRaw := desired.DefaultExpr
	if desiredRaw == "" {
		desiredRaw = desired.Default
	}
	actualRaw := actual.DefaultExpr
	if actualRaw == "" {
		actualRaw = actual.Default
	}
	desiredDefault := domainDefaultKey(desired.Default, desired.DefaultExpr)
	actualDefault := domainDefaultKey(actual.Default, actual.DefaultExpr)
	if desiredDefault != actualDefault {
		dd.DefaultChanged = &[2]string{actualRaw, desiredRaw}
		changed = true
	}

	if desired.NotNull != actual.NotNull {
		dd.NotNullChanged = &[2]bool{actual.NotNull, desired.NotNull}
		changed = true
	}

	if desired.Comment != actual.Comment {
		dd.CommentChanged = &[2]string{actual.Comment, desired.Comment}
		changed = true
	}

	if !changed {
		return nil
	}
	return dd
}

// diffSMTransitions compares state machine transition maps between two schemas.
// State changes (enum values added/removed) are handled by diffEnums; this
// function detects changes to the transition edges themselves.
func diffSMTransitions(d *SchemaDiff, desired, actual *model.Schema) {
	desiredByName := make(map[string]*model.SMTransitionMap, len(desired.StateMachineTransitions))
	for i := range desired.StateMachineTransitions {
		smt := &desired.StateMachineTransitions[i]
		desiredByName[smt.TypeName] = smt
	}
	actualByName := make(map[string]*model.SMTransitionMap, len(actual.StateMachineTransitions))
	for i := range actual.StateMachineTransitions {
		smt := &actual.StateMachineTransitions[i]
		actualByName[smt.TypeName] = smt
	}

	// Check all desired SMs for changes vs actual.
	var names []string
	for name := range desiredByName {
		names = append(names, name)
	}
	// Also check actual SMs not present in desired (removed SM types).
	for name := range actualByName {
		if _, ok := desiredByName[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		desiredSMT := desiredByName[name]
		actualSMT := actualByName[name]

		if desiredSMT == nil || actualSMT == nil {
			// SM type entirely added or removed -- transitions are part of enum
			// add/remove, not tracked as transition-only diff.
			continue
		}

		smDiff := diffSMTransitionMap(name, desiredSMT, actualSMT)
		if smDiff != nil {
			d.SMTransitionsChanged = append(d.SMTransitionsChanged, *smDiff)
		}
	}
}

// diffSMTransitionMap compares two SMTransitionMaps and returns nil if identical.
func diffSMTransitionMap(typeName string, desired, actual *model.SMTransitionMap) *SMTransitionDiff {
	// Build sets of transition edges for each side.
	desiredEdges := smTransitionEdges(desired)
	actualEdges := smTransitionEdges(actual)

	var added, removed []SMTransitionRef
	for edge := range desiredEdges {
		if !actualEdges[edge] {
			added = append(added, edge)
		}
	}
	for edge := range actualEdges {
		if !desiredEdges[edge] {
			removed = append(removed, edge)
		}
	}

	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	// Sort for deterministic output.
	sortSMTransitionRefs(added)
	sortSMTransitionRefs(removed)

	return &SMTransitionDiff{
		TypeName:           typeName,
		TransitionsAdded:   added,
		TransitionsRemoved: removed,
	}
}

// smTransitionEdges extracts the set of directed edges from a transition map.
func smTransitionEdges(smt *model.SMTransitionMap) map[SMTransitionRef]bool {
	edges := make(map[SMTransitionRef]bool)
	for from, tos := range smt.Transitions {
		for _, to := range tos {
			edges[SMTransitionRef{From: from, To: to}] = true
		}
	}
	return edges
}

// sortSMTransitionRefs sorts transition references by From, then To.
func sortSMTransitionRefs(refs []SMTransitionRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].From != refs[j].From {
			return refs[i].From < refs[j].From
		}
		return refs[i].To < refs[j].To
	})
}
