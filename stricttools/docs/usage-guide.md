+++
title = "Usage Guide"
description = "Parsing, querying, editing and writing TOML with go-toml-edit: the read-layer, path and node comment access, strict decoding, merging and diffing."
nav_group = "Guides"
nav_order = 1
+++

# Usage Guide

go-toml-edit is a Go library for parsing, editing, and serializing TOML documents while preserving comments, whitespace, and formatting. It provides a lossless AST that keeps every byte of the original document intact, only re-rendering the fragments you explicitly modify.

## Installation

```
go get github.com/smm-h/go-toml-edit
```

All public types and functions live in the `tomledit` package. The package name does not match the last element of the module path, so import it under its name:

```go
import tomledit "github.com/smm-h/go-toml-edit"
```

The package has no runtime dependencies: it imports the standard library and nothing else.

## Two surfaces, two questions

A parsed document is readable through two surfaces, and which one to use follows from the question being asked.

- The **syntactic** surface is the AST -- `Walk`, `Resolve` and the concrete node types. It answers what the file *contains*, in the form it was written: spellings, quoting styles, integer bases, comments, blank lines and the source span of every construct.
- The **logical** surface is the read-layer -- `doc.Root()`, `Record` and `Entry`. It answers what the document *means*, with the spellings folded away: a dotted key, a `[header]` table and an inline table are indistinguishable through it.

Read values through the read-layer or the typed accessors; use the AST to edit, or to inspect how something was written.

## Parsing TOML

### From a byte slice

`tomledit.Parse` takes a `[]byte` of TOML source and returns a `*Document` AST that preserves every byte of the original input, including comments, whitespace and quoting styles.

```go
input := []byte(`
# Server configuration
[server]
host = "localhost"  # bind address
port = 8080
`)

doc, err := tomledit.Parse(input)
if err != nil {
    log.Fatal(err)
}
```

Parsing stops at the first failure and reports it as an `*tomledit.Error` of kind `KindSyntax`, carrying the position, the span and the source line of the offending construct:

```go
var diag *tomledit.Error
if errors.As(err, &diag) {
    fmt.Printf("parse error at line %d, column %d: %s\n",
        diag.Pos.Line, diag.Pos.Column, diag.Message)
}
```

See [Diagnostics](#diagnostics) for the full contract.

### From a file

`tomledit.ParseFile` reads and parses a file, and the document remembers the filename: every diagnostic it later produces -- from parsing, decoding, access or editing -- names the file it came from.

```go
doc, err := tomledit.ParseFile("config.toml")
if err != nil {
    log.Fatal(err)
}
```

A file that cannot be read is reported as the underlying `*fs.PathError`, matchable with `errors.Is` against `fs.ErrNotExist` and the rest -- not as an `*Error`, because nothing was parsed and so there is nothing to diagnose. Reading the bytes yourself and calling `Parse` is still available; the document then carries no filename.

## Querying values

### Typed getters

Each getter resolves a path and reads the value it names, returning `(T, error)`:

```go
host, err := doc.GetString("server.host")       // (string, error)
port, err := doc.GetInt("server.port")           // (int64, error)
debug, err := doc.GetBool("server.debug")        // (bool, error)
rate, err := doc.GetFloat("server.rate")         // (float64, error)
ts, err := doc.GetTime("server.started_at")      // (time.Time, error)
```

The error distinguishes the failures a boolean cannot: `KindBadPath` for a path that does not parse, `KindNotFound` for one naming nothing, `KindWrongContainer` for a step that does not apply to what it addresses, `KindTypeMismatch` for a value of the wrong kind, and `KindInexact` for a value the target cannot hold exactly.

`GetFloat` accepts an integer the target holds exactly; `GetInt` never accepts a float, however whole it is written. `GetTime` reads an offset date-time verbatim and a local date-time or local date as UTC, and refuses a string -- even one spelling a valid RFC 3339 timestamp. A `time.Time` *decode* target refuses one too: both surfaces read the one conversion table, and no target decodes itself. To read a string as a time, read it as a string and parse it.

Path syntax:

- Dot separates keys: `"server.host"`, `"database.connection.pool_size"`
- Brackets index into arrays: `"items[0]"`, `"items[-1]"` (negative indices count from the end)
- Brackets may follow each other for nested arrays: `"matrix[0][1]"`
- A quoted segment carries a key verbatim, so a key with dots stays one segment: `` `server."host.name"` ``
- A backslash escapes the next byte: `` `host\.name` `` is the single key `host.name`

### Resolve, Lookup and Has

`Resolve` returns the node a path names, with a diagnostic when it cannot:

```go
node, err := doc.Resolve("server.host")
if err != nil {
    fmt.Println("resolution failed:", err)
}
```

`Lookup` and `Has` are the comma-ok form:

```go
node, ok := doc.Lookup("server.host")
if doc.Has("server.tls") { /* ... */ }
```

All three answer about **concrete nodes**. A path naming something no single node stands for -- an array-of-tables collection, or a table only a longer header or a dotted key implies -- is `KindWrongContainer` from `Resolve` and `false` from `Lookup` and `Has`, even though the read-layer carries it. Index the collection (`products[0]`), or read the structure through the read-layer.

### The read-layer

`doc.Root()` returns the document's logical view: a `Record` whose entries are the top-level keys in first-appearance order, whatever spelling produced them.

```go
for entry := range doc.Root().Entries() {
    switch entry.Kind() {
    case tomledit.EntryValue:
        node, _ := entry.Node()
        fmt.Printf("%s = %v\n", entry.Key(), node.(tomledit.Scalar).Value())
    case tomledit.EntryRecord:
        rec, _ := entry.Record()
        fmt.Printf("%s is a table of %d keys\n", entry.Key(), rec.Len())
    case tomledit.EntryRecords:
        recs, _ := entry.Records()
        fmt.Printf("%s is an array of %d tables\n", entry.Key(), len(recs))
    }
}
```

`Record` carries `Entries()`, `Len()`, `Get(key)`, `Span()` and `Node()`; `Entry` carries `Key()`, `KeySpan()`, `Kind()`, `Node()`, `Record()`, `Records()` and `RecordsSpan()`. `Record.Node()` answers `(node, true)` for a record one construct stands for -- a header table, an inline table, an array-of-tables entry, or the document itself for the root record -- and `(nil, false)` for an implied one.

This is also how you count and enumerate: `Record.Len` is a table's key count, `Entry.Records` hands out an array-of-tables' entries (its length is the count), and an array node's `Elements()` hands out a plain array's.

```go
entry, ok := doc.Root().Get("servers")
if ok {
    servers, _ := entry.Records()
    fmt.Println(len(servers))
}
```

Layer handles are snapshots: valid until the next mutation of the document, and stale afterwards -- a record obtained before a write keeps answering what the document said then. Mutating a document while iterating its entries is unspecified. The layer is built lazily and cached, so repeated reads share one fold, and any write drops it.

### Cursor API

The cursor provides a fluent, nil-safe API for step-by-step navigation. Each call returns a cursor positioned at the next node, and the first navigation error is captured and propagated, so a chain reports it from its terminal.

```go
host, err := doc.Key("server").Key("host").String()
port, err := doc.Key("server").Key("port").Int()
```

Navigate into arrays and array-of-tables with `At`:

```go
name, err := doc.Key("products").At(0).Key("name").String()
```

Check a navigation that ended early with `Err()`:

```go
cursor := doc.Key("nonexistent").Key("path")
if cursor.Err() != nil {
    fmt.Println("navigation error:", cursor.Err())
}
```

A cursor also iterates and counts what it is positioned at:

```go
cursor := doc.Key("servers")
for i, node := range cursor.Items() {
    fmt.Printf("server %d\n", i)
    _ = node
}
fmt.Println(cursor.Len())
```

`Cursor.String()` returns `(string, error)` and is deliberately not a `fmt.Stringer`.

## Modifying values

### Set

`Set` writes a value at a path. A key the parent does not carry yet is created there; a path whose parent does not exist is an error, which is what `SetCreate` relaxes.

```go
err := doc.Set("server.port", 9090)
err = doc.Set("server.host", "0.0.0.0")
err = doc.Set("server.debug", true)
err = doc.Set("server.rate", 0.75)
```

Supported Go types: `string`, `bool`, `int`/`int8`-`int64`, `uint`/`uint8`-`uint64`, `float32`/`float64`, `time.Time`, `tomledit.LocalDateTime`, `tomledit.LocalDate`, `tomledit.LocalTime`, `[]any` and typed slices, `map[string]any` (written with its keys sorted), and `[]tomledit.Pair` (written in the order given). A value is a *Go value*: an AST node is not one, and passing a node -- including one resolved out of this or another document -- is refused as an unsupported type. Copying a value from one key to another is a read followed by a write.

**The equality rule.** `Set` is a no-op if and only if the bytes it would write for the value are exactly the bytes the value fragment already carries. Nothing else counts as equal, and nothing equal is written: the node keeps its spelling, its lexeme and its span, and the document records no edit. A value the library wrote before carries no lexeme, and the comparison is against its canonical rendering.

One rule, no special cases -- NaN spellings, infinities, signed zeros, integer-versus-float, date-time offsets, string quoting and integer bases alike. Two consequences follow. An idempotent tool that writes back values it read normalizes a non-canonical spelling the first time it touches it (`0x2A` set to `42` becomes `42`) and is byte-stable from then on; and a same-content write over a literal-quoted string converts it to basic quoting. What you `Set` is what the file says. Deciding not to write is not undoing a write: a no-op `Set` never clears dirtiness an earlier edit recorded.

**Refusals.** A path naming a key bound by a structural construct -- a `[header]` table, an array-of-tables, or a table another construct only implied -- is refused with `KindWrongContainer`: those change through a structural operation, or through an explicit `Delete` followed by the write. A sign-bit NaN is `KindBadInput`, as is text that is not valid UTF-8 anywhere it appears: a string value, a string inside a container value, a map or `[]Pair` key, or a key the path itself names.

The *parent* table's spelling decides only where a write goes, never whether it happens. A key an existing table already carries is replaced through the pair that binds it; a new key of a table a dotted key spelled out joins the same region as that dotted pair; a new key of a table only a longer header implies arrives under the anchoring header the write gives that table.

### SetCreate

`SetCreate` is `Set` plus the intermediate tables: the ones the path names and the document does not carry are created as standard `[header]` tables appended to the document.

```go
// Even if [database] does not exist yet, this creates it.
err := doc.SetCreate("database.connection.host", "db.example.com")
```

It refuses exactly what `Set` refuses, and a refused write creates nothing: the value is converted and every key checked before a single table is made, so the document is left byte-for-byte as it was rather than carrying empty headers of an abandoned path.

### Arrays and inline tables

Pass a slice to write an array, and a map or a `[]Pair` to write an inline table. A map is written with its keys sorted; `[]Pair` keeps the order given, and refuses a duplicate key.

```go
err := doc.Set("server.ports", []any{8080, 8081, 8082})
err = doc.Set("server.aliases", []string{"a", "b"})

err = doc.Set("server.tls", map[string]any{
    "cert": "/path/to/cert.pem",
    "key":  "/path/to/key.pem",
})

err = doc.Set("server.tls", []tomledit.Pair{
    {Key: "cert", Value: "/path/to/cert.pem"},
    {Key: "key", Value: "/path/to/key.pem"},
})
```

A container value is compared as a whole against the stored container's whole byte range, and **replaced wholesale** when it differs: the interior comments and spellings of the old container do not survive. That is what setting a container value means.

### Array elements by index

```go
err := doc.Set("server.ports[0]", 9090)   // replace the first element
err = doc.Set("server.ports[-1]", 9999)   // replace the last
```

## Adding new sections

### NewTable

```go
err := doc.NewTable("logging")
err = doc.Set("logging.level", "info")
err = doc.Set("logging.file", "/var/log/app.log")
```

`NewTable` refuses exactly what the parser's own duplicate detection refuses. The header must be able to bind its name: anything else in the document already binding it -- a value, an inline table, a table with its own header, an array-of-tables, or a table a dotted key implied -- is `KindConflict`. A table implied only by a *longer* header (the `a` of an earlier `[a.b]`) has no header of its own yet, and this is how it gets one.

The refusal is about the path's final key only. With `apple.color` written under `[fruit]`, creating `[fruit.apple]` is refused and `[fruit.apple.texture]` is not, exactly as TOML has it.

### NewArrayTable

Append a new `[[array-table]]` entry. Multiple entries with the same path are successive elements of the array; use `[-1]` to write into the most recently appended one.

```go
err := doc.NewArrayTable("products")
err = doc.Set("products[-1].name", "Widget")
err = doc.Set("products[-1].price", 9.99)

err = doc.NewArrayTable("products")
err = doc.Set("products[-1].name", "Gadget")
err = doc.Set("products[-1].price", 19.99)
```

## Structural operations

`PermuteChildren` reorders the children of the container a path names; the empty path addresses the document's own children. The order is a **gather**: `order[i]` is the index of the child moving to position `i`, so children `[A, B]` with order `[1, 0]` end up `[B, A]`. It must be a total bijection -- a wrong length, an out-of-range index and a repeated one are each `KindBadInput`, with nothing reordered.

```go
err := doc.PermuteChildren("server", []int{2, 0, 1})
```

The indices address the children as they stand right now, so read them, compute the order, and permute in one editing sequence: an edit in between that adds or removes a child shifts every index after it. A child's own trivia travels with it -- its leading comments, its inline comment and the blank lines before it are part of the child, not of the position it used to hold. Dropping children is composed as delete-then-permute.

`AppendToArray` and `RemoveFromArray` cover array element insertion and removal. Unlike `Delete`, which is idempotent by contract, `RemoveFromArray` names a position that must exist: an index outside the array is `KindNotFound`.

```go
err := doc.AppendToArray("server.ports", 8082)
err = doc.RemoveFromArray("server.ports", 0)
err = doc.RemoveFromArray("server.ports", -1) // the last element
```

`EnsureDefaults` seeds the paths the document does not already carry, in the order given, and returns the paths it added:

```go
added, err := doc.EnsureDefaults([]tomledit.Default{
    {Path: "server.port", Value: 8080},
    {Path: "logging.level", Value: "info"},
})
```

A path the document carries in *any* spelling is left alone, and nothing is ever overwritten. Missing intermediate tables are created as standard `[header]` tables, never inline, so running the same list against the same document twice writes the same bytes. The seeding stops at the first error; everything written before it stays written, and `added` names exactly those paths.

## Removing keys

`Delete` removes the node at a path, handling key-value pairs, entire table sections including their sub-tables, array-of-tables entries, and individual array elements.

```go
err := doc.Delete("server.debug")    // remove a key-value pair
err = doc.Delete("logging")          // remove an entire table
err = doc.Delete("products[0]")      // remove an array-of-tables entry
err = doc.Delete("nonexistent.key")  // no-op, no error
```

Removal is idempotent by contract: a path the document does not carry is a silent no-op, so an ensure-absent loop can call it unconditionally. A path that cannot be parsed is still reported. The spelling of the table the key sits in changes nothing: in a table no single node stands for, the removal reaches the dotted pair that binds a value, or the headers that spell a table out.

## Renaming keys

`RenameKey` renames the **binding**, whatever constructs spell it out -- which is why it works on header paths and not only on key-value ones.

```go
err := doc.RenameKey("server.host", "bind_address")
err = doc.RenameKey("server", "listener")
```

A name bound by a value is renamed in the pair that writes it. A name bound by a table is renamed in every header naming that table, the headers of the tables nested inside it included, and in every dotted pair written under it. A name bound by an array-of-tables is renamed in every entry's header. Renaming one spelling and leaving the others would produce a document binding two different names to one table's content.

The renamed key part is the only fragment invalidated: the brackets, the other parts, the whitespace between them and the line's comment all splice as written. It reports `KindNotFound` when the path names nothing, `KindWrongContainer` when the last segment is an array index or the parent names an array-of-tables rather than one of its entries, `KindConflict` when anything in the parent already binds the new name, and `KindBadInput` when the new name is not valid UTF-8. A refused rename changes nothing.

## Walking the document

`Walk` is the **syntactic** traversal: it walks the syntax tree in the order the file writes it and hands the visitor the concrete nodes, each with the spelling, the trivia and the span it was written with.

```go
err := doc.Walk(func(path string, node tomledit.Node) error {
    fmt.Printf("%s = %v (%s)\n", path, node.(tomledit.Scalar).Value(), node.Type())
    return nil
}, tomledit.WalkLeaves)
```

`Value()` lives on `Scalar`, the sub-interface the value-carrying kinds implement -- so a leaf walk type-asserts to it. Two modes are available:

- `tomledit.WalkLeaves` -- only scalar values; containers are recursed into but not yielded
- `tomledit.WalkAll` -- containers *and* their children are yielded

Return `tomledit.ErrSkipTable` from the callback to skip the children of the current inline table or array; return any other non-nil error to stop the walk. Array-of-tables paths include the index: `"products[0].name"`.

The **logical** traversal is the read-layer: recurse over `Record.Entries`. Use `Walk` to ask what the file contains and how it is written; use the read-layer to ask what the document means.

## Comments

go-toml-edit preserves all comments through its trivia system, which attaches leading comments, inline comments and standalone comments to their nearest content nodes.

### Reading comments

The path-based getters are the direct route, and they mirror the setters exactly:

```go
inline, err := doc.GetComment("server.host")           // "the hostname"
leading, err := doc.GetLeadingComments("server.host")  // []string{"where to bind"}
```

`GetComment` and `GetLeadingComments` resolve a path to the same node `SetComment` and `SetLeadingComments` write to, so what one writes the other reads back, and they refuse the same paths with the same error kinds -- `KindNotFound` for a path naming nothing, `KindWrongContainer` for a member of an inline table (TOML gives an inline table nowhere to put a comment, so nothing there can carry one), `KindBadPath` for a malformed path. A node with no inline comment answers the empty string; a node with no leading comments answers `nil`. Neither is an error.

Both answers are **normalized**: the content without the `#` and the whitespace around it, so a node written `x = 1 # note` answers `"note"`. That is exactly the text the setters take, so content read from one document and written into another is written as it was read.

The node-level getters are the general mechanism underneath. `Comment()` returns the inline comment written on a node's own line and `LeadingComments()` the comment lines written above it, normalized the same way -- which is what a traversal (`Walk`, `Children()`) reads, since it holds nodes rather than paths. Which node owns the comments depends on the construct:

| Construct | Node the comments are on |
| --- | --- |
| A key-bound value (`host = "localhost"`) | The enclosing `*KeyValueNode` -- the pair, not the value |
| A table or an array-of-tables entry | Its header node (`*TableNode`, `*ArrayTableNode`) |
| An array element | The element node itself |

The key-bound case is the one that surprises a reader working from nodes: `Resolve("server.host")` answers the *value* node, and a value node carries no comments of its own, so both node-level getters answer empty on it. (`GetComment("server.host")` has no such problem -- it resolves the pair, as `SetComment` does.) From a node, reach the pair by iterating the enclosing container's `Children()` and matching on the key:

```go
tbl, err := doc.Resolve("server")
if err != nil {
    return err
}
table, ok := tbl.(*tomledit.TableNode)
if !ok {
    return fmt.Errorf("server is not a [table]")
}
for _, child := range table.Children() {
    kv, ok := child.(*tomledit.KeyValueNode)
    if !ok {
        continue
    }
    parts := kv.Key().Parts()
    if len(parts) != 1 || parts[0] != "host" {
        continue
    }
    fmt.Println("inline:", kv.Comment())
    fmt.Println("leading:", kv.LeadingComments())
}
```

For a pair standing under no header at all, `doc.Children()` is the same route. The array element is the asymmetric case: an element's comments sit on the element, so `Resolve("ports[0]")` answers a node whose `Comment()` is the element's own inline comment and needs no such detour.

Two inputs do not survive that round trip, both because content and marker stop being separable once the marker is gone: content that itself begins with `#` (`## section` reads as `# section`, and the setter puts its own marker in front), and the empty comment (`#` reads as `""`, which the setters take to mean removal). `Raw()` is the byte-exact read for a caller that needs the bytes as written.

### Setting inline comments

```go
err := doc.SetComment("server.port", "default HTTP port")
// Produces: port = 8080 # default HTTP port
```

The `#` is added automatically, and an empty string removes the comment:

```go
err := doc.SetComment("server.port", "")
```

### Setting leading comments

`SetLeadingComments` replaces all comment lines above a node. Each element is one line, without the `#`.

```go
err := doc.SetLeadingComments("server.host", []string{
    "The hostname or IP address to bind to.",
    "Use 0.0.0.0 for all interfaces.",
})
```

This produces:

```toml
# The hostname or IP address to bind to.
# Use 0.0.0.0 for all interfaces.
host = "localhost"
```

A nil or empty slice removes the leading comments. An empty *string element* is not removal: the setter puts its `"# "` marker in front of whatever it is given, so an empty element writes the line `"# "` -- a hash and a trailing space, with no content after it.

### What a comment may carry

The text must be something a comment can carry: valid UTF-8, and no control character other than a tab. A newline, a carriage return, U+0000 and U+007F are each refused with `KindBadInput` -- these are the lexer's own rules for reading a comment, mirrored at the write, so a document cannot be written into a form that no longer parses back into itself.

Validation runs **before** the path is resolved: bad text on a path the document does not carry reports `KindBadInput`, not `KindNotFound`. One refused element refuses a whole `SetLeadingComments` call, and a refusal leaves the comments already there untouched.

### Comments and inline tables

TOML gives an inline table no place to put a comment. A comment write targeting a member of one is refused with `KindWrongContainer` -- the container structurally cannot host the operation.

## Comment preservation guarantee

Parsing a document and serializing it back with `Bytes()` produces the exact original bytes, with no changes to comments, whitespace, quoting styles or formatting.

```go
original := []byte(`# My config
[server]
host = "localhost"  # primary host
port = 8080
`)

doc, _ := tomledit.Parse(original)
output := doc.Bytes()

// output is byte-for-byte identical to original
fmt.Println(bytes.Equal(original, output)) // true
```

When you modify a value, only the fragments the edit invalidated are re-rendered. Everything else -- comments, blank lines, indentation, quoting style, even the whitespace around the `=` -- is spliced from the original source:

```go
doc, _ := tomledit.Parse([]byte(`# Server settings
[server]
host = "localhost"  # bind address
port = 8080         # default port
debug = true
`))

doc.Set("server.port", 9090)
doc.Delete("server.debug")

fmt.Print(string(doc.Bytes()))
// Output:
// # Server settings
// [server]
// host = "localhost"  # bind address
// port = 9090         # default port
```

The comment on `port` survives the value change and keeps its column. The comment on `host` is untouched.

## Serializing back to TOML

### Bytes -- round-trip fidelity

`Bytes()` returns the document as TOML bytes, splicing every byte range no edit touched and re-rendering only the ranges the edits invalidated. Serialization cost is proportional to the number of edits rather than the document size.

```go
output := doc.Bytes()
```

### WriteFile -- atomic, round-trip checked

```go
err := doc.WriteFile("config.toml")
```

`WriteFile` renders the document, proves the rendered bytes parse *and* that re-rendering that parse reproduces them byte for byte, then writes them to a temporary file in the destination's own directory and renames it over the destination in one step. A failure anywhere before the rename leaves the destination exactly as it was and no temporary file behind.

A round-trip failure is an `*Error` of kind `KindRoundTrip` whose `Offset` names the byte at which the two renderings disagree -- or, when the rendered bytes do not parse at all, where they stopped being TOML, with the parse error wrapped. A filesystem failure is reported as the underlying `*fs.PathError`. An existing destination keeps its file mode; a new file is created with mode 0o644 before umask.

### Format -- canonical style

`Format()` re-renders the entire document from semantic values with consistent formatting, ignoring the original raw bytes and quoting choices. It does not mutate the document -- it returns a new byte slice.

```go
output := doc.Format(
    tomledit.WithIndentWidth(2), // indent values under tables; default 0
    tomledit.WithLineWidth(100), // line width before arrays wrap; default 80
)
```

The writer's blank-line grouping survives at document and table-body level: a run of blank lines becomes exactly one. The output never begins with a blank line and always ends with exactly one newline, so blank lines at either end of the document are dropped.

The one gap the formatter opens itself is before a `[table]` or `[[array-table]]` header written flush against what precedes it: `Format` always inserts the missing blank line, and there is no option to stop it. What this library preserves is content -- the comments -- while whitespace is formatting, and `Format` is where the library is strict about output looking good. The insertion adds only: it never removes a blank line, and never doubles one already there.

Arrays are restructured wholesale (inline or multi-line from the configured line width), so blank lines *between array elements* do not survive `Format`. `Bytes` preserves them; that difference is the distance between the two exits.

## Diffing two documents

```go
a, _ := tomledit.Parse([]byte(`name = "Alice"
age = 30
`))
b, _ := tomledit.Parse([]byte(`name = "Bob"
email = "bob@example.com"
`))

changes := tomledit.Diff(a, b)
for _, c := range changes {
    fmt.Printf("%s: %s (old=%v, new=%v)\n",
        c.Kind, c.Path, c.OldValue, c.NewValue)
}
// removed: age (old=30, new=<nil>)
// added: email (old=<nil>, new=bob@example.com)
// modified: name (old=Alice, new=Bob)
```

Change kinds: `tomledit.Added`, `tomledit.Removed`, `tomledit.Modified`.

`Diff` reads values, never spellings. Two documents writing one value differently -- `0x2A` against `42`, `1_000` against `1000`, one instant in two zone offsets, a literal string against a basic one, an array-of-tables against an inline array of inline tables -- report no difference, so a document always compares equal to itself, `nan` values included. Types are not bridged: an integer and a float never compare equal, so `1` and `1.0` are a modification.

## Merging documents

`Merge` copies from another parsed document into this one: only keys the target does not already carry are set, and existing values are never overwritten.

```go
base, _ := tomledit.Parse([]byte(`[server]
host = "localhost"
`))
defaults, _ := tomledit.Parse([]byte(`[server]
host = "0.0.0.0"
port = 8080
`))

err := base.Merge(defaults)
// base.server.host is still "localhost"
// base.server.port is now 8080
```

Both sides are read through the read-layer, so what merges is what the source *means*, not how it is written: a key bound by a dotted key, a header table or an inline table arrives the same way, and a key the target spells with a longer header counts as present just as much as one with a header of its own. Array-of-tables are atomic -- anything already at the path keeps what it has, entries and all.

Comments come along at the level of the line that binds a key. For a key that is new in the target, the comments written above it and the one written after it on the same line travel with it; comments written *inside* a container value do not, because a new container is written from its values, so a comment between an array's elements is dropped. For a key the target already has, the source's leading comments are appended to the target's and its inline comment fills in only where the target has none -- so merging one source twice doubles its leading comments.

Merging a nested table also writes the tables above it: a source whose only content is `[deep.nest]` leaves an empty `[deep]` header in the target above it, since `Merge` writes through `SetCreate`, which spells every intermediate table the path names as a header of its own.

To seed a document from a list of defaults expressed in Go rather than from another document, use [`EnsureDefaults`](#structural-operations).

## Decoding into Go types

### Strictness

Decoding is strict, and strictness is the only mode. An unknown key, an unknown table, a value of a refused kind, a value the target cannot hold exactly, and a missing required key are all errors. There is no lenient mode and no option to skip a check.

- Struct fields are matched by their `toml` tag, or, with no tag, by their exact field name. Matching is **exact**: a document key differing only in case matches nothing and is therefore an unknown key.
- A `toml:"-"` tag, and an unexported field, exclude a name from the document's universe entirely -- a document key naming one is as unknown as any other.
- The only tag option read is `required` (`toml:"port,required"`), which makes the key's absence an error. Any other option is refused at construction, because an option nothing reads is a silent no-op.
- A map-typed or `any`-typed target matches every key by construction, so it reports no unknown keys. That is totality, not leniency.
- Embedded structs promote their fields and pointer fields are allocated as they are reached.
- No target decodes itself. There is no interface a type implements to take its own decoding over, and a string value is never handed to the target to parse: the engine is the only decode path, so every value went through the conversion table and every refusal is the table's own. A type needing a representation the table does not carry decodes the plain value and converts it itself.

Every independent violation is collected and reported together, in document order: validation continues across sibling keys and tables but never descends below a construct it has already refused.

A missing key is the one diagnostic the document cannot place, since it is about something the document does not contain. Missing required keys are reported after the entries of the record they belong to, and among themselves in lexicographic key order -- a map has no order of its own to report them in.

### The entry points

Every decode entry point **returns the value it builds** -- a `*T`, pointing at memory the decode allocated -- so a failed decode leaves no caller-owned target to observe:

```go
type Config struct {
    Title  string `toml:"title"`
    Server struct {
        Host string `toml:"host"`
        Port int    `toml:"port"`
    } `toml:"server"`
}

cfg, err := tomledit.Unmarshal[Config](data)      // parse + decode
```

Go has no parameterized methods, so the document- and node-level forms are package functions taking the document or node first:

```go
doc, err := tomledit.Parse(data)
cfg, err := tomledit.Decode[Config](doc)          // decode an existing document

node, err := doc.Resolve("server")
srv, err := tomledit.DecodeNode[Server](node)     // decode one construct
```

`Decode` is the one to reach for when you need both the AST for comment-preserving edits and the decoded values for application logic:

```go
cfg, err := tomledit.Decode[Config](doc)
if err != nil {
    log.Fatal(err)
}
fmt.Println(cfg.Server.Port)
doc.Set("server.port", cfg.Server.Port+1)
```

On any failure each of these returns a nil result. There is no partially written value and nothing to inspect after an error -- and no consumer code runs during the walk, so a failed decode has no side effects anywhere.

### Overlaying a document on defaults

`DecodeOver` is the defaults-overlay form: the caller supplies a **factory** that builds the seed, the document overlays it, and every key the document does not carry keeps what the seed put there.

```go
defaults := func() Config {
    return Config{Host: "localhost", Port: 8080}
}
cfg, written, err := tomledit.DecodeOver(doc, defaults)
```

The seed is a factory rather than a value so that what the decode fills is an allocation of `DecodeOver`'s own and never memory the caller still holds. The second result names the paths the decode wrote, in document order and in the library's path syntax, so a caller can tell a value the document supplied from one the seed left behind; a value written whole -- an array, an `any`-typed table, a target that decodes itself -- counts as one path. On failure both results are nil.

### Decoding into maps

Decode into `map[string]any` for schema-free access. Nested tables become nested maps, and arrays become slices. As with every type argument, the result is a pointer to the value the decode built:

```go
m, err := tomledit.Unmarshal[map[string]any](data)
if err != nil {
    log.Fatal(err)
}
fmt.Println((*m)["title"])
```

A map target matches every key by construction, so it reports no unknown keys.

### The conversion table

All value conversion -- the decode engine, the struct front end and every accessor family -- is driven by one table:

| TOML value | Go targets accepted | Rule |
|---|---|---|
| string | `string` | verbatim |
| integer | `int`, `int8`-`int64`, `uint`-`uint64` | range-checked; overflow and negative-into-unsigned are errors |
| integer | `float64`, `float32` | only if exactly representable; inexact is an error |
| float | `float64` | verbatim |
| float | `float32` | range-checked; precision truncation to the declared width is permitted |
| float | integer targets | never -- an error even for whole floats |
| boolean | `bool` | verbatim |
| offset date-time | `time.Time` | verbatim |
| local date-time | `LocalDateTime`; `time.Time` | the declared target expresses intent |
| local date | `LocalDate`; `time.Time` | same as local date-time |
| local time | `LocalTime` | verbatim |
| array | slice; `[]any` | elementwise by this table |
| array | fixed-size Go array | exact length required -- both under- and over-length are errors |
| any table form | struct, `map[string]T`, `map[string]any`, `any` | map values decode elementwise by this table |
| any value | `any` | the native mapping: string, int64, float64, bool, time.Time, the Local types, `[]any`, `map[string]any` |

Conversion *between* TOML types happens only when provably value-preserving; narrowing into the declared Go target's width is the caller's explicit choice, range-checked, never silent-wrapping.

The table is the whole answer: nothing runs before it, and no target decodes itself. A `time.Time` field therefore refuses an RFC 3339 string exactly as `AsTime` and `GetTime` do.

## Validating against a descriptor

For a schema known only at runtime, describe the expected shape as data instead of as a struct. The same engine runs.

```go
spec := &tomledit.Spec{Fields: map[string]tomledit.Field{
    "server": {Kind: tomledit.FieldKindTable, Required: true, Table: &tomledit.Spec{
        Fields: map[string]tomledit.Field{
            "host":  {Kind: tomledit.FieldKindString, Required: true},
            "port":  {Kind: tomledit.FieldKindInteger},
            "tags":  {Kind: tomledit.FieldKindArray, Elem: &tomledit.Field{Kind: tomledit.FieldKindString}},
            "extra": tomledit.FieldAny(),
        },
    }},
}}

err := doc.Validate(spec)
```

`Field.Kind` is one of `FieldKindString`, `FieldKindInteger`, `FieldKindFloat`, `FieldKindBoolean`, the four date-time kinds (`FieldKindOffsetDateTime`, `FieldKindLocalDateTime`, `FieldKindLocalDate`, `FieldKindLocalTime`), `FieldKindArray`, `FieldKindTable` and `FieldKindAny`. `Elem` is required for an array kind and `Table` for a table kind; either on a kind that has no use for it is a construction error, reported before the document is looked at. `Spec.Dynamic` describes every key `Fields` does not name -- a nil `Dynamic` means no other key is permitted.

`DecodeSpec` validates and then returns the document's values as native Go data:

```go
values, err := doc.DecodeSpec(spec)
```

It is **atomic**, without exception: a map only when the document has no violations at all, and `(nil, err)` otherwise. No reflection is involved and no consumer code runs, so the result is exactly what the document says.

## Diagnostics

Every failure that depends on the document -- its syntax, its keys, its values, the path a caller asked for -- is an `*tomledit.Error`:

```go
type Error struct {
    Kind     ErrorKind
    Path     string   // document path, in the library's path syntax
    Pos      Position // Line, Column (1-based), Offset (0-based)
    Span     Span
    Message  string
    File     string   // empty when no file is known
    Snippet  string   // source excerpt, parse-stage diagnostics
    Expected string   // type-mismatch detail
    Got      string   // type-mismatch detail
    Value    any      // offending value (inexact, bad input)
    Keys     []string // unknown-table inventory
    Offset   int      // KindRoundTrip: first divergence in the rendered bytes
}
```

It renders in the compiler convention -- the non-empty parts of `[location, path, message]` joined by `": "` -- so a diagnostic from a `ParseFile` document reads `config.toml:12:7: server.port: expected integer, got string`, and one from an in-memory parse drops the filename.

Match a kind with `errors.Is` against a sentinel, and read the structure with `errors.As`:

```go
if errors.Is(err, tomledit.ErrTypeMismatch) { /* ... */ }

var diag *tomledit.Error
if errors.As(err, &diag) {
    fmt.Println(diag.Kind, diag.Path, diag.Pos.Line, diag.Expected, diag.Got)
}
```

The kinds and their sentinels:

| Kind | Sentinel | Reported for |
|---|---|---|
| `KindSyntax` | `ErrSyntax` | a lexing or parsing failure |
| `KindUnknownKey` | `ErrUnknownKey` | a key matching no field of the target |
| `KindUnknownTable` | `ErrUnknownTable` | an unknown table or array-of-tables; `Keys` carries its direct children |
| `KindMissingKey` | `ErrMissingKey` | a required key the document does not carry |
| `KindTypeMismatch` | `ErrTypeMismatch` | a value whose kind the target refuses |
| `KindInexact` | `ErrInexact` | a value the target cannot hold exactly, including a fixed-array length mismatch |
| `KindNotFound` | `ErrNotFound` | a path naming nothing |
| `KindBadPath` | `ErrBadPath` | a syntactically invalid path |
| `KindWrongContainer` | `ErrWrongContainer` | a path step, or the operation itself, is structurally inapplicable |
| `KindBadInput` | `ErrBadInput` | an invalid input to an editing operation |
| `KindConflict` | `ErrConflict` | an edit that would produce an invalid document |
| `KindRoundTrip` | `ErrRoundTrip` | `WriteFile`'s rendered bytes did not survive a re-parse |

A decode returns its violations as an `*Errors` aggregate. It renders as its first diagnostic, so a single-error call site reads like a single error; the whole list is reachable through `errors.As` and `Unwrap() []error`, in document order. `errors.Is` against a kind sentinel matches when any contained diagnostic carries that kind.

```go
var all *tomledit.Errors
if errors.As(err, &all) {
    for _, e := range all.Unwrap() {
        fmt.Println(e)
    }
}
```

Parsing stays first-error-only: a parse cannot meaningfully continue past a syntax error, so parse errors are never wrapped in the aggregate.

A failure that depends only on your own Go code -- a target of the wrong shape, an unknown struct-tag option, a descriptor built with a missing sub-descriptor -- is a plain error instead, because there is no document position to point at.

## Renderers and path helpers

The literal renderers are exported for consumers writing TOML by hand. They are **total**: every Go string and every `float64` has an output.

```go
tomledit.QuoteString(`say "hi"`)  // "say \"hi\""
tomledit.QuoteKey("bare_key")     // bare_key
tomledit.QuoteKey("not bare")     // "not bare"
tomledit.FormatFloat(1)           // 1.0
tomledit.FormatFloat(math.NaN())  // nan
```

`FormatFloat` writes the shortest form that reads back as the same `float64`, with a float marker always present, and the non-finite values as `nan`, `inf` and `-inf` -- never `+nan`, `-nan` or `+inf`. A string that is not valid UTF-8 renders each invalid byte as `U+FFFD`; the write paths refuse such text before it can reach a renderer.

The path helpers parse and render the library's own path syntax, with `JoinPath` as the single quoting authority:

```go
segs, err := tomledit.ParsePath(`servers[0]."host.name"`)
// []PathSegment{{Kind: SegmentKey, Key: "servers"},
//               {Kind: SegmentIndex, Index: 0},
//               {Kind: SegmentKey, Key: "host.name"}}
path := tomledit.JoinPath(segs) // servers[0]."host.name"
```

## Node types

Every value in the AST is a `tomledit.Node`, which carries `Type()`, `Span()`, `Raw()`, `Comment()` and `LeadingComments()`. The value-carrying kinds additionally implement `tomledit.Scalar`, which adds `Value()` and the typed `As*` accessors:

| Node type | TOML value | `Value()` returns |
|---|---|---|
| `*StringNode` | string | `string` |
| `*IntegerNode` | integer | `int64` |
| `*FloatNode` | float | `float64` |
| `*BooleanNode` | boolean | `bool` |
| `*DateTimeNode` | offset date-time | `time.Time` |
| `*LocalDateTimeNode` | local date-time | `tomledit.LocalDateTime` |
| `*LocalDateNode` | local date | `tomledit.LocalDate` |
| `*LocalTimeNode` | local time | `tomledit.LocalTime` |

The structure-carrying kinds have no `Value()`; they expose their contents through named accessors:

| Node type | TOML construct | Accessors |
|---|---|---|
| `*Document` | the document root | `Children()` |
| `*TableNode` | `[table]` header | `Children()`, `KeyPath()` |
| `*ArrayTableNode` | `[[array-table]]` header | `Children()`, `KeyPath()` |
| `*ArrayNode` | array | `Elements()` |
| `*InlineTableNode` | inline table | `Children()` |
| `*KeyValueNode` | a key-value pair | `Key()`, `Val()` |
| `*KeyNode` | a key | `Parts()`, `RawParts()`, `Styles()` |
| `*CommentNode` | a standalone comment | `Text()` |

Node fields are unexported; use the accessors. Every slice a read hands back is a copy, so writing into what you read changes neither the node nor what the document renders. Use type assertions to reach kind-specific detail:

```go
node, err := doc.Resolve("server.port")
if intNode, ok := node.(*tomledit.IntegerNode); ok {
    v, _ := intNode.AsInt()
    fmt.Println(v)            // 8080
    fmt.Println(intNode.Base()) // tomledit.IntegerDecimal
}
```

## Source positions

Every node from a parsed document carries a `Span`: a half-open range whose ends each hold a 1-based line, a 1-based column and a 0-based byte offset.

```go
node, err := doc.Resolve("server.host")
if err == nil {
    span := node.Span()
    if span.IsValid() {
        fmt.Printf("line %d col %d (offset %d) to line %d col %d\n",
            span.Start.Line, span.Start.Column, span.Start.Offset,
            span.End.Line, span.End.Column)
    }
}
```

Spans reflect the most recent `Parse`. Edits do not recompute them, and nodes created programmatically (via `Set`, `Merge`, ...) carry the zero span, where `IsValid()` returns false. Re-parse the output of `Bytes()` when fresh positions are needed after editing.

The read-layer carries positions too: `Entry.KeySpan()` is where a key is written, `Record.Span()` is a record's anchoring span, and `Entry.RecordsSpan()` is the synthesized span of an array-of-tables collection, which no single node stands for.

## Complete example

Parse a configuration file, update values, remove a key, add a new section with auto-created intermediate tables, attach a comment, rename a key, and write the modified document back -- preserving all original comments and formatting in untouched regions.

```go
package main

import (
    "log"

    tomledit "github.com/smm-h/go-toml-edit"
)

func main() {
    doc, err := tomledit.ParseFile("config.toml")
    if err != nil {
        log.Fatal(err)
    }

    // Update existing values
    if err := doc.Set("server.port", 9090); err != nil {
        log.Fatal(err)
    }
    if err := doc.Set("server.host", "0.0.0.0"); err != nil {
        log.Fatal(err)
    }

    // Remove a key (idempotent -- no error if it is not there)
    if err := doc.Delete("server.debug"); err != nil {
        log.Fatal(err)
    }

    // Add a new section with auto-created intermediate tables
    if _, err := doc.EnsureDefaults([]tomledit.Default{
        {Path: "database.host", Value: "db.example.com"},
        {Path: "database.port", Value: 5432},
        {Path: "database.name", Value: "myapp"},
    }); err != nil {
        log.Fatal(err)
    }

    // Add a comment, and rename a key
    if err := doc.SetComment("database.host", "primary database server"); err != nil {
        log.Fatal(err)
    }
    if err := doc.RenameKey("server.host", "bind_address"); err != nil {
        log.Fatal(err)
    }

    // Write back atomically, after checking the bytes survive a round trip
    if err := doc.WriteFile("config.toml"); err != nil {
        log.Fatal(err)
    }
}
```
