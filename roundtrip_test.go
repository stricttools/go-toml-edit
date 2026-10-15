package tomledit

import (
	"errors"
	"strings"
	"testing"
)

// RoundTripVerifiedBytes hands out rendered bytes only after they survive a
// parse and a re-render. The rendering seam (renderForWrite) is what makes the
// refusals testable without a document that fails to round-trip on demand.
// Each test restores what it replaced.

const roundTripDoc = `# a document
title = "example"

[server]
host = "localhost"
port = 8080
`

// Fails if RoundTripVerifiedBytes stops returning what the document renders.
func TestRoundTripVerifiedBytes_ReturnsTheRenderedDocument(t *testing.T) {
	doc := parseOrFail(t, roundTripDoc)
	out, err := doc.RoundTripVerifiedBytes("config.toml")
	if err != nil {
		t.Fatalf("RoundTripVerifiedBytes: %v", err)
	}
	if string(out) != roundTripDoc {
		t.Errorf("rendered %q, want %q", out, roundTripDoc)
	}
}

// Fails if RoundTripVerifiedBytes stops refusing bytes that do not parse. The
// diagnostic is KindRoundTrip carrying the offset the bytes stopped being TOML
// at, with the parse error still reachable, and it names the destination.
func TestRoundTripVerifiedBytes_RefusesRenderedBytesThatDoNotParse(t *testing.T) {
	const path = "configs/config.toml"

	original := renderForWrite
	renderForWrite = func(*Document) []byte { return []byte("broken = \n") }
	defer func() { renderForWrite = original }()

	out, err := parseOrFail(t, roundTripDoc).RoundTripVerifiedBytes(path)
	if out != nil {
		t.Errorf("a refused render still handed out %q", out)
	}
	if !errors.Is(err, ErrRoundTrip) {
		t.Fatalf("RoundTripVerifiedBytes = %v, want %v", err, ErrRoundTrip)
	}
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("the round-trip diagnostic does not report the syntax error under it: %v", err)
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("RoundTripVerifiedBytes = %v (%T), want a *Error", err, err)
	}
	if diag.Offset == 0 {
		t.Errorf("the diagnostic carries offset 0, want where the bytes stopped parsing")
	}
	if diag.File != path {
		t.Errorf("the diagnostic names file %q, want the destination %q", diag.File, path)
	}
	if diag.Path != "" {
		t.Errorf("the diagnostic carries Path %q, but Path is a document path and this failure names no document position", diag.Path)
	}
	if rendered := diag.Error(); strings.Count(rendered, path) != 1 {
		t.Errorf("the rendered diagnostic names the file %d times, want once: %q", strings.Count(rendered, path), rendered)
	}
}

// Fails if RoundTripVerifiedBytes stops checking that the rendered bytes
// survive a re-render, or stops reporting where the two disagree.
func TestRoundTripVerifiedBytes_RefusesARenderThatDoesNotSurviveAReRender(t *testing.T) {
	original := renderForWrite
	calls := 0
	renderForWrite = func(*Document) []byte {
		calls++
		if calls == 1 {
			return []byte("a = 1\n")
		}
		return []byte("a = 2\n")
	}
	defer func() { renderForWrite = original }()

	_, err := parseOrFail(t, roundTripDoc).RoundTripVerifiedBytes("config.toml")
	if !errors.Is(err, ErrRoundTrip) {
		t.Fatalf("RoundTripVerifiedBytes = %v, want %v", err, ErrRoundTrip)
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("RoundTripVerifiedBytes = %v (%T), want a *Error", err, err)
	}
	if diag.Offset != 4 {
		t.Errorf("the diagnostic reports the first divergence at byte %d, want 4", diag.Offset)
	}
}

// Fails if an empty file name starts being recorded on the diagnostic.
func TestRoundTripVerifiedBytes_AnEmptyFileNamesNone(t *testing.T) {
	original := renderForWrite
	renderForWrite = func(*Document) []byte { return []byte("broken = \n") }
	defer func() { renderForWrite = original }()

	_, err := parseOrFail(t, roundTripDoc).RoundTripVerifiedBytes("")
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("RoundTripVerifiedBytes = %v (%T), want a *Error", err, err)
	}
	if diag.File != "" {
		t.Errorf("the diagnostic names file %q, want none", diag.File)
	}
}
