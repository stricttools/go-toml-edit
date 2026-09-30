# A VS Code extension backed by a TOML language server built on go-toml-edit

## Context

go-toml-edit is a comment-preserving TOML 1.0 parser and editor: a lossless
AST where every node records its source span, trivia attached to nodes,
byte-identical round-trips for untouched regions, a strict formatter
(`Format`), path-addressed edits (`Set`, `SetCreate`, `Delete`, `RenameKey`,
`SetComment`, the structural operations), a logical read-layer (`Root`,
`Record`, `Entry`), strict decoding with every violation collected
(`Unmarshal`, `Decode`), runtime descriptor validation (`Spec`, `Validate`,
`DecodeSpec`), `Diff`, and `Merge`. Every document-dependent failure is an
`*Error` carrying a kind, a path, and a position.

Those are the pieces a language server is made of: diagnostics with
positions, spans for symbols and ranges, a formatter, a rename that follows
every spelling of a key, and schema validation. An editor extension is the
most direct way to put the library's guarantees (comments are never lost, the
formatter is strict, TOML 1.0 compliance is complete) in front of anyone
editing TOML by hand or through an agent.

## Problem

- TOML files are edited in VS Code with whatever TOML extension the user
  happens to have, whose formatter and parser disagree with this library's.
  A project that formats its TOML with `Format` cannot have the editor format
  it the same way, and the editor's formatter may reorder or drop what this
  library preserves.
- There is no editor surface for the library's diagnostics: duplicate keys,
  table redefinitions, and other TOML 1.0 violations surface only when a Go
  program parses the file.
- Descriptor validation (`Spec`) and strict decoding exist only as Go API; a
  project cannot point its editor at a schema and see unknown keys, missing
  required keys, type mismatches, and inexact values as squiggles.

## Proposal

A VS Code extension, the name to be chosen, whose language server is written
in Go on top of this library.

### Features

- **Diagnostics.** Syntax errors with their position and span. A validation
  pass against a schema, when one is configured for the file, reporting every
  violation the library collects (`KindUnknownKey`, `KindUnknownTable`,
  `KindMissingKey`, `KindTypeMismatch`, `KindInexact`) at the offending node's
  span, with the error kind as the diagnostic code.
- **Formatting.** `textDocument/formatting` returns `Format()` output as a
  single edit, with the indent and line-width options mapped from the LSP
  formatting options and settings. Range formatting is not offered, since the
  formatter works on whole documents and its table separation is not optional.
- **Document symbols and outline.** Tables, array-of-tables entries, and keys
  from `Walk` with their spans, so the outline and breadcrumbs follow the
  logical structure of the document, dotted keys and inline tables included.
- **Hover.** The full logical path of the key or value under the cursor (as
  `JoinPath` spells it), its type, and, with a schema, the field's declared
  kind and whether it is required.
- **Rename.** `textDocument/rename` on a key runs `RenameKey`, which renames
  the binding in every spelling; the server returns the byte diff between the
  old and new document as text edits, so comments and untouched regions are
  byte-identical.
- **Completion.** With a schema, the key names `Fields` declares for the
  table at the cursor that the document does not yet carry. `Spec` has no
  notion of allowed values, so value completion would need the descriptor to
  grow one first; that is a separate library decision.
- **Folding and selection ranges** from node spans.
- **Code actions.** Add missing required keys from the schema (through
  `EnsureDefaults` or `SetCreate`), remove an unknown key (`Delete`), each
  applied through the library so comments stay where they are.

### Schemas

The library's schema is the `Spec` descriptor, which is Go data. The editor
needs a schema file, and the format is an open design question:

- A TOML or JSON serialization of `Spec`: one authority, no translation; a
  new format to document and version.
- JSON Schema, which other TOML tooling and many published schemas use: wide
  reuse; a translation from JSON Schema into `Spec` that cannot express
  everything JSON Schema can, which would have to refuse what it cannot
  translate rather than ignore it.

Which files a schema applies to is also to be designed (a setting mapping
globs to schemas, or a directive comment in the file). Without a schema the
server offers everything above except the schema-driven features.

### Architecture

- The **extension** is TypeScript, as every VS Code extension is: it starts
  the server over stdio with `vscode-languageclient`, registers it for the
  `toml` language, and contributes the settings.
- The **language server** is Go and imports this library. It keeps the
  parsed `*Document` of each open file, re-parses on change (the library has
  no incremental parser, and TOML files are small enough for whole-file
  re-parses), and answers the protocol requests from the AST and the
  read-layer. Candidate Go libraries for the protocol layer are the
  `go.lsp.dev` family or a hand-written JSON-RPC layer.

## Gaps in the library the server exposes

- **Parsing stops at the first failure.** While a file has a syntax error the
  server has one diagnostic and no document, so the outline, hovers, and
  folding disappear until it is fixed. Options: keep serving the last good
  parse for structure (with positions that may be stale), or add error
  recovery to the parser (a real change to the parser, with its own tests and
  compliance implications). This is the most consequential design decision.
- **Spans reflect the last parse and edits do not recompute them.** The server
  must re-parse after every change rather than edit its copy; since clients
  send full text or deltas that the server applies to its own buffer, this
  costs nothing beyond the re-parse.
- **Byte offsets versus LSP positions.** `Span` carries 1-based lines and
  columns and a byte offset; LSP positions are 0-based and default to UTF-16
  code units. The server must convert, negotiating UTF-8 position encoding
  where the client supports it.

## Solutions considered

### A Go language server importing the library (recommended)

- Pros: the editor's formatter, diagnostics, and rename are the library's
  own, so they cannot disagree with what a Go program does to the same file;
  any LSP client can use it.
- Cons: a binary to build and ship per platform; the first-failure parser
  limits what the editor can show on broken files.

### Extension-only logic, compiling the library to WebAssembly

- Pros: no native binary to ship per platform; runs in VS Code for the Web.
- Cons: the protocol plumbing then lives in TypeScript and calls into the
  WebAssembly module, splitting the logic across two languages; the WASM
  bundle size and startup cost.

### Where the server lives

- The library has no runtime dependencies (a test asserts it over the
  non-test sources) and is a library only, with no binary. The server needs a
  protocol dependency and is a binary, so it cannot be part of the root
  package. Options: a nested Go module in this repository (for example under
  a new directory with its own `go.mod` requiring the library), which keeps
  the server and the library in step but adds a second module to release; or
  a separate repository in the stricttools family, which keeps this one a
  pure library. Either way the no-runtime-dependency test for the library is
  unchanged.

## Affected files and new components

- New: the server (its own module, per the choice above), with protocol
  handlers, a span-to-LSP position converter, and a document-to-text-edit
  differ for rename and code actions.
- New: the TypeScript extension with its `package.json`, client, and
  settings.
- New: the schema file format and its loader into `Spec` (`spec.go` is the
  data model it targets).
- Possibly changed: `parser.go` and `lexer.go`, if error recovery is chosen.
- Tests: the server's handlers against `testdata/` and the toml-test corpus
  (every valid case must produce no diagnostics and a formatting result equal
  to `Format`; every invalid case must produce a diagnostic).

## Distribution

- Publish the extension to both the VS Code Marketplace and Open VSX, the open
  registry VSCodium, Cursor, and other VS Code derivatives install from. Each
  needs its own publisher account and token; `vsce` publishes to the first and
  `ovsx` to the second, from the same `.vsix`.
- The extension is TypeScript/JavaScript; the server is a Go binary, shipped
  either inside per-platform `.vsix` packages (VS Code supports
  platform-specific extensions) or found on PATH, with a missing binary
  reported as an error naming the setting rather than silently downloaded.
- The library releases with rlsbl, and rlsbl has no VS Code publishing target;
  the extension's publish step would need to be added to rlsbl or run from a
  workflow of its own.

## Effort

- Server with diagnostics, formatting, symbols, folding, and hover without a
  schema: a few days.
- Rename and schema-free code actions through the library's edit surface, with
  the text-edit differ: two to three days.
- Schema format, loader, schema diagnostics, completion, and schema code
  actions: about a week.
- Parser error recovery, if chosen: a week or more, dominated by keeping full
  TOML 1.0 compliance and round-trip fidelity.
- Extension packaging and dual-registry publishing: a day or two.
