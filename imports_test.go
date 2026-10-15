package tomledit

import (
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The library has NO RUNTIME DEPENDENCIES: the non-test sources import the
// standard library and nothing else. The module requires BurntSushi/toml and
// toml-test, but both are reachable only from _test.go files -- they are the
// benchmark comparison and the compliance corpus -- so a consumer building this
// package links no third-party code.
//
// The README states this, which is why it is asserted rather than assumed.
// An import path whose first segment carries a dot names a module domain;
// standard-library paths never do.
//
// Fails the moment a non-test source file imports anything outside the standard
// library.
func TestNonTestSourcesImportOnlyTheStandardLibrary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := gotoken.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, goparser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		checked++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquoting import %s: %v", name, imp.Path.Value, err)
			}
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports %q, which is outside the standard library; "+
					"the README promises no runtime dependencies", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files were examined; the check proved nothing")
	}
}

// The root package computes only over the values it is handed: every operation
// that touches the operating system -- reading a file, writing one through a
// temporary file and a rename, drawing a random name -- is the tomlfile
// subpackage's. strictgo admits the root package to strict programs as a pure
// module on that basis, so it is asserted rather than assumed.
//
// Fails the moment a non-test source file imports a package that reaches the
// operating system, or calls a function of package time that reads or waits on
// the clock.
func TestNonTestSourcesTouchNoOperatingSystem(t *testing.T) {
	refused := func(path string) bool {
		switch path {
		case "os", "io/ioutil", "path/filepath", "syscall", "unsafe", "net", "crypto/rand", "math/rand", "plugin":
			return true
		}
		for _, prefix := range []string{"os/", "net/", "math/rand/", "runtime/debug"} {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
		return false
	}
	clock := map[string]bool{
		"Now": true, "Since": true, "Until": true, "Sleep": true, "Tick": true,
		"After": true, "AfterFunc": true, "NewTimer": true, "NewTicker": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := gotoken.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, goparser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		checked++
		timeName := ""
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquoting import %s: %v", name, imp.Path.Value, err)
			}
			if refused(path) {
				t.Errorf("%s imports %q, which reaches the operating system; it belongs in the tomlfile subpackage", name, path)
			}
			if path == "time" {
				timeName = "time"
				if imp.Name != nil {
					timeName = imp.Name.Name
				}
			}
		}
		if timeName == "" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == timeName && clock[sel.Sel.Name] {
				t.Errorf("%s: %s calls time.%s, which reads or waits on the clock", fset.Position(sel.Pos()), name, sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test source files were examined; the check proved nothing")
	}
}
