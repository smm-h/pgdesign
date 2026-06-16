package codegen

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

// TestNodeTypeExhaustiveness verifies that every sqlexpr.Node type is either
// handled in codegen's analysis code or explicitly listed as knownUnhandled.
// This catches new node types added to sqlexpr that codegen should consider.
func TestNodeTypeExhaustiveness(t *testing.T) {
	// Node types that codegen intentionally does not handle.
	// These are value literals or expression types with no corresponding
	// codegen pattern -- add a comment explaining why when adding entries.
	knownUnhandled := map[string]bool{
		"StringLiteral": true, // value literal, not a structural pattern
		"IntLiteral":    true, // value literal, not a structural pattern
		"FloatLiteral":  true, // value literal, not a structural pattern
		"NullLiteral":   true, // value literal, not a structural pattern
		"CaseExpr":      true, // no codegen patterns for CASE expressions yet
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	codegenDir := filepath.Dir(thisFile)
	sqlexprDir := filepath.Join(codegenDir, "..", "sqlexpr")

	// Discover all sqlexpr.Node implementors.
	nodeTypes := findSqlexprNodeTypes(t, sqlexprDir)
	if len(nodeTypes) == 0 {
		t.Fatal("found no Node types in sqlexpr package")
	}

	// Discover all sqlexpr types referenced in codegen's type assertions.
	handledTypes := findCodegenHandledTypes(t, codegenDir)

	// Every node type must be either handled or in knownUnhandled.
	for _, nt := range nodeTypes {
		if handledTypes[nt] {
			continue
		}
		if knownUnhandled[nt] {
			continue
		}
		t.Errorf("sqlexpr.Node type %q is neither handled in codegen analysis nor listed in knownUnhandled", nt)
	}

	// Verify knownUnhandled entries are still valid node types (catch stale entries).
	nodeTypeSet := make(map[string]bool, len(nodeTypes))
	for _, nt := range nodeTypes {
		nodeTypeSet[nt] = true
	}
	for name := range knownUnhandled {
		if !nodeTypeSet[name] {
			t.Errorf("knownUnhandled entry %q is not a sqlexpr.Node type (stale entry?)", name)
		}
	}
}

// findSqlexprNodeTypes parses the sqlexpr package and returns all type names
// that implement the Node interface (have a nodeType() method).
func findSqlexprNodeTypes(t *testing.T, pkgDir string) []string {
	t.Helper()
	fset := gotoken.NewFileSet()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("reading sqlexpr dir: %v", err)
	}

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

// findCodegenHandledTypes parses all non-test Go files in the codegen package
// and collects all sqlexpr types that codegen works with. This includes:
// - type assertion targets of the form *sqlexpr.TypeName
// - function parameter types of the form *sqlexpr.TypeName
func findCodegenHandledTypes(t *testing.T, pkgDir string) map[string]bool {
	t.Helper()
	fset := gotoken.NewFileSet()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("reading codegen dir: %v", err)
	}

	result := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := goparser.ParseFile(fset, filepath.Join(pkgDir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		goast.Inspect(f, func(n goast.Node) bool {
			switch node := n.(type) {
			case *goast.TypeAssertExpr:
				// Look for node.(*sqlexpr.TypeName)
				collectStarSqlexpr(node.Type, result)
			case *goast.FuncDecl:
				// Look for function parameters of the form *sqlexpr.TypeName
				if node.Type.Params != nil {
					for _, field := range node.Type.Params.List {
						collectStarSqlexpr(field.Type, result)
					}
				}
			}
			return true
		})
	}
	return result
}

// collectStarSqlexpr checks if expr is *sqlexpr.TypeName and adds TypeName to the set.
func collectStarSqlexpr(expr goast.Expr, result map[string]bool) {
	star, ok := expr.(*goast.StarExpr)
	if !ok {
		return
	}
	sel, ok := star.X.(*goast.SelectorExpr)
	if !ok {
		return
	}
	pkg, ok := sel.X.(*goast.Ident)
	if !ok || pkg.Name != "sqlexpr" {
		return
	}
	result[sel.Sel.Name] = true
}
