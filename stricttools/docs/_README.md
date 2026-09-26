+++
title = "README.md"
+++
# go-toml-edit

Zero-dep TOML editing library for Go with comment preservation

## Why

Every Go TOML library either discards comments during parsing or provides
read-only access to them. If you parse a config file, change one value, and
write it back, the comments are gone. Python has
[tomlkit](https://github.com/sdispater/tomlkit); Go had nothing.
go-toml-edit fills this gap with a lossless AST that preserves every comment,
blank line, and formatting detail through arbitrary edits.

What it preserves is content — comments, values, structure. Whitespace is
formatting, not content: this library also reformats TOML, and `Format()` is
strict about how output looks.

## Before / After

```go
package main

import (
	"fmt"
	"log"

	tomledit "github.com/smm-h/go-toml-edit"
)

func main() {
	doc, err := tomledit.Parse([]byte(`# Server config
[server]
host = "localhost"  # primary host
port = 8080
`))
	if err != nil {
		log.Fatal(err)
	}

	if err := doc.Set("server.port", 9090); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
}
```

Output -- comments survive the edit:

```toml
# Server config
[server]
host = "localhost"  # primary host
port = 9090
```

## Installation

```
go get github.com/smm-h/go-toml-edit
```

**No runtime dependencies.** The package imports the standard library and
nothing else; the module's BurntSushi/toml and toml-test requirements are
test-only (the benchmark comparison and the compliance corpus). A test asserts
this over the non-test sources.

## Two surfaces, two questions

A parsed document is readable through two surfaces, and which one to use follows
from the question being asked.

- The **syntactic** surface is the AST: `Walk`, `Resolve` and the concrete node
  types. It answers what the file *contains*, in the form it was written --
  spellings, quoting styles, integer bases, comments, blank lines, and the
  source span of every construct.
- The **logical** surface is the read-layer: `doc.Root()` returns a `Record`
  whose `Entry` values are the document's keys in first-appearance order. It
  answers what the document *means*, with the spellings folded away. A dotted
  key, a `[header]` table and an inline table are indistinguishable through it.

Reads are spelling-blind; writes are structurally conservative. A value write
touches value fragments and nothing else, and a structural construct changes
only through a structural operation or an explicit `Delete`.

## Feature Comparison

| Feature | go-toml-edit | BurntSushi/toml | pelletier/go-toml/v2 |
|---------|-------------|-----------------|---------------------|
| Comment preservation | Yes | No | Read-only (unstable) |
| Round-trip editing | Yes | No | No |
| Set/Delete/RenameKey API | Yes | No | No |
| Unmarshal to struct | Yes (strict-only) | Yes | Yes |
| Marshal from Go values | No | Yes | Yes |
| Position spans on AST nodes | Yes | No | Unstable parser only |
| TOML 1.0 compliance | Full | Full | Full |
| Formatter | Yes | No | No |
| Document diffing | Yes | No | No |
| Deep merge | Yes | No | No |

## Quick Start

### Parse and Read

```go
doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
port = 8080
`))
host, err := doc.GetString("server.host") // "localhost", nil
port, err := doc.GetInt("server.port")    // 8080, nil
```

Every getter returns `(T, error)`. A missing path, a bad path, a wrong type and
a value the target cannot hold exactly are distinct diagnostics -- see
[Diagnostics](#diagnostics).

### Read the logical structure

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

### Edit Values

```go
doc.Set("server.port", 9090)               // update existing key
doc.SetCreate("database.host", "db.local") // create key + intermediate table
doc.Delete("server.debug")                 // remove a key (idempotent)
doc.RenameKey("server.host", "address")    // rename the binding, every spelling
```

`Set` is a no-op if and only if the bytes it would write are the bytes already
there -- so a tool that writes back what it read touches nothing. When they
differ, the value is written in the library's canonical spelling, and a
container value (a map, a slice, a `[]Pair`) is replaced wholesale.

### Structural editing

```go
doc.NewTable("logging")
doc.NewArrayTable("products")
doc.AppendToArray("server.ports", 8082)
doc.RemoveFromArray("server.ports", 0)
doc.PermuteChildren("server", []int{2, 0, 1}) // order[i] = index moving to i
doc.EnsureDefaults([]tomledit.Default{
	{Path: "server.port", Value: 8080},
	{Path: "logging.level", Value: "info"},
})
```

### Comment Management

```go
doc.SetComment("server.port", "default: 8080")
doc.SetLeadingComments("server", []string{
	"Server configuration",
	"See docs for all options",
})

inline, err := doc.GetComment("server.port")           // "default: 8080"
leading, err := doc.GetLeadingComments("server")       // the two lines above
```

The getters resolve a path to the same node the setters write to, so what one
writes the other reads back, and they refuse the same paths with the same error
kinds. Both answer normalized text -- the content without the `#` and the
whitespace around it -- which is exactly what the setters take. A node with no
inline comment answers the empty string, one with no leading comments answers
`nil`, and neither is an error.

### Fluent Cursor

```go
host, err := doc.Key("database").Key("host").String()
port, err := doc.Key("database").Key("port").Int()
```

### Strict decoding

```go
type Config struct {
	Title  string `toml:"title"`
	Server struct {
		Host string `toml:"host"`
		Port int    `toml:"port"`
	} `toml:"server"`
}
cfg, err := tomledit.Unmarshal[Config](data)
```

Decoding is strict and strictness is the only mode: an unknown key, an unknown
table, a value of a refused kind, a value the target cannot hold exactly, and a
missing `toml:"...,required"` key are all errors. Key matching is exact -- a
document key differing only in case matches nothing. Every independent violation
is collected and reported together, in document order -- with one placement rule
source order cannot give: a record's missing required keys are reported after
that record's own entries, and among themselves in lexicographic key order.

The entry points return the value they build -- a `*T`, pointing at memory they
allocated -- so a failed decode leaves nothing to inspect:
`Unmarshal[T](data)`, `Decode[T](doc)`, `DecodeNode[T](node)`, and
`DecodeOver[T](doc, seed)` -- the last overlaying a document on a seed the
factory builds, and reporting which paths the document supplied.

### Descriptor validation

For a schema known only at runtime, describe it as data instead of as a struct:

```go
spec := &tomledit.Spec{Fields: map[string]tomledit.Field{
	"host": {Kind: tomledit.FieldKindString, Required: true},
	"port": {Kind: tomledit.FieldKindInteger},
}}
err := doc.Validate(spec)                 // diagnostics only
values, err := doc.DecodeSpec(spec)       // validated, then native Go data
```

`DecodeSpec` is atomic: it returns a map only when the document has no
violations at all.

### Format

```go
formatted := doc.Format(tomledit.WithIndentWidth(2))
```

`Format` re-renders everything in canonical spellings. It preserves the writer's
blank-line grouping at document and table-body level -- a run of blank lines
becomes one. One gap it opens itself, unconditionally: a table header written
flush against what precedes it always gets a blank line above it. What the
library preserves is content -- the comments -- while whitespace is formatting,
and `Format` is where the library is strict about output looking good, so the
separation is not a caller's option. The insertion never removes a blank line
and never doubles one already there.

### Walk

`Walk` is the syntactic traversal: it hands the visitor the concrete nodes, with
the spellings and trivia they were written with.

```go
doc.Walk(func(path string, node tomledit.Node) error {
	fmt.Printf("%s = %v\n", path, node.(tomledit.Scalar).Value())
	return nil
}, tomledit.WalkLeaves)
```

`Value()` is on `Scalar`, the sub-interface the value-carrying node kinds
implement; `Node` itself carries `Type()`, `Span()`, `Raw()`, `Comment()` and
`LeadingComments()`.

### Position Spans

Every AST node carries its source position from the most recent parse, enabling
custom semantic validation with precise line/column diagnostics:

```go
doc.Walk(func(path string, node tomledit.Node) error {
	sp := node.Span() // 1-based line/column, plus a byte offset
	fmt.Printf("%s at %d:%d-%d:%d\n", path,
		sp.Start.Line, sp.Start.Column, sp.End.Line, sp.End.Column)
	return nil
}, tomledit.WalkLeaves)
```

`Span()` is available on all nodes: tables and array-tables (covering only
the `[header]`), key-value pairs (key through value), keys, all scalars,
arrays, inline tables, and comments. `Span.End` is exclusive -- the position
immediately after the node's last byte.

**Span policy:** spans reflect the last `Parse`. Edit operations do not
recompute them: nodes created programmatically (via `Set`, `Merge`, ...) return
the zero span (`Span.IsValid()` reports false), and edited nodes keep their
parse-time spans. Re-parse the output of `Bytes()` when fresh positions are
needed after editing.

### Files

```go
doc, err := tomledit.ParseFile("config.toml") // remembers the filename
err = doc.WriteFile("config.toml")            // atomic, round-trip checked
```

A document loaded with `ParseFile` names the file in every diagnostic it later
produces. `WriteFile` proves the rendered bytes re-parse to themselves before
replacing the destination in one step.

### Diagnostics

```go
_, err := doc.GetInt("server.port")

if errors.Is(err, tomledit.ErrTypeMismatch) { /* ... */ }

var diag *tomledit.Error
if errors.As(err, &diag) {
	fmt.Println(diag.Kind, diag.Path, diag.Pos.Line, diag.Expected, diag.Got)
}
```

Every failure that depends on the document -- parse, decode, edit, access --
is an `*Error` with a kind, a path and a position, rendered in the compiler
convention (`config.toml:12:7: server.port: expected integer, got string`). A
decode reports its violations together as an `*Errors` aggregate, reachable with
`errors.As`.

### Diff

```go
changes := tomledit.Diff(a, b)
for _, c := range changes {
	fmt.Printf("%s: %s\n", c.Kind, c.Path)
}
// removed: age
// added: email
// modified: name
```

`Diff` reads values, never spellings: two documents writing one value
differently report no difference, so a document always compares equal to itself.
Types are not bridged -- `1` and `1.0` are a modification.

### Merge

```go
base, _ := tomledit.Parse([]byte(`[server]
host = "localhost"
`))
defaults, _ := tomledit.Parse([]byte(`[server]
host = "0.0.0.0"
port = 8080
`))
base.Merge(defaults)
// host keeps "localhost" (already set); port added from defaults.
```

## Performance

go-toml-edit retains the full AST -- source positions, trivia, comment nodes,
and the lexeme every value was written as -- because that is what
comment-preserving round-trip editing needs. A decode-only library discards all
of it, so it parses faster and allocates less. Reach for a decode-only library
when you only need Go values out of a TOML file; reach for this one when the
comments have to survive the write-back.

Run `go test -bench .` to measure on your own machine. `benchmarks.txt` holds a
captured run beside BurntSushi/toml.

## API Reference

[pkg.go.dev/github.com/smm-h/go-toml-edit](https://pkg.go.dev/github.com/smm-h/go-toml-edit)

## Deferred work

A struct-to-TOML `Marshal`, the remaining structural-manipulation suite (node
moves, positional insertion, table/inline conversion), external node
construction with spelling control, and a streaming parser are deferred rather
than planned. TOML 1.1 is not supported: this library targets TOML 1.0 only.
See `todo/` for details.

## License

[MIT](LICENSE)
