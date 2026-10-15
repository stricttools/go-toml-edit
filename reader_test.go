package tomledit

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The hand-reading front end: every accessor's success, absence, and wrong-type
// paths, the unknown-key report, the collector's ordering across files, and the
// syntax-error diagnostic.

// readerFor parses src as the named file into a fresh collector, failing the
// test on a syntax error.
func readerFor(t *testing.T, file, src string) (*Reader, *Collector) {
	t.Helper()
	c := &Collector{}
	r := c.Read(file, []byte(src))
	if r == nil {
		t.Fatalf("Read(%s) reported a syntax error: %v", file, c.Err())
	}
	return r, c
}

// renderedLines returns every diagnostic the collector holds, sorted, one
// rendering each.
func renderedLines(t *testing.T, c *Collector) []string {
	t.Helper()
	err := c.Err()
	if err == nil {
		return nil
	}
	var lines []string
	for _, d := range diagnosticsOf(t, err) {
		lines = append(lines, d.Error())
	}
	return lines
}

// wantLines fails unless the collector holds exactly the given renderings, in
// order.
func wantLines(t *testing.T, c *Collector, want ...string) {
	t.Helper()
	got := renderedLines(t, c)
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diagnostics:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Fails if Str stops returning a string it reads, or stops reporting an absent
// required key at the table's line, a wrong type at the key's line, or starts
// reporting an absent optional key.
func TestReaderStr(t *testing.T) {
	r, c := readerFor(t, "f.toml", "name = \"tool\"\ncount = 3\n")
	if s, ok := r.Str("name", true, `name = "<name>"`); !ok || s != "tool" {
		t.Errorf("Str(name) = %q, %v; want \"tool\", true", s, ok)
	}
	if _, ok := r.Str("help", false, `help = "<sentence>"`); ok {
		t.Error("Str on an absent optional key reported true")
	}
	wantLines(t, c)

	if _, ok := r.Str("title", true, `title = "<title>"`); ok {
		t.Error("Str on an absent required key reported true")
	}
	if _, ok := r.Str("count", true, `count = "<count>"`); ok {
		t.Error("Str on an integer reported true")
	}
	wantLines(t, c,
		`f.toml:1: title: is missing; write title = "<title>"`,
		`f.toml:2: count: is an integer, not a string; write count = "<count>"`,
	)

	diags := diagnosticsOf(t, c.Err())
	if diags[0].Kind != KindMissingKey || diags[0].Fix != `write title = "<title>"` || diags[0].Message != "is missing" {
		t.Errorf("missing-key diagnostic = %+v", diags[0])
	}
	if diags[1].Kind != KindTypeMismatch || diags[1].Expected != "a string" || diags[1].Got != "an integer" {
		t.Errorf("type-mismatch diagnostic = %+v", diags[1])
	}
}

// Fails if Integer stops reading an integer or stops refusing another type.
func TestReaderInteger(t *testing.T) {
	r, c := readerFor(t, "f.toml", "format = 1\nlimit = \"ten\"\n")
	if n, ok := r.Integer("format", true, "format = 1"); !ok || n != 1 {
		t.Errorf("Integer(format) = %d, %v; want 1, true", n, ok)
	}
	if _, ok := r.Integer("limit", true, "limit = <integer>"); ok {
		t.Error("Integer on a string reported true")
	}
	if _, ok := r.Integer("depth", true, "depth = <integer>"); ok {
		t.Error("Integer on an absent required key reported true")
	}
	wantLines(t, c,
		`f.toml:1: depth: is missing; write depth = <integer>`,
		`f.toml:2: limit: is a string, not an integer; write limit = <integer>`,
	)
}

// Fails if Boolean stops reading true and false, or stops naming both
// spellings in its fix.
func TestReaderBoolean(t *testing.T) {
	r, c := readerFor(t, "f.toml", "a = true\nb = false\nc = 1\n")
	if v, ok := r.Boolean("a", true); !ok || !v {
		t.Errorf("Boolean(a) = %v, %v; want true, true", v, ok)
	}
	if v, ok := r.Boolean("b", true); !ok || v {
		t.Errorf("Boolean(b) = %v, %v; want false, true", v, ok)
	}
	r.Boolean("c", true)
	r.Boolean("d", true)
	if _, ok := r.Boolean("e", false); ok {
		t.Error("Boolean on an absent optional key reported true")
	}
	wantLines(t, c,
		`f.toml:1: d: is missing; write d = true or d = false`,
		`f.toml:3: c: is an integer, not a boolean; write c = true or c = false`,
	)
}

// Fails if Flag stops treating absence as false, or stops refusing a false
// that only restates the absence.
func TestReaderFlag(t *testing.T) {
	r, c := readerFor(t, "f.toml", "on = true\noff = false\nodd = \"yes\"\n")
	if !r.Flag("on") {
		t.Error("Flag(on) = false, want true")
	}
	if r.Flag("absent") {
		t.Error("Flag on an absent key = true, want false")
	}
	if r.Flag("off") {
		t.Error("Flag(off) = true, want false")
	}
	r.Flag("odd")
	wantLines(t, c,
		`f.toml:2: off: is false, and its absence already means that; write off = true, or remove the key`,
		`f.toml:3: odd: is a string, not a boolean; write odd = true, or remove the key`,
	)
	if d := diagnosticsOf(t, c.Err())[0]; d.Kind != KindInvalidValue {
		t.Errorf("a false flag is %s, want %s", d.Kind, KindInvalidValue)
	}
}

// Fails if StrList stops reading a list of strings, or stops reporting a
// non-string element at its own path and line.
func TestReaderStrList(t *testing.T) {
	r, c := readerFor(t, "f.toml", "tags = [\"a\", \"b\"]\nmixed = [\n  \"x\",\n  2,\n]\nnot = \"x\"\n")
	if got, ok := r.StrList("tags", true, `tags = ["<tag>", ...]`); !ok || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("StrList(tags) = %v, %v; want [a b], true", got, ok)
	}
	if _, ok := r.StrList("mixed", true, `mixed = ["<word>", ...]`); ok {
		t.Error("StrList on a list holding an integer reported true")
	}
	if _, ok := r.StrList("not", true, `not = ["<word>", ...]`); ok {
		t.Error("StrList on a string reported true")
	}
	wantLines(t, c,
		`f.toml:4: mixed[1]: is an integer, not a string; write mixed = ["<word>", ...]`,
		`f.toml:6: not: is a string, not a list of strings; write not = ["<word>", ...]`,
	)
}

// Fails if NonEmpty stops refusing an empty list, starts refusing a non-empty
// one, or starts reading the key it checks.
func TestReaderNonEmpty(t *testing.T) {
	r, c := readerFor(t, "f.toml", "format = 1\nempty = []\nfull = [1]\n")
	r.Get("format")
	r.NonEmpty("empty", "at least one value")
	r.NonEmpty("full", "at least one value")
	r.NonEmpty("absent", "at least one value")
	r.Get("empty")
	r.Get("full")
	r.Done()
	wantLines(t, c, `f.toml:2: empty: is empty; write at least one value`)

	r2, c2 := readerFor(t, "g.toml", "empty = []\n")
	r2.NonEmpty("empty", "x")
	r2.Done()
	wantLines(t, c2,
		`g.toml:1: empty: is empty; write x`,
		`g.toml:1: empty: is not a key of this table; remove it`,
	)
}

// Fails if Tables stops reading [[header]] entries and arrays of inline tables
// alike, stops giving each table its indexed path and own line, or stops
// reporting a missing key of an entry at the entry's own line.
func TestReaderTables(t *testing.T) {
	src := "format = 1\n\n[[command]]\nname = \"a\"\n\n[[command]]\nhelp = \"x\"\n\n[tool]\nflag = [{ name = \"v\" }, 3]\n"
	r, c := readerFor(t, "o.toml", src)
	cmds := r.Tables("command")
	if len(cmds) != 2 {
		t.Fatalf("Tables(command) = %d readers, want 2", len(cmds))
	}
	if cmds[0].Path() != "command[0]" || cmds[1].Path() != "command[1]" {
		t.Errorf("paths = %q, %q; want command[0], command[1]", cmds[0].Path(), cmds[1].Path())
	}
	if cmds[0].TableLine() != 3 || cmds[1].TableLine() != 6 {
		t.Errorf("table lines = %d, %d; want 3, 6", cmds[0].TableLine(), cmds[1].TableLine())
	}
	for _, cmd := range cmds {
		cmd.Str("name", true, `name = "<name>"`)
		cmd.Done()
	}
	tool := r.Sub("tool")
	flags := tool.Tables("flag")
	if len(flags) != 1 || flags[0].Path() != "tool.flag[0]" {
		t.Fatalf("Tables(flag) = %v, want one reader at tool.flag[0]", flags)
	}
	if s, ok := flags[0].Str("name", true, "name"); !ok || s != "v" {
		t.Errorf("inline table name = %q, %v; want \"v\", true", s, ok)
	}
	if r.Tables("absent") != nil {
		t.Error("Tables on an absent key returned readers")
	}
	r.Tables("format")
	wantLines(t, c,
		`o.toml:1: format: is an integer, not a list of tables; write [[format]] entries`,
		`o.toml:6: command[1].name: is missing; write name = "<name>"`,
		`o.toml:7: command[1].help: is not a key of this table; remove it, or write one of name`,
		`o.toml:10: tool.flag[1]: is an integer, not a table; write [[tool.flag]] entries`,
	)
}

// Fails if Sub stops reading a table, or stops naming the header to write
// when the key holds something else.
func TestReaderSub(t *testing.T) {
	r, c := readerFor(t, "f.toml", "server = 1\n\n[[command]]\nflag = \"x\"\n\n[db]\nport = 5\n")
	if r.Sub("absent") != nil {
		t.Error("Sub on an absent key returned a reader")
	}
	if r.Sub("server") != nil {
		t.Error("Sub on an integer returned a reader")
	}
	db := r.Sub("db")
	if db == nil || db.Path() != "db" || db.TableLine() != 6 {
		t.Fatalf("Sub(db) = %+v, want a reader at db on line 6", db)
	}
	if n, ok := db.Integer("port", true, "port = <integer>"); !ok || n != 5 {
		t.Errorf("db.port = %d, %v; want 5, true", n, ok)
	}
	cmd := r.Tables("command")[0]
	cmd.Sub("flag")
	wantLines(t, c,
		`f.toml:1: server: is an integer, not a table; write [server]`,
		`f.toml:4: command[0].flag: is a string, not a table; write [command.flag]`,
	)
}

// Fails if Get stops marking a key known and recording its line, if Peek and
// PeekTrue start doing so, or if Lines stops being the live map.
func TestReaderGetPeekLines(t *testing.T) {
	r, c := readerFor(t, "f.toml", "a = 1\n\nb = true\nc = false\n")
	lines := r.Lines()
	if v, held := r.Peek("a"); !held || v.Type() != ValueInteger || v.Int() != 1 || v.Line() != 1 {
		t.Errorf("Peek(a) = %+v, %v", v, held)
	}
	if !r.PeekTrue("b") || r.PeekTrue("c") || r.PeekTrue("a") || r.PeekTrue("z") {
		t.Error("PeekTrue answered wrong")
	}
	if len(lines) != 0 {
		t.Errorf("Peek recorded lines %v", lines)
	}
	if !r.Has("b") || r.Has("z") {
		t.Error("Has answered wrong")
	}
	if lines["b"] != 3 {
		t.Errorf("Lines()[b] = %d, want 3 (the map is live)", lines["b"])
	}
	if _, held := lines["z"]; held {
		t.Error("an absent key recorded a line")
	}
	if r.Line("c") != 4 || r.Line("z") != 1 {
		t.Errorf("Line(c), Line(z) = %d, %d; want 4, 1", r.Line("c"), r.Line("z"))
	}
	if !reflect.DeepEqual(r.Keys(), []string{"a", "b", "c"}) {
		t.Errorf("Keys() = %v", r.Keys())
	}
	r.Done()
	wantLines(t, c,
		`f.toml:1: a: is not a key of this table; remove it, or write one of b, z`,
		`f.toml:4: c: is not a key of this table; remove it, or write one of b, z`,
	)
}

// Fails if Done stops reporting unread keys with the kinds the decode engine
// uses: KindUnknownKey for a value, KindUnknownTable (listing its keys) for a
// table and an array of tables.
func TestReaderDoneKinds(t *testing.T) {
	r, c := readerFor(t, "f.toml", "x = 1\n[t]\na = 1\nb = 2\n[[list]]\nc = 3\n")
	r.Done()
	diags := diagnosticsOf(t, c.Err())
	if len(diags) != 3 {
		t.Fatalf("Done reported %d diagnostics, want 3: %v", len(diags), c.Err())
	}
	if diags[0].Kind != KindUnknownKey || diags[0].Path != "x" || diags[0].Fix != "remove it" {
		t.Errorf("x: %+v", diags[0])
	}
	if diags[1].Kind != KindUnknownTable || diags[1].Path != "t" || !reflect.DeepEqual(diags[1].Keys, []string{"a", "b"}) {
		t.Errorf("t: %+v", diags[1])
	}
	if diags[2].Kind != KindUnknownTable || diags[2].Path != "list" || !reflect.DeepEqual(diags[2].Keys, []string{"c"}) {
		t.Errorf("list: %+v", diags[2])
	}
	if !errors.Is(c.Err(), ErrUnknownKey) || !errors.Is(c.Err(), ErrUnknownTable) {
		t.Error("the collected error does not match the unknown-key sentinels")
	}
}

// Fails if Errorf, ErrorfAt, and Collector.Errorf stop placing a diagnostic
// at the line and path they are given, or stop reporting KindInvalidValue.
func TestReaderErrorf(t *testing.T) {
	r, c := readerFor(t, "f.toml", "[t]\nname = \"Bad\"\n")
	sub := r.Sub("t")
	sub.Errorf("name", "%q is not a kebab-case name; write lowercase letters", "Bad")
	sub.Errorf("absent", "is needed")
	sub.ErrorfAt(9, "t.other[2]", "clashes")
	c.Errorf("g.toml", 3, "x", "clashes with %s", "f.toml")
	wantLines(t, c,
		`f.toml:1: t.absent: is needed`,
		`f.toml:2: t.name: "Bad" is not a kebab-case name; write lowercase letters`,
		`f.toml:9: t.other[2]: clashes`,
		`g.toml:3: x: clashes with f.toml`,
	)
	for _, d := range diagnosticsOf(t, c.Err()) {
		if d.Kind != KindInvalidValue {
			t.Errorf("%v is %s, want %s", d, d.Kind, KindInvalidValue)
		}
	}
}

// Fails if the collector stops sorting by file, line, path, and message
// across files, whatever order they were read in, or starts returning an
// error when it holds nothing.
func TestCollectorSortsAcrossFiles(t *testing.T) {
	c := &Collector{}
	if c.Err() != nil || c.Len() != 0 {
		t.Fatal("an empty collector reported an error")
	}
	b := c.Read("b.toml", []byte("z = 1\ny = 2\n"))
	a := c.Read("a.toml", []byte("[t]\nq = 1\n"))
	b.Done()
	a.Sub("t").Missing("p", "p = 1")
	a.Sub("t").Missing("o", "o = 1")
	a.Done()
	wantLines(t, c,
		`a.toml:1: t.o: is missing; write o = 1`,
		`a.toml:1: t.p: is missing; write p = 1`,
		`b.toml:1: z: is not a key of this table; remove it`,
		`b.toml:2: y: is not a key of this table; remove it`,
	)
	if c.Len() != 4 {
		t.Errorf("Len() = %d, want 4", c.Len())
	}
	// The aggregate renders its first diagnostic, as every *Errors does.
	if got, want := c.Err().Error(), `a.toml:1: t.o: is missing; write o = 1`; got != want {
		t.Errorf("Err().Error() = %q, want %q", got, want)
	}
}

// Fails if a syntax error stops being recorded as a KindSyntax diagnostic
// naming the file, its line, and the fix, or if Read starts returning a reader
// for a document it could not parse.
func TestCollectorReadSyntaxError(t *testing.T) {
	c := &Collector{}
	if r := c.Read("bad.toml", []byte("a = 1\nb = \n")); r != nil {
		t.Fatal("Read returned a reader for invalid TOML")
	}
	diags := diagnosticsOf(t, c.Err())
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	d := diags[0]
	if d.Kind != KindSyntax || d.File != "bad.toml" || d.Pos.Line != 2 || d.Fix != "write valid TOML" {
		t.Errorf("syntax diagnostic = %+v", d)
	}
	if !strings.HasPrefix(d.Error(), "bad.toml:2:") || !strings.HasSuffix(d.Error(), "; write valid TOML") {
		t.Errorf("rendering = %q", d.Error())
	}
	if !errors.Is(c.Err(), ErrSyntax) {
		t.Error("the collected error does not match ErrSyntax")
	}
}

// Fails if Collector.Add stops absorbing every diagnostic of a decode's
// aggregate, with the file, line, and path the decode engine fills, into the
// same sorted list a Reader reports to.
func TestCollectorAddsDecodeDiagnostics(t *testing.T) {
	type config struct {
		Name string `toml:"name"`
		Port int    `toml:"port,required"`
	}
	path := "configs/decode.toml"
	doc := parseWithFileName(t, path, "name = \"x\"\n\n[extra]\nk = 1\n")
	_, err := Decode[config](doc)
	c := &Collector{}
	c.Add(err)
	c.Add(nil)
	diags := diagnosticsOf(t, c.Err())
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %v", len(diags), c.Err())
	}
	// Sorted by line: the missing key sits at the root table's line 1, the
	// unknown table at its header on line 3.
	if diags[0].Kind != KindMissingKey || diags[0].Path != "port" || diags[0].File != path || diags[0].Pos.Line != 1 {
		t.Errorf("first = %+v", diags[0])
	}
	if diags[1].Kind != KindUnknownTable || diags[1].Path != "extra" || diags[1].File != path || diags[1].Pos.Line != 3 {
		t.Errorf("second = %+v", diags[1])
	}
}

// Fails if a Value stops answering its type, line, and contents, or Describe
// stops quoting strings and naming everything else.
func TestValueAccessors(t *testing.T) {
	r, _ := readerFor(t, "f.toml", "s = \"x\"\nf = 1.5\nd = 1979-05-27\nl = [1, 2]\nt = { a = 1 }\n")
	cases := []struct {
		key      string
		typ      ValueType
		describe string
	}{
		{"s", ValueString, `"x"`},
		{"f", ValueFloat, "a float"},
		{"d", ValueDateTime, "a date or time"},
		{"l", ValueList, "a list"},
		{"t", ValueTable, "a table"},
	}
	for i, tc := range cases {
		v, held := r.Peek(tc.key)
		if !held || v.Type() != tc.typ || v.Describe() != tc.describe || v.Line() != i+1 {
			t.Errorf("%s: type %s, describe %q, line %d; want %s, %q, %d", tc.key, v.Type(), v.Describe(), v.Line(), tc.typ, tc.describe, i+1)
		}
	}
	if v, _ := r.Peek("f"); v.Float() != 1.5 || v.Text() != "" || v.Int() != 0 {
		t.Errorf("float accessors: %v %q %d", v.Float(), v.Text(), v.Int())
	}
	if v, _ := r.Peek("l"); len(v.List()) != 2 || v.List()[1].Int() != 2 {
		t.Errorf("List() = %+v", v.List())
	}
	if v, _ := r.Peek("t"); v.List() != nil {
		t.Error("List() on a table returned elements")
	} else if _, ok := v.Node(); ok {
		t.Error("Node() on a table reported a node")
	} else if child := r.Child("t", v); child == nil || child.Path() != "t" {
		t.Error("Child on a table value returned no reader")
	}
	if v, _ := r.Peek("s"); r.Child("s", v) != nil {
		t.Error("Child on a string value returned a reader")
	}
}

// Fails if PathWithoutIndices stops spelling a path the way headers do.
func TestPathWithoutIndices(t *testing.T) {
	for in, want := range map[string]string{
		"command[0].flag[1]": "command.flag",
		"a.b":                "a.b",
		"x[12]":              "x",
		"":                   "",
	} {
		if got := PathWithoutIndices(in); got != want {
			t.Errorf("PathWithoutIndices(%q) = %q, want %q", in, got, want)
		}
	}
}
