package tomledit

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const commentsTestDocument = `[server]
host = "localhost"
port = 8080

[database]
name = "mydb"
`

func parseCommentsTestDoc(t *testing.T) *Document {
	t.Helper()
	doc, err := Parse([]byte(commentsTestDocument))
	if err != nil {
		t.Fatalf("failed to parse test document: %v", err)
	}
	return doc
}

// Test 1: Set inline comment on a key-value
func TestSetComment_KeyValue(t *testing.T) {
	doc := parseCommentsTestDoc(t)
	err := doc.SetComment("server.host", "the server hostname")
	if err != nil {
		t.Fatalf("SetComment returned error: %v", err)
	}
	out := string(doc.Bytes())
	if !strings.Contains(out, "# the server hostname") {
		t.Errorf("expected inline comment in output, got:\n%s", out)
	}
}

// Test 2: Set leading comments on a key-value
func TestSetLeadingComments_KeyValue(t *testing.T) {
	doc := parseCommentsTestDoc(t)
	err := doc.SetLeadingComments("server.port", []string{"Port configuration", "Change as needed"})
	if err != nil {
		t.Fatalf("SetLeadingComments returned error: %v", err)
	}
	out := string(doc.Bytes())
	if !strings.Contains(out, "# Port configuration") {
		t.Errorf("expected leading comment line 1 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "# Change as needed") {
		t.Errorf("expected leading comment line 2 in output, got:\n%s", out)
	}
}

// Test 3: Set comment on a table header
func TestSetComment_TableHeader(t *testing.T) {
	doc := parseCommentsTestDoc(t)
	err := doc.SetComment("database", "DB settings")
	if err != nil {
		t.Fatalf("SetComment returned error: %v", err)
	}
	out := string(doc.Bytes())
	if !strings.Contains(out, "[database] # DB settings") {
		t.Errorf("expected inline comment on table header, got:\n%s", out)
	}
}

// Test 4: Remove comment (empty string)
func TestSetComment_Remove(t *testing.T) {
	// First set a comment, then remove it
	doc := parseCommentsTestDoc(t)
	err := doc.SetComment("server.host", "some comment")
	if err != nil {
		t.Fatalf("SetComment (set) returned error: %v", err)
	}
	out1 := string(doc.Bytes())
	if !strings.Contains(out1, "# some comment") {
		t.Fatalf("comment was not set, got:\n%s", out1)
	}

	err = doc.SetComment("server.host", "")
	if err != nil {
		t.Fatalf("SetComment (remove) returned error: %v", err)
	}
	out2 := string(doc.Bytes())
	if strings.Contains(out2, "# some comment") {
		t.Errorf("comment was not removed, got:\n%s", out2)
	}
}

// Test 5: Error on non-existent path
func TestSetComment_NonExistent(t *testing.T) {
	doc := parseCommentsTestDoc(t)
	err := doc.SetComment("nonexistent.key", "comment")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

// Test 6: Round-trip: set comment, serialize, verify comment appears in output
func TestSetComment_RoundTrip(t *testing.T) {
	doc := parseCommentsTestDoc(t)
	err := doc.SetComment("server.host", "important setting")
	if err != nil {
		t.Fatalf("SetComment returned error: %v", err)
	}
	out := doc.Bytes()
	doc2, err := Parse(out)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v\noutput was:\n%s", err, string(out))
	}
	// The re-parsed document should still have the host value
	val, err := doc2.GetString("server.host")
	if err != nil || val != "localhost" {
		t.Errorf("round-trip: expected host=\"localhost\", got %q (ok=%v)", val, err)
	}
	// And the comment should be in the serialized output
	out2 := string(doc2.Bytes())
	if !strings.Contains(out2, "# important setting") {
		t.Errorf("round-trip: comment lost after re-parse, got:\n%s", out2)
	}
}

// Test 7: Set comment then Get still works (comments don't affect values)
func TestSetComment_GetStillWorks(t *testing.T) {
	doc := parseCommentsTestDoc(t)
	err := doc.SetComment("server.port", "HTTP port")
	if err != nil {
		t.Fatalf("SetComment returned error: %v", err)
	}
	val, err := doc.GetInt("server.port")
	if err != nil || val != 8080 {
		t.Errorf("expected port=8080 after SetComment, got %d (ok=%v)", val, err)
	}

	err = doc.SetLeadingComments("database.name", []string{"Database name"})
	if err != nil {
		t.Fatalf("SetLeadingComments returned error: %v", err)
	}
	name, err := doc.GetString("database.name")
	if err != nil || name != "mydb" {
		t.Errorf("expected name=\"mydb\" after SetLeadingComments, got %q (ok=%v)", name, err)
	}
}

// Fails if the comment getters answer with a comment's bytes rather than its
// text: the "#" and the whitespace around it are the spelling, and a caller
// reading a comment to write it somewhere else would have to strip them itself.
func TestComments_GettersAnswerNormalizedText(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		inline  string
		leading []string
	}{
		{"spaced", "# lead\nx = 1 # note\n", "note", []string{"lead"}},
		{"unspaced", "#lead\nx = 1 #note\n", "note", []string{"lead"}},
		{"padded", "#   lead   \nx = 1 #   note   \n", "note", []string{"lead"}},
		{"hash-only", "#\nx = 1 #\n", "", []string{""}},
		{"indented", "[t]\n  # lead\n  x = 1 # note\n", "note", []string{"lead"}},
		{"none", "x = 1\n", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseOrFail(t, tc.src)
			node := doc.Children()[len(doc.Children())-1]
			if tbl, ok := node.(*TableNode); ok {
				node = tbl.Children()[0]
			}
			if got := node.Comment(); got != tc.inline {
				t.Errorf("Comment() = %q, want %q", got, tc.inline)
			}
			got := node.LeadingComments()
			if len(got) != len(tc.leading) {
				t.Fatalf("LeadingComments() = %q, want %q", got, tc.leading)
			}
			for i := range got {
				if got[i] != tc.leading[i] {
					t.Errorf("LeadingComments()[%d] = %q, want %q", i, got[i], tc.leading[i])
				}
			}
		})
	}
}

// Fails if the getters and the path-based setters stop agreeing on what a
// comment's text is: reading a comment and writing it back would then reformat
// it, which is what Merge's comment copying depends on not happening.
func TestComments_GetterFeedsSetterUnchanged(t *testing.T) {
	doc := parseOrFail(t, "# lead one\n# lead two\nx = 1 # note\ny = 2\n")
	src := doc.Children()[0]

	if err := doc.SetComment("y", src.Comment()); err != nil {
		t.Fatalf("SetComment: %v", err)
	}
	if err := doc.SetLeadingComments("y", src.LeadingComments()); err != nil {
		t.Fatalf("SetLeadingComments: %v", err)
	}
	want := "# lead one\n# lead two\nx = 1 # note\n# lead one\n# lead two\ny = 2 # note\n"
	if got := string(doc.Bytes()); got != want {
		t.Errorf("the copied comments read:\n  got:  %q\n  want: %q", got, want)
	}
}

// Fails if a node-level comment setter becomes exported again: the public
// spelling of a comment write is path-based, so that the document decides
// where the comment goes and can refuse a container that cannot host one.
func TestComments_NodeSettersAreNotExported(t *testing.T) {
	var n Node = &StringNode{}
	if _, exported := any(n).(interface{ SetComment(string) }); exported {
		t.Error("a node exposes SetComment; the public spelling is (*Document).SetComment(path, text)")
	}
	if _, exported := any(n).(interface{ SetLeadingComments([]string) }); exported {
		t.Error("a node exposes SetLeadingComments; the public spelling is (*Document).SetLeadingComments(path, texts)")
	}
}

// =============================================================================
// The path-based comment getters
// =============================================================================

// What the path-based setters write, the path-based getters read back: the two
// resolve the same node for the same path, so a comment written through a path
// is readable through it.
//
// Fails if the getters ever resolve a different node than the setters, or stop
// answering the normalized text the setters take.
func TestComments_GettersRoundTripThroughTheSetters(t *testing.T) {
	doc := parseOrFail(t, "[server]\nhost = \"localhost\"\nport = 8080\n\n[[items]]\nname = \"a\"\n")

	cases := []struct {
		path    string
		inline  string
		leading []string
	}{
		{path: "server.host", inline: "the hostname", leading: []string{"where to bind", "keep it local"}},
		{path: "server", inline: "server settings", leading: []string{"the server block"}},
		{path: "items[0]", inline: "the first item", leading: []string{"one entry"}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if err := doc.SetComment(c.path, c.inline); err != nil {
				t.Fatalf("SetComment(%q): %v", c.path, err)
			}
			if err := doc.SetLeadingComments(c.path, c.leading); err != nil {
				t.Fatalf("SetLeadingComments(%q): %v", c.path, err)
			}

			got, err := doc.GetComment(c.path)
			if err != nil {
				t.Fatalf("GetComment(%q): %v", c.path, err)
			}
			if got != c.inline {
				t.Errorf("GetComment(%q) = %q, want %q", c.path, got, c.inline)
			}

			gotLeading, err := doc.GetLeadingComments(c.path)
			if err != nil {
				t.Fatalf("GetLeadingComments(%q): %v", c.path, err)
			}
			if !slices.Equal(gotLeading, c.leading) {
				t.Errorf("GetLeadingComments(%q) = %q, want %q", c.path, gotLeading, c.leading)
			}
		})
	}
}

// The path getters answer the same normalized text the node-level getters do,
// for a document nobody has edited: the "#" and the whitespace around it are
// gone.
//
// Fails if the path getters ever start answering the raw bytes.
func TestComments_PathGettersAnswerNormalizedText(t *testing.T) {
	doc := parseOrFail(t, "#lead\n#  spaced out\nx = 1   #note\n")

	if got, err := doc.GetComment("x"); err != nil || got != "note" {
		t.Errorf("GetComment(\"x\") = %q, %v; want \"note\", nil", got, err)
	}
	got, err := doc.GetLeadingComments("x")
	if err != nil {
		t.Fatalf("GetLeadingComments: %v", err)
	}
	want := []string{"lead", "spaced out"}
	if !slices.Equal(got, want) {
		t.Errorf("GetLeadingComments(\"x\") = %q, want %q", got, want)
	}
}

// A comment on an array ELEMENT is carried by the element itself, and the
// getters reach it through the index segment.
//
// Fails if an index path stops resolving to the element that carries the
// comment.
func TestComments_GettersReadAnArrayElement(t *testing.T) {
	doc := parseOrFail(t, "items = [\n  # about one\n  1, # one\n  2,\n]\n")

	if got, err := doc.GetComment("items[0]"); err != nil || got != "one" {
		t.Errorf("GetComment(\"items[0]\") = %q, %v; want \"one\", nil", got, err)
	}
	got, err := doc.GetLeadingComments("items[0]")
	if err != nil {
		t.Fatalf("GetLeadingComments: %v", err)
	}
	if want := []string{"about one"}; !slices.Equal(got, want) {
		t.Errorf("GetLeadingComments(\"items[0]\") = %q, want %q", got, want)
	}

	// The second element carries nothing: the empty answers, not an error.
	if got, err := doc.GetComment("items[1]"); err != nil || got != "" {
		t.Errorf("GetComment(\"items[1]\") = %q, %v; want \"\", nil", got, err)
	}
	if got, err := doc.GetLeadingComments("items[1]"); err != nil || got != nil {
		t.Errorf("GetLeadingComments(\"items[1]\") = %#v, %v; want nil, nil", got, err)
	}
}

// A node with no comments answers the empty string and nil, not an error.
//
// Fails if the getters ever report absence as a failure.
func TestComments_GettersAnswerEmptyForNone(t *testing.T) {
	doc := parseOrFail(t, "x = 1\n")

	if got, err := doc.GetComment("x"); err != nil || got != "" {
		t.Errorf("GetComment(\"x\") = %q, %v; want \"\", nil", got, err)
	}
	if got, err := doc.GetLeadingComments("x"); err != nil || got != nil {
		t.Errorf("GetLeadingComments(\"x\") = %#v, %v; want nil, nil", got, err)
	}
}

// The getters navigate exactly as the setters do, so they refuse exactly what
// the setters refuse, with the same kind on the same case.
//
// Fails if a getter ever reports a different kind than the setter for the same
// path, or stops reporting one at all.
func TestComments_GetterNavigationErrorsMatchTheSetters(t *testing.T) {
	const src = "title = \"t\"\npoint = { x = 1 }\n\n[server]\nhost = \"localhost\"\n"

	cases := []struct {
		name string
		path string
		kind ErrorKind
		sent error
	}{
		{name: "missing key", path: "server.port", kind: KindNotFound, sent: ErrNotFound},
		{name: "member of an inline table", path: "point.x", kind: KindWrongContainer, sent: ErrWrongContainer},
		{name: "key under a scalar", path: "title.nested", kind: KindWrongContainer, sent: ErrWrongContainer},
		{name: "malformed path", path: "title[", kind: KindBadPath, sent: ErrBadPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := parseOrFail(t, src)

			setterErr := doc.SetComment(c.path, "x")
			if setterErr == nil {
				t.Fatalf("SetComment(%q) succeeded; the case no longer refuses anything", c.path)
			}

			for _, probe := range []struct {
				name string
				err  error
			}{
				{name: "GetComment", err: func() error { _, err := doc.GetComment(c.path); return err }()},
				{name: "GetLeadingComments", err: func() error { _, err := doc.GetLeadingComments(c.path); return err }()},
				{name: "SetComment", err: setterErr},
			} {
				if probe.err == nil {
					t.Fatalf("%s(%q) returned no error", probe.name, c.path)
				}
				var e *Error
				if !errors.As(probe.err, &e) {
					t.Fatalf("%s(%q) error is not an *Error: %v", probe.name, c.path, probe.err)
				}
				if e.Kind != c.kind {
					t.Errorf("%s(%q) kind = %v, want %v", probe.name, c.path, e.Kind, c.kind)
				}
				if !errors.Is(probe.err, c.sent) {
					t.Errorf("%s(%q) does not match %v", probe.name, c.path, c.sent)
				}
				if e.Path != c.path {
					t.Errorf("%s(%q) reports path %q", probe.name, c.path, e.Path)
				}
			}
		})
	}
}

// A document parsed under a file name names the file on the getters'
// diagnostics, just as it does on the setters'.
//
// Fails if the getters ever return a diagnostic that does not carry the
// document's origin.
func TestComments_GetterDiagnosticsNameTheFile(t *testing.T) {
	path := "configs/config.toml"
	doc := parseWithFileName(t, path, "[server]\nhost = \"localhost\"\n")

	// Reading what is there works the same through a named document.
	if err := doc.SetComment("server.host", "bind address"); err != nil {
		t.Fatalf("SetComment: %v", err)
	}
	if got, err := doc.GetComment("server.host"); err != nil || got != "bind address" {
		t.Errorf("GetComment = %q, %v; want \"bind address\", nil", got, err)
	}

	for _, probe := range []struct {
		name string
		err  error
	}{
		{name: "GetComment", err: func() error { _, err := doc.GetComment("server.port"); return err }()},
		{name: "GetLeadingComments", err: func() error { _, err := doc.GetLeadingComments("server.port"); return err }()},
	} {
		var e *Error
		if !errors.As(probe.err, &e) {
			t.Fatalf("%s error is not an *Error: %v", probe.name, probe.err)
		}
		if e.File != path {
			t.Errorf("%s reports file %q, want %q", probe.name, e.File, path)
		}
		if !strings.Contains(probe.err.Error(), "config.toml") {
			t.Errorf("%s message does not name the file: %s", probe.name, probe.err)
		}
	}
}
