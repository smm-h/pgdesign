// Package diff compares two resolved schemas (desired vs actual) and produces
// a structured diff with risk annotations on each change.
package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/risk"
)

// SchemaDiff describes the differences between a desired and actual schema.
type SchemaDiff struct {
	TablesAdded       []string    `json:"tables_added"`
	TablesRemoved     []string    `json:"tables_removed"`
	TablesChanged     []TableDiff `json:"tables_changed"`
	EnumsAdded        []string    `json:"enums_added"`
	EnumsRemoved      []string    `json:"enums_removed"`
	EnumsChanged      []EnumDiff  `json:"enums_changed"`
	ExtensionsAdded   []string    `json:"extensions_added"`
	ExtensionsRemoved []string    `json:"extensions_removed"`
	ViewsAdded        []string    `json:"views_added,omitempty"`
	ViewsRemoved      []string    `json:"views_removed,omitempty"`
	ViewsChanged      []ViewDiff  `json:"views_changed,omitempty"`
	MaterializedViewsAdded   []string               `json:"materialized_views_added,omitempty"`
	MaterializedViewsRemoved []string               `json:"materialized_views_removed,omitempty"`
	MaterializedViewsChanged []MaterializedViewDiff `json:"materialized_views_changed,omitempty"`
	SequencesAdded   []string       `json:"sequences_added,omitempty"`
	SequencesRemoved []string       `json:"sequences_removed,omitempty"`
	SequencesChanged []SequenceDiff `json:"sequences_changed,omitempty"`
}

// TableDiff describes the differences within a single table.
type TableDiff struct {
	Name           string                   `json:"name"`
	ColumnsAdded   []model.Column           `json:"columns_added"`
	ColumnsRemoved []string                 `json:"columns_removed"`
	ColumnsChanged []ColumnChange           `json:"columns_changed"`
	FKsAdded       []model.FK               `json:"fks_added"`
	FKsRemoved     []string                 `json:"fks_removed"`
	FKsChanged     []FKChange               `json:"fks_changed"`
	IndexesAdded   []model.Index            `json:"indexes_added"`
	IndexesRemoved []string                 `json:"indexes_removed"`
	IndexesChanged []IndexChange            `json:"indexes_changed"`
	UniquesAdded   []model.UniqueConstraint `json:"uniques_added"`
	UniquesRemoved []string                 `json:"uniques_removed"`
	ChecksAdded    []model.CheckConstraint  `json:"checks_added"`
	ChecksRemoved     []string                    `json:"checks_removed"`
	ExclusionsAdded   []model.ExclusionConstraint `json:"exclusions_added"`
	ExclusionsRemoved []string                    `json:"exclusions_removed"`
	CommentChanged      *[2]string               `json:"comment_changed"`                // [old, new]
	PKChanged           *[2][]string             `json:"pk_changed"`                     // [old, new]
	OwnerChanged        *[2]string               `json:"owner_changed"`
	PartitioningChanged *PartitionDiff           `json:"partitioning_changed,omitempty"`
	AppendOnlyChanged   *[2]bool                 `json:"append_only_changed,omitempty"`
}

// ColumnChange describes a change to a single column, with risk classification.
type ColumnChange struct {
	Name            string              `json:"name"`
	TypeChanged     *[2]string          `json:"type_changed"`     // [old, new]
	NullableChanged *[2]bool            `json:"nullable_changed"` // [old, new]
	DefaultChanged  *[2]string          `json:"default_changed"`  // [old, new]
	CommentChanged   *[2]string          `json:"comment_changed"`   // [old, new]
	GeneratedChanged *[2]string          `json:"generated_changed,omitempty"` // [old, new]
	StoredChanged    *[2]bool            `json:"stored_changed,omitempty"`    // [old, new]
	IdentityChanged  *[2]string          `json:"identity_changed,omitempty"`  // [old, new]
	ArrayChanged      *[2]bool            `json:"array_changed,omitempty"`
	CollationChanged  *[2]string          `json:"collation_changed,omitempty"`
	JSONSchemaChanged *[2]string          `json:"json_schema_changed,omitempty"`
	StatisticsChanged *[2]*int            `json:"statistics_changed,omitempty"`
	Risk              risk.Classification `json:"risk"`
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

// PartitionDiff describes changes to a table's partitioning configuration.
type PartitionDiff struct {
	StrategyChanged *[2]string `json:"strategy_changed,omitempty"`
	KeyChanged      *[2]string `json:"key_changed,omitempty"`
	ChildrenAdded   []string   `json:"children_added,omitempty"`
	ChildrenRemoved []string   `json:"children_removed,omitempty"`
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
		len(d.SequencesChanged) == 0
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
		// Count total column changes across all changed tables.
		totalCols := 0
		for _, td := range d.TablesChanged {
			totalCols += len(td.ColumnsAdded) + len(td.ColumnsRemoved) + len(td.ColumnsChanged)
		}
		s := fmt.Sprintf("%d table(s) changed", n)
		if totalCols > 0 {
			s += fmt.Sprintf(" (%d column(s) modified)", totalCols)
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

	return strings.Join(parts, ", ")
}

// Diff compares desired (from TOML) against actual (from introspection) and
// returns a structured diff. Items in desired but not in actual are "added";
// items in actual but not in desired are "removed".
func Diff(desired, actual *model.Schema) *SchemaDiff {
	d := &SchemaDiff{}

	diffTables(d, desired, actual)
	diffEnums(d, desired, actual)
	diffExtensions(d, desired, actual)
	diffViews(d, desired, actual)
	diffMaterializedViews(d, desired, actual)
	diffSequences(d, desired, actual)

	return d
}

// diffTables matches tables by schema-qualified name and diffs matched pairs.
func diffTables(d *SchemaDiff, desired, actual *model.Schema) {
	added, removed, matched := matchObjects(desired.Tables, actual.Tables, func(t model.Table) string {
		return tableKey(&t)
	})
	for _, t := range added {
		d.TablesAdded = append(d.TablesAdded, tableKey(&t))
	}
	for _, t := range removed {
		d.TablesRemoved = append(d.TablesRemoved, tableKey(&t))
	}
	for _, p := range matched {
		td := diffTable(&p.Desired, &p.Actual)
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
func diffTable(desired, actual *model.Table) TableDiff {
	td := TableDiff{Name: tableKey(desired)}

	diffColumns(&td, desired, actual)
	diffFKs(&td, desired, actual)
	diffIndexes(&td, desired, actual)
	diffUniques(&td, desired, actual)
	diffChecks(&td, desired, actual)
	diffExclusions(&td, desired, actual)

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

	// AppendOnly
	if desired.AppendOnly != actual.AppendOnly {
		td.AppendOnlyChanged = &[2]bool{actual.AppendOnly, desired.AppendOnly}
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
		td.CommentChanged == nil &&
		td.PKChanged == nil &&
		td.OwnerChanged == nil &&
		td.PartitioningChanged == nil &&
		td.AppendOnlyChanged == nil
}

// diffColumns matches columns by name and classifies changes with risk.
func diffColumns(td *TableDiff, desired, actual *model.Table) {
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
		cc := diffColumn(&p.Desired, &p.Actual)
		if cc != nil {
			td.ColumnsChanged = append(td.ColumnsChanged, *cc)
		}
	}
}

// diffColumn compares two matched columns and returns nil if identical.
func diffColumn(desired, actual *model.Column) *ColumnChange {
	cc := ColumnChange{Name: desired.Name}
	changed := false

	// Type comparison (case-insensitive, normalized).
	dt := normalizeType(desired.PGType)
	at := normalizeType(actual.PGType)
	if dt != at {
		cc.TypeChanged = &[2]string{actual.PGType, desired.PGType}
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

	// Generated comparison.
	if desired.Generated != actual.Generated {
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
func IsWidening(oldType, newType string) bool {
	old := strings.ToLower(strings.TrimSpace(oldType))
	new_ := strings.ToLower(strings.TrimSpace(newType))

	// int -> bigint
	if (old == "integer" || old == "int" || old == "int4") &&
		(new_ == "bigint" || new_ == "int8") {
		return true
	}

	// smallint -> integer or bigint
	if (old == "smallint" || old == "int2") &&
		(new_ == "integer" || new_ == "int" || new_ == "int4" || new_ == "bigint" || new_ == "int8") {
		return true
	}

	// varchar -> text
	if strings.HasPrefix(old, "character varying") || strings.HasPrefix(old, "varchar") {
		if new_ == "text" {
			return true
		}
	}

	// varchar(N) -> varchar(M) where M > N
	oldLen := extractVarcharLen(old)
	newLen := extractVarcharLen(new_)
	if oldLen > 0 && newLen > 0 && newLen > oldLen {
		return true
	}

	// real -> double precision
	if (old == "real" || old == "float4") &&
		(new_ == "double precision" || new_ == "float8") {
		return true
	}

	return false
}

var varcharLenRe = regexp.MustCompile(`(?:varchar|character varying)\s*\(\s*(\d+)\s*\)`)

// extractVarcharLen extracts the length from varchar(N) or character varying(N).
func extractVarcharLen(t string) int {
	m := varcharLenRe.FindStringSubmatch(t)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// normalizeType lowercases and trims whitespace for comparison.
func normalizeType(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// normalizeDefault returns a normalized default value for comparison.
// DefaultExpr takes precedence over Default (literal).
func normalizeDefault(literal *string, expr string) string {
	if expr != "" {
		return strings.ToLower(strings.TrimSpace(expr))
	}
	if literal != nil {
		return strings.ToLower(strings.TrimSpace(*literal))
	}
	return ""
}

// diffFKs matches foreign keys by name.
func diffFKs(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjects(desired.FKs, actual.FKs, func(fk model.FK) string {
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
	added, removed, matched := matchObjects(desired.Indexes, actual.Indexes, func(idx model.Index) string {
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

func indexEqual(a, b *model.Index) bool {
	return a.Name == b.Name &&
		sliceEqual(a.Columns, b.Columns) &&
		boolSliceEqual(a.Desc, b.Desc) &&
		a.Method == b.Method &&
		mapEqual(a.Opclasses, b.Opclasses) &&
		mapEqual(a.Collations, b.Collations) &&
		a.Where == b.Where &&
		sliceEqual(a.Include, b.Include) &&
		mapEqual(a.With, b.With)
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
	added, removed, matched := matchObjects(desired.Uniques, actual.Uniques, func(u model.UniqueConstraint) string {
		return u.Name
	})
	for _, u := range added {
		td.UniquesAdded = append(td.UniquesAdded, u)
	}
	for _, u := range removed {
		td.UniquesRemoved = append(td.UniquesRemoved, u.Name)
	}
	for _, p := range matched {
		if !sliceEqual(p.Desired.Columns, p.Actual.Columns) {
			td.UniquesRemoved = append(td.UniquesRemoved, p.Actual.Name)
			td.UniquesAdded = append(td.UniquesAdded, p.Desired)
		}
	}
}

// diffChecks matches check constraints by name.
func diffChecks(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjects(desired.Checks, actual.Checks, func(c model.CheckConstraint) string {
		return c.Name
	})
	for _, c := range added {
		td.ChecksAdded = append(td.ChecksAdded, c)
	}
	for _, c := range removed {
		td.ChecksRemoved = append(td.ChecksRemoved, c.Name)
	}
	for _, p := range matched {
		if p.Desired.Expr != p.Actual.Expr {
			td.ChecksRemoved = append(td.ChecksRemoved, p.Actual.Name)
			td.ChecksAdded = append(td.ChecksAdded, p.Desired)
		}
	}
}

// diffExclusions matches exclusion constraints by name.
func diffExclusions(td *TableDiff, desired, actual *model.Table) {
	added, removed, matched := matchObjects(desired.Exclusions, actual.Exclusions, func(e model.ExclusionConstraint) string {
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
	if a.Method != b.Method || a.Where != b.Where || a.Deferrable != b.Deferrable || a.InitiallyDeferred != b.InitiallyDeferred {
		return false
	}
	if len(a.Elements) != len(b.Elements) {
		return false
	}
	for i := range a.Elements {
		if a.Elements[i].Column != b.Elements[i].Column || a.Elements[i].Operator != b.Elements[i].Operator {
			return false
		}
	}
	return true
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
	idxAdded, idxRemoved, idxMatched := matchObjects(desired.Indexes, actual.Indexes, func(idx model.Index) string {
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
