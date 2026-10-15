package tomledit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The diagnostic contract: one error type, matched by kind through errors.Is
// and structurally through errors.As, and an aggregate that renders as its
// first diagnostic while exposing all of them.

// Fails if the rendering of a diagnostic stops following the compiler
// convention -- the location "file:line:column" ("file:line" when the column
// is unknown), the path and the message joined with ": ", and the fix after
// "; " -- or starts including a part the diagnostic does not carry. Every
// combination of file, position and path presence is covered.
func TestErrorRendering(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "file and position",
			err: &Error{
				Kind: KindSyntax, File: "config.toml",
				Pos: Position{Line: 3, Column: 10, Offset: 42}, Message: "expected a value, got end of input",
			},
			want: "config.toml:3:10: expected a value, got end of input",
		},
		{
			name: "position only",
			err: &Error{
				Kind: KindSyntax,
				Pos:  Position{Line: 3, Column: 10, Offset: 42}, Message: "expected a value, got end of input",
			},
			want: "3:10: expected a value, got end of input",
		},
		{
			name: "file and path, no position",
			err: &Error{
				Kind: KindNotFound, File: "config.toml", Path: "server.port",
				Message: "key not found",
			},
			want: "config.toml: server.port: key not found",
		},
		{
			name: "path only",
			err:  &Error{Kind: KindNotFound, Path: "server.port", Message: "key not found"},
			want: "server.port: key not found",
		},
		{
			name: "message only",
			err:  &Error{Kind: KindNotFound, Message: `key "host" not found`},
			want: `key "host" not found`,
		},
		{
			name: "file only",
			err:  &Error{Kind: KindSyntax, File: "config.toml", Message: "unexpected character"},
			want: "config.toml: unexpected character",
		},
		{
			name: "file, position, path and message",
			err: &Error{
				Kind: KindSyntax, File: "config.toml", Path: "server.host",
				Pos: Position{Line: 2, Column: 1, Offset: 6}, Message: "duplicate key",
			},
			want: "config.toml:2:1: server.host: duplicate key",
		},
		{
			name: "position and path, no file",
			err: &Error{
				Kind: KindSyntax, Path: "server.host",
				Pos: Position{Line: 2, Column: 1, Offset: 6}, Message: "duplicate key",
			},
			want: "2:1: server.host: duplicate key",
		},
		{
			name: "file, line without a column, path, message and fix",
			err: &Error{
				Kind: KindMissingKey, File: "config.toml", Path: "server.port",
				Pos: Position{Line: 4}, Message: "is missing", Fix: "write port = <integer>",
			},
			want: "config.toml:4: server.port: is missing; write port = <integer>",
		},
		{
			name: "line without a column, no file",
			err:  &Error{Kind: KindInvalidValue, Path: "a", Pos: Position{Line: 7}, Message: "is empty"},
			want: "7: a: is empty",
		},
		{
			name: "message and fix only",
			err:  &Error{Kind: KindSyntax, Message: "unexpected character", Fix: "write valid TOML"},
			want: "unexpected character; write valid TOML",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Fails if a diagnostic stops matching the sentinel of its own kind, starts
// matching another kind's sentinel, or stops being reachable through a
// wrapping chain.
func TestErrorIsAndAs(t *testing.T) {
	diag := &Error{Kind: KindNotFound, Message: "nothing here"}

	if !errors.Is(diag, ErrNotFound) {
		t.Errorf("errors.Is(diag, ErrNotFound) = false, want true")
	}
	if errors.Is(diag, ErrSyntax) {
		t.Errorf("errors.Is(diag, ErrSyntax) = true, want false")
	}

	wrapped := fmt.Errorf("reading config: %w", diag)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Errorf("errors.Is through a wrapping chain = false, want true")
	}
	var got *Error
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As through a wrapping chain failed")
	}
	if got != diag {
		t.Errorf("errors.As yielded %v, want the original diagnostic", got)
	}
}

// Fails if a diagnostic stops carrying the error it reports, so that a caller
// can no longer reach the underlying failure.
func TestErrorUnwrapsItsCause(t *testing.T) {
	cause := errors.New("invalid escape sequence")
	diag := newError(KindSyntax, "%s", cause.Error()).wrapping(cause)
	if !errors.Is(diag, cause) {
		t.Errorf("errors.Is(diag, cause) = false, want true")
	}
	if diag.Unwrap() != cause {
		t.Errorf("Unwrap() = %v, want the cause", diag.Unwrap())
	}
}

// Fails if the aggregate stops rendering as its first diagnostic, stops
// exposing every diagnostic in document order, or starts being returned empty.
func TestErrorsAggregateContract(t *testing.T) {
	first := &Error{Kind: KindUnknownKey, Path: "a", Message: "unknown key"}
	second := &Error{Kind: KindMissingKey, Path: "b", Message: "missing key"}
	err := newErrors([]*Error{first, second})
	if err == nil {
		t.Fatalf("newErrors returned nil for a non-empty list")
	}

	if got, want := err.Error(), first.Error(); got != want {
		t.Errorf("Error() = %q, want the first diagnostic %q", got, want)
	}

	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As with an *Errors target failed")
	}
	all := agg.Unwrap()
	if len(all) != 2 || all[0] != error(first) || all[1] != error(second) {
		t.Errorf("Unwrap() = %v, want the diagnostics in document order", all)
	}

	var single *Error
	if !errors.As(err, &single) {
		t.Fatalf("errors.As with an *Error target failed")
	}
	if single != first {
		t.Errorf("errors.As yielded %v, want the first diagnostic", single)
	}

	if !errors.Is(err, ErrUnknownKey) || !errors.Is(err, ErrMissingKey) {
		t.Errorf("errors.Is must match the kind of any contained diagnostic")
	}
	if errors.Is(err, ErrSyntax) {
		t.Errorf("errors.Is matched a kind no contained diagnostic carries")
	}

	if got := newErrors(nil); got != nil {
		t.Errorf("newErrors(nil) = %v, want nil: an empty aggregate is never returned", got)
	}
}

// Fails if the zero aggregate panics when rendered. The package never returns
// an empty aggregate -- no diagnostics is a nil error -- but Errors is
// exported, so a consumer can construct one and print it.
func TestZeroErrorsRenders(t *testing.T) {
	var e Errors
	if got, want := e.Error(), "no diagnostics"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if all := e.Unwrap(); len(all) != 0 {
		t.Errorf("Unwrap() = %v, want no diagnostics", all)
	}
}

// Fails if any error-producing surface stops reporting the unified
// diagnostic: one representative failure per surface, matched by kind through
// errors.Is and structurally through errors.As, as the documented contract
// promises.
func TestDiagnosticContractAcrossSurfaces(t *testing.T) {
	const src = `title = "hello"
tags = [1, 2]
point = { x = 1 }

[server]
host = "localhost"

[[products]]
name = "a"
`
	// wantPath is the path the diagnostic must name; "" means the surface
	// reports no path.
	tests := []struct {
		name     string
		run      func(d *Document) error
		wantKind ErrorKind
		wantSent error
		wantPath string
	}{
		{
			name:     "parse: syntax",
			run:      func(*Document) error { _, err := Parse([]byte("a = = 1\n")); return err },
			wantKind: KindSyntax,
			wantSent: ErrSyntax,
		},
		{
			name:     "resolve: bad path",
			run:      func(d *Document) error { _, err := d.Resolve("tags["); return err },
			wantKind: KindBadPath,
			wantSent: ErrBadPath,
			wantPath: "tags[",
		},
		{
			name:     "resolve: not found",
			run:      func(d *Document) error { _, err := d.Resolve("server.port"); return err },
			wantKind: KindNotFound,
			wantSent: ErrNotFound,
			wantPath: "server.port",
		},
		{
			name:     "resolve: index out of range",
			run:      func(d *Document) error { _, err := d.Resolve("tags[9]"); return err },
			wantKind: KindNotFound,
			wantSent: ErrNotFound,
			wantPath: "tags[9]",
		},
		{
			name:     "resolve: wrong container",
			run:      func(d *Document) error { _, err := d.Resolve("title.nested"); return err },
			wantKind: KindWrongContainer,
			wantSent: ErrWrongContainer,
			wantPath: "title.nested",
		},
		{
			name:     "resolve: key on an array-of-tables collection",
			run:      func(d *Document) error { _, err := d.Resolve("products.name"); return err },
			wantKind: KindWrongContainer,
			wantSent: ErrWrongContainer,
			wantPath: "products.name",
		},
		{
			name:     "set: unsupported value",
			run:      func(d *Document) error { return d.Set("title", make(chan int)) },
			wantKind: KindBadInput,
			wantSent: ErrBadInput,
			wantPath: "title",
		},
		{
			name:     "set: missing parent",
			run:      func(d *Document) error { return d.Set("nope.deep", 1) },
			wantKind: KindNotFound,
			wantSent: ErrNotFound,
			wantPath: "nope.deep",
		},
		{
			name:     "rename: missing key",
			run:      func(d *Document) error { return d.RenameKey("server.port", "p") },
			wantKind: KindNotFound,
			wantSent: ErrNotFound,
			wantPath: "server.port",
		},
		{
			name:     "rename: existing sibling",
			run:      func(d *Document) error { return d.RenameKey("title", "tags") },
			wantKind: KindConflict,
			wantSent: ErrConflict,
			wantPath: "title",
		},
		{
			name:     "rename: an array index",
			run:      func(d *Document) error { return d.RenameKey("tags[0]", "x") },
			wantKind: KindWrongContainer,
			wantSent: ErrWrongContainer,
			wantPath: "tags[0]",
		},
		{
			name:     "new table: already exists",
			run:      func(d *Document) error { return d.NewTable("server") },
			wantKind: KindConflict,
			wantSent: ErrConflict,
			wantPath: "server",
		},
		{
			name:     "new table: index in path",
			run:      func(d *Document) error { return d.NewTable("a[0]") },
			wantKind: KindBadPath,
			wantSent: ErrBadPath,
			wantPath: "a[0]",
		},
		{
			name:     "delete: bad path",
			run:      func(d *Document) error { return d.Delete("a[") },
			wantKind: KindBadPath,
			wantSent: ErrBadPath,
			wantPath: "a[",
		},
		{
			name:     "comment: inside an inline table",
			run:      func(d *Document) error { return d.SetComment("point.x", "no") },
			wantKind: KindWrongContainer,
			wantSent: ErrWrongContainer,
			wantPath: "point.x",
		},
		{
			name:     "comment: missing path",
			run:      func(d *Document) error { return d.SetLeadingComments("server.port", []string{"x"}) },
			wantKind: KindNotFound,
			wantSent: ErrNotFound,
			wantPath: "server.port",
		},
		{
			name:     "cursor: missing key",
			run:      func(d *Document) error { return d.Key("server").Key("port").Err() },
			wantKind: KindNotFound,
			wantSent: ErrNotFound,
		},
		{
			name:     "cursor: index on a scalar",
			run:      func(d *Document) error { return d.Key("title").At(0).Err() },
			wantKind: KindWrongContainer,
			wantSent: ErrWrongContainer,
		},
		{
			name: "ensure defaults: unsupported value",
			run: func(d *Document) error {
				_, err := d.EnsureDefaults([]Default{{Path: "fresh", Value: make(chan int)}})
				return err
			},
			wantKind: KindBadInput,
			wantSent: ErrBadInput,
			wantPath: "fresh",
		},
		{
			// The decode engine reports an aggregate; errors.Is matches the
			// kind of any diagnostic in it and errors.As yields the first, so
			// the contract holds through the aggregate as it does for a lone
			// diagnostic. The target knows every key but "title".
			name: "decode: unknown key",
			run: func(d *Document) error {
				_, err := Decode[struct {
					Tags   []int          `toml:"tags"`
					Point  map[string]int `toml:"point"`
					Server struct {
						Host string `toml:"host"`
					} `toml:"server"`
					Products []struct {
						Name string `toml:"name"`
					} `toml:"products"`
				}](d)
				return err
			},
			wantKind: KindUnknownKey,
			wantSent: ErrUnknownKey,
			wantPath: "title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse([]byte(src))
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			err = tt.run(doc)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !errors.Is(err, tt.wantSent) {
				t.Errorf("errors.Is(err, %v) = false, want true (err: %v)", tt.wantSent, err)
			}
			var diag *Error
			if !errors.As(err, &diag) {
				t.Fatalf("errors.As(*Error) failed for %T: %v", err, err)
			}
			if diag.Kind != tt.wantKind {
				t.Errorf("kind = %v, want %v (err: %v)", diag.Kind, tt.wantKind, err)
			}
			if diag.Message == "" {
				t.Errorf("diagnostic carries no message")
			}
			if tt.wantPath != "" && diag.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", diag.Path, tt.wantPath)
			}
		})
	}
}

// Fails if a parse-stage diagnostic stops carrying a position, a byte offset
// that agrees with it, or the source line it sits on -- the three things every
// error site in the lexer and parser is required to fill.
func TestParseDiagnosticsCarryPositionAndSnippet(t *testing.T) {
	tests := []struct {
		name  string
		input string
		stage string // which stage reports it, for the failure message
	}{
		{"unterminated string", "a = \"hello\n", "lexer"},
		{"invalid character", "a = @\n", "lexer"},
		{"unclosed array", "a = [1, 2", "parser"},
		{"unclosed inline table", "a = {b = 1", "parser"},
		{"unclosed table header", "[foo", "parser"},
		{"missing value", "a =\n", "parser"},
		{"missing equals", "a 1\n", "parser"},
		{"bad integer", "a = 0x\n", "parser"},
		{"duplicate key", "a = 1\na = 2\n", "tracker"},
		{"duplicate table", "[foo]\n[foo]\n", "tracker"},
		{"table over key", "a = 1\n[a]\n", "tracker"},
		{"table over array table", "[[p]]\n[p]\n", "tracker"},
		{"dotted key through a value", "a = 1\na.b = 2\n", "tracker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte(tt.input)
			_, err := Parse(src)
			if err == nil {
				t.Fatalf("%s: expected a parse error for %q", tt.stage, tt.input)
			}
			var diag *Error
			if !errors.As(err, &diag) {
				t.Fatalf("%s: expected *Error, got %T: %v", tt.stage, err, err)
			}
			if diag.Kind != KindSyntax {
				t.Errorf("kind = %v, want %v", diag.Kind, KindSyntax)
			}
			if !diag.Pos.IsValid() {
				t.Fatalf("position is invalid: %+v", diag.Pos)
			}
			if diag.Pos.Offset < 0 || diag.Pos.Offset > len(src) {
				t.Fatalf("offset %d outside the %d-byte source", diag.Pos.Offset, len(src))
			}
			if want := offsetOfLineColumn(src, diag.Pos.Line, diag.Pos.Column); diag.Pos.Offset != want {
				t.Errorf("offset = %d, want %d (line %d, column %d)",
					diag.Pos.Offset, want, diag.Pos.Line, diag.Pos.Column)
			}
			if diag.Snippet == "" {
				t.Errorf("snippet is empty")
			}
			if line := sourceLineAt(src, diag.Pos.Offset); diag.Snippet != line {
				t.Errorf("snippet = %q, want the source line %q", diag.Snippet, line)
			}
		})
	}
}

// sourceLineAt returns the whole source line a diagnostic at offset quotes:
// the line containing offset, except at the end of a newline-terminated source,
// where it is the last line with content. It mirrors snippetAt without the
// length limit.
func sourceLineAt(src []byte, offset int) string {
	if offset < 0 || offset > len(src) {
		return ""
	}
	end := offset
	if end == len(src) {
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
	return string(src[start:end])
}

// Fails if a parser-stage diagnostic stops carrying the span of the construct
// it concerns.
func TestParserDiagnosticCarriesSpan(t *testing.T) {
	src := []byte("a = 1\nb 2\n")
	_, err := Parse(src)
	if err == nil {
		t.Fatalf("expected a parse error")
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if !diag.Span.IsValid() {
		t.Fatalf("span is invalid: %+v", diag.Span)
	}
	if got := string(src[diag.Span.Start.Offset:diag.Span.End.Offset]); got != "2" {
		t.Errorf("span slices %q, want the offending token %q", got, "2")
	}
}

// Fails if a long source line stops being truncated in the snippet, letting a
// diagnostic quote an unbounded amount of source.
func TestSnippetIsTruncated(t *testing.T) {
	long := "a = " + strings.Repeat("1", 200) + "x\n"
	_, err := Parse([]byte(long))
	if err == nil {
		t.Fatalf("expected a parse error")
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if len(diag.Snippet) != snippetLimit {
		t.Errorf("snippet length = %d, want %d", len(diag.Snippet), snippetLimit)
	}
}

// Fails if a diagnostic positioned at the end of a newline-terminated source
// quotes nothing. The end-of-input offset sits on the empty final line, so the
// excerpt worth quoting is the line the unfinished construct opened on.
func TestSnippetAtEndOfInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"unclosed array", "a = [1,\n", "a = [1,"},
		{"empty unclosed array", "a = [\n", "a = ["},
		{"unclosed array over lines", "a = [\n  1,\n", "  1,"},
		{"unclosed array after a comment", "a = [1, # tail\n", "a = [1, # tail"},
		{"trailing blank lines", "a = [1,\n\n\n", "a = [1,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte(tt.input)
			_, err := Parse(src)
			if err == nil {
				t.Fatalf("expected a parse error for %q", tt.input)
			}
			var diag *Error
			if !errors.As(err, &diag) {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			if diag.Pos.Offset != len(src) {
				t.Fatalf("offset = %d, want %d (end of input): this case no longer "+
					"exercises an end-of-input diagnostic", diag.Pos.Offset, len(src))
			}
			if diag.Snippet != tt.want {
				t.Errorf("snippet = %q, want %q", diag.Snippet, tt.want)
			}
		})
	}
}

// =============================================================================
// Parse-error messages name tokens in the reader's vocabulary
// =============================================================================

// A parse error is read by whoever wrote the TOML, not by whoever wrote the
// lexer, so it names what it saw the way the writer typed it: a fixed piece of
// punctuation is quoted as its glyph, and everything else is plain words. The
// lexer's internal spellings -- RightBracket, Newline, EOF, BasicString --
// never appear.
//
// Fails if any parse message reverts to the internal token vocabulary, or if a
// message's wording changes without this pin being updated.
func TestParseErrorMessagesUseReaderVocabulary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unterminated table header", input: "[foo\n", want: "expected ']', got newline"},
		{name: "unterminated array table header", input: "[[foo\n", want: "expected ']]', got newline"},
		{name: "unterminated inline table", input: "a = {b = 1\n", want: "expected '}', got newline"},
		{name: "missing equals", input: "\"x\" \"y\"\n", want: "expected '=', got a string"},
		{name: "missing value", input: "a = \n", want: "expected a value, got newline"},
		{name: "value is an equals sign", input: "a = 1\nb = = 2\n", want: "expected a value, got '='"},
		{name: "array cut short", input: "a = [1, 2\n", want: "expected ',' or ']' in array, got end of input"},
		{name: "array missing separator", input: "a = [1 2]\n", want: "expected ',' or ']' in array, got an integer"},
		{name: "stray equals at top level", input: "= 1\n", want: "'=' is not valid here"},
		{name: "stray bracket at top level", input: "] = 1\n", want: "']' is not valid here"},
		{name: "stray bracket in a table body", input: "[t]\n] = 1\n", want: "']' is not valid in a table body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected a parse error for %q", tt.input)
			}
			var diag *Error
			if !errors.As(err, &diag) {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			if diag.Message != tt.want {
				t.Errorf("message = %q, want %q", diag.Message, tt.want)
			}
			for _, internal := range []string{
				"RightBracket", "LeftBracket", "DoubleRightBracket", "DoubleLeftBracket",
				"RightBrace", "LeftBrace", "Newline", "EOF", "BasicString", "LiteralString",
				"BareKey", "Equals", "Comma", "Integer",
			} {
				if strings.Contains(diag.Message, internal) {
					t.Errorf("message %q names the token type %s by its internal spelling", diag.Message, internal)
				}
			}
		})
	}
}
