// Package modelgen is a pure random generator of VALID pgdesign models,
// built on pgregory.net/rapid so shrinking is structural and comes for free
// with the combinators. It is L9's input source: the property tests that check
// the kernel's laws consume generated models rather than a handful of hand-built
// fixtures (the seed package generates row DATA and cannot serve this role).
//
// This is increment A of the staged generator (roadmap 1.6): flat models only —
// multiple schemas, tables with snake_case names, typed builtin columns drawn
// from the real semtype builtin registry, mandatory table comments, and a
// surrogate primary key per table. It deliberately generates NO foreign keys,
// NO custom types, NO views or functions, and NO expressions; those fragments
// arrive in later increments alongside the consumers that need them.
//
// Oracle doctrine: well-formedness invariants (snake_case names, comments,
// resolvable types, a valid PK) are constructed by design; broader policy
// invariants are left to generate-then-validate-reject so their distributions
// stay wide. The oracle itself (modelgen_test.go) is validate — generated
// models must Build + Canonicalize cleanly AND pass validate with zero errors,
// with the extension and type registries populated.
//
// modelgen is test-support infrastructure: it is imported only by property
// tests, never by the production CLI, which keeps the rapid dependency out of
// shipped binaries.
package modelgen

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"pgregory.net/rapid"
)

// Config controls the shape of generated models. Increment A covers flat
// tables; every field is additive so later increments (FKs, type closures,
// state machines, injective / bridge-proven fragments) extend this struct
// rather than reshape it.
type Config struct {
	// MinSchemas and MaxSchemas bound the number of schemas per model
	// (inclusive). A model with more than one schema is a multi-schema layout.
	MinSchemas, MaxSchemas int

	// MinTables and MaxTables bound the number of tables per schema (inclusive).
	MinTables, MaxTables int

	// MinExtraColumns and MaxExtraColumns bound the number of columns per table
	// BEYOND the mandatory surrogate primary-key column (inclusive).
	MinExtraColumns, MaxExtraColumns int

	// PGVersion is written into every generated schema's [meta].version. It
	// gates version-conditional features in later increments; increment A
	// generates no version-gated features, so any recent version is safe.
	PGVersion int

	// ColumnTypes is the pool of semantic type names that non-PK columns are
	// drawn from. When empty, DefaultConfig / withDefaults fills it from the
	// real builtin registry (minus the identity/surrogate types reserved for
	// the primary key), so the pool tracks the registry instead of a hardcoded
	// copy that could drift.
	ColumnTypes []string

	// MinGroups and MaxGroups bound the number of [groups] entries per model
	// (inclusive). Each group references a random non-empty subset of the
	// model's tables. Groups are schema-global identity (they participate in the
	// revision and, since the reverse-conformance work, in diff); generating
	// them lets identity/diff property tests exercise the group collection
	// rather than always seeing it empty. Zero groups is a valid model, so the
	// range is NOT defaulted away — DefaultConfig leaves it 0..2 explicitly.
	MinGroups, MaxGroups int

	// IncludeStateMachines is the L10 UNRESTRICTED-vs-INJECTIVE fragment knob
	// (roadmap 1.6 / 5.8). The INJECTIVE fragment (false) excludes state-machine
	// types, which introspect lossily as plain enums — so the re-introspection
	// oracle only ever runs over models where introspection is injective. The
	// UNRESTRICTED side (true) admits SM types for the manifest oracle, which is
	// not lossy. NOTE (flagged): SM-type GENERATION is the remaining modelgen
	// increment; until it lands this knob is the wired extension point and the
	// generator emits the injective fragment either way. The L10 split-oracle
	// STRUCTURE is already in place, so adding SM generation later needs no test
	// reshape — only this generator gains SM emission when the knob is true.
	IncludeStateMachines bool
}

// pkColumnName is the fixed name of the surrogate primary-key column every
// generated table carries.
const pkColumnName = "id"

// fallbackTableIndex is the table index the pair-derivation uses when every table
// in a schema was dropped and one must be added back. It is far beyond any
// generated table index so the fallback name never collides with a dropped table.
const fallbackTableIndex = 1_000_000

// pkColumnType is the semantic type of the surrogate primary-key column: the
// builtin "id" (a NOT NULL uuid defaulting to gen_random_uuid()).
const pkColumnType = "id"

// DefaultConfig returns the increment-A defaults: small multi-schema models
// with a handful of tables and columns each.
func DefaultConfig() Config {
	return Config{
		MinSchemas: 1, MaxSchemas: 3,
		MinTables: 1, MaxTables: 4,
		MinExtraColumns: 0, MaxExtraColumns: 5,
		PGVersion:   16,
		ColumnTypes: defaultColumnTypes(),
		MinGroups:   0, MaxGroups: 2,
	}
}

// withDefaults fills zero-valued knobs where zero is unambiguously "unset". A
// model needs at least one schema and one table, so a zero max there means the
// caller left it blank. PGVersion and ColumnTypes are likewise filled when
// blank. The extra-column range is NOT defaulted: zero extra columns (a bare
// surrogate-PK table) is a valid explicit choice, so honoring it literally
// beats guessing — DefaultConfig sets the 0..5 spread explicitly.
func withDefaults(cfg Config) Config {
	if cfg.MaxSchemas == 0 {
		cfg.MinSchemas, cfg.MaxSchemas = 1, 3
	}
	if cfg.MaxTables == 0 {
		cfg.MinTables, cfg.MaxTables = 1, 4
	}
	if cfg.PGVersion == 0 {
		cfg.PGVersion = 16
	}
	if len(cfg.ColumnTypes) == 0 {
		cfg.ColumnTypes = defaultColumnTypes()
	}
	return cfg
}

// defaultColumnTypes draws the non-PK column-type pool from the real builtin
// registry, excluding the identity/surrogate types ("id", "auto_id"): those are
// reserved for the primary key, and scattering identity columns through a table
// would generate models that are only accidentally valid.
func defaultColumnTypes() []string {
	var out []string
	for _, name := range semtype.NewBuiltinRegistry().BuiltinNames() {
		if name == "id" || name == "auto_id" {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Generator returns a rapid generator of valid flat models as a slice of
// per-schema RawSchema values, ready for model.BuildMulti. Because it is a
// rapid.Generator, it composes into larger generators and shrinks structurally.
func Generator(cfg Config) *rapid.Generator[[]*parse.RawSchema] {
	cfg = withDefaults(cfg)
	return rapid.Custom(func(t *rapid.T) []*parse.RawSchema {
		nSchemas := rapid.IntRange(cfg.MinSchemas, cfg.MaxSchemas).Draw(t, "schema_count")
		raws := make([]*parse.RawSchema, 0, nSchemas)
		for s := 0; s < nSchemas; s++ {
			raws = append(raws, genSchema(t, cfg, s))
		}
		genGroups(t, cfg, raws)
		return raws
	})
}

// Draw is a convenience for property tests: it draws one model from the
// generator built for cfg.
func Draw(t *rapid.T, cfg Config) []*parse.RawSchema {
	return Generator(cfg).Draw(t, "model")
}

// GeneratePair draws a MODEL PAIR (a, b) for L10's round-trip property: b is
// a's model mutated by structural edits — tables and columns added and dropped —
// that keep every SHARED column's type FIXED. That invariant is what makes the
// pair applyable: diff(a,b) lowers only to table and column add/drop ops, never a
// column-type-change ALTER (which would need a USING cast that empty-table apply
// cannot always satisfy). Both a and b are independently valid models. Groups are
// forced off for the pair so the diff stays focused on table/column DDL.
func GeneratePair(cfg Config) *rapid.Generator[[2][]*parse.RawSchema] {
	cfg = withDefaults(cfg)
	cfg.MinGroups, cfg.MaxGroups = 0, 0 // pairs exercise table/column DDL, not groups
	return rapid.Custom(func(t *rapid.T) [2][]*parse.RawSchema {
		a := Generator(cfg).Draw(t, "model_a")
		b := deriveModel(t, cfg, a)
		return [2][]*parse.RawSchema{a, b}
	})
}

// DrawPair is a convenience for property tests: it draws one model pair from the
// pair generator built for cfg.
func DrawPair(t *rapid.T, cfg Config) (a, b []*parse.RawSchema) {
	p := GeneratePair(cfg).Draw(t, "model_pair")
	return p[0], p[1]
}

// ExamplePair draws a DETERMINISTIC model pair for the given seed, without a
// rapid.T — the entry point DB-gated tests use to iterate a bounded number of
// generated pairs in a plain loop (rapid's Example gives reproducible samples).
func ExamplePair(cfg Config, seed int) (a, b []*parse.RawSchema) {
	p := GeneratePair(cfg).Example(seed)
	return p[0], p[1]
}

// deriveModel builds b from a by dropping/keeping/mutating a's tables and adding
// new ones, preserving shared columns' types. It guarantees b has at least one
// table overall so the post-state is a non-empty, buildable model.
func deriveModel(t *rapid.T, cfg Config, a []*parse.RawSchema) []*parse.RawSchema {
	b := make([]*parse.RawSchema, 0, len(a))
	total := 0
	for si, raw := range a {
		// Carry the user types (e.g. state machines) forward so a table that keeps
		// its SM column still resolves its type in b — the pair mutates tables and
		// columns, never the type closure.
		nb := &parse.RawSchema{Meta: raw.Meta, Types: raw.Types}
		nextIdx := len(raw.Tables)
		for ti, tbl := range raw.Tables {
			if rapid.Bool().Draw(t, fmt.Sprintf("drop_table_%d_%d", si, ti)) {
				continue // drop this table in b
			}
			nb.Tables = append(nb.Tables, deriveTable(t, cfg, tbl))
		}
		// Add new tables (globally-unique names beyond a's index range).
		nNew := rapid.IntRange(0, cfg.MaxTables).Draw(t, fmt.Sprintf("add_tables_%d", si))
		for k := 0; k < nNew; k++ {
			nb.Tables = append(nb.Tables, genTable(t, cfg, si, nextIdx+k))
		}
		total += len(nb.Tables)
		b = append(b, nb)
	}
	// Guarantee a non-empty post-state: if every table was dropped, add one back
	// with an index far beyond any original table's, so its name (t_0_<big>) can
	// never collide with a dropped table's name — a collision would make diff match
	// them and emit a bogus type-change ALTER on a "shared" column.
	if total == 0 && len(b) > 0 {
		b[0].Tables = append(b[0].Tables, genTable(t, cfg, 0, fallbackTableIndex))
	}
	return b
}

// deriveTable copies tbl into b, keeping the surrogate PK and every column's TYPE
// fixed. It may drop some non-PK columns and append new ones (new names never
// collide with kept columns because they index beyond the original count).
func deriveTable(t *rapid.T, cfg Config, tbl parse.RawTable) parse.RawTable {
	nt := parse.RawTable{
		Name:    tbl.Name,
		Comment: tbl.Comment,
		PK:      tbl.PK,
	}
	for ci, col := range tbl.Columns {
		// Never drop the PK or the state-machine column: both are protected so the
		// shared-column-type invariant holds and the SM type stays used in b.
		if col.Name == pkColumnName || col.Name == smColumnName {
			nt.Columns = append(nt.Columns, col)
			continue
		}
		if rapid.Bool().Draw(t, fmt.Sprintf("drop_col_%s_%d", tbl.Name, ci)) {
			continue // drop this non-PK column
		}
		nt.Columns = append(nt.Columns, col) // keep name AND type
	}
	// New columns use a DISTINCT "nc_" prefix so they can never collide with a kept
	// "col_N" column — a name collision would make diff see a same-named column with
	// a different type and emit a type-change ALTER (which is exactly what the pair
	// derivation must never produce).
	nNew := rapid.IntRange(0, cfg.MaxExtraColumns).Draw(t, fmt.Sprintf("add_cols_%s", tbl.Name))
	for k := 0; k < nNew; k++ {
		colType := rapid.SampledFrom(cfg.ColumnTypes).Draw(t, fmt.Sprintf("%s_newcol_%d_type", tbl.Name, k))
		nt.Columns = append(nt.Columns, parse.RawColumn{
			Name: fmt.Sprintf("nc_%d", k),
			Type: colType,
		})
	}
	return nt
}

// genGroups attaches [groups] entries to the model, drawn from the full set of
// generated (globally-unique) table names. Groups are model-level: they are
// merged across raw schemas by BuildMulti, so all groups are attached to the
// first raw schema. Each group references a random non-empty subset of tables,
// so resolveGroups always validates cleanly (every referenced table exists).
func genGroups(t *rapid.T, cfg Config, raws []*parse.RawSchema) {
	if cfg.MaxGroups == 0 || len(raws) == 0 {
		return
	}
	var allTables []string
	for _, raw := range raws {
		for _, tbl := range raw.Tables {
			allTables = append(allTables, tbl.Name)
		}
	}
	if len(allTables) == 0 {
		return
	}
	nGroups := rapid.IntRange(cfg.MinGroups, cfg.MaxGroups).Draw(t, "group_count")
	if nGroups == 0 {
		return
	}
	groups := make(map[string][]string, nGroups)
	for g := 0; g < nGroups; g++ {
		var members []string
		for i, tbl := range allTables {
			if rapid.Bool().Draw(t, fmt.Sprintf("group_%d_has_%d", g, i)) {
				members = append(members, tbl)
			}
		}
		if len(members) == 0 {
			// Guarantee a non-empty group so the entry is meaningful.
			members = []string{allTables[rapid.IntRange(0, len(allTables)-1).Draw(t, fmt.Sprintf("group_%d_fallback", g))]}
		}
		groups[fmt.Sprintf("group_%d", g)] = members
	}
	raws[0].Groups = groups
}

// smColumnName is the fixed name of the state-machine column injected into a
// table when IncludeStateMachines is on. Like the PK column, it is protected from
// the pair derivation's random column drops so the SM type stays USED across the
// round-trip (an SM column keeps the state enum live and the sm_type manifest
// entry exercised on both endpoints).
const smColumnName = "sm_state"

func genSchema(t *rapid.T, cfg Config, s int) *parse.RawSchema {
	schemaName := fmt.Sprintf("schema_%d", s)
	nTables := rapid.IntRange(cfg.MinTables, cfg.MaxTables).Draw(t, fmt.Sprintf("table_count_%d", s))
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{
			Version: cfg.PGVersion,
			Schema:  schemaName,
		},
	}
	for i := 0; i < nTables; i++ {
		raw.Tables = append(raw.Tables, genTable(t, cfg, s, i))
	}
	// State-machine increment (roadmap 1.6 / 5.8b): when enabled, declare one SM
	// type in this schema and reference it from the first table via the protected
	// SM column. The SM type materializes a state enum (DDL) plus the first-class
	// sm_type object (identity/manifest), so the model exercises the manifest side
	// that re-introspection cannot observe (introspect sees only the enum).
	if cfg.IncludeStateMachines && len(raw.Tables) > 0 {
		smType, initial := genStateMachine(t, fmt.Sprintf("sm_status_%d", s))
		raw.Types = append(raw.Types, smType)
		raw.Tables[0].Columns = append(raw.Tables[0].Columns, parse.RawColumn{
			Name:    smColumnName,
			Type:    smType.Name,
			Default: &initial, // matches the SM initial state (validate E224)
		})
	}
	return raw
}

// genStateMachine draws a valid state-machine RawType (roadmap 5.8b): 2..4 states
// with the last marked terminal, a linear transition chain so every state is
// reachable from the initial state (no W027), no transition requires (no E223),
// and an enforcement trigger. It returns the type and its initial state. Named to
// avoid collisions with generated table/column names.
func genStateMachine(t *rapid.T, name string) (parse.RawType, string) {
	nStates := rapid.IntRange(2, 4).Draw(t, name+"_state_count")
	states := make([]parse.RawSMState, nStates)
	stateNames := make([]string, nStates)
	for i := 0; i < nStates; i++ {
		sn := fmt.Sprintf("st_%d", i)
		stateNames[i] = sn
		st := parse.RawSMState{Name: sn}
		if i == nStates-1 {
			term := true
			st.Terminal = &term
		}
		states[i] = st
	}
	var transitions []parse.RawSMTransition
	for i := 0; i < nStates-1; i++ {
		transitions = append(transitions, parse.RawSMTransition{
			Name: fmt.Sprintf("advance_%d", i),
			From: []string{stateNames[i]},
			To:   stateNames[i+1],
		})
	}
	initial := stateNames[0]
	enforce := true
	comment := fmt.Sprintf("Generated state machine %s", name)
	return parse.RawType{
		Name:           name,
		Kind:           "state_machine",
		States:         states,
		Transitions:    transitions,
		InitialState:   &initial,
		EnforceTrigger: &enforce,
		Comment:        &comment,
	}, initial
}

func genTable(t *rapid.T, cfg Config, s, i int) parse.RawTable {
	// Globally-unique, snake_case table name (schema-qualified by index so two
	// schemas never share a table name in the merged model).
	name := fmt.Sprintf("t_%d_%d", s, i)
	comment := fmt.Sprintf("Generated table %s", name)

	columns := []parse.RawColumn{{Name: pkColumnName, Type: pkColumnType}}

	nExtra := rapid.IntRange(cfg.MinExtraColumns, cfg.MaxExtraColumns).Draw(t, name+"_column_count")
	for j := 0; j < nExtra; j++ {
		colType := rapid.SampledFrom(cfg.ColumnTypes).Draw(t, fmt.Sprintf("%s_col_%d_type", name, j))
		columns = append(columns, parse.RawColumn{
			Name: fmt.Sprintf("col_%d", j),
			Type: colType,
		})
	}

	return parse.RawTable{
		Name:    name,
		Comment: &comment,
		PK:      []string{pkColumnName},
		Columns: columns,
	}
}
