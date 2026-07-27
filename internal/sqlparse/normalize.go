package sqlparse

import (
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	pg_query "github.com/wasilibs/go-pgquery"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// N — the normalization primitive (roadmap 1.2, law L1).
//
// NormalizeExpr is the single canonical form for SQL scalar expressions. It is
// the SOLE ≈_syn computation in the codebase: every comparison engine (encoder,
// differ, validate's W018) routes expression comparison through ExprEqual /
// NormalizeExpr so that exactly one normalizer defines syntactic equivalence
// (L1(b) — a second, disagreeing normalizer is precisely what the law forbids).
//
// N works by parse -> AST folding -> deparse via go-pgquery, PLUS the
// catalog-INDEPENDENT foldings that go-pgquery's deparse does NOT perform for
// free (verified by the empirical deparse survey), applied to BOTH sides:
//
//   - IN <-> = ANY(ARRAY[...]): `x = ANY(ARRAY[a, b, c])` canonicalizes to
//     `x IN (a, b, c)`. One-sided rewriting is ruled out (L1(c)) — a user may
//     write PG's own = ANY form directly, so both spellings must converge.
//   - cast-type-name aliases: `x::integer` (which go-pgquery parses to
//     pg_catalog.int4 and deparses to `x::int`) and `x::int4` converge to
//     `x::int4` via the typeinfo alias map. Empirically NOT free from deparse.
//
// N is best-effort TOTAL: user SQL can be partial or unparseable (opaque
// leaves per L1). On any parse or deparse failure N returns the input verbatim
// (trimmed) rather than erroring — normalization never rejects input.
//
// N is homed here in internal/sqlparse (the go-pgquery leaf) and must never
// import model/diff/validate — those import N. The only extra dependency is
// typeinfo, itself a pure leaf.
//
// The set of foldings N performs is PINNED: the golden corpus regression
// fixtures pin N against pgdesign's own refactors, and the N-folding backlog
// XFAIL fixtures pin the KNOWN-MISSING foldings (asserting current
// non-convergence). Any change to N's output is an epoch event.

// NormalizeExpr returns the canonical (≈_syn-normal) form of a SQL scalar
// expression. On parse/deparse failure it returns the trimmed input verbatim.
func NormalizeExpr(expr string) string {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return ""
	}

	res, err := pg_query.Parse("SELECT " + trimmed)
	if err != nil {
		return trimmed
	}
	node := selectTargetExpr(res)
	if node == nil {
		return trimmed
	}

	foldExpr(node)

	out, err := DeparseExpr(node)
	if err != nil {
		return trimmed
	}
	return strings.TrimSpace(out)
}

// ExprEqual reports whether two SQL expressions are ≈_syn-equal: N(a) = N(b).
// This is the kernel of N and the single equality every comparison engine uses.
func ExprEqual(a, b string) bool {
	return NormalizeExpr(a) == NormalizeExpr(b)
}

// selectTargetExpr extracts the single target expression from a parsed
// `SELECT <expr>` statement, or nil if the shape is unexpected.
func selectTargetExpr(res *pg.ParseResult) *pg.Node {
	if res == nil || len(res.Stmts) != 1 {
		return nil
	}
	sel := res.Stmts[0].GetStmt().GetSelectStmt()
	if sel == nil || len(sel.TargetList) != 1 {
		return nil
	}
	rt := sel.TargetList[0].GetResTarget()
	if rt == nil {
		return nil
	}
	return rt.Val
}

// foldExpr applies the catalog-independent foldings to a node and recurses into
// every child node. It mutates the AST in place through the concrete node
// pointers; the generic protoreflect walk only serves to REACH every node.
func foldExpr(node *pg.Node) {
	if node == nil {
		return
	}
	switch inner := node.Node.(type) {
	case *pg.Node_AExpr:
		foldInAny(inner.AExpr)
	case *pg.Node_TypeCast:
		foldCastName(inner.TypeCast.GetTypeName())
	}
	recurseNodes(node)
}

// recurseNodes descends into every *pg.Node reachable from msg's fields,
// calling foldExpr on each. It uses protobuf reflection so that no AST node
// type is missed regardless of the expression's structure.
func recurseNodes(msg proto.Message) {
	m := msg.ProtoReflect()
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}
		if fd.IsList() {
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				handleChild(list.Get(i).Message().Interface())
			}
			return true
		}
		if fd.IsMap() {
			// pg AST has no message-valued maps in expression nodes; skip.
			return true
		}
		handleChild(v.Message().Interface())
		return true
	})
}

// handleChild dispatches a child message: a *pg.Node re-enters foldExpr (which
// applies foldings then recurses); any other message (a concrete node body like
// *pg.A_Expr) is recursed into directly.
func handleChild(child proto.Message) {
	if n, ok := child.(*pg.Node); ok {
		foldExpr(n)
		return
	}
	recurseNodes(child)
}

// foldInAny rewrites `<lexpr> = ANY(ARRAY[...])` into the IN-list form
// `<lexpr> IN (...)`, the canonical spelling. Only the equality/ANY case is
// folded: `<> ALL` (NOT IN's ANY-form) is deliberately left alone — its
// convergence with NOT IN is a KNOWN-MISSING folding pinned by the backlog.
func foldInAny(ae *pg.A_Expr) {
	if ae == nil || ae.Kind != pg.A_Expr_Kind_AEXPR_OP_ANY {
		return
	}
	if len(ae.Name) != 1 || ae.Name[0].GetString_().GetSval() != "=" {
		return
	}
	arr := ae.Rexpr.GetAArrayExpr()
	if arr == nil {
		return
	}
	ae.Kind = pg.A_Expr_Kind_AEXPR_IN
	ae.Rexpr = &pg.Node{Node: &pg.Node_List{List: &pg.List{Items: arr.Elements}}}
}

// foldCastName canonicalizes a cast's type name through the typeinfo alias map
// so pg-internal aliases converge. go-pgquery parses `integer` to
// `pg_catalog.int4` (deparsing to `int`) while `int4` stays bare (deparsing to
// `int4`) — divergent forms this fold collapses to the canonical short name.
//
// Only bare names and pg_catalog-qualified names are rewritten. Schema-qualified
// user types (e.g. myschema.mytype) are left untouched. Type modifiers and
// array bounds live in separate fields and are preserved.
func foldCastName(tn *pg.TypeName) {
	if tn == nil {
		return
	}
	var base string
	switch len(tn.Names) {
	case 1:
		base = tn.Names[0].GetString_().GetSval()
	case 2:
		if tn.Names[0].GetString_().GetSval() != "pg_catalog" {
			return
		}
		base = tn.Names[1].GetString_().GetSval()
	default:
		return
	}
	if base == "" {
		return
	}
	canonical := typeinfo.Parse(base).Base
	if canonical == "" {
		return
	}
	tn.Names = []*pg.Node{strNode(canonical)}
}

// strNode wraps a string as a pg String node.
func strNode(s string) *pg.Node {
	return &pg.Node{Node: &pg.Node_String_{String_: &pg.String{Sval: s}}}
}
