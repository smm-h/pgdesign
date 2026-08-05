package test

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSecondNormalizer enforces roadmap 1.2's boundary: N (≈_syn) is computed
// in exactly ONE place, internal/sqlparse. L1(b) forbids a second ≈-computation;
// two disagreeing normalizers shipped before 1.2 (diff's normalizeDefault and
// validate's normalizeExpr) and were retired. This guard prevents their return.
//
// Two mechanisms:
//
//  1. The go-pgquery LEAF guard: only internal/sqlparse may import go-pgquery.
//     Everything that needs parse/deparse routes through sqlparse. There is one
//     PRINCIPLED EXCEPTION — internal/introspect, which uses the go-pgquery AST
//     for STRUCTURAL extraction (decomposing pg_get_indexdef output into
//     columns/opclasses/collations), not for ≈-computation. The other named
//     structural-extraction engine, internal/sqlexpr (E213 column refs,
//     CHECK-pattern extraction), does not import go-pgquery at all.
//
//  2. The ≈-entry-point guard: NormalizeExpr and ExprEqual are defined only in
//     internal/sqlparse, and the retired normalizer internals (canonicalString,
//     collapseWhitespace) appear nowhere.
func TestNoSecondNormalizer(t *testing.T) {
	testenv.Isolate(t)
	repoRoot := filepath.Join("..", "..")

	// Packages permitted to import go-pgquery directly. sqlparse IS N's home;
	// introspect is the documented structural-extraction exception.
	pgqueryLeafAllowed := map[string]bool{
		filepath.Join("internal", "sqlparse"):   true,
		filepath.Join("internal", "introspect"): true,
	}

	// Functions that compute/expose ≈ and must live only in sqlparse.
	exprEqualityFuncs := map[string]bool{
		"NormalizeExpr": true,
		"ExprEqual":     true,
	}
	// Retired second-normalizer internals that must never reappear.
	bannedFuncs := map[string]bool{
		"canonicalString":    true,
		"collapseWhitespace": true,
	}

	fset := token.NewFileSet()
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == "testdata" || base == "vendor" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, _ := filepath.Rel(repoRoot, path)
		pkgDir := filepath.Dir(rel)

		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if perr != nil {
			return nil // non-buildable stray file; skip
		}

		// Import guard.
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(p, "go-pgquery") || strings.Contains(p, "pg_query_go") {
				if !pgqueryLeafAllowed[pkgDir] {
					t.Errorf("%s imports go-pgquery (%s) outside the sqlparse leaf; route through internal/sqlparse (N)", rel, p)
				}
			}
		}

		// Function-definition guard needs full parse.
		ff, ferr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if ferr != nil {
			return nil
		}
		for _, decl := range ff.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			if bannedFuncs[name] {
				t.Errorf("%s defines retired normalizer %q — a second ≈-computation is forbidden (L1(b))", rel, name)
			}
			if exprEqualityFuncs[name] && pkgDir != filepath.Join("internal", "sqlparse") {
				t.Errorf("%s defines %q outside internal/sqlparse — N has exactly one home", rel, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}
