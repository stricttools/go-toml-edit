package tomledit

import (
	"errors"
	"testing"
)

func TestParseArrayOfInlineTables(t *testing.T) {
	input := `items = [{name = "a", value = 1}, {name = "b", value = 2}]` + "\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// Verify we can read values from the array of inline tables
	s, err := doc.GetString("items[0].name")
	if err != nil || s != "a" {
		t.Fatalf("expected 'a', got %q (ok=%v)", s, err)
	}
	s, err = doc.GetString("items[1].name")
	if err != nil || s != "b" {
		t.Fatalf("expected 'b', got %q (ok=%v)", s, err)
	}
	// Round-trip
	out := doc.Bytes()
	if string(out) != input {
		t.Fatalf("round-trip failed:\ngot:  %q\nwant: %q", out, input)
	}
}

func TestParseArrayCommentsPreserved(t *testing.T) {
	input := "arr = [\n    1,\n    # comment between elements\n    2,\n    3,\n]\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	out := doc.Bytes()
	if string(out) != input {
		t.Fatalf("round-trip failed:\ngot:  %q\nwant: %q", out, input)
	}
}

// requireParseError is a helper that asserts Parse returns a syntax diagnostic
// at the given line and column. Returns the diagnostic for further assertions.
func requireParseError(t *testing.T, input string, wantLine, wantCol int) *Error {
	t.Helper()
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatalf("expected parse error for input %q, got nil", input)
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if pe.Kind != KindSyntax {
		t.Errorf("kind = %v, want %v", pe.Kind, KindSyntax)
	}
	if pe.Pos.Line != wantLine || pe.Pos.Column != wantCol {
		t.Errorf("position mismatch: got line=%d col=%d, want line=%d col=%d\n  error: %s",
			pe.Pos.Line, pe.Pos.Column, wantLine, wantCol, pe.Message)
	}
	return pe
}

// =============================================================================
// Parser-level EOF-position tests
// =============================================================================

func TestParseErrorPosition_UnclosedArrayMidLine(t *testing.T) {
	// "a = [1, 2" -- EOF after the "2"
	// a(1) (2)=(3) (4)[(5)1(6),(7) (8)2(9) -> col after advance past 2 = 10
	requireParseError(t, "a = [1, 2", 1, 10)
}

func TestParseErrorPosition_UnclosedArrayMultiLine(t *testing.T) {
	// "a = [\n1, 2" -- newline resets to line 2
	// line 2: 1(1),(2) (3)2(4) -> col after advance past 2 = 5
	requireParseError(t, "a = [\n1, 2", 2, 5)
}

func TestParseErrorPosition_UnclosedInlineTable(t *testing.T) {
	// "a = {b = 1" -- EOF after the "1"
	// a(1) (2)=(3) (4){(5)b(6) (7)=(8) (9)1(10) -> col after 1 = 11
	requireParseError(t, "a = {b = 1", 1, 11)
}

func TestParseErrorPosition_UnclosedTableHeader(t *testing.T) {
	// "[foo" -- EOF after "foo"
	// [(1)f(2)o(3)o(4) -> col after advance past last o = 5
	requireParseError(t, "[foo", 1, 5)
}

func TestParseErrorPosition_UnclosedArrayTableHeader(t *testing.T) {
	// "[[foo" -- EOF after "foo"
	// [[(1,2) f(3)o(4)o(5) -> col after advance past last o = 6
	requireParseError(t, "[[foo", 1, 6)
}

// =============================================================================
// Lexer EOF-token position test
// =============================================================================

func TestLexEOFTokenPosition(t *testing.T) {
	// "a = 1\nb = 2" -- after lexing, EOF should be at line 2, col past "2"
	// Line 2: b(1) (2)=(3) (4)2(5) -> after advance past 2, col = 6
	// But wait, the tokens after 2 on line 2:
	// "b" at col 1, " " at col 2, "=" at col 3, " " at col 4, "2" at col 5
	// After advancing past "2", col = 6. EOF at line 2, col 6.
	tokens, err := lex([]byte("a = 1\nb = 2"))
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	eof := tokens[len(tokens)-1]
	if eof.Type != tokenEOF {
		t.Fatalf("last token is %s, not EOF", eof.Type)
	}
	if eof.Line != 2 || eof.Column != 6 {
		t.Errorf("EOF position: got line=%d col=%d, want line=2 col=6", eof.Line, eof.Column)
	}
}

// =============================================================================
// Guard tests (the lexer catches these)
// =============================================================================

func TestParseErrorPosition_UnclosedString_Guard(t *testing.T) {
	// 'a = "hello' -- unterminated string, caught at lexer level
	// Should have non-zero position (the lexer uses errorfAt with the
	// opening quote's position).
	_, err := Parse([]byte(`a = "hello`))
	if err == nil {
		t.Fatal("expected error for unclosed string")
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if pe.Pos.Line == 0 && pe.Pos.Column == 0 {
		t.Errorf("expected non-zero position for unclosed string, got line=%d col=%d", pe.Pos.Line, pe.Pos.Column)
	}
}

func TestParseErrorPosition_EmptyInput_Guard(t *testing.T) {
	// "" -- empty input should parse successfully (no error)
	_, err := Parse([]byte(""))
	if err != nil {
		t.Fatalf("expected empty input to parse successfully, got: %v", err)
	}
}

// =============================================================================
// definitionTracker position tests
// =============================================================================

func TestParseErrorPosition_DuplicateKey(t *testing.T) {
	// "a = 1\na = 2" -- error should point to the second "a" at line 2, col 1
	requireParseError(t, "a = 1\na = 2", 2, 1)
}

func TestParseErrorPosition_DuplicateTable(t *testing.T) {
	// "[foo]\n[foo]" -- error should point to the second "foo" key
	// Line 2: [(1) foo starts at col 2
	requireParseError(t, "[foo]\n[foo]", 2, 2)
}
