// Package tomledit: Zero-dep TOML editing library for Go with comment preservation.
//
// The tree preserves comments, whitespace and formatting. Values can be read,
// set, deleted and renamed without disturbing unrelated parts of the file. A
// document is serialized back with [Document.Bytes] (round-trip fidelity) or
// reformatted with [Document.Format].
//
// # Two surfaces, two questions
//
// A document is readable through two surfaces, and which one to use follows
// from the question being asked.
//
// The SYNTACTIC surface is the AST: [Document.Walk], [Document.Resolve] and the
// concrete node types. It answers what the file CONTAINS, in the form it was
// written -- spellings, quoting styles, integer bases, comments, blank lines
// and the source span of every construct. A value written as an inline table
// arrives as an [*InlineTableNode]; one written under a header arrives as that
// header's children.
//
// The LOGICAL surface is the read-layer: [Document.Root] returns a [Record],
// whose [Entry] values are the document's keys in first-appearance order. It
// answers what the document MEANS -- values, structure and order with the
// spellings folded away. A dotted key, a [header] table and an inline table are
// indistinguishable through it, and so is a table only a longer header implies.
//
// Read values through the read-layer or the typed accessors; use the AST to
// edit, or to inspect how something was written. Reads are spelling-blind;
// writes are structurally conservative -- a value write touches value fragments
// and nothing else, and a structural construct changes only through a
// structural operation or an explicit [Document.Delete].
//
// # Strictness and diagnostics
//
// Decoding is strict and strictness is the only mode: an unknown key, an
// unknown table, a value of a refused kind, a value the target cannot hold
// exactly, and a missing required key are all errors. There is no lenient mode
// and no option to skip a check.
//
// Every failure that depends on the document -- from parsing, decoding,
// editing or access alike -- is an [*Error] carrying a kind, a document path
// and a position, matchable with errors.Is against the [ErrSyntax] family of
// kind sentinels and readable with errors.As. A decode reports every
// independent violation together as an [*Errors] aggregate in document order;
// parsing reports its first failure alone, because a parse cannot continue past
// a syntax error.
//
// # Reading by hand
//
// A file whose rules go beyond its shape is read key by key through a
// [Reader] over one table, which records every problem in a [Collector]:
// [Collector.Read] parses a file's bytes into a Reader over its top-level
// table, the accessors ([Reader.Str], [Reader.Integer], [Reader.Flag],
// [Reader.Tables], [Reader.Sub], and the rest) read keys and report a missing
// key or a value of the wrong type with a fix saying what to write, and
// [Reader.Done] reports every key nobody read as unknown, with the kinds a
// strict decode uses. [Collector.Err] returns everything recorded, from every
// file, sorted by file, line, path, and message.
//
// # Capabilities
//
//   - Lossless round-trip: parse and re-serialize without losing comments or
//     formatting.
//   - Path-based access: read and write values through paths such as
//     "server.host", "items[0]" and "items[-1]".
//   - Structural editing: create tables and array-of-tables, reorder children,
//     append to and remove from arrays, rename and delete keys, seed defaults.
//   - Strict decoding: into Go structs through [Unmarshal], [Decode],
//     [DecodeNode] and [DecodeOver], or against a hand-built descriptor through
//     [Document.Validate] and [Document.DecodeSpec].
//   - Diff and merge: compare two documents, or merge one into another.
//   - File names and writes: [ParseWithFileName] records a filename for later
//     diagnostics, and [Document.RoundTripVerifiedBytes] renders only bytes
//     that survive a round trip. The package reads and writes no file itself:
//     the tomlfile subpackage reads a file and parses it, and writes a document
//     atomically through that check.
package tomledit
