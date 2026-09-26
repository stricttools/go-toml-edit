+++
title = "CLAUDE.md"
+++
# go-toml-edit

Comment-preserving TOML parser and editor library for Go. Single `tomledit` package, no CLI.

## Architecture

Hybrid AST with byte ranges. Every node records its span in the original source. Trivia (comments, whitespace, blank-line runs) is attached to nodes rather than discarded. On serialization, clean fragments splice their original bytes; dirty (edited) fragments re-render from semantic values. This gives zero-diff round-trips for untouched regions.

### Parse-edit-render pipeline

1. Lexer tokenizes source, producing tokens with byte offsets
2. Parser builds AST from tokens, attaching trivia (comments, whitespace, blank lines) to the nearest node
3. Each node stores its byte range `[start, end)` in the original source
4. Edit operations (Set, Delete, RenameKey) mark the affected FRAGMENTS dirty and propagate dirtiness upward through parent references
5. Render: clean fragments splice `src[start:end]`; dirty fragments serialize from their semantic value in the canonical spellings

### Source files

Everything is one package at the repository root; `scripts/` holds one-off migration tools that are not part of the library. File names name their subject: the lexer, the parser, the read-layer fold, path resolution, the edit surface, the fragment renderer, the formatter, the decode engine and the diagnostics each have a file of their own.

### Key design decisions

- What the library preserves is CONTENT — comments, values, structure.
  Whitespace is formatting, not content: this library reformats TOML, and
  `Format()` is strict about how output looks (its table separation is not
  an option)
- An API or behavioral asymmetry must name what it protects, or it falls
  (write-side conservatism protects documents from destruction; read-side
  withholding protects nothing and is not done)
- Single package: everything is in `tomledit`, no internal/ subpackages
- Two read surfaces: the AST answers syntactic questions (what the file contains and how it is written), the read-layer answers logical ones (what the document means). The read-layer is a post-parse fold, built lazily, cached under a generation counter, and invalidated whole by any write
- Path syntax supports: `server.host`, `array[0]`, `array[-1]`, `"key.with.dots"`; `ParsePath`/`JoinPath` are the exported helpers and `JoinPath` is the single quoting authority for path text
- `Node` unifies all AST types with Type(), Span(), Raw(), Comment(), LeadingComments(); `Scalar` is the sub-interface the value-carrying kinds implement, adding Value() and the typed `As*` accessors
- Node fields are unexported: reads go through the accessor block (returned slices are copies), and writes go only through the scalar-payload mutator and the structure mutator
- Spans reflect the most recent Parse: edits never recompute them; programmatically created nodes carry the zero (invalid) Span
- Dirty tracking is per-fragment, so only edited fragments re-render and everything else splices
- One diagnostic type: every document-dependent failure is an `*Error` with a kind, path and position; decode collects its violations into an `*Errors` aggregate
- Decoding is strict and strictness is the only mode; one engine, descriptor-driven, with the reflection front end deriving a descriptor from a struct

## Build and test

```sh
go test ./...           # all tests including toml-test compliance
go test -bench .        # benchmarks against BurntSushi/toml
go test -fuzz FuzzParse # fuzz the parser
```

## Testing strategy

- Audit test files (`*_audit_test.go`) cover parser, serializer, comments, edits, diff, merge, unmarshal, walk, format, cursor
- TOML 1.0 compliance: the full valid and invalid corpora from the official toml-test suite, with the 1.1-only cases skipped (the skip set is derived from toml-test's own version-filtered listing, and the case counts are asserted in the test)
- Round-trip fidelity: `Parse(x).Bytes()` must equal `x` for any valid TOML
- The corpus battery runs the fidelity properties over the whole valid corpus: untouched round-trip, value-mutation sibling isolation, trivia-mutation value isolation, rename construct isolation, renderer correctness with lexeme splicing disabled, and Set-equality byte stability
- The exported surface is held by a committed API snapshot (`testdata/api-snapshot.txt`); any addition or removal fails the test until it is regenerated
- Fuzz testing for parser robustness

## Release workflow

This project uses [rlsbl](https://github.com/smm-h/rlsbl) for release orchestration.

- The bump type, description and context go in `.rlsbl/releases/unreleased.toml` (scaffold it with `rlsbl release init`); there is no flag form of them
- Release with `rlsbl release run --no-allow-dirty --watch --approve-consequential`
- Go library -- no publish step. Tagged releases are available via `go get`.
- Never publish manually, and never push manually -- the release is the only push

## Conventions

- Go library only -- no CLI, no binary
- Full TOML 1.0 compliance is non-negotiable: the toml-test suite must pass. TOML 1.1 is out of scope
- Comments are first-class: any operation that silently loses comments is a bug
- Round-trip fidelity: `Parse(x).Bytes()` must equal `x` for any valid TOML input
- No runtime dependencies: the non-test sources import the standard library only, asserted by a test
- BurntSushi/toml is a dev dependency for benchmarks only, not a runtime dependency
- toml-test/v2 is a test dependency for compliance verification only
