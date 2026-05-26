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
	ChecksRemoved  []string                 `json:"checks_removed"`
	CommentChanged *[2]string               `json:"comment_changed"` // [old, new]
	PKChanged      *[2][]string             `json:"pk_changed"`      // [old, new]
	OwnerChanged   *[2]string               `json:"owner_changed"`
}

// ColumnChange describes a change to a single column, with risk classification.
type ColumnChange struct {
	Name            string              `json:"name"`
	TypeChanged     *[2]string          `json:"type_changed"`     // [old, new]
	NullableChanged *[2]bool            `json:"nullable_changed"` // [old, new]
	DefaultChanged  *[2]string          `json:"default_changed"`  // [old, new]
	CommentChanged  *[2]string          `json:"comment_changed"`  // [old, new]
	Risk            risk.Classification `json:"risk"`
}

// EnumDiff describes changes to an enum type.
type EnumDiff struct {
	Name          string   `json:"name"`
	ValuesAdded   []string `json:"values_added"`
	ValuesRemoved []string `json:"values_removed"`
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

// IsEmpty returns true if the diff contains no changes.
func (d *SchemaDiff) IsEmpty() bool {
	return len(d.TablesAdded) == 0 &&
		len(d.TablesRemoved) == 0 &&
		len(d.TablesChanged) == 0 &&
		len(d.EnumsAdded) == 0 &&
		len(d.EnumsRemoved) == 0 &&
		len(d.EnumsChanged) == 0 &&
		len(d.ExtensionsAdded) == 0 &&
		len(d.ExtensionsRemoved) == 0
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

	return d
}

// diffTables matches tables by schema-qualified name and diffs matched pairs.
func diffTables(d *SchemaDiff, desired, actual *model.Schema) {
	actualByKey := make(map[string]*model.Table, len(actual.Tables))
	for i := range actual.Tables {
		t := &actual.Tables[i]
		actualByKey[tableKey(t)] = t
	}

	desiredKeys := make(map[string]bool, len(desired.Tables))
	for i := range desired.Tables {
		dt := &desired.Tables[i]
		key := tableKey(dt)
		desiredKeys[key] = true

		at, found := actualByKey[key]
		if !found {
			d.TablesAdded = append(d.TablesAdded, key)
			continue
		}

		td := diffTable(dt, at)
		if !isTableDiffEmpty(&td) {
			d.TablesChanged = append(d.TablesChanged, td)
		}
	}

	for _, at := range actual.Tables {
		key := tableKey(&at)
		if !desiredKeys[key] {
			d.TablesRemoved = append(d.TablesRemoved, key)
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
		td.CommentChanged == nil &&
		td.PKChanged == nil &&
		td.OwnerChanged == nil
}

// diffColumns matches columns by name and classifies changes with risk.
func diffColumns(td *TableDiff, desired, actual *model.Table) {
	actualByName := make(map[string]*model.Column, len(actual.Columns))
	for i := range actual.Columns {
		actualByName[actual.Columns[i].Name] = &actual.Columns[i]
	}

	desiredNames := make(map[string]bool, len(desired.Columns))
	for _, dc := range desired.Columns {
		desiredNames[dc.Name] = true

		ac, found := actualByName[dc.Name]
		if !found {
			td.ColumnsAdded = append(td.ColumnsAdded, dc)
			continue
		}

		cc := diffColumn(&dc, ac)
		if cc != nil {
			td.ColumnsChanged = append(td.ColumnsChanged, *cc)
		}
	}

	for _, ac := range actual.Columns {
		if !desiredNames[ac.Name] {
			td.ColumnsRemoved = append(td.ColumnsRemoved, ac.Name)
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
		widening := isWidening(cc.TypeChanged[0], cc.TypeChanged[1])
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

	return highest
}

// isWidening returns true if oldType -> newType is a safe widening conversion.
func isWidening(oldType, newType string) bool {
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
func normalizeDefault(literal, expr string) string {
	if expr != "" {
		return strings.ToLower(strings.TrimSpace(expr))
	}
	return strings.ToLower(strings.TrimSpace(literal))
}

// diffFKs matches foreign keys by name.
func diffFKs(td *TableDiff, desired, actual *model.Table) {
	actualByName := make(map[string]*model.FK, len(actual.FKs))
	for i := range actual.FKs {
		actualByName[actual.FKs[i].Name] = &actual.FKs[i]
	}

	desiredNames := make(map[string]bool, len(desired.FKs))
	for _, dfk := range desired.FKs {
		desiredNames[dfk.Name] = true

		afk, found := actualByName[dfk.Name]
		if !found {
			td.FKsAdded = append(td.FKsAdded, dfk)
			continue
		}

		if !fkEqual(&dfk, afk) {
			td.FKsChanged = append(td.FKsChanged, FKChange{
				Name: dfk.Name,
				Old:  *afk,
				New:  dfk,
			})
		}
	}

	for _, afk := range actual.FKs {
		if !desiredNames[afk.Name] {
			td.FKsRemoved = append(td.FKsRemoved, afk.Name)
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
	actualByName := make(map[string]*model.Index, len(actual.Indexes))
	for i := range actual.Indexes {
		actualByName[actual.Indexes[i].Name] = &actual.Indexes[i]
	}

	desiredNames := make(map[string]bool, len(desired.Indexes))
	for _, didx := range desired.Indexes {
		desiredNames[didx.Name] = true

		aidx, found := actualByName[didx.Name]
		if !found {
			td.IndexesAdded = append(td.IndexesAdded, didx)
			continue
		}

		if !indexEqual(&didx, aidx) {
			td.IndexesChanged = append(td.IndexesChanged, IndexChange{
				Name: didx.Name,
				Old:  *aidx,
				New:  didx,
			})
		}
	}

	for _, aidx := range actual.Indexes {
		if !desiredNames[aidx.Name] {
			td.IndexesRemoved = append(td.IndexesRemoved, aidx.Name)
		}
	}
}

func indexEqual(a, b *model.Index) bool {
	return a.Name == b.Name &&
		sliceEqual(a.Columns, b.Columns) &&
		a.Method == b.Method &&
		a.Opclass == b.Opclass &&
		a.Where == b.Where &&
		sliceEqual(a.Include, b.Include)
}

// diffUniques matches unique constraints by name.
func diffUniques(td *TableDiff, desired, actual *model.Table) {
	actualByName := make(map[string]*model.UniqueConstraint, len(actual.Uniques))
	for i := range actual.Uniques {
		actualByName[actual.Uniques[i].Name] = &actual.Uniques[i]
	}

	desiredNames := make(map[string]bool, len(desired.Uniques))
	for _, du := range desired.Uniques {
		desiredNames[du.Name] = true

		au, found := actualByName[du.Name]
		if !found {
			td.UniquesAdded = append(td.UniquesAdded, du)
			continue
		}

		if !sliceEqual(du.Columns, au.Columns) {
			// Unique constraint columns changed: remove old, add new.
			td.UniquesRemoved = append(td.UniquesRemoved, au.Name)
			td.UniquesAdded = append(td.UniquesAdded, du)
		}
	}

	for _, au := range actual.Uniques {
		if !desiredNames[au.Name] {
			td.UniquesRemoved = append(td.UniquesRemoved, au.Name)
		}
	}
}

// diffChecks matches check constraints by name.
func diffChecks(td *TableDiff, desired, actual *model.Table) {
	actualByName := make(map[string]*model.CheckConstraint, len(actual.Checks))
	for i := range actual.Checks {
		actualByName[actual.Checks[i].Name] = &actual.Checks[i]
	}

	desiredNames := make(map[string]bool, len(desired.Checks))
	for _, dc := range desired.Checks {
		desiredNames[dc.Name] = true

		ac, found := actualByName[dc.Name]
		if !found {
			td.ChecksAdded = append(td.ChecksAdded, dc)
			continue
		}

		if dc.Expr != ac.Expr {
			// Check expression changed: remove old, add new.
			td.ChecksRemoved = append(td.ChecksRemoved, ac.Name)
			td.ChecksAdded = append(td.ChecksAdded, dc)
		}
	}

	for _, ac := range actual.Checks {
		if !desiredNames[ac.Name] {
			td.ChecksRemoved = append(td.ChecksRemoved, ac.Name)
		}
	}
}

// diffEnums matches enums by schema-qualified name.
func diffEnums(d *SchemaDiff, desired, actual *model.Schema) {
	actualByKey := make(map[string]*model.Enum, len(actual.Enums))
	for i := range actual.Enums {
		e := &actual.Enums[i]
		actualByKey[enumKey(e)] = e
	}

	desiredKeys := make(map[string]bool, len(desired.Enums))
	for i := range desired.Enums {
		de := &desired.Enums[i]
		key := enumKey(de)
		desiredKeys[key] = true

		ae, found := actualByKey[key]
		if !found {
			d.EnumsAdded = append(d.EnumsAdded, key)
			continue
		}

		ed := diffEnum(de, ae)
		if ed != nil {
			d.EnumsChanged = append(d.EnumsChanged, *ed)
		}
	}

	for _, ae := range actual.Enums {
		key := enumKey(&ae)
		if !desiredKeys[key] {
			d.EnumsRemoved = append(d.EnumsRemoved, key)
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
func diffEnum(desired, actual *model.Enum) *EnumDiff {
	added := stringDiff(desired.Values, actual.Values)
	removed := stringDiff(actual.Values, desired.Values)

	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	return &EnumDiff{
		Name:          enumKey(desired),
		ValuesAdded:   added,
		ValuesRemoved: removed,
	}
}

// diffExtensions compares extension lists.
func diffExtensions(d *SchemaDiff, desired, actual *model.Schema) {
	d.ExtensionsAdded = stringDiff(desired.Extensions, actual.Extensions)
	d.ExtensionsRemoved = stringDiff(actual.Extensions, desired.Extensions)
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
