+++
title = "Design Guide"
description = "How go-toml-edit works inside: the lex-parse-render pipeline, the sealed AST, the read-layer, fragment dirty tracking, and round-trip fidelity."
+++

# Design Guide

go-toml-edit is a comment-preserving TOML parser and editor for Go. Most TOML libraries parse into Go values (structs, maps) and discard everything that is not data: comments, whitespace, quoting style, key ordering, blank lines. go-toml-edit takes a different approach. It parses into a lossless AST that retains every byte of the original source, then provides editing operations that surgically modify only the fragments you touch. The result is that unmodified parts of the file come back byte-for-byte identical.

This is what configuration files need. TOML files are often maintained by humans who add comments to explain why a setting exists, group related keys with blank lines, and choose specific quoting styles for readability. A tool that rewrites the entire file after changing one value destroys that context. go-toml-edit exists so that programmatic edits respect the human's work.

## Two surfaces

A parsed document is readable through two surfaces with opposite stances, and most of the individual design decisions below follow from the pair.

- The **syntactic** surface is the AST. It answers what the file *contains*, in the form it was written: spellings, quoting styles, integer bases, trivia, and the source span of every construct. `Walk`, `Resolve` and the concrete node types belong to it.
- The **logical** surface is the read-layer. It answers what the document *means*: values, structure and order, with the spellings folded away. `Root`, `Record` and `Entry` belong to it.

Reads are spelling-blind: through the read-layer an inline table and a header table answer the same questions, and a value's base or quoting style is invisible to a reader who did not ask for it. Writes are structurally conservative: a value write touches value fragments and nothing else, and a structural construct changes only through a structural operation or an explicit `Delete`. The library never rewrites one spelling into another as a side effect of a write the caller meant as a value edit.

## Pipeline

The library follows a three-stage pipeline -- lex, parse, render -- where the lexer tokenizes every byte of the source (including whitespace and comments), the parser builds a lossless AST with trivia attached to content nodes, and the renderer splices the original bytes of clean fragments while re-rendering the dirty ones from their semantic values.

### Lexing

The lexer (`lexer.go`) scans TOML source bytes into a flat sequence of tokens. Every byte in the source is accounted for -- whitespace, newlines, and comments each produce their own token kind. Each token records its raw bytes and its source position: a 1-based line and column, and a 0-based byte offset. The lexer tracks context (key position vs. value position) and bracket nesting depth to correctly distinguish table headers (`[table]`) from array values (`[1, 2, 3]`). The token vocabulary is internal: no exported function takes or returns a token.

### Parsing

The parser (`parser.go`) is a recursive-descent parser that consumes the token stream and builds a tree of AST nodes. The central mechanism is **trivia collection**: before parsing each content node (a key-value pair, a table header), the parser sweeps up all preceding whitespace, blank lines, and comment tokens. These become the node's **trivia** -- metadata that travels with the node through edits and back out during serialization.

Trivia is held in an unexported struct that every node carries via `nodeBase`. It is not part of the public API: comments are read through `Comment()` and `LeadingComments()`, and the exact bytes through `Raw()`.

- leading whitespace: spaces or tabs on the same line before the node
- leading comments: the full comment lines above the node, each with its `#` and its newline
- the blank-line runs around those comments, held as *bytes* rather than as a count, so a blank line carrying whitespace of its own survives verbatim
- the inline gap: the bytes standing between the construct and its inline comment, which is none at all in `x = 1# note`
- the inline comment itself
- the trailing newline

Each node also stores its `raw` bytes -- the exact slice of the original source that produced it -- and a `Span` recording its start and end positions.

### Rendering

The renderer (`render.go`) serializes the AST back to bytes per **fragment** rather than per node. A construct decomposes into the byte ranges it is written from: for a key-value line, the leading trivia, the key bytes, the separator bytes around the `=`, the value bytes, the inline gap, the inline comment and the trailing newline; for a table header, the leading trivia, the per-part key bytes with the brackets and dots around them, the inline comment and the newline; for a container, the brackets or braces, the per-element fragments and the separators between them.

- **Clean fragments** splice their original bytes straight into the output, reading the stored field rather than going through `Raw()` -- which answers a caller with a copy. This is what guarantees byte-for-byte round-trip fidelity.
- **Dirty fragments** are re-rendered from the semantic value, in the canonical spellings of `QuoteString`, `QuoteKey` and `FormatFloat`.

Fragment dirtiness recurses: a clean sub-fragment inside a dirty container still splices its own bytes. So a value write leaves the line's spacing, its inline comment and the quoting of its key exactly as they were; a comment write leaves the value's base and quoting; a write inside an array or inline table leaves every sibling, separator and interior comment; and a rename leaves everything but the renamed key part.

Dirtiness is propagated *upward* at write time through parent references maintained by the structure mutator, so asking whether a subtree contains an edit is a constant-time flag check rather than a walk. Serialization cost is proportional to the number of edits, not to the document size: a 10,000-line config file with one changed value produces output where 9,999 lines are byte-for-byte copies of the original.

One byte belongs to no fragment: the newline the document (or a table body) writes at the join between two constructs when the preceding one did not end in one. A file ending without a final newline gives its last construct none either -- which is right until something is written after it, and then the two would otherwise run together into a document that no longer parses.

## AST structure

### Node and Scalar

All AST nodes implement the `Node` interface, the universal handle:

```go
type Node interface {
    Type() NodeType
    Span() Span
    Raw() []byte
    Comment() string
    LeadingComments() []string

    // ... plus unexported methods, which seal the interface
}
```

`Node` is **sealed**: alongside the exported methods above it declares unexported ones (the dirtiness marks, the parent link, the trivia handle and the raw-byte and span setters the parser and the mutators drive), so only this package can implement it. A caller cannot write a `Node` of its own, and a test cannot substitute a double for one -- the way to obtain a node is to parse a document or to edit one. `Scalar`, which embeds `Node`, is sealed by the same declarations.

`Type()` returns an enum (`NodeDocument`, `NodeTable`, `NodeKeyValue`, `NodeString`, `NodeInteger`, ...). `Span()` returns the source range. `Raw()` returns a *copy* of the original source bytes, so writing into it changes neither the node nor what the document renders. `Comment()` and `LeadingComments()` are the normalized comment getters: they answer the content without the `#` and the whitespace around it, in the same text the path-based setters take.

The value-carrying kinds -- strings, integers, floats, booleans and the four date-time flavors -- implement the `Scalar` sub-interface, which embeds `Node` and adds `Value()` plus the typed accessor family (`AsString`, `AsInt`, `AsFloat`, `AsBool`, `AsTime`, `AsLocalDateTime`, `AsLocalDate`, `AsLocalTime`). Structure-carrying kinds have no `Value()` at all; they expose their children through named accessors instead.

All node struct fields are unexported. Reads go through the accessor block, and every slice a read hands back is a copy -- an accessor returning the document's own backing array would let a caller edit the document by writing into what it read, outside every mutator and every dirtiness mark. Writes go through exactly two funnels: the scalar payload mutator (which clears the stored lexeme and bumps the document's generation counter) and the structure mutator (for every change to container content or key parts).

### Concrete node types

The document root is a `Document` whose children are the top-level constructs: `KeyValueNode`, `TableNode`, `ArrayTableNode`, and `CommentNode`. Each node type carries its own trivia and raw bytes, and captures TOML-specific detail such as key quoting styles, integer bases and string styles.

- **TableNode** represents a `[table]` header. `KeyPath()` gives the decoded key parts (`["server", "database"]` for `[server.database]`) and `Children()` the constructs written under it.
- **ArrayTableNode** represents an `[[array-table]]` header. Same shape as TableNode; multiple entries with the same key path are successive elements of the array.
- **KeyValueNode** pairs a `KeyNode` (`Key()`, possibly dotted) with a value node (`Val()`).
- **KeyNode** carries `Parts()` (decoded strings), `RawParts()` (the original bytes per part) and `Styles()` (quoting style per part -- bare, basic-quoted or literal-quoted). This is what lets the renderer preserve the original quoting of a clean key part, and invalidate only the renamed one.
- **Value nodes**: `StringNode`, `IntegerNode`, `FloatNode`, `BooleanNode`, `DateTimeNode`, `LocalDateTimeNode`, `LocalDateNode`, `LocalTimeNode`. Each holds its payload and the lexeme it was parsed from, plus metadata such as `Style()` or `Base()`.
- **ArrayNode** holds its `Elements()`.
- **InlineTableNode** holds its `Children()`.
- **CommentNode** represents standalone comment lines and blank-line runs that are not attached to any content node (typically at the end of the file, or between tables).

### The read-layer

TOML has several spellings for one logical structure. A table header `[a.b.c]` implicitly creates `a` and `a.b`. Dotted keys `database.host = "localhost"` and `database.port = 5432` form a table under `database`. An inline table is a table too, and `[[servers]]` entries collect under one key. The **read-layer** (`record.go`) is a post-parse *fold* that resolves all of this into one ordered logical tree:

- `(*Document) Root() *Record` is the document's top-level record.
- `(*Record) Entries() iter.Seq[Entry]` yields its keys in first-appearance order; `Len`, `Get` and `Span` answer the rest.
- `(Entry) Kind()` says whether the key holds a value, a record, or an array-of-tables' records; `Node`, `Record`, `Records` and `RecordsSpan` hand back what it holds.

The fold rules: key order is first-appearance order across all binding forms; dotted keys expand into implicit records; a header whose prefixes do not exist creates them implicitly and a later shorter header reopens the implicit record (first-header-wins for anchoring); a sub-table header under an array-of-tables prefix addresses the *last* entry; inline tables fold into ordinary records, so a record's origin spelling is not distinguishable through the layer; every entry carries its key's span and every record its anchoring span. A conflict the parser accepts cannot reach the fold, and any internal impossibility is a hard error rather than a silent guess.

`(*Record) Node()` answers `(node, true)` for a record one concrete construct stands for -- a header table, an inline table, an array-of-tables entry, and the root record (whose node is the `Document`) -- and `(nil, false)` for an implied one. `(Entry) Node()` answers only for value-kinded entries.

The layer is built lazily and cached under a document-level generation counter, so repeated reads share one fold and concurrent reads of a shared document stay safe. Any write drops it. Handles are snapshots: a `Record` obtained before a write keeps answering what the document said then, and mutating a document while iterating its entries is unspecified.

## Path resolution

Paths use dot-separated keys with bracket indices (`server.host`, `items[0]`, `items[-1]`, `"key.with.dots"`). `ParsePath` produces a sequence of `PathSegment` values -- key lookups or index lookups -- and `JoinPath` renders them back, as the single quoting authority for path text.

Resolution (`resolve.go`) walks the **read-layer**, not the raw AST, so a path step means the same thing whatever spelling the document used. A position reached this way is one of three things:

- a **record** -- a table in any spelling, carrying a concrete node when one stands for it and none when the table is only implied;
- a **collection** -- the entries of an array-of-tables, which no single node stands for; indexing it reaches an entry, which is a record with a node;
- a **value** -- a scalar or an array, always a concrete node.

Everything that answers with a `Node` (`Resolve`, `Lookup`, the getters, the edit operations) needs a position that carries one, and refuses a position that does not with `KindWrongContainer`. `Lookup` and `Has` are the comma-ok form and answer about *concrete* nodes, so a logical-only path reports false even though the read-layer carries it -- that asymmetry is the price of one honest answer per surface. Everything that addresses structure -- an index into a collection, a key inside a table -- works from the position itself and never needs a node.

## Diagnostics

Every failure that depends on the document -- its syntax, its keys, its values, the path a caller asked for -- is an `*Error`: a kind, a document path, a position (line, column and byte offset), a span, a message, and kind-specific detail such as expected/got, the offending value, or an unknown table's direct-child inventory. A failure that depends only on the consumer's own Go code -- a target of the wrong shape, an unknown struct-tag option, a descriptor built with a missing sub-descriptor -- is a plain error instead, because there is no document position to point at.

`Error()` renders in the compiler convention: the non-empty parts of `[location, path, message]` joined by `": "`, where location is `file:LINE:COL`, `LINE:COL`, the bare `file`, or absent. A document loaded with `ParseFile` remembers its filename, and every later diagnostic from that document carries it.

Matching is through the standard idioms: `errors.Is` against the kind sentinels (`ErrSyntax`, `ErrNotFound`, `ErrTypeMismatch`, ...), and `errors.As` for structured access. A decode collects every independent violation and returns them as an `*Errors` aggregate, which renders as its first diagnostic and exposes the whole list through `Unwrap() []error`. Parsing stays first-error-only: a parse cannot meaningfully continue past a syntax error.

## Editing model

### Dirty tracking

Parsing produces an all-clean tree. An edit marks the fragments it invalidated and propagates a subtree flag upward. When you call `Set("server.port", 9090)`, the port's value fragment is what re-renders; the `[server]` header, the `host` pair, the separator bytes and every comment splice their original bytes.

### Set and SetCreate

`Set` writes a value at a path; a key the parent does not carry yet is created there. `SetCreate` additionally creates the intermediate tables the path names, as standard `[header]` tables.

Values enter as **Go values** -- string, bool, the integer and float widths, `time.Time`, the three Local types, slices, `map[string]any` (written with keys sorted), and `[]Pair` (written in the order given). An AST node is not a Go value: passing one is refused as an unsupported type. Copying a value from one key to another is a read followed by a write.

Three contracts govern the write:

- **Equality.** A `Set` is a no-op if and only if the bytes it would write are exactly the bytes the value fragment already carries; against a value the library wrote before (which has no lexeme) the comparison is with the canonical rendering. Nothing else counts as equal, and nothing equal is written. A container value is compared as a whole and, when it differs, **replaced wholesale** -- the interior comments and spellings of the old container do not survive. A no-op never clears dirtiness an earlier edit recorded.
- **Refusals.** A name bound by a structural construct -- a header table, an array-of-tables, or a table another construct only implied -- refuses with `KindWrongContainer`: those change through a structural operation, or through an explicit `Delete` followed by the write. A sign-bit NaN and text that is not valid UTF-8 (a string value, a string inside a container, a map or `[]Pair` key, a key the path names) are `KindBadInput`.
- **All-or-nothing.** `SetCreate` converts the value and checks every key before a single intermediate table is made, so a refused write creates nothing and leaves no empty headers behind.

The parent table's spelling decides only *where* the write goes, never whether it happens: a new key of a table a dotted key spelled out joins the same region as that dotted pair, and a new key of a table only a longer header implies arrives under the anchoring header the write gives that table.

### Delete

`Delete` removes the node at a path, handling key-value pairs, entire tables (including their sub-tables), array-of-tables entries and array elements. It is idempotent by contract: a path the document does not carry is a silent no-op, so an ensure-absent loop can call it unconditionally. A path that cannot be parsed is still reported.

### RenameKey

`RenameKey` renames the **binding**, whatever constructs spell it out -- which is why it works on header paths and not only on key-value ones. A name bound by a value is renamed in the pair that writes it; a name bound by a table is renamed in every header naming that table, the headers of the tables nested inside it included, and in every dotted pair written under it; a name bound by an array-of-tables is renamed in every entry's header. Renaming one spelling and leaving the others would produce a document binding two different names to one table's content. The renamed key part is the only fragment invalidated, so brackets, sibling parts, interior whitespace and the line's comment all splice.

### Structural operations

`PermuteChildren` reorders the children of a concrete container (the empty path addresses the document's own children). The order is a *gather*: `order[i]` is the index of the child moving to position `i`, and it must be a total bijection -- a wrong length, an out-of-range index and a repeated one are each `KindBadInput` with nothing reordered. A child's trivia travels with it, so grouping keys moves their comments too. `AppendToArray` and `RemoveFromArray` cover array element insertion and removal, and `EnsureDefaults` seeds the paths a document does not already carry -- in any spelling -- creating missing intermediates as standard tables and returning exactly the paths it added.

## Comment preservation

Comments are preserved at three levels in the AST, so that every comment in the original source survives parsing, editing, and serialization without loss or displacement, regardless of which nodes are modified.

1. **Leading comments**: full comment lines above a node, held in trivia with their `#` and newline, together with the blank-line runs between them.
2. **Inline comments**: a comment on the same line after a value, held in trivia beside the gap that separates it from the value.
3. **Standalone comments**: `CommentNode` entries in the document or table children list, for comments not attached to any key-value pair (at the end of a file, or between tables with no following pair).
4. **Array comments**: arrays carry per-element comments through element trivia, plus the comments after the last element and before the closing `]`.

`SetComment` and `SetLeadingComments` on `Document` are the public spelling; per-node setters are internal. They take the text without the `#`, which is exactly what `Comment()` and `LeadingComments()` answer -- so content read from one document and written into another is written as it was read. Two inputs do not survive that round trip, both because content and marker stop being separable once the marker is gone: content that itself begins with `#`, and the empty comment (which the setters read as removal). A caller needing the bytes exactly reads `Raw()`.

The text has to be something a comment can carry: valid UTF-8 and no control character other than a tab -- the lexer's own rules, mirrored at the write, so a document cannot be written into a form that no longer parses back into itself. Validation runs *before* path resolution, so bad text on a missing path reports `KindBadInput` rather than `KindNotFound`, and one refused element refuses a whole block. A comment write targeting a key inside an inline table refuses with `KindWrongContainer`: TOML gives an inline table no place to put a comment.

During `Merge`, what travels is the comments of the line that binds a key. For a new key, that is the comments written above it and the one written after it on the same line; comments written *inside* a container value do not travel, because a container that is new in the target is written from its values, so a comment between an array's elements is dropped. For an existing key, source leading comments are appended to the target's leading comments and source inline comments fill in only where the target has none -- which means merging one source twice doubles its leading comments.

## Round-trip guarantee

The round-trip guarantee states that for any valid TOML input `x`, calling `Parse(x).Bytes()` produces output identical to `x`, byte for byte, with no changes to whitespace, comments, quoting style, key ordering, or blank lines. This property is enforced by the test suite, including a pass over every file in the toml-test valid corpus, fuzz testing, and the full compliance suite.

The guarantee holds because:

- The lexer captures every byte (including whitespace and comments) as tokens with their raw bytes and offsets.
- The parser stores all trivia -- whitespace, comments, blank-line runs, newlines -- on the node it belongs to.
- Each node stores its complete raw bytes, and each scalar the lexeme it was written as.
- The renderer splices the original bytes of every clean fragment.

After editing, the guarantee narrows to: **unmodified fragments** are reproduced byte-for-byte, and only what an edit invalidated is written anew.

## Canonical rendering

What the library writes -- and only that -- is written in canonical form:

- **String**: the basic (double-quoted) form; escapes limited to TOML's own set, with lowercase `\u` hex for control characters; non-ASCII verbatim.
- **Integer**: decimal digits, an optional leading `-`, no underscores, no `+`.
- **Float**: the shortest form that reads back as the same `float64`, with a float marker always present (`.0` appended when the shortest form has neither a fractional part nor an exponent); `-0.0` keeps its sign; the non-finite values are `nan`, `inf` and `-inf`, never `+nan`, `-nan` or `+inf`.
- **Boolean**: `true` / `false`.
- **Date-times**: RFC 3339 with an uppercase `T`, seconds always present, fractional seconds trimmed of trailing zeros, `Z` for a zero offset and `±HH:MM` otherwise; the local flavors the same minus the offset.

`QuoteString`, `QuoteKey` and `FormatFloat` are exported so a consumer rendering TOML by hand can reuse them. They are **total**: every Go string and every float64 has an output, and a string that is not valid UTF-8 renders each invalid byte as `U+FFFD`. Their inverse property is scoped to valid UTF-8, which costs nothing, because the write paths refuse invalid UTF-8 before it can reach a renderer.

All TOML-valid spellings remain valid *input* and are preserved byte-for-byte while untouched. Canonicalization applies only to what the library writes.

## Spans

Each node records a `Span` -- the half-open source range `[Start, End)` it occupied in the original source, with each end carrying a line, a column, and a byte offset. Spans are set by `Parse` and reflect the most recent parse. Important constraints:

- Edit operations do not update spans. A node whose value was changed still carries the span from when it was parsed.
- Programmatically created nodes (via `Set`, `Merge`, ...) carry the zero Span (`IsValid()` returns false).
- To get fresh spans after editing, serialize with `Bytes()` and re-parse the result.

The read-layer synthesizes one span the AST has no node for: an array-of-tables collection's span, from the first entry's header start to the last entry's content end.

## Additional capabilities

### Format

`Format()` produces normalized TOML bytes by re-rendering every node from its semantic value, ignoring all raw bytes. It applies consistent formatting: a configurable indentation width, a line width for array wrapping, and an unconditional blank line before table headers. `Format` does not mutate the document -- it returns a new byte slice.

The writer's blank-line grouping survives at document and table-body level: a run of blank lines becomes exactly one. The output never begins with a blank line and always ends with exactly one newline, so blank lines at either end of the document are dropped. The table separation is the one gap the formatter opens itself, and it is unconditional: `Format()` separates every table header from what stands above it, with no option to stop it. Preservation is about content -- the comments -- while whitespace is formatting, and `Format` is where the library is strict about output looking good. The insertion adds only: it never removes a blank line, and never doubles one already there. Arrays are restructured wholesale (inline or multi-line from the configured line width), so blank lines *between array elements* do not survive formatting -- `Bytes` preserves them, and that difference is the distance between the two exits.

### Walk

`Walk` is the syntactic traversal: it walks the syntax tree in the order the file writes it and hands the visitor the concrete nodes, each with the spelling, the trivia and the span it was written with. It produces dot-separated paths with bracket indices for array-of-tables entries (`servers[0].host`), and handles the ownership rule that sub-tables between two `[[array]]` headers with the same key path belong to the preceding entry. The logical traversal is the read-layer: a consumer recurses over `Record.Entries`.

### Diff and Merge

`Diff` compares two documents by collecting all leaf values and producing a list of `Change` values (Added, Removed, Modified). It reads values, never spellings, and the governing property is that a document always compares equal to itself: two not-a-numbers compare equal, and two offset date-times naming one instant compare equal whatever zone offsets they were written in. The counterweight is that two values of different Go types are never equal, which is what keeps an integer and a float apart.

`Merge` copies from another document into this one, on both sides through the read-layer -- so what merges is what the source *means*, and a key the target spells with a longer header counts as present just as much as one with a header of its own. The target wins for every key it already carries, and array-of-tables are atomic.

### Cursor

The `Cursor` API provides fluent, nil-safe navigation: `doc.Key("server").Key("host").String()`. Its position is a read-layer position, so it navigates compound tables and collections. Terminals return `(T, error)`; a cursor captures the first navigation error and propagates it, so a chain that ended early reports that error from its terminal, or from `Err()`.

### Enumeration

Entry enumeration is the read-layer's: `Record.Entries` yields a table's keys in first-appearance order and `Record.Len` counts them, `Entry.Records` hands out an array-of-tables' entries, and an array node's `Elements` hands out a plain array's. The Cursor keeps its own `Items()` (a range-over-func iterator of index/node pairs) and `Len()` for navigating chains.

### Decoding

Decoding is strict, and strictness is the only mode: an unknown key, an unknown table, a value of a refused kind, a value the target cannot hold exactly, and a missing required key are all errors. There is no lenient mode and no option to skip a check.

There is **one engine**. Its core is descriptor-driven: `Spec` and `Field` describe the expected shape as data, and the engine walks the read-layer -- never the raw AST, which is what makes dotted keys, headers and inline tables indistinguishable to validation. The reflection-based struct front end derives a descriptor from a struct and runs that same engine. Key matching is exact-only in both schema sources: a document key differing only in case matches nothing and is therefore an unknown key. A `toml:"-"` tag, and an unexported field, exclude a name from the document's universe entirely, so a document key naming one is as unknown as any other; the only tag option read is `required`, and any other is a construction-time error.

All value conversion is driven by one table, shared by the engine, the struct front end and every accessor family. Conversion *between* TOML types happens only when provably value-preserving (an integer into a float, exactness-checked); narrowing into the declared Go target's width is the caller's explicit choice, range-checked, never silent-wrapping; a float never becomes an integer, however whole it is written; a fixed-size Go array requires an exact length. Nothing runs before the table and no target decodes itself: there is no interface a type implements to take its own decoding over, and a string value is never handed to the target to parse. The engine is the only decode path, so a `time.Time` field refuses an RFC 3339 string exactly as `AsTime` on a string node does, and a type needing a representation the table does not carry decodes the plain value and converts it itself.

The entry points **return the value they build**: `Unmarshal[T](data)`, `Decode[T](doc)`, `DecodeNode[T](node)`, `DecodeOver[T](doc, seed)` and `(*Document) DecodeSpec(spec)`. Because the engine keeps walking after a violation in order to collect the rest, a decode into a caller-supplied target would leave it partially written; allocating the result instead means a failure returns nil and there is nothing to observe. `DecodeOver` covers the defaults-overlay pattern -- the caller supplies a *factory* that builds the seed, so what the decode fills is never memory the caller still holds -- and its second result names the paths the document supplied, in document order.

## Limitations and edge cases

- **Span staleness**: spans become stale after edits. The library does not recompute them, because doing so would require re-lexing the modified output. Re-parse the output of `Bytes()` when accurate spans are needed after editing.
- **Read-layer handles are snapshots**: a `Record` or `Entry` obtained before a write keeps answering what the document said before it. Staleness is documented, not detected.
- **Inline table comments**: TOML 1.0 forbids comments inside inline tables, so a comment write targeting a member of one is refused.
- **Trivia attachment**: the parser attaches trivia to the next content node. A comment at the very end of a file becomes a standalone `CommentNode`; a comment between two tables is attached as leading trivia to the second. This heuristic is correct for real-world files but can produce surprising results for unusual comment placement.
- **Canonicalization on write**: what an edit rewrites comes back in canonical form. A `Set` that changes a value spelled `0x2A` writes `42`; a same-content `Set` over a literal-quoted string converts it to basic quoting. What you Set is what the file says. The bytes an edit did *not* touch keep their spelling exactly, including sibling parts of the same key.
- **Whole-document canonical form** -- table style, key order, global indentation -- is out of scope. `Format` normalizes rendering, not structure.
- **TOML 1.1** is not supported: this library targets TOML 1.0, and 1.1-only syntax is rejected.
