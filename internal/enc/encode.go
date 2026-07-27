package enc

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// canonicalJSON marshals v to compact, deterministic JSON with HTML escaping
// disabled (so SQL expressions keep their <, >, & verbatim) and the trailing
// newline the streaming encoder appends stripped. encoding/json emits struct
// fields in declaration order and map keys in sorted order, which is the whole
// determinism story for the form types.
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// sortedCopy returns a sorted copy of a set-valued leaf slice. The copy keeps
// the encoder from mutating the caller's model.
func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// typeinfo.Type
// ---------------------------------------------------------------------------

type paramsForm struct {
	Precision   *int   `json:"precision,omitempty"`
	Scale       *int   `json:"scale,omitempty"`
	Length      *int   `json:"length,omitempty"`
	RawModifier string `json:"raw_modifier,omitempty"`
}

type typeForm struct {
	Base       string     `json:"base"`
	DomainName string     `json:"domain_name,omitempty"`
	Params     paramsForm `json:"params,omitempty"`
}

func typeToForm(t typeinfo.Type) typeForm {
	return typeForm{
		Base:       t.Base,
		DomainName: t.DomainName,
		Params: paramsForm{
			Precision:   t.Params.Precision,
			Scale:       t.Params.Scale,
			Length:      t.Params.Length,
			RawModifier: t.Params.RawModifier,
		},
	}
}

func typeFromForm(f typeForm) typeinfo.Type {
	return typeinfo.Type{
		Base:       f.Base,
		DomainName: f.DomainName,
		Params: typeinfo.Params{
			Precision:   f.Params.Precision,
			Scale:       f.Params.Scale,
			Length:      f.Params.Length,
			RawModifier: f.Params.RawModifier,
		},
	}
}

// ---------------------------------------------------------------------------
// Column
// ---------------------------------------------------------------------------

type columnForm struct {
	Name             string   `json:"name"`
	PGType           typeForm `json:"pg_type"`
	Collation        string   `json:"collation,omitempty"`
	NotNull          bool     `json:"not_null"`
	Default          *string  `json:"default,omitempty"`
	DefaultExpr      string   `json:"default_expr,omitempty"`
	Generated        string   `json:"generated,omitempty"`
	Stored           bool     `json:"stored,omitempty"`
	Identity         string   `json:"identity,omitempty"`
	Comment          string   `json:"comment,omitempty"`
	SemanticTypeName string   `json:"semantic_type_name,omitempty"`
	Array            bool     `json:"array,omitempty"`
	JSONSchema       string   `json:"json_schema,omitempty"`
	Statistics       *int     `json:"statistics,omitempty"`
	TypeKind         string   `json:"type_kind,omitempty"`
}

func columnToForm(c model.Column) columnForm {
	return columnForm{
		Name:             c.Name,
		PGType:           typeToForm(c.PGType),
		Collation:        c.Collation,
		NotNull:          c.NotNull,
		Default:          c.Default,
		DefaultExpr:      c.DefaultExpr,
		Generated:        c.Generated,
		Stored:           c.Stored,
		Identity:         c.Identity,
		Comment:          c.Comment,
		SemanticTypeName: c.SemanticTypeName,
		Array:            c.Array,
		JSONSchema:       c.JSONSchema,
		Statistics:       c.Statistics,
		TypeKind:         c.TypeKind,
	}
}

func columnFromForm(f columnForm) model.Column {
	return model.Column{
		Name:             f.Name,
		PGType:           typeFromForm(f.PGType),
		Collation:        f.Collation,
		NotNull:          f.NotNull,
		Default:          f.Default,
		DefaultExpr:      f.DefaultExpr,
		Generated:        f.Generated,
		Stored:           f.Stored,
		Identity:         f.Identity,
		Comment:          f.Comment,
		SemanticTypeName: f.SemanticTypeName,
		Array:            f.Array,
		JSONSchema:       f.JSONSchema,
		Statistics:       f.Statistics,
		TypeKind:         f.TypeKind,
	}
}

// ---------------------------------------------------------------------------
// FK
// ---------------------------------------------------------------------------

type fkForm struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema,omitempty"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
	OnDelete   string   `json:"on_delete"`
}

func fkToForm(k model.FK) fkForm {
	return fkForm{
		Name:       k.Name,
		Columns:    k.Columns,
		RefSchema:  k.RefSchema,
		RefTable:   k.RefTable,
		RefColumns: k.RefColumns,
		OnDelete:   k.OnDelete,
	}
}

func fkFromForm(f fkForm) model.FK {
	return model.FK{
		Name:       f.Name,
		Columns:    f.Columns,
		RefSchema:  f.RefSchema,
		RefTable:   f.RefTable,
		RefColumns: f.RefColumns,
		OnDelete:   f.OnDelete,
	}
}

// ---------------------------------------------------------------------------
// Index
// ---------------------------------------------------------------------------

type indexForm struct {
	Name       string            `json:"name"`
	Columns    []string          `json:"columns"`
	Desc       []bool            `json:"desc,omitempty"`
	Method     string            `json:"method,omitempty"`
	Opclasses  map[string]string `json:"opclasses,omitempty"`
	Collations map[string]string `json:"collations,omitempty"`
	Where      string            `json:"where,omitempty"`
	Include    []string          `json:"include,omitempty"`
	With       map[string]string `json:"with,omitempty"`
	Unique     bool              `json:"unique"`
}

func indexToForm(i model.Index) indexForm {
	return indexForm{
		Name:       i.Name,
		Columns:    i.Columns,
		Desc:       i.Desc,
		Method:     i.Method,
		Opclasses:  i.Opclasses,
		Collations: i.Collations,
		Where:      i.Where,
		Include:    sortedCopy(i.Include), // INCLUDE columns are a set, not positional
		With:       i.With,
		Unique:     i.Unique,
	}
}

func indexFromForm(f indexForm) model.Index {
	return model.Index{
		Name:       f.Name,
		Columns:    f.Columns,
		Desc:       f.Desc,
		Method:     f.Method,
		Opclasses:  f.Opclasses,
		Collations: f.Collations,
		Where:      f.Where,
		Include:    f.Include,
		With:       f.With,
		Unique:     f.Unique,
	}
}

// ---------------------------------------------------------------------------
// UniqueConstraint / CheckConstraint
// ---------------------------------------------------------------------------

type uniqueForm struct {
	Name              string   `json:"name"`
	Columns           []string `json:"columns"`
	Deferrable        bool     `json:"deferrable,omitempty"`
	InitiallyDeferred bool     `json:"initially_deferred,omitempty"`
}

func uniqueToForm(u model.UniqueConstraint) uniqueForm {
	return uniqueForm{Name: u.Name, Columns: u.Columns, Deferrable: u.Deferrable, InitiallyDeferred: u.InitiallyDeferred}
}

func uniqueFromForm(f uniqueForm) model.UniqueConstraint {
	return model.UniqueConstraint{Name: f.Name, Columns: f.Columns, Deferrable: f.Deferrable, InitiallyDeferred: f.InitiallyDeferred}
}

type checkForm struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

func checkToForm(c model.CheckConstraint) checkForm { return checkForm{Name: c.Name, Expr: c.Expr} }
func checkFromForm(f checkForm) model.CheckConstraint {
	return model.CheckConstraint{Name: f.Name, Expr: f.Expr}
}

// ---------------------------------------------------------------------------
// Exclusion constraint
// ---------------------------------------------------------------------------

type exclusionElementForm struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
}

type exclusionForm struct {
	Name              string                 `json:"name"`
	Method            string                 `json:"method"`
	Elements          []exclusionElementForm `json:"elements"`
	Where             string                 `json:"where,omitempty"`
	Deferrable        bool                   `json:"deferrable,omitempty"`
	InitiallyDeferred bool                   `json:"initially_deferred,omitempty"`
}

func exclusionToForm(e model.ExclusionConstraint) exclusionForm {
	// Elements are intra-object SEMANTIC (they order the backing index's
	// columns), so they are preserved, never sorted.
	els := make([]exclusionElementForm, len(e.Elements))
	for i, el := range e.Elements {
		els[i] = exclusionElementForm{Column: el.Column, Operator: el.Operator}
	}
	return exclusionForm{
		Name:              e.Name,
		Method:            e.Method,
		Elements:          els,
		Where:             e.Where,
		Deferrable:        e.Deferrable,
		InitiallyDeferred: e.InitiallyDeferred,
	}
}

func exclusionFromForm(f exclusionForm) model.ExclusionConstraint {
	els := make([]model.ExclusionElement, len(f.Elements))
	for i, el := range f.Elements {
		els[i] = model.ExclusionElement{Column: el.Column, Operator: el.Operator}
	}
	return model.ExclusionConstraint{
		Name:              f.Name,
		Method:            f.Method,
		Elements:          els,
		Where:             f.Where,
		Deferrable:        f.Deferrable,
		InitiallyDeferred: f.InitiallyDeferred,
	}
}

// ---------------------------------------------------------------------------
// Policy / Trigger
// ---------------------------------------------------------------------------

type policyForm struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	Operation    string `json:"operation"`
	Role         string `json:"role,omitempty"`
	Using        string `json:"using,omitempty"`
	WithCheck    string `json:"with_check,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func policyToForm(p model.Policy) policyForm {
	return policyForm{
		Name: p.Name, Type: p.Type, Operation: p.Operation, Role: p.Role,
		Using: p.Using, WithCheck: p.WithCheck, ErrorCode: p.ErrorCode, ErrorMessage: p.ErrorMessage,
	}
}

func policyFromForm(f policyForm) model.Policy {
	return model.Policy{
		Name: f.Name, Type: f.Type, Operation: f.Operation, Role: f.Role,
		Using: f.Using, WithCheck: f.WithCheck, ErrorCode: f.ErrorCode, ErrorMessage: f.ErrorMessage,
	}
}

type triggerForm struct {
	Name              string   `json:"name"`
	Function          string   `json:"function"`
	Events            []string `json:"events"`
	Timing            string   `json:"timing"`
	ForEach           string   `json:"for_each"`
	When              string   `json:"when,omitempty"`
	Constraint        bool     `json:"constraint,omitempty"`
	Deferrable        bool     `json:"deferrable,omitempty"`
	InitiallyDeferred bool     `json:"initially_deferred,omitempty"`
	ReferencingOld    string   `json:"referencing_old,omitempty"`
	ReferencingNew    string   `json:"referencing_new,omitempty"`
	Comment           string   `json:"comment,omitempty"`
}

func triggerToForm(t model.Trigger) triggerForm {
	return triggerForm{
		Name:              t.Name,
		Function:          t.Function,
		Events:            sortedCopy(t.Events), // events are a SET; PG fires them regardless of DDL order
		Timing:            t.Timing,
		ForEach:           t.ForEach,
		When:              t.When,
		Constraint:        t.Constraint,
		Deferrable:        t.Deferrable,
		InitiallyDeferred: t.InitiallyDeferred,
		ReferencingOld:    t.ReferencingOld,
		ReferencingNew:    t.ReferencingNew,
		Comment:           t.Comment,
	}
}

func triggerFromForm(f triggerForm) model.Trigger {
	return model.Trigger{
		Name:              f.Name,
		Function:          f.Function,
		Events:            f.Events,
		Timing:            f.Timing,
		ForEach:           f.ForEach,
		When:              f.When,
		Constraint:        f.Constraint,
		Deferrable:        f.Deferrable,
		InitiallyDeferred: f.InitiallyDeferred,
		ReferencingOld:    f.ReferencingOld,
		ReferencingNew:    f.ReferencingNew,
		Comment:           f.Comment,
	}
}

// ---------------------------------------------------------------------------
// Functional dependency
// ---------------------------------------------------------------------------

type funcDepForm struct {
	Determinant []string `json:"determinant"`
	Dependent   []string `json:"dependent"`
}

func funcDepToForm(d fd.FuncDep) funcDepForm {
	// Determinant and dependent are column SETS; sort for canonicality. Source
	// is excluded (provenance, not identity).
	return funcDepForm{Determinant: sortedCopy(d.Determinant), Dependent: sortedCopy(d.Dependent)}
}

func funcDepFromForm(f funcDepForm) fd.FuncDep {
	return fd.FuncDep{Determinant: f.Determinant, Dependent: f.Dependent}
}

// ---------------------------------------------------------------------------
// Partitioning / Maintenance
// ---------------------------------------------------------------------------

type partitionForm struct {
	Strategy string          `json:"strategy"`
	Columns  []string        `json:"columns"`
	Name     string          `json:"name,omitempty"`
	Bound    string          `json:"bound,omitempty"`
	Children []partitionForm `json:"children"`
}

func partitionToForm(p *model.PartitionSpec) *partitionForm {
	if p == nil {
		return nil
	}
	children := make([]partitionForm, len(p.Children))
	for i := range p.Children {
		children[i] = *partitionToForm(&p.Children[i])
	}
	return &partitionForm{
		Strategy: p.Strategy,
		Columns:  p.Columns, // partition key columns are SEMANTIC (positional)
		Name:     p.Name,
		Bound:    p.Bound,
		Children: children,
	}
}

func partitionFromForm(f *partitionForm) *model.PartitionSpec {
	if f == nil {
		return nil
	}
	children := make([]model.PartitionSpec, len(f.Children))
	for i := range f.Children {
		children[i] = *partitionFromForm(&f.Children[i])
	}
	return &model.PartitionSpec{
		Strategy: f.Strategy,
		Columns:  f.Columns,
		Name:     f.Name,
		Bound:    f.Bound,
		Children: children,
	}
}

type maintenanceForm struct {
	Interval           string `json:"interval"`
	Premake            int    `json:"premake"`
	Retention          string `json:"retention"`
	RetentionKeepTable bool   `json:"retention_keep_table"`
	Schedule           string `json:"schedule,omitempty"`
}

func maintenanceToForm(m *model.MaintenanceConfig) *maintenanceForm {
	if m == nil {
		return nil
	}
	return &maintenanceForm{
		Interval:           m.Interval,
		Premake:            m.Premake,
		Retention:          m.Retention,
		RetentionKeepTable: m.RetentionKeepTable,
		Schedule:           m.Schedule,
	}
}

func maintenanceFromForm(f *maintenanceForm) *model.MaintenanceConfig {
	if f == nil {
		return nil
	}
	return &model.MaintenanceConfig{
		Interval:           f.Interval,
		Premake:            f.Premake,
		Retention:          f.Retention,
		RetentionKeepTable: f.RetentionKeepTable,
		Schedule:           f.Schedule,
	}
}

// ---------------------------------------------------------------------------
// Table (top-level form)
// ---------------------------------------------------------------------------

type tableForm struct {
	Codec        int              `json:"codec"`
	Kind         Kind             `json:"kind"`
	Name         string           `json:"name"`
	Schema       string           `json:"schema"`
	Comment      string           `json:"comment"`
	Columns      []columnForm     `json:"columns"`
	PK           []string         `json:"pk"`
	FKs          []fkForm         `json:"fks,omitempty"`
	Indexes      []indexForm      `json:"indexes,omitempty"`
	Uniques      []uniqueForm     `json:"uniques,omitempty"`
	Checks       []checkForm      `json:"checks,omitempty"`
	Exclusions   []exclusionForm  `json:"exclusions,omitempty"`
	Partitioning *partitionForm   `json:"partitioning,omitempty"`
	Dependencies []funcDepForm    `json:"dependencies,omitempty"`
	Maintenance  *maintenanceForm `json:"maintenance,omitempty"`
	Owner        string           `json:"owner,omitempty"`
	Policies     []policyForm     `json:"policies,omitempty"`
	Triggers     []triggerForm    `json:"triggers,omitempty"`
	EnableRLS    bool             `json:"enable_rls,omitempty"`
	ForceRLS     bool             `json:"force_rls,omitempty"`
	AppendOnly   bool             `json:"append_only,omitempty"`
}

func tableToForm(t model.Table) tableForm {
	cols := make([]columnForm, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = columnToForm(c)
	}
	fks := make([]fkForm, len(t.FKs))
	for i, k := range t.FKs {
		fks[i] = fkToForm(k)
	}
	idxs := make([]indexForm, len(t.Indexes))
	for i, x := range t.Indexes {
		idxs[i] = indexToForm(x)
	}
	uniqs := make([]uniqueForm, len(t.Uniques))
	for i, u := range t.Uniques {
		uniqs[i] = uniqueToForm(u)
	}
	checks := make([]checkForm, len(t.Checks))
	for i, c := range t.Checks {
		checks[i] = checkToForm(c)
	}
	excls := make([]exclusionForm, len(t.Exclusions))
	for i, e := range t.Exclusions {
		excls[i] = exclusionToForm(e)
	}
	deps := make([]funcDepForm, len(t.Dependencies))
	for i, d := range t.Dependencies {
		deps[i] = funcDepToForm(d)
	}
	pols := make([]policyForm, len(t.Policies))
	for i, p := range t.Policies {
		pols[i] = policyToForm(p)
	}
	trigs := make([]triggerForm, len(t.Triggers))
	for i, tr := range t.Triggers {
		trigs[i] = triggerToForm(tr)
	}
	return tableForm{
		Codec:        CodecVersion,
		Kind:         KindTable,
		Name:         t.Name,
		Schema:       t.Schema,
		Comment:      t.Comment,
		Columns:      cols,
		PK:           t.PK,
		FKs:          fks,
		Indexes:      idxs,
		Uniques:      uniqs,
		Checks:       checks,
		Exclusions:   excls,
		Partitioning: partitionToForm(t.Partitioning),
		Dependencies: deps,
		Maintenance:  maintenanceToForm(t.Maintenance),
		Owner:        t.Owner,
		Policies:     pols,
		Triggers:     trigs,
		EnableRLS:    t.EnableRLS,
		ForceRLS:     t.ForceRLS,
		AppendOnly:   t.AppendOnly,
	}
}

func tableFromForm(f tableForm) model.Table {
	cols := make([]model.Column, len(f.Columns))
	for i, c := range f.Columns {
		cols[i] = columnFromForm(c)
	}
	fks := make([]model.FK, len(f.FKs))
	for i, k := range f.FKs {
		fks[i] = fkFromForm(k)
	}
	idxs := make([]model.Index, len(f.Indexes))
	for i, x := range f.Indexes {
		idxs[i] = indexFromForm(x)
	}
	uniqs := make([]model.UniqueConstraint, len(f.Uniques))
	for i, u := range f.Uniques {
		uniqs[i] = uniqueFromForm(u)
	}
	checks := make([]model.CheckConstraint, len(f.Checks))
	for i, c := range f.Checks {
		checks[i] = checkFromForm(c)
	}
	excls := make([]model.ExclusionConstraint, len(f.Exclusions))
	for i, e := range f.Exclusions {
		excls[i] = exclusionFromForm(e)
	}
	deps := make([]fd.FuncDep, len(f.Dependencies))
	for i, d := range f.Dependencies {
		deps[i] = funcDepFromForm(d)
	}
	pols := make([]model.Policy, len(f.Policies))
	for i, p := range f.Policies {
		pols[i] = policyFromForm(p)
	}
	trigs := make([]model.Trigger, len(f.Triggers))
	for i, tr := range f.Triggers {
		trigs[i] = triggerFromForm(tr)
	}
	return model.Table{
		Name:         f.Name,
		Schema:       f.Schema,
		Comment:      f.Comment,
		Columns:      cols,
		PK:           f.PK,
		FKs:          fks,
		Indexes:      idxs,
		Uniques:      uniqs,
		Checks:       checks,
		Exclusions:   excls,
		Partitioning: partitionFromForm(f.Partitioning),
		Dependencies: deps,
		Maintenance:  maintenanceFromForm(f.Maintenance),
		Owner:        f.Owner,
		Policies:     pols,
		Triggers:     trigs,
		EnableRLS:    f.EnableRLS,
		ForceRLS:     f.ForceRLS,
		AppendOnly:   f.AppendOnly,
	}
}

// EncodeTable returns the canonical bytes for a single table.
func EncodeTable(t model.Table) ([]byte, error) { return canonicalJSON(tableToForm(t)) }

// ---------------------------------------------------------------------------
// View / MaterializedView
// ---------------------------------------------------------------------------

type viewForm struct {
	Codec     int      `json:"codec"`
	Kind      Kind     `json:"kind"`
	Name      string   `json:"name"`
	Schema    string   `json:"schema,omitempty"`
	Query     string   `json:"query"`
	Comment   string   `json:"comment,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

func viewToForm(v model.View) viewForm {
	return viewForm{
		Codec: CodecVersion, Kind: KindView,
		Name: v.Name, Schema: v.Schema, Query: v.Query, Comment: v.Comment, DependsOn: v.DependsOn,
	}
}

func viewFromForm(f viewForm) model.View {
	return model.View{Name: f.Name, Schema: f.Schema, Query: f.Query, Comment: f.Comment, DependsOn: f.DependsOn}
}

// EncodeView returns the canonical bytes for a view.
func EncodeView(v model.View) ([]byte, error) { return canonicalJSON(viewToForm(v)) }

type matViewForm struct {
	Codec     int         `json:"codec"`
	Kind      Kind        `json:"kind"`
	Name      string      `json:"name"`
	Schema    string      `json:"schema,omitempty"`
	Query     string      `json:"query"`
	Comment   string      `json:"comment,omitempty"`
	DependsOn []string    `json:"depends_on,omitempty"`
	WithData  bool        `json:"with_data"`
	Indexes   []indexForm `json:"indexes,omitempty"`
}

func matViewToForm(mv model.MaterializedView) matViewForm {
	idxs := make([]indexForm, len(mv.Indexes))
	for i, x := range mv.Indexes {
		idxs[i] = indexToForm(x)
	}
	return matViewForm{
		Codec: CodecVersion, Kind: KindMatView,
		Name: mv.Name, Schema: mv.Schema, Query: mv.Query, Comment: mv.Comment,
		DependsOn: mv.DependsOn, WithData: mv.WithData, Indexes: idxs,
	}
}

func matViewFromForm(f matViewForm) model.MaterializedView {
	idxs := make([]model.Index, len(f.Indexes))
	for i, x := range f.Indexes {
		idxs[i] = indexFromForm(x)
	}
	return model.MaterializedView{
		Name: f.Name, Schema: f.Schema, Query: f.Query, Comment: f.Comment,
		DependsOn: f.DependsOn, WithData: f.WithData, Indexes: idxs,
	}
}

// EncodeMaterializedView returns the canonical bytes for a materialized view.
func EncodeMaterializedView(mv model.MaterializedView) ([]byte, error) {
	return canonicalJSON(matViewToForm(mv))
}

// ---------------------------------------------------------------------------
// Sequence
// ---------------------------------------------------------------------------

type sequenceForm struct {
	Codec     int    `json:"codec"`
	Kind      Kind   `json:"kind"`
	Name      string `json:"name"`
	Schema    string `json:"schema,omitempty"`
	Start     *int64 `json:"start,omitempty"`
	Increment *int64 `json:"increment,omitempty"`
	MinValue  *int64 `json:"min_value,omitempty"`
	MaxValue  *int64 `json:"max_value,omitempty"`
	Cache     *int64 `json:"cache,omitempty"`
	Cycle     bool   `json:"cycle,omitempty"`
	OwnedBy   string `json:"owned_by,omitempty"`
	Comment   string `json:"comment,omitempty"`
}

func sequenceToForm(s model.Sequence) sequenceForm {
	return sequenceForm{
		Codec: CodecVersion, Kind: KindSequence,
		Name: s.Name, Schema: s.Schema, Start: s.Start, Increment: s.Increment,
		MinValue: s.MinValue, MaxValue: s.MaxValue, Cache: s.Cache, Cycle: s.Cycle,
		OwnedBy: s.OwnedBy, Comment: s.Comment,
	}
}

func sequenceFromForm(f sequenceForm) model.Sequence {
	return model.Sequence{
		Name: f.Name, Schema: f.Schema, Start: f.Start, Increment: f.Increment,
		MinValue: f.MinValue, MaxValue: f.MaxValue, Cache: f.Cache, Cycle: f.Cycle,
		OwnedBy: f.OwnedBy, Comment: f.Comment,
	}
}

// EncodeSequence returns the canonical bytes for a standalone sequence.
func EncodeSequence(s model.Sequence) ([]byte, error) { return canonicalJSON(sequenceToForm(s)) }

// ---------------------------------------------------------------------------
// Function
// ---------------------------------------------------------------------------

type funcArgForm struct {
	Name    string   `json:"name"`
	Type    typeForm `json:"type"`
	Default string   `json:"default,omitempty"`
}

type functionForm struct {
	Codec           int           `json:"codec"`
	Kind            Kind          `json:"kind"`
	Name            string        `json:"name"`
	Schema          string        `json:"schema,omitempty"`
	Language        string        `json:"language"`
	ReturnType      string        `json:"return_type,omitempty"`
	Args            []funcArgForm `json:"args,omitempty"`
	Body            string        `json:"body"`
	Comment         string        `json:"comment,omitempty"`
	Volatility      string        `json:"volatility,omitempty"`
	Parallel        string        `json:"parallel,omitempty"`
	SecurityDefiner bool          `json:"security_definer,omitempty"`
	IsProc          bool          `json:"is_proc,omitempty"`
	Cost            *float64      `json:"cost,omitempty"`
	Rows            *float64      `json:"rows,omitempty"`
	DependsOn       []string      `json:"depends_on,omitempty"`
}

func functionToForm(fn model.Function) functionForm {
	args := make([]funcArgForm, len(fn.Args))
	for i, a := range fn.Args {
		args[i] = funcArgForm{Name: a.Name, Type: typeToForm(a.Type), Default: a.Default}
	}
	return functionForm{
		Codec: CodecVersion, Kind: KindFunction,
		Name: fn.Name, Schema: fn.Schema, Language: fn.Language, ReturnType: fn.ReturnType,
		Args: args, Body: fn.Body, Comment: fn.Comment, Volatility: fn.Volatility,
		Parallel: fn.Parallel, SecurityDefiner: fn.SecurityDefiner, IsProc: fn.IsProc,
		Cost: fn.Cost, Rows: fn.Rows, DependsOn: fn.DependsOn,
	}
}

func functionFromForm(f functionForm) model.Function {
	args := make([]model.FunctionArg, len(f.Args))
	for i, a := range f.Args {
		args[i] = model.FunctionArg{Name: a.Name, Type: typeFromForm(a.Type), Default: a.Default}
	}
	return model.Function{
		Name: f.Name, Schema: f.Schema, Language: f.Language, ReturnType: f.ReturnType,
		Args: args, Body: f.Body, Comment: f.Comment, Volatility: f.Volatility,
		Parallel: f.Parallel, SecurityDefiner: f.SecurityDefiner, IsProc: f.IsProc,
		Cost: f.Cost, Rows: f.Rows, DependsOn: f.DependsOn,
	}
}

// EncodeFunction returns the canonical bytes for a function.
func EncodeFunction(fn model.Function) ([]byte, error) { return canonicalJSON(functionToForm(fn)) }

// ---------------------------------------------------------------------------
// Enum / Domain / CompositeType
// ---------------------------------------------------------------------------

type enumForm struct {
	Codec   int      `json:"codec"`
	Kind    Kind     `json:"kind"`
	Name    string   `json:"name"`
	Schema  string   `json:"schema,omitempty"`
	Values  []string `json:"values"`
	Comment string   `json:"comment,omitempty"`
}

func enumToForm(e model.Enum) enumForm {
	return enumForm{
		Codec: CodecVersion, Kind: KindEnum,
		Name: e.Name, Schema: e.Schema, Values: e.Values, Comment: e.Comment,
	}
}

func enumFromForm(f enumForm) model.Enum {
	return model.Enum{Name: f.Name, Schema: f.Schema, Values: f.Values, Comment: f.Comment}
}

// EncodeEnum returns the canonical bytes for an enum type.
func EncodeEnum(e model.Enum) ([]byte, error) { return canonicalJSON(enumToForm(e)) }

type domainForm struct {
	Codec       int      `json:"codec"`
	Kind        Kind     `json:"kind"`
	Name        string   `json:"name"`
	Schema      string   `json:"schema,omitempty"`
	BaseType    typeForm `json:"base_type"`
	NotNull     bool     `json:"not_null,omitempty"`
	Default     string   `json:"default,omitempty"`
	DefaultExpr string   `json:"default_expr,omitempty"`
	Check       string   `json:"check,omitempty"`
	Comment     string   `json:"comment,omitempty"`
}

func domainToForm(d model.Domain) domainForm {
	return domainForm{
		Codec: CodecVersion, Kind: KindDomain,
		Name: d.Name, Schema: d.Schema, BaseType: typeToForm(d.BaseType),
		NotNull: d.NotNull, Default: d.Default, DefaultExpr: d.DefaultExpr, Check: d.Check, Comment: d.Comment,
	}
}

func domainFromForm(f domainForm) model.Domain {
	return model.Domain{
		Name: f.Name, Schema: f.Schema, BaseType: typeFromForm(f.BaseType),
		NotNull: f.NotNull, Default: f.Default, DefaultExpr: f.DefaultExpr, Check: f.Check, Comment: f.Comment,
	}
}

// EncodeDomain returns the canonical bytes for a domain type.
func EncodeDomain(d model.Domain) ([]byte, error) { return canonicalJSON(domainToForm(d)) }

type compositeFieldForm struct {
	Name   string   `json:"name"`
	PGType typeForm `json:"pg_type"`
}

type compositeForm struct {
	Codec   int                  `json:"codec"`
	Kind    Kind                 `json:"kind"`
	Name    string               `json:"name"`
	Schema  string               `json:"schema,omitempty"`
	Fields  []compositeFieldForm `json:"fields"`
	Comment string               `json:"comment,omitempty"`
}

func compositeToForm(c model.CompositeType) compositeForm {
	fields := make([]compositeFieldForm, len(c.Fields))
	for i, f := range c.Fields {
		fields[i] = compositeFieldForm{Name: f.Name, PGType: typeToForm(f.PGType)}
	}
	return compositeForm{
		Codec: CodecVersion, Kind: KindComposite,
		Name: c.Name, Schema: c.Schema, Fields: fields, Comment: c.Comment,
	}
}

func compositeFromForm(f compositeForm) model.CompositeType {
	fields := make([]model.CompositeField, len(f.Fields))
	for i, ff := range f.Fields {
		fields[i] = model.CompositeField{Name: ff.Name, PGType: typeFromForm(ff.PGType)}
	}
	return model.CompositeType{Name: f.Name, Schema: f.Schema, Fields: fields, Comment: f.Comment}
}

// EncodeCompositeType returns the canonical bytes for a composite type.
func EncodeCompositeType(c model.CompositeType) ([]byte, error) {
	return canonicalJSON(compositeToForm(c))
}

// ---------------------------------------------------------------------------
// Schema meta (the schema-global header)
// ---------------------------------------------------------------------------

type schemaMetaForm struct {
	Codec      int                 `json:"codec"`
	Kind       Kind                `json:"kind"`
	Name       string              `json:"name"`
	Extensions []string            `json:"extensions,omitempty"`
	Groups     map[string][]string `json:"groups,omitempty"`
	PGVersion  int                 `json:"pg_version"`
}

func schemaMetaToForm(s *model.Schema) schemaMetaForm {
	var groups map[string][]string
	if len(s.Groups) > 0 {
		groups = make(map[string][]string, len(s.Groups))
		for k, v := range s.Groups {
			// Group membership is a SET; sort for canonicality. Map keys are
			// sorted by encoding/json on emission.
			groups[k] = sortedCopy(v)
		}
	}
	return schemaMetaForm{
		Codec:      CodecVersion,
		Kind:       KindSchemaMeta,
		Name:       s.Name,
		Extensions: s.Extensions,
		Groups:     groups,
		PGVersion:  s.PGVersion,
	}
}

// EncodeSchemaMeta returns the canonical bytes for the schema-global header:
// name, extensions (canonical order), groups, and pg_version. The per-object
// collections (tables, views, types, ...) are encoded separately.
func EncodeSchemaMeta(s *model.Schema) ([]byte, error) {
	return canonicalJSON(schemaMetaToForm(s))
}
