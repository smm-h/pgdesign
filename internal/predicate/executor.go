package predicate

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/smm-h/pgdesign/internal/catalog"
)

// Execer is the DB surface the Go executor needs. It extends the read-only
// catalog.Querier with Exec — the definitional-body round-trip (ClassConstraint
// def, ClassColumn default) builds and drops a throwaway temp object to canonicalize
// the MODEL text through the live DB (roadmap 5.5+5.7 matching-strategy resolution).
// Both *pgx.Conn and pgx.Tx satisfy it, so the precondition runs in whatever
// transactional scope the apply loop opened.
type Execer interface {
	catalog.Querier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

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
// error for the migrate executor).
//
// Matching comparisons are LIVE-PATH: column TYPES compare by OID via to_regtype
// (alias-robust), definitional BODIES (constraint def, column default) round-trip
// the model text through the DB and compare PG's own canonical form, and not-null /
// index-validity are booleans. This is the same strategy the SQL renderer emits, so
// the two backends are conformant by construction.
func Check(ctx context.Context, q Execer, p Precondition) (Result, error) {
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
			return matchResult(ctx, q, p, found)
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
	con   string // constraint def (pg_get_constraintdef — canonical PG form)
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

// matchResult compares a present object's attributes against p.Match with the
// per-class strategy. The first mismatching attribute produces the precise Result;
// all-match yields OK. It needs the Execer for the definitional-body round-trips.
func matchResult(ctx context.Context, q Execer, p Precondition, f foundState) (Result, error) {
	m := p.Match
	obj := p.object()
	switch p.Class {
	case ClassColumn:
		if f.col == nil {
			return Result{OK: false, Object: obj, Expected: "attributes", Found: "none"}, nil
		}
		if m.ColumnType != "" {
			ok, _, foundType, err := catalog.ColumnTypeMatches(ctx, q, p.Schema, p.Table, p.Name, m.ColumnType)
			if err != nil {
				return Result{}, err
			}
			if !ok {
				return Result{OK: false, Object: obj + " type", Expected: m.ColumnType, Found: foundType}, nil
			}
		}
		if m.ColumnNotNull != nil && f.col.NotNull != *m.ColumnNotNull {
			return Result{OK: false, Object: obj + " nullability", Expected: notNullStr(*m.ColumnNotNull), Found: notNullStr(f.col.NotNull)}, nil
		}
		if m.ColumnDefault != nil {
			if *m.ColumnDefault == "" {
				if f.col.Default != "" {
					return Result{OK: false, Object: obj + " default", Expected: "(no default)", Found: quoteEmpty(f.col.Default)}, nil
				}
			} else {
				canonical, err := roundTripDefault(ctx, q, p.Schema, p.Table, p.Name, *m.ColumnDefault)
				if err != nil {
					return Result{}, err
				}
				if f.col.Default != canonical {
					return Result{OK: false, Object: obj + " default", Expected: quoteEmpty(canonical), Found: quoteEmpty(f.col.Default)}, nil
				}
			}
		}
		return Result{OK: true}, nil
	case ClassConstraint:
		if m.ConstraintDef != "" {
			canonical, err := roundTripConstraint(ctx, q, p.Schema, p.Table, m.ConstraintDef)
			if err != nil {
				return Result{}, err
			}
			if f.con != canonical {
				return Result{OK: false, Object: obj, Expected: canonical, Found: f.con}, nil
			}
		}
		return Result{OK: true}, nil
	case ClassIndex:
		if f.index == nil {
			return Result{OK: false, Object: obj, Expected: "definition", Found: "none"}, nil
		}
		if m.IndexMustBeValid && !f.index.Valid {
			return Result{OK: false, Object: obj + " validity", Expected: "valid", Found: "invalid"}, nil
		}
		return Result{OK: true}, nil
	default:
		// No match semantics for this class: existence already satisfied.
		return Result{OK: true}, nil
	}
}

// tempName is the fixed name for a per-check throwaway round-trip table. It is
// dropped within each round-trip (and any failure rolls the enclosing statement
// back), so reuse across sequential checks in one session is safe.
const tempName = "_pgd_pre_rt"

// roundTripConstraint canonicalizes a MODEL constraint clause (e.g.
// "CHECK (age >= 0)") through the live DB: it clones schema.table into a temp
// table, adds the clause as a constraint, reads PG's pg_get_constraintdef, and
// drops the temp table. The returned text is directly comparable to the real
// constraint's pg_get_constraintdef — so equivalent spellings do NOT false-drift
// and genuine differences do. The owning table must exist (the constraint's
// existence precondition guarantees it).
func roundTripConstraint(ctx context.Context, q Execer, schema, table, def string) (string, error) {
	src := qualIdent(schema, table)
	if _, err := q.Exec(ctx, "DROP TABLE IF EXISTS "+quoteIdent(tempName)); err != nil {
		return "", fmt.Errorf("predicate: round-trip cleanup: %w", err)
	}
	if _, err := q.Exec(ctx, fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s)", quoteIdent(tempName), src)); err != nil {
		return "", fmt.Errorf("predicate: round-trip clone %s: %w", src, err)
	}
	defer func() { _, _ = q.Exec(ctx, "DROP TABLE IF EXISTS "+quoteIdent(tempName)) }()
	if _, err := q.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", quoteIdent(tempName), quoteIdent("_pgd_c"), def)); err != nil {
		return "", fmt.Errorf("predicate: round-trip constraint %q: %w", def, err)
	}
	var canonical string
	err := q.QueryRow(ctx, `
		SELECT pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class r ON r.oid = con.conrelid
		WHERE r.relname = $1 AND con.conname = '_pgd_c' AND r.relnamespace = pg_my_temp_schema()`,
		tempName).Scan(&canonical)
	if err != nil {
		return "", fmt.Errorf("predicate: round-trip read constraint def: %w", err)
	}
	return canonical, nil
}

// roundTripDefault canonicalizes a MODEL column-default expression through the live
// DB: it clones schema.table into a temp table (the target column keeps its real
// type, so cast materialization matches), sets the expected default, reads
// pg_get_expr, and drops the temp table. The returned text is directly comparable
// to the real column's pg_get_expr default.
func roundTripDefault(ctx context.Context, q Execer, schema, table, column, def string) (string, error) {
	src := qualIdent(schema, table)
	if _, err := q.Exec(ctx, "DROP TABLE IF EXISTS "+quoteIdent(tempName)); err != nil {
		return "", fmt.Errorf("predicate: round-trip cleanup: %w", err)
	}
	if _, err := q.Exec(ctx, fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s)", quoteIdent(tempName), src)); err != nil {
		return "", fmt.Errorf("predicate: round-trip clone %s: %w", src, err)
	}
	defer func() { _, _ = q.Exec(ctx, "DROP TABLE IF EXISTS "+quoteIdent(tempName)) }()
	if _, err := q.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s", quoteIdent(tempName), quoteIdent(column), def)); err != nil {
		return "", fmt.Errorf("predicate: round-trip default %q: %w", def, err)
	}
	var canonical string
	err := q.QueryRow(ctx, `
		SELECT COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '')
		FROM pg_attribute a
		JOIN pg_class r ON r.oid = a.attrelid
		LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
		WHERE r.relname = $1 AND a.attname = $2 AND r.relnamespace = pg_my_temp_schema()`,
		tempName, column).Scan(&canonical)
	if err != nil {
		return "", fmt.Errorf("predicate: round-trip read default: %w", err)
	}
	return canonical, nil
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
