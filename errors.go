package tomledit

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrorKind classifies a diagnostic. The set is closed: every kind has exactly
// one matching sentinel (KindSyntax has ErrSyntax, and so on), and a
// diagnostic reports itself equal to the sentinel of its own kind through
// errors.Is.
type ErrorKind int

const (
	// KindSyntax is a lexing or parsing failure.
	KindSyntax ErrorKind = iota
	// KindUnknownKey is a decoded key matching no field of the target.
	KindUnknownKey
	// KindUnknownTable is a decoded table or array-of-tables matching no
	// field of the target; the diagnostic's Keys field lists the direct
	// child keys of the offending construct.
	KindUnknownTable
	// KindMissingKey is a required key that the document does not carry.
	KindMissingKey
	// KindTypeMismatch is a value whose kind is not acceptable for the
	// target; the diagnostic's Expected and Got fields name both sides.
	KindTypeMismatch
	// KindInexact is a value the target cannot hold exactly: an out-of-range
	// integer, a lossy conversion, or a length mismatch. The diagnostic's
	// Value field carries the offending value.
	KindInexact
	// KindNotFound is a path naming nothing in the document.
	KindNotFound
	// KindBadPath is a syntactically invalid path.
	KindBadPath
	// KindWrongContainer is a path step that is structurally inapplicable:
	// a key on an array, an index on a scalar, a concrete-node operation on
	// a path that names no single node.
	KindWrongContainer
	// KindBadInput is an invalid input value to an editing operation.
	KindBadInput
	// KindConflict is an edit refused because it would produce an invalid
	// document.
	KindConflict
	// KindRoundTrip is a write whose rendered bytes did not survive a
	// re-parse; the diagnostic's Offset field carries the byte offset of the
	// first divergence.
	KindRoundTrip
	// KindInvalidValue is a value a hand-reading caller refused: present, and
	// usually of an accepted type, but not a value the caller allows (an empty
	// list, a false that only restates an absence, a name breaking a rule).
	// Reader.Errorf, Reader.ErrorfAt and Collector.Errorf report this kind.
	KindInvalidValue
)

var errorKindNames = [...]string{
	KindSyntax:         "syntax error",
	KindUnknownKey:     "unknown key",
	KindUnknownTable:   "unknown table",
	KindMissingKey:     "missing key",
	KindTypeMismatch:   "type mismatch",
	KindInexact:        "inexact value",
	KindNotFound:       "not found",
	KindBadPath:        "bad path",
	KindWrongContainer: "wrong container",
	KindBadInput:       "bad input",
	KindConflict:       "conflict",
	KindRoundTrip:      "round-trip failure",
	KindInvalidValue:   "invalid value",
}

// String returns the human-readable name of the kind.
func (k ErrorKind) String() string {
	if int(k) >= 0 && int(k) < len(errorKindNames) {
		return errorKindNames[k]
	}
	return fmt.Sprintf("ErrorKind(%d)", int(k))
}

// kindError is the type of the per-kind sentinels below. It is unexported so
// that the sentinels can only be compared, never constructed or switched on by
// a consumer: matching is errors.Is against the sentinel.
type kindError ErrorKind

func (k kindError) Error() string { return "tomledit: " + ErrorKind(k).String() }

// The kind sentinels. Match a diagnostic against one with errors.Is:
//
//	if errors.Is(err, tomledit.ErrNotFound) { ... }
//
// Every ErrorKind has exactly one sentinel and every sentinel has exactly one
// kind; TestErrorKindSentinelDrift fails when one is added without the other.
var (
	ErrSyntax         error = kindError(KindSyntax)         // a lexing or parsing failure
	ErrUnknownKey     error = kindError(KindUnknownKey)     // a key matching no field of the target
	ErrUnknownTable   error = kindError(KindUnknownTable)   // a table matching no field of the target
	ErrMissingKey     error = kindError(KindMissingKey)     // a required key the document does not carry
	ErrTypeMismatch   error = kindError(KindTypeMismatch)   // a value whose kind the target refuses
	ErrInexact        error = kindError(KindInexact)        // a value the target cannot hold exactly
	ErrNotFound       error = kindError(KindNotFound)       // a path naming nothing
	ErrBadPath        error = kindError(KindBadPath)        // a syntactically invalid path
	ErrWrongContainer error = kindError(KindWrongContainer) // a structurally inapplicable path step
	ErrBadInput       error = kindError(KindBadInput)       // an invalid input to an editing operation
	ErrConflict       error = kindError(KindConflict)       // an edit that would produce an invalid document
	ErrRoundTrip      error = kindError(KindRoundTrip)      // rendered bytes that did not survive a re-parse
	ErrInvalidValue   error = kindError(KindInvalidValue)   // a value a hand-reading caller refused
)

// Error is the one diagnostic type of this package. Parse, edit, and access
// failures are all reported as *Error, so a caller can match them structurally
// (errors.As) or by kind (errors.Is against the matching Err* sentinel)
// instead of by message text:
//
//	var diag *tomledit.Error
//	if errors.As(err, &diag) {
//	    fmt.Println(diag.Kind, diag.Pos.Line, diag.Pos.Column)
//	}
//
// Which fields are populated depends on the kind and on what the reporting
// site knew: Pos (line, column and byte offset) and Snippet are filled for
// every parse-stage diagnostic; Span only where the reporting site knows the
// extent of the construct it concerns, which for the parse stage means the
// parser's token diagnostics -- the lexer's and the duplicate-definition
// tracker's carry the zero Span. Path is filled for path-addressed
// operations, File whenever the document's origin is known (see ParseWithFileName
// and Collector.Read), and the remaining fields by the kinds documented on
// ErrorKind. A diagnostic reported through a Reader carries a line without a
// column (Pos.Column is 0), and Fix when the reporting site knows what to write
// instead. An unpopulated field carries its zero value.
type Error struct {
	Kind     ErrorKind // what went wrong
	Path     string    // document path, in this package's path syntax
	Pos      Position  // primary source position; the zero Position when unknown
	Span     Span      // source range of the construct concerned, when known
	Message  string    // human-readable description, without position or file
	File     string    // source file, empty when no file is known
	Snippet  string    // source excerpt, parse-stage diagnostics
	Expected string    // type-mismatch detail: what the target accepts
	Got      string    // type-mismatch detail: what the document carries
	Value    any       // the offending value, for inexact and bad-input kinds
	Keys     []string  // the direct child keys, for unknown-table diagnostics
	Offset   int       // KindRoundTrip: first divergence in the rendered bytes
	Fix      string    // what to do about it ("write port = 8080", "remove it"); empty when unknown

	err error // the underlying error this diagnostic reports, if any
}

// Error renders the diagnostic in the compiler convention: the location, the
// path and the message, joined with ": ", with every absent part left out, and
// the fix, when there is one, after the message and "; ". The location is
// "file:line:column" when both the file and a position are known,
// "line:column" with a position alone, and the file alone when there is no
// position; a position without a column (Pos.Column 0) leaves the column out.
// A diagnostic reads as one of
//
//	config.toml:3:10: expected a value, got end of input
//	3:10: expected a value, got end of input
//	config.toml: server.port: key not found
//	config.toml:4: server.port: is missing; write port = <integer>
//	server.port: key not found
//	key not found
func (e *Error) Error() string {
	var parts []string
	if e.Pos.IsValid() {
		loc := fmt.Sprintf("%d", e.Pos.Line)
		if e.Pos.Column > 0 {
			loc += fmt.Sprintf(":%d", e.Pos.Column)
		}
		if e.File != "" {
			loc = e.File + ":" + loc
		}
		parts = append(parts, loc)
	} else if e.File != "" {
		parts = append(parts, e.File)
	}
	if e.Path != "" {
		parts = append(parts, e.Path)
	}
	parts = append(parts, e.text())
	return strings.Join(parts, ": ")
}

// text is the message with the fix, when there is one, after "; ".
func (e *Error) text() string {
	if e.Fix == "" {
		return e.Message
	}
	return e.Message + "; " + e.Fix
}

// Unwrap returns the underlying error this diagnostic reports, or nil.
func (e *Error) Unwrap() error { return e.err }

// Is reports whether the diagnostic matches target, which is true for the kind
// sentinel of its own kind.
func (e *Error) Is(target error) bool {
	k, ok := target.(kindError)
	return ok && ErrorKind(k) == e.Kind
}

// Errors is an aggregate of diagnostics reported together: in document order
// from a decode, and sorted by file, line, path, and message from a
// Collector. It renders as its first diagnostic, so a call site that prints
// the error reads like a single failure; the whole list is reachable through
// Diagnostics and through errors.Unwrap semantics:
//
//	var all *tomledit.Errors
//	if errors.As(err, &all) {
//	    for _, d := range all.Diagnostics() { ... }
//	}
//
// errors.As with an *Error target yields the first diagnostic, and errors.Is
// against a kind sentinel matches when any contained diagnostic carries that
// kind. An empty aggregate is never returned: no diagnostics means a nil error.
type Errors struct {
	diags []*Error
}

// Error renders the first diagnostic of the aggregate, or "no diagnostics"
// when it holds none. This package never produces an empty aggregate, but the
// type is exported, so rendering a zero value reports rather than panics.
func (e *Errors) Error() string {
	if len(e.diags) == 0 {
		return "no diagnostics"
	}
	return e.diags[0].Error()
}

// Diagnostics returns every diagnostic of the aggregate, in its order. The
// slice is a copy; the diagnostics are the aggregate's own.
func (e *Errors) Diagnostics() []*Error {
	out := make([]*Error, len(e.diags))
	copy(out, e.diags)
	return out
}

// Unwrap returns every diagnostic of the aggregate, in its order.
func (e *Errors) Unwrap() []error {
	out := make([]error, len(e.diags))
	for i, d := range e.diags {
		out[i] = d
	}
	return out
}

// Collector accumulates diagnostics, from one file or several, and reports
// them together as one error sorted by file, line, path, and message, so a
// tool reading a set of files reports every problem in all of them at once, in
// an order that does not depend on the order the files were read in:
//
//	var c tomledit.Collector
//	if r := c.Read("a.toml", a); r != nil { readA(r) }
//	if r := c.Read("b.toml", b); r != nil { readB(r) }
//	if err := c.Err(); err != nil { return err }
//
// The zero Collector is empty and ready to use.
type Collector struct {
	diags []*Error
}

// Add records every diagnostic err carries: each one of an *Errors aggregate
// (a decode's result, say), or the one *Error err is or wraps. An error
// carrying no diagnostic is recorded as a KindConflict diagnostic wrapping it.
// A nil err records nothing.
func (c *Collector) Add(err error) {
	if err == nil {
		return
	}
	var all *Errors
	if errors.As(err, &all) {
		c.diags = append(c.diags, all.diags...)
		return
	}
	c.diags = append(c.diags, asDiagnostic(err))
}

// Errorf records a KindInvalidValue diagnostic at a line of a file, naming the
// key path it concerns (in this package's path syntax, such as
// "command[0].flag[1].name") and a formatted message. It is the form for a
// problem found across tables or files, which no single Reader owns; within one
// table, Reader.Errorf names the file, line, and path itself.
func (c *Collector) Errorf(file string, line int, path, format string, args ...any) {
	c.diags = append(c.diags, newError(KindInvalidValue, format, args...).atLine(line).inFile(file).atPath(path))
}

// Len returns how many diagnostics the collector holds.
func (c *Collector) Len() int { return len(c.diags) }

// Err returns every diagnostic recorded so far, sorted by file, line, path,
// and message, as an *Errors aggregate, or nil when there are none. The sort
// is stable, so two diagnostics equal in all four keep the order they were
// recorded in. Recording more after Err leaves the returned error unchanged.
func (c *Collector) Err() error {
	if len(c.diags) == 0 {
		return nil
	}
	sorted := make([]*Error, len(c.diags))
	copy(sorted, c.diags)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.text() < b.text()
	})
	return newErrors(sorted)
}

// --- construction ---
//
// Every *Error in this package is built here. Error sites call newError (and
// the builders below) rather than composing the struct themselves, so that a
// new site cannot forget the fields its kind is documented to carry;
// TestDiagnosticsBuiltInOneFile fails on a diagnostic composite literal
// anywhere else.

// newError constructs a diagnostic of the given kind with a formatted message.
func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// syntaxErrorAt constructs a parse-stage diagnostic positioned at pos, with
// its snippet taken from src.
func syntaxErrorAt(src []byte, pos Position, format string, args ...any) *Error {
	return newError(KindSyntax, format, args...).at(pos).inSource(src)
}

// newErrors returns the diagnostics as one aggregate, or nil when there are
// none.
func newErrors(diags []*Error) error {
	if len(diags) == 0 {
		return nil
	}
	return &Errors{diags: diags}
}

// at records the primary source position of the diagnostic.
func (e *Error) at(pos Position) *Error {
	e.Pos = pos
	return e
}

// within records the source range of the construct the diagnostic concerns.
func (e *Error) within(span Span) *Error {
	e.Span = span
	return e
}

// inSource fills the snippet from the source line containing the diagnostic's
// position.
func (e *Error) inSource(src []byte) *Error {
	e.Snippet = snippetAt(src, e.Pos.Offset)
	return e
}

// atPath records the document path the failing operation addressed, unless the
// diagnostic already names one.
func (e *Error) atPath(path string) *Error {
	if e.Path == "" {
		e.Path = path
	}
	return e
}

// inFile records the source file, unless the diagnostic already names one.
func (e *Error) inFile(file string) *Error {
	if e.File == "" {
		e.File = file
	}
	return e
}

// expects records the two sides of a type-mismatch diagnostic: what the target
// accepts, and what the document carries.
func (e *Error) expects(expected, got string) *Error {
	e.Expected = expected
	e.Got = got
	return e
}

// withFix records what to do about the diagnostic.
func (e *Error) withFix(fix string) *Error {
	e.Fix = fix
	return e
}

// atLine records a line-only position: a diagnostic reported through a Reader
// knows the line a key was written on, and names no column.
func (e *Error) atLine(line int) *Error {
	e.Pos = Position{Line: line}
	return e
}

// syntaxDiagnostic turns the error Parse returned for the named file into the
// diagnostic Collector.Read records: the parse diagnostic's kind, position,
// span, snippet, and message, naming the file, with the fix "write valid TOML".
// An error carrying no diagnostic, which Parse never returns, is placed on
// line 1.
func syntaxDiagnostic(file string, err error) *Error {
	var diag *Error
	if !errors.As(err, &diag) {
		return newError(KindSyntax, "%s", err).atLine(1).withFix("write valid TOML").wrapping(err)
	}
	out := *diag
	out.File = file
	out.Path = ""
	out.Fix = "write valid TOML"
	out.err = err
	return &out
}

// listing records the direct child keys of the construct an unknown-table
// diagnostic names.
func (e *Error) listing(keys []string) *Error {
	e.Keys = keys
	return e
}

// asDiagnostic returns the diagnostic err is, or the first one it wraps, and
// wraps err in a fresh one when it carries none -- so a caller that must
// report a *Error always has one.
func asDiagnostic(err error) *Error {
	var diag *Error
	if errors.As(err, &diag) {
		return diag
	}
	return newError(KindConflict, "%s", err).wrapping(err)
}

// atOffset records the byte offset a round-trip diagnostic concerns: where two
// renderings of the same document first differ, or where rendered bytes stopped
// parsing.
func (e *Error) atOffset(offset int) *Error {
	e.Offset = offset
	return e
}

// withValue records the offending value of a bad-input or inexact diagnostic.
func (e *Error) withValue(v any) *Error {
	e.Value = v
	return e
}

// wrapping records the underlying error the diagnostic reports, so that
// errors.Is and errors.As reach it.
func (e *Error) wrapping(err error) *Error {
	e.err = err
	return e
}

// wrapError adds a context prefix to an error's message. When err is, or
// wraps, a diagnostic, the result is that diagnostic with the prefixed message
// -- same kind, position, span, path and file -- reporting the original;
// otherwise the result is an ordinary wrapped error.
func wrapError(err error, format string, args ...any) error {
	prefix := fmt.Sprintf(format, args...)
	var diag *Error
	if errors.As(err, &diag) {
		out := *diag
		out.Message = prefix + ": " + diag.Message
		out.err = err
		return &out
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

// walkDiagnostics calls fn for every diagnostic reachable from err, following
// both unwrapping forms.
func walkDiagnostics(err error, fn func(*Error)) {
	switch e := err.(type) {
	case *Error:
		if e == nil {
			return
		}
		fn(e)
		walkDiagnostics(e.err, fn)
	case interface{ Unwrap() error }:
		walkDiagnostics(e.Unwrap(), fn)
	case interface{ Unwrap() []error }:
		for _, sub := range e.Unwrap() {
			walkDiagnostics(sub, fn)
		}
	}
}

// stampPath records path on every diagnostic reachable from err that does not
// already name one, and returns err. Public entry points use it so that a
// diagnostic reported from deep inside a resolution still names the path the
// caller asked for.
func stampPath(err error, path string) error {
	if err == nil || path == "" {
		return err
	}
	walkDiagnostics(err, func(d *Error) { d.atPath(path) })
	return err
}

// stampFile records file on every diagnostic reachable from err that does not
// already name one, and returns err. It is how a document read from a file
// makes its diagnostics name that file.
func stampFile(err error, file string) error {
	if err == nil || file == "" {
		return err
	}
	walkDiagnostics(err, func(d *Error) { d.inFile(file) })
	return err
}

// diag records the document's origin -- the file it was read from, and the
// path the caller addressed -- on every diagnostic in err. Public methods
// return through it, so a diagnostic reported anywhere below them names both.
func (d *Document) diag(err error, path string) error {
	if err == nil {
		return err
	}
	err = stampPath(err, path)
	if d != nil {
		err = stampFile(err, d.file)
	}
	return err
}

// snippetLimit caps how many bytes of a source line a diagnostic quotes.
const snippetLimit = 60

// snippetAt returns the source line containing offset, truncated to
// snippetLimit bytes. It returns "" when offset falls outside src.
//
// A diagnostic reported at the end of a newline-terminated source sits on the
// empty line after the last newline, where quoting "the line containing the
// offset" would quote nothing; such an offset quotes the last non-empty line
// instead, which is the line the unfinished construct opened on.
func snippetAt(src []byte, offset int) string {
	if offset < 0 || offset > len(src) {
		return ""
	}
	end := offset
	if end == len(src) {
		// Step back over the empty line a newline-terminated source ends on,
		// and over any blank lines before it, to the last line with content.
		for end > 0 && src[end-1] == '\n' {
			end--
		}
	}
	start := end
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	for end < len(src) && src[end] != '\n' {
		end++
	}
	snippet := string(src[start:end])
	if len(snippet) > snippetLimit {
		snippet = snippet[:snippetLimit]
	}
	return snippet
}
