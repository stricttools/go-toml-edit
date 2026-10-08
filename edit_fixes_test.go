package tomledit

import (
	"errors"
	"math"
	"testing"
)

// The small write-path defects the redesign fixes, each pinned by the public
// surface that exhibits it.

// Fails if an unsigned value too large for an int64 reaches the document
// instead of being refused: the value-to-node path must report KindBadInput
// rather than wrapping the value around into a negative integer.
func TestSet_UnsignedOverflowIsRefused(t *testing.T) {
	doc, err := Parse([]byte("x = 1\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	err = doc.Set("x", uint64(math.MaxInt64)+1)
	if err == nil {
		t.Fatalf("Set accepted a uint64 above MaxInt64; the document now reads %q", doc.Bytes())
	}
	if !errors.Is(err, ErrBadInput) {
		t.Errorf("Set error is %v, want a bad-input diagnostic", err)
	}
}

// Fails if an unsigned overflow inside a container is accepted: the refusal
// must reach every element of a converted slice, not just a top-level value.
func TestSet_UnsignedOverflowInSliceIsRefused(t *testing.T) {
	doc, err := Parse([]byte("x = 1\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	err = doc.Set("x", []any{uint64(1), uint64(math.MaxInt64) + 1})
	if err == nil {
		t.Fatalf("Set accepted an array holding a uint64 above MaxInt64; the document now reads %q", doc.Bytes())
	}
	if !errors.Is(err, ErrBadInput) {
		t.Errorf("Set error is %v, want a bad-input diagnostic", err)
	}
}

// Fails if deleting a key an inline table does not carry marks it dirty: a
// no-op delete must leave the table's own bytes -- its spacing and its
// spelling -- exactly as the source wrote them.
func TestDelete_MissingKeyInInlineTableLeavesBytesAlone(t *testing.T) {
	const src = "t = { a = 1,   b = 2 }\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := doc.Delete("t.missing"); err != nil {
		t.Fatalf("Delete of a missing inline-table key returned %v", err)
	}
	if got := string(doc.Bytes()); got != src {
		t.Errorf("after a no-op delete the document reads %q, want %q", got, src)
	}
}

// Fails if deleting an entry of an array-of-tables leaves behind the headers of
// the tables nested inside it: a [[members.pipelines]] or [members.sub] header
// left in the document would attach its pairs to the entry that remains.
func TestDelete_ArrayTableEntryTakesNestedHeaders(t *testing.T) {
	const src = `[[members]]
name = "a"

[[members.pipelines]]
type = "go"

[members.sub]
x = 1

[[members]]
name = "b"
`
	// The blank line before the remaining [[members]] is that header's own
	// leading trivia, so it stays.
	const want = `
[[members]]
name = "b"
`
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := doc.Delete("members[0]"); err != nil {
		t.Fatalf("Delete(members[0]) returned %v", err)
	}
	got := doc.Bytes()
	if string(got) != want {
		t.Errorf("after deleting members[0] the document reads %q, want %q", got, want)
	}
	reparsed, err := Parse(got)
	if err != nil {
		t.Fatalf("re-parse of %q failed: %v", got, err)
	}
	if name, err := reparsed.GetString("members[0].name"); err != nil || name != "b" {
		t.Errorf("re-parsed members[0].name = %q, %v; want \"b\"", name, err)
	}
	for _, path := range []string{"members[0].pipelines", "members[0].sub", "members[1]"} {
		if reparsed.Has(path) {
			t.Errorf("re-parsed document still has %s", path)
		}
	}
}
