package systemcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const importPath = "github.com/alexgorbatchev/engine-cli/internal/systemcheck"

// Only composition roots observe the machine. Each declares
// `var querySystem = systemcheck.Run`, and its TestMain replaces it; library
// packages take the caller's check instead. Any other call to Run would make
// that path's tests depend on whether Engine DJ Desktop runs on the machine.
func TestOnlyStubbedCompositionRootsObserveDesktop(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found from the package directory: %v", err)
	}
	roots, violations := scanDesktopQueries(t, root)
	for _, dir := range []string{"cmd/engine-cli", "internal/api"} {
		if !slices.Contains(roots, dir) {
			t.Fatalf("scan of %s missed the known composition root %s; found %v", root, dir, roots)
		}
	}
	for _, dir := range roots {
		if !testMainAssigns(t, filepath.Join(root, dir), "querySystem") {
			violations = append(violations, dir+": TestMain does not replace querySystem, so its tests observe the machine")
		}
	}
	if len(violations) > 0 {
		t.Fatalf("Engine DJ Desktop observed outside an injected, stubbed querySystem:\n%s", strings.Join(violations, "\n"))
	}
}

// scanDesktopQueries returns the package directories that declare the injectable
// querySystem and every other reference to systemcheck.Run.
func scanDesktopQueries(t *testing.T, root string) (roots, violations []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		name := importName(f)
		if name == "" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		injected := map[ast.Expr]bool{}
		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				v := spec.(*ast.ValueSpec)
				if len(v.Names) == 1 && v.Names[0].Name == "querySystem" && len(v.Values) == 1 && isRun(v.Values[0], name) {
					injected[v.Values[0]] = true
					if !slices.Contains(roots, rel) {
						roots = append(roots, rel)
					}
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok && isRun(e, name) && !injected[e] {
				violations = append(violations, fmt.Sprintf("%s/%s:%d: calls systemcheck.Run directly", rel, filepath.Base(path), fset.Position(e.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return roots, violations
}

func importName(f *ast.File) string {
	for _, spec := range f.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err != nil || path != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "systemcheck"
	}
	return ""
}

func isRun(e ast.Expr, name string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != "Run" {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == name
}

// testMainAssigns reports whether the package's TestMain, or a function it
// calls directly, assigns the named variable.
func testMainAssigns(t *testing.T, dir, variable string) bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, path := range paths {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Body != nil {
				funcs[fn.Name.Name] = fn
			}
		}
	}
	main, ok := funcs["TestMain"]
	if !ok {
		return false
	}
	bodies := []*ast.BlockStmt{main.Body}
	ast.Inspect(main.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && funcs[id.Name] != nil {
				bodies = append(bodies, funcs[id.Name].Body)
			}
		}
		return true
	})
	for _, body := range bodies {
		assigned := false
		ast.Inspect(body, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok {
				for _, lhs := range a.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == variable {
						assigned = true
					}
				}
			}
			return !assigned
		})
		if assigned {
			return true
		}
	}
	return false
}
