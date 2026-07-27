package predicate

import (
	"context"
	"fmt"
	"strconv"

	"github.com/smm-h/pgdesign/internal/catalog"
)

// Result is the outcome of evaluating a precondition. OK is true when the world
// satisfies it. When OK is false, Object/Expected/Found name the violation
// precisely for a hard error.
type Result struct {
	OK       bool
	Object   string
	Expected string
	Found    string
}

// Err renders a violated precondition as an error, naming object/expected/found.
// It returns nil when OK.
func (r Result) Err() error {
	if r.OK {
		return nil
	}
	return fmt.Errorf("precondition: %s: expected %s, found %s", r.Object, r.Expected, r.Found)
}

// Check evaluates a precondition against the live catalog via the shared query
// layer. A DB/query failure returns a non-nil error; a violated precondition is
// Result{OK:false,...} (not an error), so the caller decides the policy (hard
// error for the migrate executor). Matching comparisons use EXACT equality on the
// catalog's own rendering — the same comparison the SQL renderer emits — so the
// two backends are conformant by construction; producing catalog-comparable
// expected values is the caller's responsibility.
func Check(ctx context.Context, q catalog.Querier, p Precondition) (Result, error) {
	present, found, err := probe(ctx, q, p)
	if err != nil {
		return Result{}, err
	}

	switch p.Existence {
	case MustBeAbsent:
		if present {
			return Result{OK: false, Object: p.object(), Expected: "absent", Found: "present"}, nil
		}
		return Result{OK: true}, nil
	case MustBePresent:
		if !present {
			return Result{OK: false, Object: p.object(), Expected: "present", Found: "absent"}, nil
		}
		if p.Match != nil {
			return matchResult(p, found), nil
		}
		return Result{OK: true}, nil
	default:
		return Result{}, fmt.Errorf("predicate: unknown existence %d", p.Existence)
	}
}

// foundState holds the catalog attributes probe collected for a present object,
// used by present-and-matching comparison.
type foundState struct {
	col   *catalog.ColumnInfo
	con   string // constraint def
	index *catalog.IndexInfo
}

// probe reports whether the object exists and gathers its attributes (for
// present-and-matching). Existence for a Match-bearing precondition consults the
// same probe that fetched the attributes, so present/found are consistent.
func probe(ctx context.Context, q catalog.Querier, p Precondition) (bool, foundState, error) {
	switch p.Class {
	case ClassTable:
		ok, err := catalog.TableExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassView:
		ok, err := catalog.ViewExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassMatView:
		ok, err := catalog.MatViewExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassSequence:
		ok, err := catalog.SequenceExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassEnum:
		ok, err := catalog.EnumExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassDomain:
		ok, err := catalog.DomainExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassComposite:
		ok, err := catalog.CompositeExists(ctx, q, p.Schema, p.Name)
		return ok, foundState{}, err
	case ClassFunction:
		ok, err := catalog.FunctionExists(ctx, q, p.Schema, p.Name, p.ArgSig)
		return ok, foundState{}, err
	case ClassExtension:
		ok, err := catalog.ExtensionExists(ctx, q, p.Name)
		return ok, foundState{}, err
	case ClassEnumValue:
		ok, err := catalog.EnumHasValue(ctx, q, p.Schema, p.Name, p.Value)
		return ok, foundState{}, err
	case ClassTrigger:
		ok, err := catalog.TriggerExists(ctx, q, p.Schema, p.Table, p.Name)
		return ok, foundState{}, err
	case ClassPolicy:
		ok, err := catalog.PolicyExists(ctx, q, p.Schema, p.Table, p.Name)
		return ok, foundState{}, err
	case ClassColumn:
		info, ok, err := catalog.Column(ctx, q, p.PGVersion, p.Schema, p.Table, p.Name)
		return ok, foundState{col: info}, err
	case ClassConstraint:
		def, ok, err := catalog.ConstraintDef(ctx, q, p.Schema, p.Table, p.Name)
		return ok, foundState{con: def}, err
	case ClassIndex:
		info, ok, err := catalog.Index(ctx, q, p.Schema, p.Name)
		return ok, foundState{index: info}, err
	default:
		return false, foundState{}, fmt.Errorf("predicate: unknown class %q", p.Class)
	}
}

// matchResult compares a present object's attributes against p.Match. The first
// mismatching attribute produces the precise Result; all-match yields OK.
func matchResult(p Precondition, f foundState) Result {
	m := p.Match
	obj := p.object()
	switch p.Class {
	case ClassColumn:
		if f.col == nil {
			return Result{OK: false, Object: obj, Expected: "attributes", Found: "none"}
		}
		if m.ColumnType != "" && f.col.Type != m.ColumnType {
			return Result{OK: false, Object: obj + " type", Expected: m.ColumnType, Found: f.col.Type}
		}
		if m.ColumnNotNull != nil && f.col.NotNull != *m.ColumnNotNull {
			return Result{OK: false, Object: obj + " nullability", Expected: notNullStr(*m.ColumnNotNull), Found: notNullStr(f.col.NotNull)}
		}
		if m.ColumnDefault != nil && f.col.Default != *m.ColumnDefault {
			return Result{OK: false, Object: obj + " default", Expected: quoteEmpty(*m.ColumnDefault), Found: quoteEmpty(f.col.Default)}
		}
		return Result{OK: true}
	case ClassConstraint:
		if m.ConstraintDef != "" && f.con != m.ConstraintDef {
			return Result{OK: false, Object: obj, Expected: m.ConstraintDef, Found: f.con}
		}
		return Result{OK: true}
	case ClassIndex:
		if f.index == nil {
			return Result{OK: false, Object: obj, Expected: "definition", Found: "none"}
		}
		if m.IndexMustBeValid && !f.index.Valid {
			return Result{OK: false, Object: obj + " validity", Expected: "valid", Found: "invalid"}
		}
		if m.IndexDef != "" && f.index.Def != m.IndexDef {
			return Result{OK: false, Object: obj, Expected: m.IndexDef, Found: f.index.Def}
		}
		return Result{OK: true}
	default:
		// No match semantics for this class: existence already satisfied.
		return Result{OK: true}
	}
}

func notNullStr(b bool) string {
	if b {
		return "NOT NULL"
	}
	return "nullable"
}

func quoteEmpty(s string) string {
	if s == "" {
		return "(no default)"
	}
	return strconv.Quote(s)
}
