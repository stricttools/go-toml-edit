package tomledit

import (
	"errors"
	"testing"
)

// Resolution over the read-layer: what the concrete-node surfaces answer, and
// what they refuse.

const resolveTestDoc = `top = 1
dotted.deep = 2
inline = {x = 3}

[table]
k = "v"

[compound.leaf]
m = 4

[[coll]]
name = "first"

[[coll]]
name = "second"
`

// Fails if a path naming exactly one node stops resolving to it -- the surface
// every consumer reads values through.
func TestResolve_ConcretePaths(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	cases := []struct {
		path string
		want NodeType
	}{
		{"top", NodeInteger},
		{"dotted.deep", NodeInteger},
		{"inline", NodeInlineTable},
		{"inline.x", NodeInteger},
		{"table", NodeTable},
		{"table.k", NodeString},
		{"compound.leaf", NodeTable},
		{"compound.leaf.m", NodeInteger},
		{"coll[0]", NodeArrayTable},
		{"coll[-1].name", NodeString},
	}
	for _, c := range cases {
		node, err := doc.Resolve(c.path)
		if err != nil {
			t.Errorf("Resolve(%q): %v", c.path, err)
			continue
		}
		if node.Type() != c.want {
			t.Errorf("Resolve(%q) is a %s, want a %s", c.path, node.Type(), c.want)
		}
		if !doc.Has(c.path) {
			t.Errorf("Has(%q) reported false", c.path)
		}
		if _, ok := doc.Lookup(c.path); !ok {
			t.Errorf("Lookup(%q) reported false", c.path)
		}
	}
}

// Fails if a logical-only path -- one naming something no single node stands
// for -- starts resolving to a node again. That is what the retired virtual
// views used to invent, and inventing one is worse than refusing.
func TestResolve_LogicalOnlyPathsAreRefused(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, path := range []string{
		"dotted",   // implied by a dotted key
		"compound", // implied by a longer header
		"coll",     // an array-of-tables
	} {
		node, err := doc.Resolve(path)
		if err == nil {
			t.Errorf("Resolve(%q) answered with %T, want a refusal", path, node)
			continue
		}
		if !errors.Is(err, ErrWrongContainer) {
			t.Errorf("Resolve(%q) = %v, want a wrong-container diagnostic", path, err)
		}
		var diag *Error
		if errors.As(err, &diag) && diag.Path != path {
			t.Errorf("Resolve(%q) diagnostic names path %q", path, diag.Path)
		}
		if _, ok := doc.Lookup(path); ok {
			t.Errorf("Lookup(%q) reported true", path)
		}
		if doc.Has(path) {
			t.Errorf("Has(%q) reported true", path)
		}
	}
}

// Fails if the read-layer stops carrying what the concrete-node surfaces
// refuse: the asymmetry is only acceptable because Root answers everything.
func TestResolve_LayerCarriesWhatLookupRefuses(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	root := doc.Root()
	for _, key := range []string{"dotted", "compound", "coll"} {
		if _, ok := root.Get(key); !ok {
			t.Errorf("the read-layer has no entry %q, though the document binds it", key)
		}
	}
}

// Fails if a path step stops being refused for the reason it is inapplicable:
// a key on a collection needs an index first, a key on a scalar names nothing,
// and an index into a table is not a step at all.
func TestResolve_InapplicableSteps(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	cases := []struct {
		path string
		kind ErrorKind
	}{
		{"coll.name", KindWrongContainer},  // key on a collection
		{"top.sub", KindWrongContainer},    // key on a scalar
		{"table[0]", KindWrongContainer},   // index into a table
		{"coll[9]", KindNotFound},          // index out of range
		{"missing", KindNotFound},          // nothing there
		{"table.missing", KindNotFound},    // nothing there, inside a table
		{"bad[", KindBadPath},              // not a path
		{"", KindBadPath},                  // no path at all
		{"inline.missing", KindNotFound},   // nothing there, inside an inline table
		{"compound.missing", KindNotFound}, // nothing there, inside an implied table
	}
	for _, c := range cases {
		_, err := doc.Resolve(c.path)
		if err == nil {
			t.Errorf("Resolve(%q) succeeded, want %v", c.path, c.kind)
			continue
		}
		var diag *Error
		if !errors.As(err, &diag) {
			t.Errorf("Resolve(%q) = %T, want an *Error", c.path, err)
			continue
		}
		if diag.Kind != c.kind {
			t.Errorf("Resolve(%q) is a %v, want a %v (%v)", c.path, diag.Kind, c.kind, err)
		}
	}
}

// Fails if an inline table reached through an array index stops folding on
// demand: a path into it is as legitimate as one into any other table.
func TestResolve_InlineTableInsideAnArray(t *testing.T) {
	doc, err := Parse([]byte("rows = [{a = 1}, {a = 2}]\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	node, err := doc.Resolve("rows[1].a")
	if err != nil {
		t.Fatalf("Resolve(rows[1].a): %v", err)
	}
	if n, ok := node.(*IntegerNode); !ok || n.val.get() != 2 {
		t.Errorf("rows[1].a = %v, want the integer 2", node)
	}
	if _, err := doc.Resolve("rows[0].missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing key inside an inline table = %v, want not-found", err)
	}
}

// Fails if edit paths stop addressing the entries of a collection: reads
// refuse a collection, writes still index into one.
func TestResolve_EditPathsKeepCollectionAddressing(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := doc.Set("coll[0].name", "renamed"); err != nil {
		t.Fatalf("Set(coll[0].name): %v", err)
	}
	if got, err := doc.GetString("coll[0].name"); err != nil || got != "renamed" {
		t.Errorf("coll[0].name = %q, want %q", got, "renamed")
	}
	if err := doc.Delete("coll[0]"); err != nil {
		t.Fatalf("Delete(coll[0]): %v", err)
	}
	if n := cursorAt(t, doc, "coll").Len(); n != 1 {
		t.Errorf("after deleting an entry the collection holds %d, want 1", n)
	}
	if got, err := doc.GetString("coll[0].name"); err != nil || got != "second" {
		t.Errorf("the surviving entry is %q, want %q", got, "second")
	}
}

// Fails if the Cursor's iteration surfaces stop reading a collection through
// the layer -- they answer about entries, which the concrete-node surfaces
// refuse to hand out as one node.
func TestResolve_ItemsAndLenOverACollection(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if n := cursorAt(t, doc, "coll").Len(); n != 2 {
		t.Errorf("Len(coll) = %d, want 2", n)
	}
	var seen int
	for i, node := range cursorAt(t, doc, "coll").Items() {
		if _, ok := node.(*ArrayTableNode); !ok {
			t.Errorf("coll[%d] is a %T, want an *ArrayTableNode", i, node)
		}
		seen++
	}
	if seen != 2 {
		t.Errorf("Items(coll) yielded %d entries, want 2", seen)
	}
	if n := cursorAt(t, doc, "dotted").Len(); n != -1 {
		t.Errorf("Len over a table = %d, want -1", n)
	}
}

// Fails if a comment setter starts writing into nothing: a path naming no
// single node has nowhere to carry a comment, and saying so is the only honest
// answer.
func TestResolve_CommentsRefuseLogicalOnlyPaths(t *testing.T) {
	doc, err := Parse([]byte(resolveTestDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, path := range []string{"coll", "compound", "dotted"} {
		if err := doc.SetComment(path, "note"); !errors.Is(err, ErrWrongContainer) {
			t.Errorf("SetComment(%q) = %v, want a wrong-container diagnostic", path, err)
		}
		if err := doc.SetLeadingComments(path, []string{"note"}); !errors.Is(err, ErrWrongContainer) {
			t.Errorf("SetLeadingComments(%q) = %v, want a wrong-container diagnostic", path, err)
		}
	}
	// A path that does name a node still takes its comment.
	if err := doc.SetComment("table", "note"); err != nil {
		t.Errorf("SetComment(table): %v", err)
	}
}

// Fails if a document no fold can make sense of starts resolving by guessing.
// The editing surface refuses to build one, so the test assembles it by hand;
// every read of it must still say so.
func TestResolve_UnfoldableDocumentIsRefused(t *testing.T) {
	doc := unfoldableDoc(t)
	if _, err := doc.Resolve("a"); !errors.Is(err, ErrConflict) {
		t.Errorf("Resolve on an unfoldable document = %v, want a conflict diagnostic", err)
	}
	if doc.Has("a") {
		t.Error("Has answered true for an unfoldable document")
	}
}

// Fails if a resolution diagnostic stops naming the file the document came
// from, or the path the caller asked for.
func TestResolve_DiagnosticsNameFileAndPath(t *testing.T) {
	path := "configs/config.toml"
	doc := parseWithFileName(t, path, "[[coll]]\nx = 1\n")
	_, err := doc.Resolve("coll")
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("Resolve = %v, want an *Error", err)
	}
	if diag.File != path {
		t.Errorf("diagnostic names file %q, want %q", diag.File, path)
	}
	if diag.Path != "coll" {
		t.Errorf("diagnostic names path %q, want %q", diag.Path, "coll")
	}
}

// Fails if a rendered diagnostic path stops being pasteable back into a path
// operation -- the property that makes JoinPath the one renderer.
func TestResolve_DiagnosticPathPastesBack(t *testing.T) {
	doc, err := Parse([]byte("[\"odd key\"]\nx = 1\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, err = doc.Resolve(`"odd key".missing`)
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("Resolve = %v, want an *Error", err)
	}
	segs, err := ParsePath(diag.Path)
	if err != nil {
		t.Fatalf("the diagnostic path %q does not parse: %v", diag.Path, err)
	}
	again, err := ParsePath(JoinPath(segs))
	if err != nil {
		t.Fatalf("re-parsing the rendered path: %v", err)
	}
	if !segmentsEqual(again, segs) {
		t.Errorf("the diagnostic path %q does not survive a render-parse cycle", diag.Path)
	}
	// The message renders the container's path the same way.
	if _, err := doc.Resolve(`"odd key".missing.deeper`); err == nil {
		t.Error("resolving through a missing key succeeded")
	}
}
