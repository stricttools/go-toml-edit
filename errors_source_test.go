// The test lives in the external test package for the reason api_snapshot_test
// gives: the package's own identifiers include `parser`, `token` and `lexer`,
// which would collide with the go/parser and go/token imports these source
// walks need.
package tomledit_test

// Two structural guards over the diagnostic contract:
//
//   - every *Error and *Errors in the package is built in errors.go, so that
//     no error site can skip the constructor and forget the fields its kind is
//     documented to carry;
//   - the kind set and the sentinel set stay in step with each other and with
//     the table below.

import (
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tomledit "github.com/stricttools/go-toml-edit"
)

// diagnosticFile is the one file allowed to construct diagnostics.
const diagnosticFile = "errors.go"

// diagnosticTypes are the types whose construction is restricted.
var diagnosticTypes = map[string]bool{"Error": true, "Errors": true}

// packageFiles parses every non-test Go source of the package in this
// directory.
func packageFiles(t *testing.T) (*gotoken.FileSet, map[string]*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := gotoken.NewFileSet()
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, filepath.Join(".", name), nil, goparser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = file
	}
	if len(files) == 0 {
		t.Fatalf("no non-test Go sources found")
	}
	return fset, files
}

// Fails if any file other than errors.go composes a diagnostic itself --
// through a composite literal (named, or written with its type elided inside a
// container literal) or new() -- instead of routing through the constructor
// there.
func TestDiagnosticsBuiltInOneFile(t *testing.T) {
	fset, files := packageFiles(t)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if name == diagnosticFile {
			continue
		}
		ast.Inspect(files[name], func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if node.Type == nil {
					// An elided literal; reached from its enclosing typed
					// literal below, which is what names its type.
					return true
				}
				if id, ok := node.Type.(*ast.Ident); ok && diagnosticTypes[id.Name] {
					t.Errorf("%s: composite literal of %s outside %s -- build diagnostics through the constructor in %s",
						fset.Position(node.Pos()), id.Name, diagnosticFile, diagnosticFile)
				}
				checkElidedElements(t, fset, node, node.Type)
			case *ast.CallExpr:
				fn, ok := node.Fun.(*ast.Ident)
				if !ok || fn.Name != "new" || len(node.Args) != 1 {
					return true
				}
				if id, ok := node.Args[0].(*ast.Ident); ok && diagnosticTypes[id.Name] {
					t.Errorf("%s: new(%s) outside %s -- build diagnostics through the constructor in %s",
						fset.Position(node.Pos()), id.Name, diagnosticFile, diagnosticFile)
				}
			}
			return true
		})
	}
}

// checkElidedElements reports the diagnostic literals written with their type
// elided inside lit, whose own type is litType: `[]*Error{{...}}`,
// `map[string]Error{"k": {...}}`, `[2]Error{{...}}` and their nestings. Such a
// literal has a nil Type of its own, so the element type of the container it
// sits in is the only thing naming it.
//
// The element type has to be spelled in the container's own literal to be
// seen: a named container type (`type diags []*Error`) or a struct field would
// need the type checker, and are not reached.
func checkElidedElements(t *testing.T, fset *gotoken.FileSet, lit *ast.CompositeLit, litType ast.Expr) {
	t.Helper()
	keyType, elemType := elidedElementTypes(litType)
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			checkElidedElement(t, fset, kv.Key, keyType)
			checkElidedElement(t, fset, kv.Value, elemType)
			continue
		}
		checkElidedElement(t, fset, elt, elemType)
	}
}

// checkElidedElement reports expr when it is an elided composite literal of a
// diagnostic type, and otherwise descends into it.
func checkElidedElement(t *testing.T, fset *gotoken.FileSet, expr ast.Expr, want ast.Expr) {
	t.Helper()
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || lit.Type != nil || want == nil {
		return
	}
	if name := diagnosticTypeName(want); name != "" {
		t.Errorf("%s: elided composite literal of %s outside %s -- build diagnostics through the constructor in %s",
			fset.Position(lit.Pos()), name, diagnosticFile, diagnosticFile)
		return
	}
	checkElidedElements(t, fset, lit, want)
}

// elidedElementTypes returns the types an elided literal has in the key and in
// the element position of a composite literal of type expr. A nil result means
// that position implies no type an elided literal could take.
func elidedElementTypes(expr ast.Expr) (key, elem ast.Expr) {
	switch typ := unpointer(expr).(type) {
	case *ast.ArrayType: // slices and arrays alike
		return nil, typ.Elt
	case *ast.MapType:
		return typ.Key, typ.Value
	}
	return nil, nil
}

// diagnosticTypeName returns the diagnostic type expr names, through any
// number of pointers, or "" when it names something else.
func diagnosticTypeName(expr ast.Expr) string {
	id, ok := unpointer(expr).(*ast.Ident)
	if !ok || !diagnosticTypes[id.Name] {
		return ""
	}
	return id.Name
}

// unpointer strips the pointer stars from a type expression.
func unpointer(expr ast.Expr) ast.Expr {
	for {
		star, ok := expr.(*ast.StarExpr)
		if !ok {
			return expr
		}
		expr = star.X
	}
}

// kindSentinels pairs every ErrorKind with its sentinel. The drift test below
// checks this table against the declarations in errors.go, so a kind or a
// sentinel added without the other -- or without an entry here -- fails.
var kindSentinels = map[string]struct {
	Kind     tomledit.ErrorKind
	Sentinel error
}{
	"KindSyntax":         {tomledit.KindSyntax, tomledit.ErrSyntax},
	"KindUnknownKey":     {tomledit.KindUnknownKey, tomledit.ErrUnknownKey},
	"KindUnknownTable":   {tomledit.KindUnknownTable, tomledit.ErrUnknownTable},
	"KindMissingKey":     {tomledit.KindMissingKey, tomledit.ErrMissingKey},
	"KindTypeMismatch":   {tomledit.KindTypeMismatch, tomledit.ErrTypeMismatch},
	"KindInexact":        {tomledit.KindInexact, tomledit.ErrInexact},
	"KindNotFound":       {tomledit.KindNotFound, tomledit.ErrNotFound},
	"KindBadPath":        {tomledit.KindBadPath, tomledit.ErrBadPath},
	"KindWrongContainer": {tomledit.KindWrongContainer, tomledit.ErrWrongContainer},
	"KindBadInput":       {tomledit.KindBadInput, tomledit.ErrBadInput},
	"KindConflict":       {tomledit.KindConflict, tomledit.ErrConflict},
	"KindRoundTrip":      {tomledit.KindRoundTrip, tomledit.ErrRoundTrip},
}

// sentinelName is the sentinel a kind constant must have: KindFoo has ErrFoo.
func sentinelName(kind string) string { return "Err" + strings.TrimPrefix(kind, "Kind") }

// Fails when a kind is declared without its sentinel, a sentinel without its
// kind, either without an entry in kindSentinels, a kind without a name in
// String, or a sentinel that matches the wrong kind.
func TestErrorKindSentinelDrift(t *testing.T) {
	_, files := packageFiles(t)
	file, ok := files[diagnosticFile]
	if !ok {
		t.Fatalf("%s is not part of the package", diagnosticFile)
	}

	// The declared kind constants and Err* sentinels, read from the source.
	var declaredKinds, declaredSentinels []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				switch {
				case gen.Tok == gotoken.CONST && strings.HasPrefix(name.Name, "Kind"):
					declaredKinds = append(declaredKinds, name.Name)
				case gen.Tok == gotoken.VAR && strings.HasPrefix(name.Name, "Err"):
					declaredSentinels = append(declaredSentinels, name.Name)
				}
			}
		}
	}
	if len(declaredKinds) == 0 || len(declaredSentinels) == 0 {
		t.Fatalf("found %d kinds and %d sentinels in %s; the declarations moved",
			len(declaredKinds), len(declaredSentinels), diagnosticFile)
	}

	sentinelSet := map[string]bool{}
	for _, name := range declaredSentinels {
		sentinelSet[name] = true
	}
	kindSet := map[string]bool{}
	for _, name := range declaredKinds {
		kindSet[name] = true
	}

	for _, kind := range declaredKinds {
		if want := sentinelName(kind); !sentinelSet[want] {
			t.Errorf("%s has no sentinel: declare `var %s error = kindError(%s)`", kind, want, kind)
		}
		if _, ok := kindSentinels[kind]; !ok {
			t.Errorf("%s has no entry in the kindSentinels table of this test", kind)
		}
	}
	for _, sentinel := range declaredSentinels {
		kind := "Kind" + strings.TrimPrefix(sentinel, "Err")
		if !kindSet[kind] {
			t.Errorf("%s has no kind: declare `%s` in the ErrorKind const block", sentinel, kind)
		}
	}
	for name := range kindSentinels {
		if !kindSet[name] {
			t.Errorf("the kindSentinels table names %s, which errors.go no longer declares", name)
		}
	}

	// Each sentinel matches its own kind and no other.
	for name, entry := range kindSentinels {
		diag := fmt.Errorf("wrapped: %w", &tomledit.Error{Kind: entry.Kind, Message: "test"})
		if !errors.Is(diag, entry.Sentinel) {
			t.Errorf("a %s diagnostic does not match %s", name, sentinelName(name))
		}
		for otherName, other := range kindSentinels {
			if otherName == name {
				continue
			}
			if errors.Is(diag, other.Sentinel) {
				t.Errorf("a %s diagnostic also matches %s", name, sentinelName(otherName))
			}
		}
		if got := entry.Kind.String(); got == "" || strings.HasPrefix(got, "ErrorKind(") {
			t.Errorf("%s has no name in ErrorKind.String(): got %q", name, got)
		}
	}
}
