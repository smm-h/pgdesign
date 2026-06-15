package sqlexpr

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWalkExhaustiveness(t *testing.T) {
	// Find the package directory from this test file's location
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	pkgDir := filepath.Dir(thisFile)

	// Parse ast.go to find all types that implement Node
	nodeTypes := findNodeTypes(t, pkgDir)
	if len(nodeTypes) == 0 {
		t.Fatal("found no Node types in ast.go")
	}

	// Parse walk.go to find all types in Walk's type switch
	walkedTypes := findWalkedTypes(t, pkgDir)

	// Every Node type must appear in Walk
	for _, nt := range nodeTypes {
		if !walkedTypes[nt] {
			t.Errorf("Node type %q implements Node but is not handled in Walk's type switch", nt)
		}
	}
}

// findNodeTypes parses all non-test .go files and finds all struct types
// that have a method named "nodeType" (which satisfies the Node interface).
func findNodeTypes(t *testing.T, pkgDir string) []string {
	t.Helper()

	fset := gotoken.NewFileSet()

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	// Collect all types that have a nodeType() method
	typesWithNodeType := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := goparser.ParseFile(fset, filepath.Join(pkgDir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "nodeType" {
				continue
			}
			// Extract the receiver type name
			for _, field := range fn.Recv.List {
				switch rt := field.Type.(type) {
				case *goast.StarExpr:
					if ident, ok := rt.X.(*goast.Ident); ok {
						typesWithNodeType[ident.Name] = true
					}
				case *goast.Ident:
					typesWithNodeType[rt.Name] = true
				}
			}
		}
	}

	var result []string
	for name := range typesWithNodeType {
		result = append(result, name)
	}
	return result
}

// findWalkedTypes parses walk.go and extracts all type names from the
// type switch in the Walk function.
func findWalkedTypes(t *testing.T, pkgDir string) map[string]bool {
	t.Helper()

	fset := gotoken.NewFileSet()
	f, err := goparser.ParseFile(fset, filepath.Join(pkgDir, "walk.go"), nil, 0)
	if err != nil {
		t.Fatalf("parsing walk.go: %v", err)
	}

	result := make(map[string]bool)

	goast.Inspect(f, func(n goast.Node) bool {
		// Look for type switch statements
		ts, ok := n.(*goast.TypeSwitchStmt)
		if !ok {
			return true
		}
		// Check each case clause
		for _, stmt := range ts.Body.List {
			cc, ok := stmt.(*goast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range cc.List {
				// Cases are *TypeName, so look for StarExpr -> Ident
				if star, ok := expr.(*goast.StarExpr); ok {
					if ident, ok := star.X.(*goast.Ident); ok {
						result[ident.Name] = true
					}
				}
			}
		}
		return true
	})

	return result
}
