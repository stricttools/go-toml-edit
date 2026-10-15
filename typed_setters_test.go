package tomledit

import (
	"errors"
	"math"
	"testing"
)

// The typed setters write arrays and inline tables from statically typed Go
// values, for callers whose own rules forbid them to build a []any or a
// map[string]any. Each one is Set or SetCreate with the value already
// converted, so it shares their refusals, their equality rule, and their
// diagnostics.

func wantDoc(t *testing.T, doc *Document, want string) {
	t.Helper()
	if got := string(doc.Bytes()); got != want {
		t.Errorf("document reads %q, want %q", got, want)
	}
}

// Fails if a typed array setter writes anything other than the array Set
// writes for the same values.
func TestTypedArraySetters_WriteWhatSetWrites(t *testing.T) {
	cases := []struct {
		name  string
		write func(d *Document) error
		value any
	}{
		{"strings", func(d *Document) error { return d.SetStrings("t.v", []string{"a", "b"}) }, []string{"a", "b"}},
		{"ints", func(d *Document) error { return d.SetInts("t.v", []int64{1, -2}) }, []int64{1, -2}},
		{"floats", func(d *Document) error { return d.SetFloats("t.v", []float64{1.5, math.Inf(1)}) }, []float64{1.5, math.Inf(1)}},
		{"bools", func(d *Document) error { return d.SetBools("t.v", []bool{true, false}) }, []bool{true, false}},
		{"empty", func(d *Document) error { return d.SetStrings("t.v", nil) }, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			typed := mustParse(t, "[t]\n")
			if err := c.write(typed); err != nil {
				t.Fatalf("typed setter: %v", err)
			}
			untyped := mustParse(t, "[t]\n")
			if err := untyped.Set("t.v", c.value); err != nil {
				t.Fatalf("Set: %v", err)
			}
			wantDoc(t, typed, string(untyped.Bytes()))
		})
	}
}

// Fails if the SetCreate forms do not create the tables the path names, or if
// the Set forms do.
func TestTypedArraySetters_CreateOnlyInTheCreateForms(t *testing.T) {
	creates := []func(d *Document) error{
		func(d *Document) error { return d.SetCreateStrings("a.b.v", []string{"x"}) },
		func(d *Document) error { return d.SetCreateInts("a.b.v", []int64{1}) },
		func(d *Document) error { return d.SetCreateFloats("a.b.v", []float64{1.5}) },
		func(d *Document) error { return d.SetCreateBools("a.b.v", []bool{true}) },
		func(d *Document) error { return d.SetCreateInlineTable("a.b.v", InlineTable{}) },
	}
	for i, create := range creates {
		doc := mustParse(t, "")
		if err := create(doc); err != nil {
			t.Fatalf("create form %d: %v", i, err)
		}
		if !doc.Has("a.b.v") {
			t.Errorf("create form %d: a.b.v is missing after the write; the document reads %q", i, doc.Bytes())
		}
	}
	sets := []func(d *Document) error{
		func(d *Document) error { return d.SetStrings("a.b.v", []string{"x"}) },
		func(d *Document) error { return d.SetInts("a.b.v", []int64{1}) },
		func(d *Document) error { return d.SetFloats("a.b.v", []float64{1.5}) },
		func(d *Document) error { return d.SetBools("a.b.v", []bool{true}) },
		func(d *Document) error { return d.SetInlineTable("a.b.v", InlineTable{}) },
	}
	for i, set := range sets {
		doc := mustParse(t, "")
		if err := set(doc); !errors.Is(err, ErrNotFound) {
			t.Errorf("set form %d with a missing parent reported %v, want a not-found diagnostic", i, err)
		}
		wantDoc(t, doc, "")
	}
}

// Fails if a typed setter accepts what Set refuses: text that is not valid
// UTF-8 and a NaN with its sign bit set.
func TestTypedArraySetters_ShareSetsRefusals(t *testing.T) {
	const src = "x = 1\n"
	doc := mustParse(t, src)
	refuseWrite(t, doc, src, "SetStrings", doc.SetStrings("x", []string{"ok", badUTF8}))
	refuseWrite(t, doc, src, "SetCreateFloats", doc.SetCreateFloats("y", []float64{math.Copysign(math.NaN(), -1)}))
}

// Fails if a typed write of the bytes already stored is recorded as an edit.
func TestTypedArraySetters_KeepSetsEqualityRule(t *testing.T) {
	const src = "v = [\"a\", \"b\"] # kept\n"
	doc := mustParse(t, src)
	if err := doc.SetStrings("v", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	wantDoc(t, doc, src)
}

// Fails if an inline table is not written in the order its keys were added,
// with each value typed as it was added and nested tables kept.
func TestSetInlineTable_WritesTypedValuesInOrder(t *testing.T) {
	var inner InlineTable
	inner.Int("n", 7)
	var tbl InlineTable
	tbl.String("z", "last-added-first").
		Int("i", 3).
		Float("f", 2.5).
		Bool("b", true).
		Strings("ss", []string{"p", "q"}).
		Ints("is", []int64{1, 2}).
		Floats("fs", []float64{0.5}).
		Bools("bs", []bool{false}).
		Table("inner", inner)
	doc := mustParse(t, "[t]\n")
	if err := doc.SetInlineTable("t.v", tbl); err != nil {
		t.Fatal(err)
	}
	wantDoc(t, doc, "[t]\nv = {z = \"last-added-first\", i = 3, f = 2.5, b = true, ss = [\"p\", \"q\"], is = [1, 2], fs = [0.5], bs = [false], inner = {n = 7}}\n")
}

// Fails if adding to a table after nesting it changes the nested copy.
func TestInlineTable_TableCopiesTheNestedTable(t *testing.T) {
	var inner InlineTable
	inner.Int("a", 1)
	var outer InlineTable
	outer.Table("in", inner)
	inner.Int("b", 2)
	doc := mustParse(t, "")
	if err := doc.SetInlineTable("v", outer); err != nil {
		t.Fatal(err)
	}
	wantDoc(t, doc, "v = {in = {a = 1}}\n")
}

// Fails if a duplicate key or a key that is not valid UTF-8 reaches the
// document instead of being refused as bad input when the table is written.
func TestSetInlineTable_RefusesBadKeys(t *testing.T) {
	const src = "x = 1\n"
	doc := mustParse(t, src)
	var dup InlineTable
	dup.String("k", "a").Int("k", 1)
	refuseWrite(t, doc, src, "a duplicate key", doc.SetInlineTable("x", dup))
	var bad InlineTable
	bad.Bool(badUTF8, true)
	refuseWrite(t, doc, src, "a key that is not UTF-8", doc.SetCreateInlineTable("y", bad))
	var badValue InlineTable
	badValue.Strings("k", []string{badUTF8})
	refuseWrite(t, doc, src, "a string that is not UTF-8", doc.SetInlineTable("x", badValue))
}
