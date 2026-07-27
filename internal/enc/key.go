package enc

import (
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// Kind is the object kind of a manifest key. Kind-qualification is what keeps a
// table named x and a function named x from ever colliding: their keys differ
// in Kind even when schema and name coincide.
type Kind string

const (
	KindSchemaMeta   Kind = "schema"    // the schema-global header (name, extensions, pg_version, groups)
	KindTable        Kind = "table"     //
	KindView         Kind = "view"      //
	KindMatView      Kind = "matview"   //
	KindSequence     Kind = "sequence"  //
	KindFunction     Kind = "function"  //
	KindEnum         Kind = "enum"      //
	KindDomain       Kind = "domain"    //
	KindComposite    Kind = "composite" //
	KindRegistrySnap Kind = "registry"  // the semtype registry snapshot (SM transition residue)
)

// Key is a kind-qualified manifest key: (kind, schema, name) plus, for
// functions, the argument signature so overloads are distinct entries. A
// manifest (roadmap 1.4) is a sorted map of Key -> object-id; this type and its
// construction and collision behavior live here, in the encoder kernel, so 1.4
// can build the manifest on top without re-deriving key identity.
type Key struct {
	Kind   Kind
	Schema string
	Name   string
	// ArgSig is the parenthesized, comma-joined canonical argument-type
	// signature, e.g. "(int4,text)". It is populated ONLY for KindFunction so
	// that two overloads of the same function name are distinct keys. Empty for
	// every other kind.
	ArgSig string
}

// String renders the key in a stable, collision-free textual form:
//
//	kind:schema.name           (most kinds)
//	function:schema.name(args) (functions carry their signature)
//	schema:name                (the schema-global header; no schema qualifier)
//	registry:                  (the singleton registry snapshot)
//
// The kind prefix is always present, so keys of different kinds never collide
// even with identical schema and name.
func (k Key) String() string {
	switch k.Kind {
	case KindSchemaMeta:
		return string(k.Kind) + ":" + k.Name
	case KindRegistrySnap:
		return string(k.Kind) + ":"
	default:
		return string(k.Kind) + ":" + k.qualified() + k.ArgSig
	}
}

func (k Key) qualified() string {
	if k.Schema == "" {
		return k.Name
	}
	return k.Schema + "." + k.Name
}

// FunctionArgSig builds the canonical argument-type signature for a function
// from its resolved args, in positional order. Argument names and defaults are
// NOT part of the signature — PostgreSQL overload resolution keys on argument
// TYPES alone, so the signature must too.
func FunctionArgSig(args []model.FunctionArg) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = typeinfo.Reconstruct(a.Type)
	}
	return "(" + strings.Join(parts, ",") + ")"
}

// KeyForTable returns the manifest key for a table.
func KeyForTable(t model.Table) Key {
	return Key{Kind: KindTable, Schema: t.Schema, Name: t.Name}
}

// KeyForView returns the manifest key for a view.
func KeyForView(v model.View) Key {
	return Key{Kind: KindView, Schema: v.Schema, Name: v.Name}
}

// KeyForMatView returns the manifest key for a materialized view.
func KeyForMatView(mv model.MaterializedView) Key {
	return Key{Kind: KindMatView, Schema: mv.Schema, Name: mv.Name}
}

// KeyForSequence returns the manifest key for a standalone sequence.
func KeyForSequence(s model.Sequence) Key {
	return Key{Kind: KindSequence, Schema: s.Schema, Name: s.Name}
}

// KeyForFunction returns the manifest key for a function, including its
// argument signature so overloads are distinct.
func KeyForFunction(f model.Function) Key {
	return Key{Kind: KindFunction, Schema: f.Schema, Name: f.Name, ArgSig: FunctionArgSig(f.Args)}
}

// KeyForEnum returns the manifest key for an enum type.
func KeyForEnum(e model.Enum) Key {
	return Key{Kind: KindEnum, Schema: e.Schema, Name: e.Name}
}

// KeyForDomain returns the manifest key for a domain type.
func KeyForDomain(d model.Domain) Key {
	return Key{Kind: KindDomain, Schema: d.Schema, Name: d.Name}
}

// KeyForComposite returns the manifest key for a composite type.
func KeyForComposite(c model.CompositeType) Key {
	return Key{Kind: KindComposite, Schema: c.Schema, Name: c.Name}
}
