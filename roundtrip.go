package tomledit

import "bytes"

// renderForWrite is the rendering seam of RoundTripVerifiedBytes. It is a
// variable so that the round-trip tests can show the check catches a render
// that does not survive one. Nothing outside a test reassigns it.
var renderForWrite = func(d *Document) []byte { return d.Bytes() }

// RoundTripVerifiedBytes renders the document and returns the rendered bytes
// only after they have proved they survive a round trip: they must parse, and
// re-rendering that parse must reproduce them byte for byte. It computes over
// the document alone and touches no file; the tomlfile subpackage writes a
// document through it, so nothing that fails the check is ever written.
//
// A failure is an *Error of kind KindRoundTrip whose Offset field carries the
// byte at which the two renders disagree -- or, when the rendered bytes do not
// parse at all, the offset at which they stopped being TOML, with the parse
// error wrapped so errors.Is still reaches it. The diagnostic names file, the
// destination the bytes are meant for; an empty file names none.
func (d *Document) RoundTripVerifiedBytes(file string) ([]byte, error) {
	out := renderForWrite(d)
	if err := verifyRoundTrip(out); err != nil {
		return nil, err.inFile(file)
	}
	return out, nil
}

// verifyRoundTrip reports whether rendered bytes survive a parse and a
// re-render, as the diagnostic they fail with.
func verifyRoundTrip(out []byte) *Error {
	back, err := Parse(out)
	if err != nil {
		diag := asDiagnostic(err)
		return newError(KindRoundTrip, "the rendered document does not parse: %s", diag.Message).
			at(diag.Pos).atOffset(diag.Pos.Offset).inSource(out).wrapping(err)
	}
	again := renderForWrite(back)
	if bytes.Equal(out, again) {
		return nil
	}
	off := firstDivergence(out, again)
	return newError(KindRoundTrip,
		"the rendered document does not survive a re-render: %d bytes written, %d bytes read back, first differing at byte %d",
		len(out), len(again), off).atOffset(off)
}

// firstDivergence returns the index of the first byte at which a and b differ,
// which for two byte slices where one is a prefix of the other is the length of
// the shorter.
func firstDivergence(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
