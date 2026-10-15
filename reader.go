package tomledit

import (
	"sort"
	"strconv"
	"strings"
)

// The hand-reading front end.
//
// The decode engine reads a document against a description of it -- a Go type
// or a Spec -- and that covers a file whose rules are its shape. A file whose
// rules go beyond its shape (a key whose meaning depends on another, a list
// whose entries must not repeat, a message that says what to write instead) is
// read by hand, key by key, through a Reader. Both paths report the same
// diagnostic type, with the same kinds, so a tool that uses both reports one
// list.
//
// A Reader reads one table of the read-layer. Every key read through it
// becomes known, and Done reports every key nobody asked about as unknown, so
// strictness needs no list of allowed keys kept beside the code that reads
// them.

// ValueType classifies a value a Reader hands back. A table in any spelling is
// ValueTable; a plain array and an array of tables -- whether written as
// [[header]] entries or as an array of inline tables -- are both ValueList.
type ValueType int

const (
	ValueString   ValueType = iota // a string
	ValueInteger                   // an integer
	ValueBoolean                   // true or false
	ValueFloat                     // a float
	ValueTable                     // a table in any spelling
	ValueList                      // an array, or an array of tables
	ValueDateTime                  // an offset date-time, local date-time, local date, or local time
)

var valueTypePhrases = [...]string{
	ValueString:   "a string",
	ValueInteger:  "an integer",
	ValueBoolean:  "a boolean",
	ValueFloat:    "a float",
	ValueTable:    "a table",
	ValueList:     "a list",
	ValueDateTime: "a date or time",
}

// String returns the phrase a diagnostic uses for a value of this type: "a
// string", "an integer", "a date or time".
func (t ValueType) String() string {
	if int(t) >= 0 && int(t) < len(valueTypePhrases) {
		return valueTypePhrases[t]
	}
	return "ValueType(" + strconv.Itoa(int(t)) + ")"
}

// Value is one value of a document as a Reader sees it: its type, the line it
// was written on, and what it holds. It is a view over the document's own
// nodes and records, not a copy of them.
type Value struct {
	typ  ValueType
	line int

	node    Node      // the scalar or array node, for every type but ValueTable and an array of tables
	record  *Record   // ValueTable
	records []*Record // ValueList written as an array of tables
}

// valueOfEntry is the value one entry of a record holds. fallback is the line
// used when the source carries none (a construct built by an edit).
func valueOfEntry(e Entry, fallback int) Value {
	line := lineOf(e.KeySpan(), fallback)
	switch e.kind {
	case EntryRecord:
		return Value{typ: ValueTable, line: line, record: e.record}
	case EntryRecords:
		return Value{typ: ValueList, line: line, records: e.records}
	default:
		return nodeValue(e.node, line)
	}
}

// nodeValue is the value a value node holds, written on its own line, or on
// fallback when the node carries no span. An inline table folds into a record,
// as it does everywhere in the read-layer.
func nodeValue(n Node, fallback int) Value {
	if n == nil {
		return Value{typ: ValueDateTime, line: fallback}
	}
	line := lineOf(n.Span(), fallback)
	switch x := n.(type) {
	case *StringNode:
		return Value{typ: ValueString, line: line, node: n}
	case *IntegerNode:
		return Value{typ: ValueInteger, line: line, node: n}
	case *BooleanNode:
		return Value{typ: ValueBoolean, line: line, node: n}
	case *FloatNode:
		return Value{typ: ValueFloat, line: line, node: n}
	case *ArrayNode:
		return Value{typ: ValueList, line: line, node: n}
	case *InlineTableNode:
		rec, err := foldInlineTable(x)
		if err != nil {
			// A parsed document cannot reach this: the parser refuses every
			// conflict the fold would find. Root panics on the same condition.
			panic("tomledit: " + err.Error())
		}
		return Value{typ: ValueTable, line: line, record: rec}
	default:
		return Value{typ: ValueDateTime, line: line, node: n}
	}
}

// lineOf is the line a span starts on, or fallback when the span is unknown.
func lineOf(s Span, fallback int) int {
	if s.IsValid() {
		return s.Start.Line
	}
	return fallback
}

// Type returns what the value is.
func (v Value) Type() ValueType { return v.typ }

// Line returns the 1-based line the value was written on.
func (v Value) Line() int { return v.line }

// Text returns the string a ValueString holds, and "" for any other type.
func (v Value) Text() string {
	if s, ok := v.node.(*StringNode); ok {
		text, _ := s.Value().(string)
		return text
	}
	return ""
}

// Int returns the integer a ValueInteger holds, and 0 for any other type.
func (v Value) Int() int64 {
	if i, ok := v.node.(*IntegerNode); ok {
		n, _ := i.Value().(int64)
		return n
	}
	return 0
}

// Float returns the float a ValueFloat holds, and 0 for any other type.
func (v Value) Float() float64 {
	if f, ok := v.node.(*FloatNode); ok {
		n, _ := f.Value().(float64)
		return n
	}
	return 0
}

// Bool returns the boolean a ValueBoolean holds, and false for any other
// type.
func (v Value) Bool() bool {
	if b, ok := v.node.(*BooleanNode); ok {
		truth, _ := b.Value().(bool)
		return truth
	}
	return false
}

// List returns the elements a ValueList holds, in document order, and nil for
// any other type. The entries of an array of tables are ValueTable elements,
// each on the line of its own [[header]].
func (v Value) List() []Value {
	if v.typ != ValueList {
		return nil
	}
	if arr, ok := v.node.(*ArrayNode); ok {
		elements := arr.Elements()
		out := make([]Value, 0, len(elements))
		for _, el := range elements {
			out = append(out, nodeValue(el, v.line))
		}
		return out
	}
	out := make([]Value, 0, len(v.records))
	for _, rec := range v.records {
		out = append(out, Value{typ: ValueTable, line: lineOf(rec.Span(), v.line), record: rec})
	}
	return out
}

// Node returns the scalar or array node backing the value, and whether one
// does. A table and an array of tables report false: their contents are read
// through a Reader (Reader.Sub, Reader.Tables, Reader.Child).
func (v Value) Node() (Node, bool) {
	if v.node == nil {
		return nil, false
	}
	return v.node, true
}

// Describe writes the value for a diagnostic: a string quoted, any other value
// by what it is ("an integer", "a table").
func (v Value) Describe() string {
	if v.typ == ValueString {
		return strconv.Quote(v.Text())
	}
	return v.typ.String()
}

// length is the number of elements a list holds.
func (v Value) length() int {
	if arr, ok := v.node.(*ArrayNode); ok {
		return len(arr.Elements())
	}
	return len(v.records)
}

// Reader reads one table of a document by hand, recording every problem it
// finds in a Collector. Every key read through Get or an accessor built on it
// becomes known; Done reports the keys nobody read as unknown.
//
// The accessors share one convention. A required key that is absent is a
// KindMissingKey diagnostic at the table's own line; a value of the wrong type
// is a KindTypeMismatch diagnostic at the value's line; and each diagnostic's
// Fix says what to write, from the write argument the caller passes ("port =
// <integer>", "at least one [[command]] entry"). An accessor that reports a
// problem returns the zero value and false.
type Reader struct {
	c     *Collector
	file  string
	path  string // the table's own key path, empty for the top level
	rec   *Record
	line  int // the table's own line
	known map[string]bool
	lines map[string]int
}

// Read parses one file's bytes and returns a Reader over its top-level table.
// A syntax error is recorded as a KindSyntax diagnostic naming the file, at
// the position Parse reports, with the fix "write valid TOML", and Read
// returns nil.
func (c *Collector) Read(file string, data []byte) *Reader {
	doc, err := Parse(data)
	if err != nil {
		c.diags = append(c.diags, syntaxDiagnostic(file, err))
		return nil
	}
	root := doc.Root()
	return newReader(c, file, "", root, lineOf(root.Span(), 1))
}

func newReader(c *Collector, file, path string, rec *Record, line int) *Reader {
	return &Reader{c: c, file: file, path: path, rec: rec, line: line, known: map[string]bool{}, lines: map[string]int{}}
}

// File returns the name of the file the table was read from.
func (r *Reader) File() string { return r.file }

// Path returns the table's own key path, empty for the top-level table.
func (r *Reader) Path() string { return r.path }

// TableLine returns the line the table was written on: its own header, the
// inline table spelling it, or the construct that implied it; line 1 for the
// top-level table.
func (r *Reader) TableLine() int { return r.line }

// Lines returns the line of every key read so far. The map is the Reader's
// own and fills as keys are read, so a caller may keep it before reading.
func (r *Reader) Lines() map[string]int { return r.lines }

// Keys returns the table's keys in the order they were first written.
// Listing them marks none known.
func (r *Reader) Keys() []string { return recordKeyList(r.rec) }

// KeyPath returns the path of one key of the table: "command[0].flag" for the
// key "flag" of the table "command[0]".
func (r *Reader) KeyPath(key string) string {
	if r.path == "" {
		return key
	}
	return r.path + "." + key
}

// Line returns the line a key was written on, or the table's own line when
// the table does not hold the key.
func (r *Reader) Line(key string) int {
	if v, held := r.Peek(key); held {
		return v.line
	}
	return r.line
}

// Errorf records a KindInvalidValue diagnostic at one key of the table: its
// line (the table's own when the key is absent) and its path.
func (r *Reader) Errorf(key, format string, args ...any) {
	r.ErrorfAt(r.Line(key), r.KeyPath(key), format, args...)
}

// ErrorfAt records a KindInvalidValue diagnostic in the Reader's file at a
// line and path the caller names: an element of a list, say, or a key the
// table derives from another.
func (r *Reader) ErrorfAt(line int, path, format string, args ...any) {
	r.c.Errorf(r.file, line, path, format, args...)
}

// report records a diagnostic of the given kind at a line and path of the
// Reader's file, with a fix.
func (r *Reader) report(kind ErrorKind, line int, path, fix, format string, args ...any) *Error {
	d := newError(kind, format, args...).atLine(line).inFile(r.file).atPath(path).withFix(fix)
	r.c.diags = append(r.c.diags, d)
	return d
}

// Peek returns the value of a key without reading it: the key does not become
// known, and its line is not recorded.
func (r *Reader) Peek(key string) (Value, bool) {
	e, held := r.rec.Get(key)
	if !held {
		return Value{}, false
	}
	return valueOfEntry(e, r.line), true
}

// Get reads a key: it becomes known, and when the table holds it its line is
// recorded in Lines. It returns the key's value and whether the table holds
// it.
func (r *Reader) Get(key string) (Value, bool) {
	r.known[key] = true
	v, held := r.Peek(key)
	if held {
		r.lines[key] = v.line
	}
	return v, held
}

// Has reads a key and reports whether the table holds it.
func (r *Reader) Has(key string) bool {
	_, held := r.Get(key)
	return held
}

// Missing records that a required key is absent: "is missing; write <write>",
// a KindMissingKey diagnostic at the table's own line.
func (r *Reader) Missing(key, write string) {
	r.report(KindMissingKey, r.Line(key), r.KeyPath(key), "write "+write, "is missing")
}

// WrongKind records that a key holds a value of the wrong type: "is <what v
// is>, not <want>; write <write>", a KindTypeMismatch diagnostic whose
// Expected is want and whose Got is what v is.
func (r *Reader) WrongKind(key string, v Value, want, write string) {
	r.report(KindTypeMismatch, r.Line(key), r.KeyPath(key), "write "+write, "is %s, not %s", v.typ, want).
		expects(want, v.typ.String())
}

// Str reads a string key. A required key that is absent is reported missing;
// an optional one returns false without a diagnostic.
func (r *Reader) Str(key string, required bool, write string) (string, bool) {
	v, held := r.Get(key)
	if !held {
		if required {
			r.Missing(key, write)
		}
		return "", false
	}
	if v.typ != ValueString {
		r.WrongKind(key, v, "a string", write)
		return "", false
	}
	return v.Text(), true
}

// Integer reads an integer key, as Str reads a string one.
func (r *Reader) Integer(key string, required bool, write string) (int64, bool) {
	v, held := r.Get(key)
	if !held {
		if required {
			r.Missing(key, write)
		}
		return 0, false
	}
	if v.typ != ValueInteger {
		r.WrongKind(key, v, "an integer", write)
		return 0, false
	}
	return v.Int(), true
}

// Boolean reads a key written as true or false; the fix it reports is
// "write <key> = true or <key> = false". It returns the value and whether the
// key was read without a problem.
func (r *Reader) Boolean(key string, required bool) (bool, bool) {
	write := key + " = true or " + key + " = false"
	v, held := r.Get(key)
	if !held {
		if required {
			r.Missing(key, write)
		}
		return false, false
	}
	if v.typ != ValueBoolean {
		r.WrongKind(key, v, "a boolean", write)
		return false, false
	}
	return v.Bool(), true
}

// Flag reads an optional key whose only value is true. Its absence is its
// other meaning, so false is a second spelling of the absence and is refused
// as a KindInvalidValue diagnostic: "is false, and its absence already means
// that; write <key> = true, or remove the key".
func (r *Reader) Flag(key string) bool {
	v, held := r.Get(key)
	if !held {
		return false
	}
	fix := "write " + key + " = true, or remove the key"
	if v.typ != ValueBoolean {
		r.WrongKind(key, v, "a boolean", key+" = true, or remove the key")
		return false
	}
	if !v.Bool() {
		r.report(KindInvalidValue, v.line, r.KeyPath(key), fix, "is false, and its absence already means that")
		return false
	}
	return true
}

// StrList reads a key holding a list of strings. An element that is not a
// string is reported at its own line and path (key[i]), and the read returns
// false.
func (r *Reader) StrList(key string, required bool, write string) ([]string, bool) {
	v, held := r.Get(key)
	if !held {
		if required {
			r.Missing(key, write)
		}
		return nil, false
	}
	if v.typ != ValueList {
		r.WrongKind(key, v, "a list of strings", write)
		return nil, false
	}
	elements := v.List()
	out := make([]string, 0, len(elements))
	for i, el := range elements {
		if el.typ != ValueString {
			r.report(KindTypeMismatch, el.line, r.KeyPath(key)+"["+strconv.Itoa(i)+"]", "write "+write, "is %s, not a string", el.typ).
				expects("a string", el.typ.String())
			return nil, false
		}
		out = append(out, el.Text())
	}
	return out, true
}

// NonEmpty refuses a key holding an empty list, as a KindInvalidValue
// diagnostic: "is empty; write <write>". It neither reads the key nor reports
// its absence or its type.
func (r *Reader) NonEmpty(key, write string) {
	if v, held := r.Peek(key); held && v.typ == ValueList && v.length() == 0 {
		r.report(KindInvalidValue, v.line, r.KeyPath(key), "write "+write, "is empty")
	}
}

// Tables reads a key holding a list of tables -- [[key]] entries, or an array
// of inline tables -- and returns one Reader per table, each at its path
// key[i]. An absent key returns nil without a diagnostic. An element that is
// not a table is reported and skipped.
func (r *Reader) Tables(key string) []*Reader {
	v, held := r.Get(key)
	if !held {
		return nil
	}
	header := PathWithoutIndices(r.KeyPath(key))
	if v.typ != ValueList {
		r.WrongKind(key, v, "a list of tables", "[["+header+"]] entries")
		return nil
	}
	var out []*Reader
	for i, el := range v.List() {
		p := r.KeyPath(key) + "[" + strconv.Itoa(i) + "]"
		if el.typ != ValueTable {
			r.report(KindTypeMismatch, el.line, p, "write [["+header+"]] entries", "is %s, not a table", el.typ).
				expects("a table", el.typ.String())
			continue
		}
		out = append(out, r.Child(p, el))
	}
	return out
}

// Sub reads a key holding one table and returns a Reader over it. An absent
// key returns nil without a diagnostic; a value that is not a table is
// reported, with the fix "write [<header>]", and returns nil.
func (r *Reader) Sub(key string) *Reader {
	v, held := r.Get(key)
	if !held {
		return nil
	}
	if v.typ != ValueTable {
		r.WrongKind(key, v, "a table", "["+PathWithoutIndices(r.KeyPath(key))+"]")
		return nil
	}
	return r.Child(r.KeyPath(key), v)
}

// Child returns a Reader over a table value at the path the caller names,
// sharing this Reader's file and collector: the form for a table met inside a
// list the caller walks itself. It returns nil when v is not a table.
func (r *Reader) Child(path string, v Value) *Reader {
	if v.typ != ValueTable || v.record == nil {
		return nil
	}
	return newReader(r.c, r.file, path, v.record, lineOf(v.record.Span(), v.line))
}

// PeekTrue reports whether the table holds key written as true, without
// reading it.
func (r *Reader) PeekTrue(key string) bool {
	v, held := r.Peek(key)
	return held && v.typ == ValueBoolean && v.Bool()
}

// Done reports every key of the table nobody read, in the order the keys were
// written: "is not a key of this table", with the fix "remove it, or write one
// of <the keys read, sorted>", or "remove it" when nothing was read. A key
// holding a table or a list of [[header]] tables is a KindUnknownTable
// diagnostic listing its direct child keys, any other a KindUnknownKey one --
// the kinds the decode engine reports for the same refusals.
func (r *Reader) Done() {
	known := make([]string, 0, len(r.known))
	for k := range r.known {
		known = append(known, k)
	}
	sort.Strings(known)
	fix := "remove it"
	if len(known) > 0 {
		fix = "remove it, or write one of " + strings.Join(known, ", ")
	}
	for e := range r.rec.Entries() {
		if r.known[e.Key()] {
			continue
		}
		line := valueOfEntry(e, r.line).line
		path := r.KeyPath(e.Key())
		switch e.kind {
		case EntryRecord:
			r.report(KindUnknownTable, line, path, fix, "is not a key of this table").listing(recordKeyList(e.record))
		case EntryRecords:
			var keys []string
			if len(e.records) > 0 {
				keys = recordKeyList(e.records[0])
			}
			r.report(KindUnknownTable, line, path, fix, "is not a key of this table").listing(keys)
		default:
			r.report(KindUnknownKey, line, path, fix, "is not a key of this table")
		}
	}
}

// PathWithoutIndices returns a key path without its array indices, the way a
// file's headers spell it: "command[0].flag[1]" gives "command.flag".
func PathWithoutIndices(path string) string {
	var b strings.Builder
	depth := 0
	for _, c := range path {
		switch {
		case c == '[':
			depth++
		case c == ']':
			depth--
		case depth == 0:
			b.WriteRune(c)
		}
	}
	return b.String()
}
