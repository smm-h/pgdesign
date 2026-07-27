package enc

import (
	"fmt"
	"strconv"
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
	KindSMType       Kind = "sm_type"   // a state-machine type definition (identity carrier)
	KindRegistrySnap Kind = "registry"  // the semtype registry snapshot (import-surface residue channel; empty for identity)

	// KindDML and KindRaw are PSEUDO-TARGET kinds for data/opaque-SQL migration
	// ops (roadmap 5.1, edge_format.md TENSION 2). They never name a schema
	// object: a DML op changes rows and a RawSQL op is opaque, so neither
	// resolves in a manifest. The grammar is PINNED by the edge format:
	//
	//	Key{Kind:"dml", Name:"<edge-seq>"} -> "dml:<edge-seq>"
	//	Key{Kind:"raw", Name:"<edge-seq>"} -> "raw:<edge-seq>"
	//
	// where <edge-seq> is the op's zero-based position within its edge. The
	// label is per-edge-unique and cross-edge-meaningless (op 0 of any edge
	// renders "dml:0"), which is exactly what edge identity needs: it keeps
	// identical data edges byte-identical without pretending a data op names a
	// schema object. Pseudo-target keys are MANIFEST NO-OPS: they never appear
	// in a revision manifest and are never resolved by the consistency checker.
	KindDML Kind = "dml" // pseudo-target for data-manipulation ops (backfill/transform)
	KindRaw Kind = "raw" // pseudo-target for opaque RawSQL bodies (SM triggers, partman config)
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

// KeyForStateMachine returns the manifest key for a state-machine type.
func KeyForStateMachine(sm model.StateMachine) Key {
	return Key{Kind: KindSMType, Schema: sm.Schema, Name: sm.Name}
}

// ParseKey reconstructs a Key from its String() form — the inverse used by the
// on-disk revision manifest (roadmap 5.2, store_layout.md), whose entries are a
// sorted map keyed by Key.String(). It handles every OBJECT kind (the kinds that
// appear in a manifest); the pseudo-target kinds dml/raw NEVER appear in a
// manifest, so ParseKey rejects them as a hard error rather than silently
// admitting a data op into the schema-object namespace.
//
// Grammar (mirrors String):
//
//	schema:name                -> KindSchemaMeta
//	registry:                  -> KindRegistrySnap
//	function:schema.name(args) -> KindFunction (ArgSig = "(args)")
//	kind:schema.name           -> other kinds, schema-qualified
//	kind:name                  -> other kinds, no schema
//
// Object names are unquoted identifiers with no '.', so the qualifier splits on
// the sole '.' when present. ParseKey round-trips every KeyFor* construction:
// ParseKey(k.String()) == k for object kinds.
func ParseKey(s string) (Key, error) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return Key{}, fmt.Errorf("enc: malformed manifest key %q (no kind prefix)", s)
	}
	kind := Kind(s[:colon])
	rest := s[colon+1:]
	switch kind {
	case KindDML, KindRaw:
		return Key{}, fmt.Errorf("enc: pseudo-target key %q is not a manifest key (dml/raw never resolve in a manifest)", s)
	case KindSchemaMeta:
		return Key{Kind: kind, Name: rest}, nil
	case KindRegistrySnap:
		if rest != "" {
			return Key{}, fmt.Errorf("enc: malformed registry key %q (want %q)", s, "registry:")
		}
		return Key{Kind: kind}, nil
	case KindFunction:
		argSig := ""
		if lp := strings.IndexByte(rest, '('); lp >= 0 {
			if !strings.HasSuffix(rest, ")") {
				return Key{}, fmt.Errorf("enc: malformed function key %q (unterminated arg signature)", s)
			}
			argSig = rest[lp:]
			rest = rest[:lp]
		}
		schema, name := splitQualified(rest)
		return Key{Kind: kind, Schema: schema, Name: name, ArgSig: argSig}, nil
	case KindTable, KindView, KindMatView, KindSequence, KindEnum, KindDomain, KindComposite, KindSMType:
		schema, name := splitQualified(rest)
		return Key{Kind: kind, Schema: schema, Name: name}, nil
	default:
		return Key{}, fmt.Errorf("enc: unknown manifest key kind %q in %q", kind, s)
	}
}

// splitQualified splits a "schema.name" qualifier on its sole '.', returning
// ("", qualifier) when unqualified.
func splitQualified(q string) (schema, name string) {
	if dot := strings.IndexByte(q, '.'); dot >= 0 {
		return q[:dot], q[dot+1:]
	}
	return "", q
}

// KeyForDML mints the PINNED pseudo-target key for a data-manipulation op at
// the given zero-based edge sequence. It renders "dml:<seq>" and never resolves
// in a manifest (edge_format.md TENSION 2).
func KeyForDML(seq int) Key { return Key{Kind: KindDML, Name: strconv.Itoa(seq)} }

// KeyForRaw mints the PINNED pseudo-target key for an opaque RawSQL op at the
// given zero-based edge sequence. It renders "raw:<seq>" and never resolves in a
// manifest (edge_format.md TENSION 2).
func KeyForRaw(seq int) Key { return Key{Kind: KindRaw, Name: strconv.Itoa(seq)} }
