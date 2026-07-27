package sqlparse

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// goldenCorpusPath is the committed regression fixture pinning N's normalized
// forms against pgdesign's OWN refactors of internal/sqlparse (L9's golden
// corpus, boundary item 12). A change to N that shifts any normalized form
// turns this test red — the change must then be reverted or handled as a
// deliberate epoch event. Regenerate deliberately with UPDATE_GOLDEN=1.
const goldenCorpusPath = "testdata/golden_corpus.tsv"

// TestGoldenCorpus pins N against every committed input -> normalized pair.
func TestGoldenCorpus(t *testing.T) {
	data, err := os.ReadFile(goldenCorpusPath)
	if err != nil {
		t.Fatalf("read golden corpus: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		var b strings.Builder
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			in := strings.SplitN(line, "\t", 2)[0]
			fmt.Fprintf(&b, "%s\t%s\n", in, NormalizeExpr(in))
		}
		if err := os.WriteFile(goldenCorpusPath, []byte(b.String()), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Log("golden corpus updated")
		return
	}

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("line %d malformed (want input<TAB>expected): %q", i+1, line)
		}
		in, want := parts[0], parts[1]
		if got := NormalizeExpr(in); got != want {
			t.Errorf("N(%q) = %q, golden = %q\n(if this is a deliberate N/epoch change, rerun with UPDATE_GOLDEN=1)", in, got, want)
		}
	}
}

// backlogEntry is one KNOWN-MISSING catalog-independent folding.
type backlogEntry struct {
	folding string
	a, b    string
}

// nFoldingBacklog is the negative-space companion to the golden corpus (L9): one
// entry per KNOWN-MISSING catalog-independent folding. Each asserts that the two
// spellings CURRENTLY DO NOT converge under N. If deparse or an N refactor ever
// starts converging one, TestNFoldingBacklog turns red (red-on-convergence
// semantics) and the entry graduates — folded into N only at an epoch event.
// Zero runtime code: these are documentation-as-tests, identity-safe.
var nFoldingBacklog = []backlogEntry{
	{"NOT IN <-> <> ALL", "x NOT IN (1, 2, 3)", "x <> ALL(ARRAY[1, 2, 3])"},
	{"single-element IN <-> =", "x IN (1)", "x = 1"},
	{"BETWEEN <-> pair of comparisons", "x BETWEEN 1 AND 10", "x >= 1 AND x <= 10"},
	{"LIKE <-> ~~", "name LIKE 'a%'", "name ~~ 'a%'"},
	{"boolean redundancy", "flag = true", "flag"},
	{"numeric-literal forms", "x = 1", "x = 1.0"},
	{"COALESCE <-> CASE", "coalesce(a, b)", "CASE WHEN a IS NOT NULL THEN a ELSE b END"},
	{"commutative ordering", "a AND b", "b AND a"},
}

// TestNFoldingBacklog asserts every backlog folding is still MISSING. A failure
// here means a folding started converging — investigate: either N gained the
// fold (graduate the entry, epoch event) or a dependency shifted behavior.
func TestNFoldingBacklog(t *testing.T) {
	for _, e := range nFoldingBacklog {
		if ExprEqual(e.a, e.b) {
			t.Errorf("BACKLOG FOLDING GRADUATED [%s]: %q and %q now converge to %q — this is an epoch event; graduate the entry and rebuild the golden corpus deliberately",
				e.folding, e.a, e.b, NormalizeExpr(e.a))
		}
	}
}

// The template grammar below is a small generator over well-formed SQL scalar
// predicates. modelgen (kernel 1.6) is not built yet, and per its KEY SCOPE
// FACT the core generator needs no SQL generator — only this local template
// grammar serves the 1.2 expression corpus. It feeds the N∘N = N idempotence
// property (L9) with structurally varied, randomly-generated input.
//
// It deliberately stays within the WELL-FORMED fragment (boolean predicates
// composed of comparisons over scalar atoms; IN/= ANY and casts applied only to
// atoms). Type-invalid shapes such as `(a = b) IN (1,2,3)` — a boolean used as
// the operand of IN — are excluded: go-pgquery's own deparse drops the parens
// such shapes need and even emits non-reparseable text, a limitation of the
// pinned parser (boundary item 12), not an N defect. Real schema expressions
// never take that shape, and N's idempotence law is over the well-formed forms.

// genAtom produces a scalar (non-boolean) atom.
func genAtom(rng *rand.Rand, depth int) string {
	atoms := []string{"x", "y", "status", "amount", "42", "'active'", "length(name)", "created_at"}
	if depth <= 0 {
		return atoms[rng.Intn(len(atoms))]
	}
	switch rng.Intn(4) {
	case 0:
		casts := []string{"int4", "integer", "bool", "numeric(10, 2)", "text", "int8"}
		return fmt.Sprintf("%s::%s", genAtom(rng, depth-1), casts[rng.Intn(len(casts))])
	case 1:
		return fmt.Sprintf("coalesce(%s, %s)", genAtom(rng, depth-1), genAtom(rng, depth-1))
	default:
		return genAtom(rng, 0)
	}
}

// genPred produces a boolean predicate.
func genPred(rng *rand.Rand, depth int) string {
	if depth <= 0 {
		cmp := []string{"=", "<>", "<", ">=", "!="}
		return fmt.Sprintf("%s %s %s", genAtom(rng, 1), cmp[rng.Intn(len(cmp))], genAtom(rng, 1))
	}
	switch rng.Intn(9) {
	case 0:
		return fmt.Sprintf("%s AND %s", genPred(rng, depth-1), genPred(rng, depth-1))
	case 1:
		return fmt.Sprintf("%s OR %s", genPred(rng, depth-1), genPred(rng, depth-1))
	case 2:
		return fmt.Sprintf("(%s)", genPred(rng, depth-1))
	case 3:
		return fmt.Sprintf("NOT %s", genPred(rng, depth-1))
	case 4:
		return fmt.Sprintf("%s IN (1, 2, 3)", genAtom(rng, 1))
	case 5:
		return fmt.Sprintf("%s = ANY(ARRAY[1, 2, 3])", genAtom(rng, 1))
	case 6:
		return fmt.Sprintf("%s IS NULL", genAtom(rng, 1))
	case 7:
		return fmt.Sprintf("%s IS NOT NULL", genAtom(rng, 1))
	default:
		cmp := []string{"=", "<>", "<", ">=", "!="}
		return fmt.Sprintf("%s %s %s", genAtom(rng, 1), cmp[rng.Intn(len(cmp))], genAtom(rng, 1))
	}
}

// TestNIdempotenceGenerated is L9's N∘N = N property over a generated
// expression corpus.
func TestNIdempotenceGenerated(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		e := genPred(rng, 4)
		once := NormalizeExpr(e)
		twice := NormalizeExpr(once)
		if once != twice {
			t.Fatalf("N not idempotent:\n  in:   %q\n  N:    %q\n  N∘N:  %q", e, once, twice)
		}
	}
}
