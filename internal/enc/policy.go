package enc

// This file is the encoder's field policy: for every DDL-reaching model struct
// and every registry-snapshot struct, it records which exported fields are
// ENCODED into canonical bytes and which are EXCLUDED, each exclusion carrying
// a reason. The reflection-based totality guard (policy_test.go) walks the real
// struct types and asserts that every exported field is classified exactly once
// — so the guard turns RED the instant a new field is added to a model struct
// without a deliberate encode-or-exclude decision. This converts "the encoder
// is complete" from a review hope into a checked law (L9).
//
// The reason strings are the contract; keep them accurate.

// EncodedModelFields returns, for every DDL-reaching model struct (keyed by its
// unqualified struct name, matching reflect.Type.Name()), the list of exported
// field names the encoder serializes into canonical bytes. It is a copy of the
// encoder's own field policy — the SAME registry the totality guard
// (policy_test.go) checks for completeness — exposed so downstream kernel
// verification can be DRIVEN BY the encoder's notion of identity rather than a
// hand-maintained parallel list. The chief consumer is roadmap 1.4's
// diff-totality mutation guard: it perturbs each encoded field and asserts diff
// is non-empty, retiring the diff-under-reporting defect class by construction.
//
// The returned map is freshly allocated; mutating it does not affect the policy.
func EncodedModelFields() map[string][]string {
	out := make(map[string][]string, len(modelFieldPolicy))
	for name, p := range modelFieldPolicy {
		fields := make([]string, len(p.encoded))
		copy(fields, p.encoded)
		out[name] = fields
	}
	return out
}

// structPolicy classifies the exported fields of a single struct.
type structPolicy struct {
	// encoded lists the exported field names serialized into canonical bytes.
	encoded []string
	// excluded maps an exported field name to the reason it is NOT encoded.
	excluded map[string]string
}

// modelFieldPolicy classifies every DDL-reaching model struct. Keys are the
// unqualified struct names (matched by the guard against reflect type names).
var modelFieldPolicy = map[string]structPolicy{
	"Schema": {
		encoded: []string{
			"Name", "Extensions", "Enums", "Domains", "CompositeTypes", "Tables",
			"Views", "MaterializedViews", "Sequences", "Functions", "Groups", "PGVersion",
			"StateMachines",
		},
		excluded: map[string]string{
			"CycleGroups":             "derived cycle-safe-DDL grouping, recomputed by Canonicalize from the FK graph; not desired-model semantics",
			"TablesByName":            "derived lookup cache (json:\"-\"), rebuilt by Canonicalize",
			"FKGraph":                 "derived FK adjacency cache (json:\"-\"), rebuilt by Canonicalize",
			"StateMachineTransitions": "derived from->to adjacency for codegen (sorted target sets, no comments); a duplicate of StateMachines with less fidelity. SM identity flows through the first-class StateMachines collection (KindSMType objects), so this derived form is excluded per roadmap 1.5",
			"ImportedTables":          "REFERENCE tables owned by another project, pulled in via [imports] (roadmap 7.3, json:\"-\"). They are facts owned elsewhere, not this project's objects, so they are excluded from this project's identity — the vendored import surface carries its own per-object ids in imports/<alias>/",
		},
	},
	"Table": {
		encoded: []string{
			"Name", "Schema", "Comment", "Columns", "PK", "FKs", "Indexes", "Uniques",
			"Checks", "Exclusions", "Partitioning", "Dependencies", "Maintenance",
			"Owner", "Policies", "Triggers", "EnableRLS", "ForceRLS", "AppendOnly",
		},
		excluded: map[string]string{
			"SourceFile":     "introspect/parse-path provenance (which file declared the table); origin metadata, not desired-model semantics",
			"PartmanManaged": "introspect-path fact (child of a partman-managed parent); never present in TOML-built models",
			"PartmanParent":  "introspect-path fact (schema-qualified partman parent); never present in TOML-built models",
		},
	},
	"Column": {
		encoded: []string{
			"Name", "PGType", "Collation", "NotNull", "Default", "DefaultExpr", "Generated",
			"Stored", "Identity", "Comment", "SemanticTypeName", "Array", "JSONSchema",
			"Statistics", "TypeKind",
		},
		excluded: map[string]string{},
	},
	"FK": {
		encoded: []string{"Name", "Columns", "RefSchema", "RefTable", "RefColumns", "OnDelete"},
		excluded: map[string]string{
			"RefAlias": "import-alias provenance (roadmap 7.1): which [imports] alias an alias:table ref_table resolved through. RefSchema/RefTable already carry the resolved target, so the alias must not flip identity — two FKs at the same resolved target are identical however the reference was spelled",
		},
	},
	"Index": {
		encoded: []string{"Name", "Columns", "Desc", "Method", "Opclasses", "Collations", "Where", "Include", "With", "Unique"},
		excluded: map[string]string{
			"IsAutoFK": "enrich-derived: marks indexes auto-added for FK coverage after resolveTable; a derivation flag, not desired-model semantics",
		},
	},
	"UniqueConstraint": {
		encoded:  []string{"Name", "Columns", "Deferrable", "InitiallyDeferred"},
		excluded: map[string]string{},
	},
	"CheckConstraint": {
		encoded:  []string{"Name", "Expr"},
		excluded: map[string]string{},
	},
	"ExclusionElement": {
		encoded:  []string{"Column", "Operator"},
		excluded: map[string]string{},
	},
	"ExclusionConstraint": {
		encoded:  []string{"Name", "Method", "Elements", "Where", "Deferrable", "InitiallyDeferred"},
		excluded: map[string]string{},
	},
	"Policy": {
		encoded:  []string{"Name", "Type", "Operation", "Role", "Using", "WithCheck", "ErrorCode", "ErrorMessage"},
		excluded: map[string]string{},
	},
	"Trigger": {
		encoded: []string{
			"Name", "Function", "Events", "Timing", "ForEach", "When", "Constraint",
			"Deferrable", "InitiallyDeferred", "ReferencingOld", "ReferencingNew", "Comment",
		},
		excluded: map[string]string{},
	},
	"Enum": {
		encoded: []string{"Schema", "Name", "Values", "Comment"},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the enum); origin metadata, not desired-model semantics",
		},
	},
	"Sequence": {
		encoded: []string{"Name", "Schema", "Start", "Increment", "MinValue", "MaxValue", "Cache", "Cycle", "OwnedBy", "Comment"},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the sequence); origin metadata, not desired-model semantics",
		},
	},
	"FunctionArg": {
		encoded:  []string{"Name", "Type", "Default"},
		excluded: map[string]string{},
	},
	"Function": {
		encoded: []string{
			"Name", "Schema", "Language", "ReturnType", "Args", "Body", "Comment",
			"Volatility", "Parallel", "SecurityDefiner", "IsProc", "Cost", "Rows", "DependsOn",
		},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the function); origin metadata, not desired-model semantics",
		},
	},
	"Domain": {
		encoded: []string{"Name", "Schema", "BaseType", "NotNull", "Default", "DefaultExpr", "Check", "Comment"},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the domain); origin metadata, not desired-model semantics",
		},
	},
	"CompositeField": {
		encoded:  []string{"Name", "PGType"},
		excluded: map[string]string{},
	},
	"CompositeType": {
		encoded: []string{"Name", "Schema", "Fields", "Comment"},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the composite type); origin metadata, not desired-model semantics",
		},
	},
	"PartitionSpec": {
		encoded:  []string{"Strategy", "Columns", "Name", "Bound", "Children"},
		excluded: map[string]string{},
	},
	"MaintenanceConfig": {
		encoded:  []string{"Interval", "Premake", "Retention", "RetentionKeepTable", "Schedule"},
		excluded: map[string]string{},
	},
	"View": {
		encoded: []string{"Name", "Schema", "Query", "Comment", "DependsOn"},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the view); origin metadata, not desired-model semantics",
		},
	},
	"MaterializedView": {
		encoded: []string{"Name", "Schema", "Query", "Comment", "DependsOn", "WithData", "Indexes"},
		excluded: map[string]string{
			"SourceFile": "parse-path provenance (which file declared the matview); origin metadata, not desired-model semantics",
		},
	},
	// typeinfo types reach DDL through Column.PGType, Domain.BaseType,
	// CompositeField.PGType, and FunctionArg.Type.
	"Type": {
		encoded:  []string{"Base", "DomainName", "Params"},
		excluded: map[string]string{},
	},
	"Params": {
		encoded:  []string{"Precision", "Scale", "Length", "RawModifier"},
		excluded: map[string]string{},
	},
	// fd.FuncDep reaches DDL/audit through Table.Dependencies.
	"FuncDep": {
		encoded: []string{"Determinant", "Dependent"},
		excluded: map[string]string{
			"Source": "FD provenance (\"declared\"/\"discovered\"/\"inferred\"); metadata that must not flip identity — mirrors the TypeDef.Source policy",
		},
	},
	// model.StateMachine is the first-class identity carrier for SM types
	// (KindSMType objects). It reaches DDL through the state-name Enum it also
	// materializes; the transition graph and comments live only here.
	"StateMachine": {
		encoded: []string{"Name", "Schema", "States", "Transitions", "InitialState", "EnforceTrigger", "Comment"},
		excluded: map[string]string{},
	},
	"SMState": {
		encoded:  []string{"Name", "Terminal", "Comment"},
		excluded: map[string]string{},
	},
	"SMTransition": {
		encoded:  []string{"Name", "From", "To", "Requires", "Comment"},
		excluded: map[string]string{},
	},
}

// registryFieldPolicy classifies the semtype registry structs. The registry
// snapshot no longer serializes ANY registry struct into identity: every
// identity-bearing piece of registry state now has a first-class MODEL home
// (state-machine graphs in model.StateMachine, enum values in model.Enum,
// composite fields in model.CompositeType, scalar CHECKs in model.Domain,
// column-level type facts in model.Column). So every field below is EXCLUDED,
// each reason naming its model home. The totality guard therefore turns red if
// a new registry field is added, forcing the escape-hatch decision: if it
// carries identity, give it a model home (and classify it here as homed);
// otherwise mark it provenance/derived. See snapshot.go.
var registryFieldPolicy = map[string]structPolicy{
	"TypeDef": {
		encoded: []string{},
		excluded: map[string]string{
			"Name":           "state-machine type name is homed in model.StateMachine.Name (and model.Enum.Name); enum/composite/domain names in their respective model collections",
			"Kind":           "type-kind selects which model collection the type materializes into; not itself identity residue",
			"BaseType":       "for a state-machine type this equals the type's own Name (a self-referential enum); scalar base types materialize into model.Domain.BaseType / Column.PGType",
			"NotNull":        "type-level nullability default is captured on resolved model columns (Column.NotNull) / model.Domain.NotNull",
			"Default":        "type-level default is captured on resolved model columns (Column.Default) / model.Domain.Default",
			"DefaultExpr":    "type-level default expression is captured on resolved model columns (Column.DefaultExpr) / model.Domain.DefaultExpr",
			"Check":          "scalar-type CHECK materializes into model.Domain.Check",
			"Unique":         "captured on resolved model columns/constraints",
			"Comment":        "type comment materializes into model.Enum.Comment / model.CompositeType.Comment / model.Domain.Comment, and (for SM types) model.StateMachine.Comment",
			"EnumValues":     "state/enum values materialize into model.Enum.Values (and SM state names into model.StateMachine.States)",
			"Fields":         "composite fields materialize into model.CompositeType.Fields",
			"States":         "state-machine states materialize into model.StateMachine.States (with per-state comment/terminal)",
			"Transitions":    "state-machine transitions materialize into model.StateMachine.Transitions (with per-transition comment)",
			"InitialState":   "state-machine initial state materializes into model.StateMachine.InitialState",
			"EnforceTrigger": "state-machine enforce-trigger flag materializes into model.StateMachine.EnforceTrigger",
			"Generated":      "captured on resolved model columns (Column.Generated)",
			"Stored":         "captured on resolved model columns (Column.Stored)",
			"Identity":       "captured on resolved model columns (Column.Identity)",
			"Array":          "captured on resolved model columns (Column.Array)",
			"Source":         "provenance metadata; must not flip identity — it has no model home by design",
		},
	},
	"SMStateDef": {
		encoded: []string{},
		excluded: map[string]string{
			"Name":     "homed in model.SMState.Name",
			"Terminal": "homed in model.SMState.Terminal",
			"Comment":  "homed in model.SMState.Comment",
		},
	},
	"SMTransitionDef": {
		encoded: []string{},
		excluded: map[string]string{
			"Name":     "homed in model.SMTransition.Name",
			"From":     "homed in model.SMTransition.From",
			"To":       "homed in model.SMTransition.To",
			"Requires": "homed in model.SMTransition.Requires",
			"Comment":  "homed in model.SMTransition.Comment",
		},
	},
}
