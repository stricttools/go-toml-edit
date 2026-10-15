package tomledit

import (
	"sync"
	"time"
)

// The concrete node kinds. Every field is unexported: a node's contents are
// read through the accessors below and written only through the mutator
// funnels of mutate.go, so nothing can put a node into a state its rendered
// bytes no longer describe. An accessor that answers with a slice answers with
// a copy, so a caller sorting or truncating what it was handed changes nothing
// in the document.

// StringStyle indicates the quoting style for a string node.
type StringStyle int

const (
	StringBasic            StringStyle = iota // StringBasic is a double-quoted string ("...").
	StringLiteral                             // StringLiteral is a single-quoted string ('...').
	StringMultiLineBasic                      // StringMultiLineBasic is a triple-double-quoted string ("""...""").
	StringMultiLineLiteral                    // StringMultiLineLiteral is a triple-single-quoted string ('''...''').
)

// IntegerBase indicates the numeric base for an integer node.
type IntegerBase int

const (
	IntegerDecimal IntegerBase = iota // IntegerDecimal is base-10 (e.g. 42).
	IntegerHex                        // IntegerHex is base-16 (e.g. 0xFF).
	IntegerOctal                      // IntegerOctal is base-8 (e.g. 0o77).
	IntegerBinary                     // IntegerBinary is base-2 (e.g. 0b1010).
)

// copyNodes returns a copy of a container's ordered contents.
func copyNodes(nodes []Node) []Node {
	out := make([]Node, len(nodes))
	copy(out, nodes)
	return out
}

// copyStrings returns a copy of a string slice.
func copyStrings(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// copyBytes returns a copy of a byte slice, nil for a nil one: a caller handed
// bytes by an accessor writes into its own array, not the node's.
func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// copyByteSlices returns a copy of a slice of byte slices, each element copied
// too. Copying only the outer slice would leave every element shared with the
// node, which is the same hole one level down.
func copyByteSlices(s [][]byte) [][]byte {
	out := make([][]byte, len(s))
	for i, b := range s {
		out[i] = copyBytes(b)
	}
	return out
}

// Document is the root node of a TOML document.
type Document struct {
	nodeBase
	children []Node

	// file is the name the document was parsed under (ParseWithFileName), or
	// "" when it was parsed with Parse. Every diagnostic the document produces names it.
	file string

	// layer is the read-layer folded from the document as it stood at
	// generation layerGen, and generation counts the writes made to the
	// document since it was parsed. Every write bumps the counter, so a cached
	// layer whose generation is behind is known to describe a document that no
	// longer exists. All three are guarded by mu, which is what keeps
	// concurrent reads of a shared document safe: the first reader folds, the
	// rest wait for it and take the same answer.
	mu         sync.Mutex
	layer      *Record
	layerGen   uint64
	generation uint64
}

// bumpGeneration records that the document changed, so the read-layer folded
// from the old one is dropped.
func (n *Document) bumpGeneration() {
	n.mu.Lock()
	n.generation++
	n.layer = nil
	n.mu.Unlock()
}

// readLayer returns the document's read-layer, folding it when the cached one
// is missing or was folded from a document that has since been written to.
//
// Parsing builds nothing: the fold happens the first time something asks a
// logical question, and every later question at the same generation is answered
// from the same record. A write drops the cache whole -- an editing sequence
// that alternates writes and reads folds once per write, which is the cost this
// wave accepts for keeping the invalidation trivially correct.
func (d *Document) readLayer() (*Record, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.layer != nil && d.layerGen == d.generation {
		return d.layer, nil
	}
	root, err := foldDocument(d)
	if err != nil {
		// A document that does not fold caches nothing: the next read asks
		// again, and answers whatever the document says then.
		return nil, err
	}
	probeLayerBuild()
	d.layer = root
	d.layerGen = d.generation
	return root, nil
}

// layerBuilds counts how many times a read-layer was actually folded, for the
// tests that pin what parsing builds and what a read reuses. Counting is off
// unless a test turns it on.
var (
	layerBuilds         int
	countingLayerBuilds bool
)

func probeLayerBuild() {
	if countingLayerBuilds {
		layerBuilds++
	}
}

// Type returns NodeDocument.
func (n *Document) Type() NodeType { return NodeDocument }

// TableNode represents a [table] header and its children.
type TableNode struct {
	nodeBase
	keyPath  []string
	children []Node

	// keySpans is the source range of each part of keyPath, in order. It is
	// empty for a table created programmatically.
	keySpans []Span

	// header carries the byte fragments the header line's key decomposes into:
	// the bytes each part was written as, and the brackets, dots and whitespace
	// standing around them. A header that re-renders splices them, so a comment
	// written above [ 'a' . b ] leaves the header spelled exactly that way. It
	// is empty for a table created programmatically, which renders its key path
	// canonically instead.
	header keyFragments
}

// Type returns NodeTable.
func (n *TableNode) Type() NodeType { return NodeTable }

// ArrayTableNode represents an [[array-table]] header and its children.
type ArrayTableNode struct {
	nodeBase
	keyPath  []string
	children []Node

	// keySpans is the source range of each part of keyPath, in order. It is
	// empty for an array table created programmatically.
	keySpans []Span

	// header carries the key's byte fragments, exactly as on TableNode.
	header keyFragments
}

// Type returns NodeArrayTable.
func (n *ArrayTableNode) Type() NodeType { return NodeArrayTable }

// KeyValueNode represents a key = value pair.
type KeyValueNode struct {
	nodeBase
	key *KeyNode
	val Node

	// sep is the separator fragment: every byte between the end of the key and
	// the start of the value, the "=" and the whitespace around it included. A
	// pair that re-renders splices it, so "x   =    1" keeps its spacing when
	// its value is replaced. It is nil for a pair created programmatically,
	// which writes " = ".
	sep []byte
}

// Type returns NodeKeyValue.
func (n *KeyValueNode) Type() NodeType { return NodeKeyValue }

// KeyNode represents a (possibly dotted) key.
type KeyNode struct {
	nodeBase
	parts  []string      // semantic parts (e.g. ["server", "host"])
	styles []StringStyle // quoting style per part

	// frag holds the key's byte fragments: the bytes of each part, as written
	// or as renamed, and the dots and whitespace standing around them. A key
	// that re-renders splices them, so renaming one part of "a . b" leaves the
	// spacing and the other part exactly as they were.
	frag keyFragments

	// partSpans is the source range of each part, in order. It is empty for a
	// key created programmatically.
	partSpans []Span
}

// partSpan returns the source range of key part i, or the zero Span when the
// key carries no per-part spans (a key created programmatically) or i is out
// of range.
func (n *KeyNode) partSpan(i int) Span {
	if i < 0 || i >= len(n.partSpans) {
		return Span{}
	}
	return n.partSpans[i]
}

// Type returns NodeKey.
func (n *KeyNode) Type() NodeType { return NodeKey }

// StringNode represents a string value.
type StringNode struct {
	nodeBase
	val   scalarValue[string]
	style StringStyle
}

// Type returns NodeString.
func (n *StringNode) Type() NodeType { return NodeString }

// Value returns the decoded string.
func (n *StringNode) Value() any { return n.val.get() }

// IntegerNode represents an integer value.
type IntegerNode struct {
	nodeBase
	val  scalarValue[int64]
	base IntegerBase
}

// Type returns NodeInteger.
func (n *IntegerNode) Type() NodeType { return NodeInteger }

// Value returns the integer value as int64.
func (n *IntegerNode) Value() any { return n.val.get() }

// FloatNode represents a float value.
type FloatNode struct {
	nodeBase
	val scalarValue[float64]
}

// Type returns NodeFloat.
func (n *FloatNode) Type() NodeType { return NodeFloat }

// Value returns the float value as float64.
func (n *FloatNode) Value() any { return n.val.get() }

// BooleanNode represents a boolean value.
type BooleanNode struct {
	nodeBase
	val scalarValue[bool]
}

// Type returns NodeBoolean.
func (n *BooleanNode) Type() NodeType { return NodeBoolean }

// Value returns the boolean value.
func (n *BooleanNode) Value() any { return n.val.get() }

// DateTimeNode represents an offset date-time value.
type DateTimeNode struct {
	nodeBase
	val scalarValue[time.Time]
}

// Type returns NodeDateTime.
func (n *DateTimeNode) Type() NodeType { return NodeDateTime }

// Value returns the time.Time value.
func (n *DateTimeNode) Value() any { return n.val.get() }

// LocalDateTimeNode represents a local date-time value (no timezone).
type LocalDateTimeNode struct {
	nodeBase
	val scalarValue[LocalDateTime]
}

// Type returns NodeLocalDateTime.
func (n *LocalDateTimeNode) Type() NodeType { return NodeLocalDateTime }

// Value returns the LocalDateTime value.
func (n *LocalDateTimeNode) Value() any { return n.val.get() }

// LocalDateNode represents a local date value.
type LocalDateNode struct {
	nodeBase
	val scalarValue[LocalDate]
}

// Type returns NodeLocalDate.
func (n *LocalDateNode) Type() NodeType { return NodeLocalDate }

// Value returns the LocalDate value.
func (n *LocalDateNode) Value() any { return n.val.get() }

// LocalTimeNode represents a local time value.
type LocalTimeNode struct {
	nodeBase
	val scalarValue[LocalTime]
}

// Type returns NodeLocalTime.
func (n *LocalTimeNode) Type() NodeType { return NodeLocalTime }

// Value returns the LocalTime value.
func (n *LocalTimeNode) Value() any { return n.val.get() }

// ArrayNode represents an array value.
type ArrayNode struct {
	nodeBase
	elements         []Node
	trailingComments [][]byte // comments after the last element, before ']'

	// gaps holds the bytes standing before, between and after the elements --
	// the brackets, the commas, the whitespace, the newlines and the interior
	// comments -- with one more entry than there are elements. An array that
	// re-renders because one element changed splices them, so every other
	// element's bytes and every separator survive verbatim. They are dropped
	// when the element count changes or an element's own trivia is written,
	// neither of which they can describe.
	gaps [][]byte
}

// Type returns NodeArray.
func (n *ArrayNode) Type() NodeType { return NodeArray }

// InlineTableNode represents an inline table value.
type InlineTableNode struct {
	nodeBase
	children []Node // KeyValueNode entries

	// gaps holds the bytes standing before, between and after the pairs -- the
	// braces, the commas and the whitespace -- exactly as ArrayNode's do.
	gaps [][]byte
}

// Type returns NodeInlineTable.
func (n *InlineTableNode) Type() NodeType { return NodeInlineTable }

// CommentNode represents a standalone comment line.
type CommentNode struct {
	nodeBase
	text string
}

// Type returns NodeComment.
func (n *CommentNode) Type() NodeType { return NodeComment }
