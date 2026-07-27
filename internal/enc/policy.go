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
		},
		excluded: map[string]string{
			"CycleGroups":             "derived cycle-safe-DDL grouping, recomputed by Canonicalize from the FK graph; not desired-model semantics",
			"TablesByName":            "derived lookup cache (json:\"-\"), rebuilt by Canonicalize",
			"FKGraph":                 "derived FK adjacency cache (json:\"-\"), rebuilt by Canonicalize",
			"StateMachineTransitions": "schema-side duplicate of the registry SM type defs; excluded per roadmap 1.5 (SM transition identity flows through the registry snapshot / type-definition path)",
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
		encoded:  []string{"Name", "Columns", "RefSchema", "RefTable", "RefColumns", "OnDelete"},
		excluded: map[string]string{},
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
}

// registryFieldPolicy classifies the registry-snapshot structs. The snapshot
// serializes only the SM transition residue that has no model-collection home;
// every other TypeDef field either lives in a model collection or is provenance.
var registryFieldPolicy = map[string]structPolicy{
	"TypeDef": {
		encoded: []string{"Name", "States", "Transitions", "InitialState", "EnforceTrigger", "Comment"},
		excluded: map[string]string{
			"Source":      "provenance metadata; excluded so Source relabeling does not flip identity",
			"Kind":        "the snapshot residue is only state-machine types; kind is implied by the snapshot section",
			"BaseType":    "for a state-machine type this equals the type's own Name (a self-referential enum); carries no identity beyond Name",
			"NotNull":     "type-level nullability default is captured on resolved model columns (Column.NotNull)",
			"Default":     "type-level default is captured on resolved model columns (Column.Default)",
			"DefaultExpr": "type-level default expression is captured on resolved model columns (Column.DefaultExpr)",
			"Check":       "scalar-type CHECK materializes into model Domains (Domain.Check); not part of the SM snapshot residue",
			"Unique":      "captured on resolved model columns/constraints",
			"EnumValues":  "state names materialize into model Enums (Enum.Values); the snapshot residue is the transition graph, not the value list",
			"Fields":      "composite fields materialize into model CompositeTypes (CompositeType.Fields)",
			"Generated":   "captured on resolved model columns (Column.Generated)",
			"Stored":      "captured on resolved model columns (Column.Stored)",
			"Identity":    "captured on resolved model columns (Column.Identity)",
			"Array":       "captured on resolved model columns (Column.Array)",
		},
	},
	"SMStateDef": {
		encoded:  []string{"Name", "Terminal", "Comment"},
		excluded: map[string]string{},
	},
	"SMTransitionDef": {
		encoded:  []string{"Name", "From", "To", "Requires", "Comment"},
		excluded: map[string]string{},
	},
}
