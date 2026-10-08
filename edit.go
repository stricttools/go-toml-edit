package tomledit

import (
	"bytes"
	"math"
	"reflect"
	"sort"
	"time"
	"unicode/utf8"
)

// Set writes a value at the given path. A key the parent does not carry yet is
// created there; a path whose parent does not exist is an error, which is what
// SetCreate relaxes.
//
// Supported value types: string, bool, int/int8-64, uint/uint8-64, float32/64,
// time.Time, LocalDateTime, LocalDate, LocalTime, []any and typed slices,
// map[string]any (written with its keys sorted), and []Pair (written in the
// order given). A value is a Go value: an AST node is not one, and passing a
// node -- including one resolved out of this or another document -- is refused
// as an unsupported type. Copying a value from one key to another is a read
// followed by a write of the value.
//
// A value write touches value fragments and nothing else. Where the path names
// a key bound by a structural construct -- a [header] table, an array-of-tables,
// or a table another construct only implied, by a longer header or a dotted key
// -- the write is refused with KindWrongContainer: those change through a
// structural operation, or through an explicit Delete followed by the write. A
// key holding an inline table is a value, and setting it replaces that value
// wholesale, interior comments and spellings included.
//
// The PARENT table's spelling decides only where the write goes, never whether
// it happens. A table no single node stands for is written where the document
// already spells it out: a key it already carries is replaced through the pair
// that binds it; a new key of a table a dotted key spelled out joins the same
// region as another dotted pair ("a.b = 1" gains "a.new = 5" beside it); and a
// new key of a table only a longer header implies arrives under the anchoring
// header the write gives that table -- the one TOML allows it.
//
// # Equality
//
// Set is a no-op if and only if the bytes it would write for the value are
// exactly the bytes the value fragment already carries. Nothing else counts as
// equal, and nothing equal is written: the node stays, with the spelling, the
// lexeme and the span it was parsed with, and the document records no edit. A
// value the library wrote before carries no lexeme, and the comparison is
// against its canonical rendering. A container value -- a map, a []Pair or a
// slice -- is compared as a whole against the stored container's whole byte
// range, and replaced WHOLESALE when it differs: the interior comments and
// spellings of the old container do not survive, which is what setting a
// container value means.
//
// One rule, no special cases: NaN spellings, infinities, signed zeros,
// integer-versus-float, date-time offsets, string quoting and integer bases
// alike. Two consequences worth stating plainly. An idempotent tool that writes
// values it read back normalises a non-canonical spelling the first time it
// touches it -- 0x2A set to 42 becomes 42 -- and is byte-stable from then on.
// And a same-content write over a literal-quoted string converts it to basic
// quoting. What you Set is what the file says.
//
// Deciding not to write is not undoing a write: a no-op Set never clears
// dirtiness an earlier edit recorded.
//
// A NaN whose sign bit is set is refused with KindBadInput, since TOML has one
// NaN spelling and writing it would drop the sign; the ordinary NaN is accepted
// and writes "nan".
//
// Text that is not valid UTF-8 is refused with KindBadInput too, wherever it
// appears: a string value, a string inside a container value, a map or []Pair
// key, and a key the path itself names. A TOML document is UTF-8, so such bytes
// would be written as replacement characters and read back as something else.
func (d *Document) Set(path string, value any) error {
	return d.diag(d.setInternal(path, value, false), path)
}

// SetCreate is like Set but creates the intermediate tables the path names and
// the document does not carry, as standard [header] tables appended to the
// document. It refuses exactly what Set refuses, and is a no-op on exactly the
// same terms -- see Set's equality rule.
//
// A refused write creates nothing. The value is converted before a single table
// is made, so a value the library will not write -- and a key it will not spell
// -- leaves the document byte-for-byte as it was, rather than leaving the
// headers of the path behind.
func (d *Document) SetCreate(path string, value any) error {
	return d.diag(d.setInternal(path, value, true), path)
}

func (d *Document) setInternal(path string, value any, create bool) error {
	segments, err := ParsePath(path)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return newError(KindBadPath, "empty path")
	}
	if err := checkWritableKeys(segments); err != nil {
		return err
	}

	// The last segment is the target key/index to set.
	parentSegs := segments[:len(segments)-1]
	lastSeg := segments[len(segments)-1]

	// The value is converted FIRST, before anything is resolved and before
	// SetCreate makes a single intermediate table, so a value the library
	// refuses -- a signed NaN, an unsupported type, an unsigned value past
	// int64, a duplicate ordered key, text that is not valid UTF-8 -- leaves the
	// document exactly as it was. The tables exist to carry the value; a write
	// that does not happen creates nothing.
	valNode, err := valueToNode(value)
	if err != nil {
		return err
	}

	// Resolve the parent node.
	parent, err := d.resolveParentForEdit(parentSegs, create)
	if err != nil {
		return err
	}

	// What the key already binds decides whether a value may be written at all.
	// A key a freshly created table would carry binds nothing, so this refusal
	// only ever answers about a parent that was already there -- which is why
	// asking it after the parent is resolved still creates nothing.
	if lastSeg.Kind == SegmentKey {
		if err := checkValueWriteTarget(parent, lastSeg.Key); err != nil {
			return err
		}
	}

	// The equality rule: a write of the bytes the value fragment already
	// carries is not a write. It is decided here, before any mutator runs, so a
	// no-op replaces no node, drops no lexeme, invalidates no span and records
	// no edit -- and, just as much, clears none that an earlier edit recorded.
	if existing, ok := storedValueNode(parent, lastSeg); ok {
		if bytes.Equal(serializeNode(existing), serializeNode(valNode)) {
			return nil
		}
	}

	switch lastSeg.Kind {
	case SegmentKey:
		return d.setKeyInParent(parent, parentSegs, lastSeg.Key, valNode)
	case SegmentIndex:
		return setIndexInParent(parent, lastSeg.Index, valNode)
	default:
		return newError(KindBadPath, "unknown segment type")
	}
}

// resolveParentForEdit resolves the parent container for an edit operation.
// When create is true, missing intermediate tables are auto-created.
func (d *Document) resolveParentForEdit(segments []PathSegment, create bool) (layerPos, error) {
	pos, err := d.walkPath(segments)
	if err == nil {
		return pos, nil
	}
	if !create {
		return layerPos{}, wrapError(err, "parent path not found")
	}
	return d.resolveOrCreateParent(segments)
}

// resolveOrCreateParent walks the path segments, creating a standard table for
// every key step that names nothing yet, and returns the final parent position.
func (d *Document) resolveOrCreateParent(segments []PathSegment) (layerPos, error) {
	for i := 1; i <= len(segments); i++ {
		root, err := d.readLayer()
		if err != nil {
			return layerPos{}, err
		}
		if _, err := walkFrom(posFromRecord(root), segments[:i]); err == nil {
			continue
		} else if segments[i-1].Kind != SegmentKey {
			// Only a key can be created; an array element must already exist.
			return layerPos{}, wrapError(err, "cannot auto-create array index")
		}
		parent, err := walkFrom(posFromRecord(root), segments[:i-1])
		if err != nil {
			return layerPos{}, err
		}
		if err := d.createIntermediateTable(parent, segments[:i]); err != nil {
			return layerPos{}, err
		}
	}

	pos, err := d.walkPath(segments)
	if err != nil {
		return layerPos{}, wrapError(err, "failed to resolve after creation")
	}
	return pos, nil
}

// createIntermediateTable appends a new [table] for the last of segs, the whole
// path of the table to create, under the parent position that path reaches.
//
// A parent no single node stands for still names a table the new header can be
// nested under -- TOML refuses only a header that REDEFINES such a table -- so
// the header names the path in full. That path has to be spellable as one: a
// parent reached through an array-of-tables index is refused, since a header
// written for it would address the array's last entry.
func (d *Document) createIntermediateTable(parent layerPos, segs []PathSegment) error {
	key := segs[len(segs)-1].Key
	var newPath []string

	switch scope := parent.node.(type) {
	case *Document:
		newPath = []string{key}
	case *TableNode:
		newPath = append(append([]string(nil), scope.keyPath...), key)
	case *ArrayTableNode:
		newPath = append(append([]string(nil), scope.keyPath...), key)
	default:
		if parent.node != nil || parent.rec == nil {
			// A concrete node no table can be nested in -- an inline table,
			// which TOML gives no way to add to, or a value.
			return newError(KindWrongContainer, "cannot create intermediate table under %s", parent.describe())
		}
		path, ok := keyPathOfSegments(segs)
		if !ok {
			return newError(KindWrongContainer,
				"cannot create table %q under a table reached through an array-of-tables index: a header created for it would address the array's LAST entry",
				key)
		}
		newPath = path
	}

	d.appendTable(newPath)
	return nil
}

// storedValueNode returns the node holding the value a write would replace, and
// whether the path names one at all. It is what the equality rule compares
// against: a scalar answers with the bytes it was written as (or, having been
// written by the library already, its canonical form), and a container answers
// with its whole byte range, which is why a container-valued write that differs
// replaces the container wholesale.
func storedValueNode(parent layerPos, seg PathSegment) (Node, bool) {
	if seg.Kind == SegmentIndex {
		arr, ok := parent.node.(*ArrayNode)
		if !ok {
			return nil, false
		}
		idx, err := normalizeIndex(seg.Index, len(arr.elements))
		if err != nil {
			return nil, false
		}
		return arr.elements[idx], true
	}
	existing, bound, err := parent.binding(seg.Key)
	if err != nil || !bound || existing.node == nil {
		return nil, false
	}
	switch existing.kind {
	case EntryValue:
		return existing.node, true
	case EntryRecord:
		// The only record a value write reaches is an inline table, which is a
		// value fragment; every other binding was refused above.
		if _, inline := existing.node.(*InlineTableNode); inline {
			return existing.node, true
		}
	}
	return nil, false
}

// checkValueWriteTarget reports whether a value may be written at key inside
// parent. A value write touches value fragments and nothing else: a name bound
// by a structural construct -- a [header] table, an array-of-tables, or a table
// another construct only implied -- changes through a structural operation or
// an explicit Delete, never as a side effect of Set. A key holding an inline
// table is a value fragment, so it is replaced wholesale like any other value,
// and a key nothing binds yet is written fresh.
func checkValueWriteTarget(parent layerPos, key string) error {
	existing, bound, err := parent.binding(key)
	if err != nil {
		return err
	}
	if !bound || existing.kind == EntryValue {
		return nil
	}
	if _, inline := existing.node.(*InlineTableNode); inline {
		return nil
	}
	return newError(KindWrongContainer,
		"key %q is %s: change it with a structural operation, or Delete it before writing a value there",
		key, describeBinding(existing))
}

// setKeyInParent sets or replaces a key-value in a parent container.
// parentSegs is the path the parent was reached by, which is what names a table
// no single node stands for when one has to be given a header.
func (d *Document) setKeyInParent(parent layerPos, parentSegs []PathSegment, key string, valNode Node) error {
	switch p := parent.node.(type) {
	case *Document:
		return setKeyInDocument(p, key, valNode)
	case *TableNode:
		return setKeyInContainer(p, key, valNode)
	case *ArrayTableNode:
		return setKeyInContainer(p, key, valNode)
	case *InlineTableNode:
		return setKeyInContainer(p, key, valNode)
	default:
		if parent.records != nil {
			// An array-of-tables holds no keys of its own; an entry of it does.
			return parent.noNodeError()
		}
		if parent.node == nil && parent.rec != nil {
			return d.setKeyInImpliedTable(parent.rec, parentSegs, key, valNode)
		}
		return newError(KindWrongContainer, "cannot set key %q in %s", key, parent.describe())
	}
}

// setKeyInImpliedTable writes a key into a table no single node stands for. The
// table has no children of its own to append to, so the write goes where the
// document already spells that table out:
//
//   - a key the table already carries is bound by a concrete pair, and the
//     write replaces that pair's value;
//   - a new key of a table a DOTTED key spelled out joins the same region as
//     another dotted pair -- a header there would redefine the table, which
//     TOML refuses;
//   - a new key of a table only a LONGER header implies gets that table the
//     anchoring header TOML does allow it, and is written inside it.
func (d *Document) setKeyInImpliedTable(rec *Record, parentSegs []PathSegment, key string, valNode Node) error {
	if kv := rec.dottedKV(key); kv != nil {
		kv.setVal(valNode)
		return nil
	}
	if container, prefix, ok := rec.impliedRegion(); ok {
		return insertDottedKey(container, prefix, key, valNode)
	}
	keyPath, ok := keyPathOfSegments(parentSegs)
	if !ok {
		return newError(KindWrongContainer,
			"key %q belongs to a table with no header of its own, reached through an array-of-tables index: a header created for it would address the array's LAST entry, so give the table its header with NewTable first",
			key)
	}
	tbl := d.appendTable(keyPath)
	return appendContent(tbl, newKeyValueNode(key, valNode))
}

// insertDottedKey writes prefix+key as one dotted pair into the region that
// spells the table out, beside the pairs already there.
func insertDottedKey(container Node, prefix []string, key string, valNode Node) error {
	children, err := contents(container)
	if err != nil {
		return err
	}
	parts := append(append([]string(nil), prefix...), key)
	at := lastDottedSiblingIndex(children, prefix) + 1
	if at == 0 {
		// No pair of the region among these children, which only a document
		// whose region is empty can reach: keep the key out of the table
		// regions that follow the first header.
		at = firstHeaderIndex(children)
	}
	return insertContent(container, at, newDottedKeyValueNode(parts, valNode))
}

// lastDottedSiblingIndex returns the position of the last pair whose key starts
// with prefix, or -1 when the children carry none.
func lastDottedSiblingIndex(children []Node, prefix []string) int {
	last := -1
	for i, child := range children {
		kv, ok := child.(*KeyValueNode)
		if !ok || len(kv.key.parts) <= len(prefix) {
			continue
		}
		if pathsEqual(kv.key.parts[:len(prefix)], prefix) {
			last = i
		}
	}
	return last
}

// keyPathOfSegments returns the key path the segments spell out, and whether
// they spell one at all: a path stepping through an array index names no table
// a header could be written for.
func keyPathOfSegments(segments []PathSegment) ([]string, bool) {
	keyPath := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg.Kind != SegmentKey {
			return nil, false
		}
		keyPath = append(keyPath, seg.Key)
	}
	return keyPath, true
}

// appendTable appends a new [header] table for the key path to the document.
func (d *Document) appendTable(keyPath []string) *TableNode {
	tbl := &TableNode{keyPath: keyPath}
	tbl.markDirty()
	tbl.nodeTrivia.TrailingNewline = []byte("\n")
	_ = appendContent(d, tbl)
	return tbl
}

// setKeyInDocument sets or replaces a key at the document's own level. A key
// created there is inserted BEFORE the first table header rather than appended:
// everything after a header belongs to that header's table, so a root key
// written at the end of the file would read as a key of the last table -- and,
// where that table already carries the same key, as a duplicate.
func setKeyInDocument(doc *Document, key string, valNode Node) error {
	for _, child := range doc.children {
		if kv, ok := child.(*KeyValueNode); ok {
			if len(kv.key.parts) == 1 && kv.key.parts[0] == key {
				kv.setVal(valNode)
				return nil
			}
		}
	}
	return insertContent(doc, firstHeaderIndex(doc.children), newKeyValueNode(key, valNode))
}

// firstHeaderIndex returns the position of the first table or array-table
// header among the children, or their count when there is none. It is the end
// of the document's own key-value region.
func firstHeaderIndex(children []Node) int {
	for i, child := range children {
		switch child.(type) {
		case *TableNode, *ArrayTableNode:
			return i
		}
	}
	return len(children)
}

// setKeyInChildren searches children for an existing KV with the given key.
// If found, replaces its value. Otherwise, appends a new KV.
func setKeyInContainer(container Node, key string, valNode Node) error {
	children, err := contents(container)
	if err != nil {
		return err
	}
	for _, child := range children {
		if kv, ok := child.(*KeyValueNode); ok {
			if len(kv.key.parts) == 1 && kv.key.parts[0] == key {
				kv.setVal(valNode)
				return nil
			}
		}
	}
	// Key not found: create a new KV and append.
	return appendContent(container, newKeyValueNode(key, valNode))
}

// setIndexInParent replaces an element at the given index in an array.
func setIndexInParent(parent layerPos, index int, valNode Node) error {
	switch p := parent.node.(type) {
	case *ArrayNode:
		idx, err := normalizeIndex(index, len(p.elements))
		if err != nil {
			return err
		}
		// Transfer trivia (leading comments, leading whitespace, inline
		// comment) from the old element to the new one so that comments
		// on the replaced element survive re-rendering.
		oldTrivia := p.elements[idx].trivia()
		newTrivia := valNode.trivia()
		newTrivia.LeadingComments = oldTrivia.LeadingComments
		newTrivia.LeadingWhitespace = oldTrivia.LeadingWhitespace
		newTrivia.InlineComment = oldTrivia.InlineComment
		items, err := contents(p)
		if err != nil {
			return err
		}
		replaced := append([]Node(nil), items...)
		replaced[idx] = valNode
		return setContents(p, replaced)
	default:
		return newError(KindWrongContainer, "cannot set index [%d] in %s", index, parent.describe())
	}
}

// newKeyValueNode creates a dirty KeyValueNode for the given key and value.
func newKeyValueNode(key string, val Node) *KeyValueNode {
	return newDottedKeyValueNode([]string{key}, val)
}

// newDottedKeyValueNode creates a dirty KeyValueNode whose key is the given
// path, written as one dotted key.
func newDottedKeyValueNode(parts []string, val Node) *KeyValueNode {
	styles := make([]StringStyle, len(parts))
	for i := range parts {
		styles[i] = StringBasic
	}
	keyNode := &KeyNode{
		parts:  parts,
		styles: styles,
		frag:   canonicalKeyFragments(parts),
	}
	keyNode.markDirty()

	kv := newPair(keyNode, val)
	kv.markDirty()
	kv.nodeTrivia.TrailingNewline = []byte("\n")
	return kv
}

// Delete removes the node at the given path from the document. It handles
// key-value pairs, tables, array-of-tables, and array elements.
//
// Removal is idempotent: a path the document does not carry is a silent no-op,
// so an ensure-absent loop can call it unconditionally. A path that cannot be
// parsed, and a document the read-layer cannot fold at all, are still reported.
//
// The spelling of the table the key sits in changes nothing about that. In a
// table no single node stands for -- one a dotted key spelled out, or one only
// a longer header implies -- the removal reaches the dotted pair that binds a
// value, or the headers that spell a table out, those of the tables nested
// inside it included.
func (d *Document) Delete(path string) error {
	return d.diag(d.deleteAt(path), path)
}

func (d *Document) deleteAt(path string) error {
	segments, err := ParsePath(path)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return newError(KindBadPath, "empty path")
	}

	parentSegs := segments[:len(segments)-1]
	lastSeg := segments[len(segments)-1]

	// Resolve the parent container. A parent the document does not carry is a
	// silent no-op -- removal is idempotent by contract -- but a document that
	// cannot be folded at all is a failure of its own and is reported: reading
	// it as "the parent is not there" would hide the real defect.
	root, err := d.readLayer()
	if err != nil {
		return err
	}
	parent, err := walkFrom(posFromRecord(root), parentSegs)
	if err != nil {
		return nil
	}

	switch lastSeg.Kind {
	case SegmentKey:
		return d.deleteKeyFromParent(parent, lastSeg.Key)
	case SegmentIndex:
		return d.deleteIndexFromParent(parent, lastSeg.Index)
	default:
		return newError(KindBadPath, "unknown segment type")
	}
}

// deleteKeyFromParent removes everything a key binds inside a parent container.
// One name can be spelled out by several constructs at once, and all of them
// go: every pair written under the key -- a dotted key binds one leaf, so a
// table spelled out that way is removed pair by pair -- and every [header] and
// [[header]] the tables under it were written as, which sit among the
// document's own children rather than inside the table they name.
//
// A key the container does not carry removes nothing, which is the contract's
// silent no-op, and so is a parent that holds no keys at all.
func (d *Document) deleteKeyFromParent(parent layerPos, key string) error {
	if parent.rec != nil {
		if e, bound := parent.rec.Get(key); bound {
			for _, header := range headerNodesOf(e) {
				d.removeChild(header)
			}
		}
	}

	// The pairs are written in the container the position stands for, or -- for
	// a table no single node stands for -- in the region that spelled it out,
	// where the key path is the one the pairs there are written with.
	container, prefix := parent.node, []string(nil)
	if container == nil {
		if parent.rec == nil {
			return nil
		}
		var spelled bool
		container, prefix, spelled = parent.rec.impliedRegion()
		if !spelled {
			return nil
		}
	}
	removePairsUnder(container, append(append([]string(nil), prefix...), key))
	return nil
}

// removePairsUnder removes from a container every pair whose key path starts
// with path.
func removePairsUnder(container Node, path []string) {
	children, err := contents(container)
	if err != nil {
		// Not a container of pairs at all: nothing there binds the key.
		return
	}
	kept := make([]Node, 0, len(children))
	removed := false
	for _, child := range children {
		if kv, ok := child.(*KeyValueNode); ok && hasPrefix(kv.key.parts, path) {
			removed = true
			continue
		}
		kept = append(kept, child)
	}
	// Only a removal that took something changes the container: a key it never
	// carried leaves it exactly as written, down to its own bytes.
	if removed {
		_ = setContents(container, kept)
	}
}

// headerNodesOf collects the [header] and [[header]] nodes that spell an
// entry's table out, and those of every table nested inside it: a sub-table's
// header is a child of the document, not of the table it names, so removing a
// table has to take them along.
func headerNodesOf(e Entry) []Node {
	switch e.kind {
	case EntryRecord:
		return appendHeaderNodes(nil, e.record)
	case EntryRecords:
		var out []Node
		for _, r := range e.records {
			out = appendHeaderNodes(out, r)
		}
		return out
	default:
		return nil
	}
}

func appendHeaderNodes(out []Node, r *Record) []Node {
	switch r.node.(type) {
	case *TableNode, *ArrayTableNode:
		out = append(out, r.node)
	}
	for _, e := range r.entries {
		out = append(out, headerNodesOf(e)...)
	}
	return out
}

// removeChild removes one node from the document's own children, by identity.
func (d *Document) removeChild(target Node) {
	for i, child := range d.children {
		if child == target {
			_ = removeContent(d, i)
			return
		}
	}
}

// deleteIndexFromParent removes an element at the given index.
func (d *Document) deleteIndexFromParent(parent layerPos, index int) error {
	if parent.records != nil {
		if len(parent.records) == 0 {
			return nil
		}
		idx, err := normalizeIndex(index, len(parent.records))
		if err != nil {
			return nil // silent no-op for out-of-range
		}
		// Remove the entry's [[header]] from the document's children, and the
		// headers of every table nested inside it, which are document children
		// too and would otherwise attach to the entry that remains.
		for _, header := range appendHeaderNodes(nil, parent.records[idx]) {
			d.removeChild(header)
		}
		return nil
	}
	switch p := parent.node.(type) {
	case *ArrayNode:
		if len(p.elements) == 0 {
			return nil // silent no-op
		}
		idx, err := normalizeIndex(index, len(p.elements))
		if err != nil {
			return nil // silent no-op for out-of-range
		}
		return removeContent(p, idx)
	default:
		return nil // silent no-op
	}
}

// RenameKey changes the key name of the node at the given path to newKey.
//
// It renames the BINDING, whatever constructs spell it out. A name bound by a
// value is renamed in the pair that writes it; a name bound by a table is
// renamed in every header that names that table, the headers of the tables
// nested inside it included, and in every dotted pair written under it. A name
// bound by an array-of-tables is renamed in every entry's header. The renamed
// key part is the only fragment invalidated: the brackets, the other parts, the
// whitespace between them and the line's comment all splice as written.
//
// It reports KindNotFound when the path names nothing, KindWrongContainer when
// the last path segment is an array index (an element has no key to rename) or
// when the parent names an array-of-tables rather than one of its entries,
// KindConflict when anything in the parent already binds newKey -- a value, a
// table in any spelling, or an array-of-tables -- and KindBadInput when newKey
// is not valid UTF-8 and so is not a key TOML can carry. A refused rename
// changes nothing.
func (d *Document) RenameKey(path string, newKey string) error {
	return d.diag(d.renameKeyAt(path, newKey), path)
}

func (d *Document) renameKeyAt(path string, newKey string) error {
	segments, err := ParsePath(path)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return newError(KindBadPath, "empty path")
	}

	// All segments must be key segments for rename to make sense.
	lastSeg := segments[len(segments)-1]
	if lastSeg.Kind != SegmentKey {
		return newError(KindWrongContainer, "cannot rename an array index")
	}

	// The new name is written into every construct that spells the old one out,
	// so it has to be a key TOML can carry. It is asked before anything is
	// resolved, so a refused rename changes nothing.
	if err := checkWritableText(newKey, "key"); err != nil {
		return err
	}

	parentSegs := segments[:len(segments)-1]

	parent, err := d.resolveParentForEdit(parentSegs, false)
	if err != nil {
		return wrapError(err, "path not found")
	}

	// An array-of-tables holds no keys of its own; an entry of it does.
	if parent.records != nil {
		return parent.noNodeError()
	}

	return renameKeyInParent(parent, keyDepth(segments), lastSeg.Key, newKey)
}

// keyDepth returns how many table levels down a path's last key sits: the
// number of key steps to it, minus itself. An index step addresses an entry of
// a collection and adds no level, so "s[0].x" reaches the same depth as "s.x" --
// which is the part of a header's key path a rename there has to change.
func keyDepth(segments []PathSegment) int {
	keys := 0
	for _, seg := range segments {
		if seg.Kind == SegmentKey {
			keys++
		}
	}
	return keys - 1
}

// renameKeyInParent renames everything that binds oldKey inside a parent
// position: the pairs written under the name, and the headers of the tables
// written under it. depth is the position of the renamed part in a header's key
// path.
func renameKeyInParent(parent layerPos, depth int, oldKey, newKey string) error {
	// The old name must bind something, and the new one must be free. Every
	// construct binds its name -- a value, a table in any spelling, an
	// array-of-tables -- and renaming onto one would leave two constructs on
	// one key. Both questions are asked before anything is renamed, so a
	// refused rename changes nothing.
	switch parent.node.(type) {
	case *Document, *TableNode, *ArrayTableNode, *InlineTableNode:
		// A concrete container: the pairs are written among its children.
	default:
		// A table no single node stands for still has constructs spelling it
		// out; anything else -- a scalar, an array -- holds no keys at all.
		if parent.node != nil || parent.rec == nil {
			return newError(KindWrongContainer, "cannot rename key in %s", parent.describe())
		}
	}

	target, bound, err := parent.binding(oldKey)
	if err != nil {
		return err
	}
	if !bound {
		return newError(KindNotFound, "key %q not found", oldKey)
	}
	existing, taken, err := parent.binding(newKey)
	if err != nil {
		return err
	}
	if taken {
		return newError(KindConflict, "key %q is already %s here", newKey, describeBinding(existing))
	}

	// The headers: the table's own, and those of every table nested inside it.
	// A nested header carries the renamed part at the same depth, since it is
	// the same table level the name stands at.
	for _, header := range headerNodesOf(target) {
		renameHeaderPart(header, depth, newKey)
	}

	// The pairs: those written in the container the position stands for, or --
	// for a table no single node stands for -- in the region that spells it
	// out, where the key path the pairs carry starts with that region's prefix.
	container, prefix := parent.node, []string(nil)
	if container == nil {
		// A table only a longer header implies writes no pairs of its own:
		// everything under it is a header, and those are already renamed. One a
		// dotted key spelled out does, in the region that spelled it.
		var spelled bool
		container, prefix, spelled = parent.rec.impliedRegion()
		if !spelled {
			return nil
		}
	}
	renamePairsUnder(container, prefix, oldKey, newKey)
	return nil
}

// renamePairsUnder renames the key part that binds oldKey in every pair of a
// container written under prefix: a pair binding the name itself, and a dotted
// pair reaching through it into a table the name spells out.
func renamePairsUnder(container Node, prefix []string, oldKey, newKey string) {
	children, err := contents(container)
	if err != nil {
		return
	}
	at := len(prefix)
	for _, child := range children {
		kv, ok := child.(*KeyValueNode)
		if !ok || len(kv.key.parts) <= at {
			continue
		}
		if kv.key.parts[at] != oldKey || !pathsEqual(kv.key.parts[:at], prefix) {
			continue
		}
		kv.key.renamePart(at, newKey)
	}
}

// NewTable creates a new [table] header at the given path and appends it to
// the document. The path must consist of key segments only (no array indices).
//
// The header must be able to bind its name. It is refused with KindConflict
// when anything else in the document already does: a value, an inline table, a
// table with its own header, an array-of-tables, or a table a dotted key
// implied (TOML does not allow giving one of those a header of its own). A
// table implied only by a LONGER header -- the "a" of an earlier [a.b] -- has
// no header of its own yet, and this is how it gets one. A prefix of the path
// that holds a value is refused on the same terms.
//
// The refusal is about the path's FINAL key only. A table a dotted key implied
// may not be redefined by a header, and it may still hold sub-tables of its
// own: with "apple.color" written under [fruit], creating [fruit.apple] is
// refused and [fruit.apple.texture] is not, exactly as TOML has it.
func (d *Document) NewTable(path string) error {
	return d.diag(d.newTableAt(path), path)
}

func (d *Document) newTableAt(path string) error {
	keyPath, err := headerKeyPath(path, "NewTable")
	if err != nil {
		return err
	}

	existing, bound, err := d.headerTarget(keyPath)
	if err != nil {
		return err
	}
	if bound {
		name := pathFromKeys(keyPath)
		switch {
		case existing.kind == EntryRecords:
			return newError(KindConflict,
				"%q is an array of tables: a [%s] header cannot bind the same name", name, name)
		case existing.kind != EntryRecord:
			return newError(KindConflict, "%q already holds a value", name)
		case existing.record.dottedImplied:
			return newError(KindConflict,
				"%q is a table a dotted key spelled out, and TOML does not allow redefining one with a header of its own; a header for a table UNDER it is fine",
				name)
		case existing.record.anchored:
			if _, inline := existing.node.(*InlineTableNode); inline {
				return newError(KindConflict,
					"%q is an inline table: a [%s] header cannot bind the same name", name, name)
			}
			return newError(KindConflict, "table [%s] already exists", name)
		}
		// A table implied by a longer header: this header anchors it.
	}

	d.appendTable(keyPath)
	return nil
}

// NewArrayTable appends a new [[array-table]] entry at the given path.
// Multiple entries with the same path are valid in TOML and represent
// successive elements of the array. The path must consist of key segments
// only (no array indices).
//
// A name already bound to an array-of-tables is exactly what a new entry
// extends. Any other binding of the name -- a value, an inline table, a table
// with a header of its own or one another construct implied -- is refused with
// KindConflict, as is a prefix of the path that holds a value.
func (d *Document) NewArrayTable(path string) error {
	return d.diag(d.newArrayTableAt(path), path)
}

func (d *Document) newArrayTableAt(path string) error {
	keyPath, err := headerKeyPath(path, "NewArrayTable")
	if err != nil {
		return err
	}

	existing, bound, err := d.headerTarget(keyPath)
	if err != nil {
		return err
	}
	if bound && existing.kind != EntryRecords {
		name := pathFromKeys(keyPath)
		if existing.kind == EntryRecord {
			return newError(KindConflict,
				"%q is a table: a [[%s]] header cannot bind the same name", name, name)
		}
		return newError(KindConflict, "%q already holds a value", name)
	}

	atbl := &ArrayTableNode{
		keyPath: keyPath,
	}
	atbl.markDirty()
	atbl.nodeTrivia.TrailingNewline = []byte("\n")

	_ = appendContent(d, atbl)
	return nil
}

// describeBinding names what an entry binds its key to, the way a diagnostic
// reads it: the construct behind the binding rather than the layer's own
// vocabulary.
func describeBinding(e Entry) string {
	switch e.kind {
	case EntryRecords:
		return "an array of tables"
	case EntryRecord:
		switch e.node.(type) {
		case *InlineTableNode:
			return "an inline table"
		case *TableNode:
			return "a table with a header of its own"
		default:
			return "a table another construct spelled out"
		}
	default:
		return "a value"
	}
}

// headerKeyPath parses a header path into its key parts, refusing an empty
// path and any index segment. op names the operation in the diagnostic.
func headerKeyPath(path string, op string) ([]string, error) {
	segments, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, newError(KindBadPath, "empty path")
	}
	keyPath := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg.Kind != SegmentKey {
			return nil, newError(KindBadPath, "%s path must contain only key segments, not indices", op)
		}
		keyPath = append(keyPath, seg.Key)
	}
	if err := checkWritableKeys(segments); err != nil {
		return nil, err
	}
	return keyPath, nil
}

// headerTarget reports what a header's key path names in the document as it
// stands: the entry its final key already binds, and whether one does. Only a
// prefix that cannot hold a table at all -- one holding a value -- is a
// conflict, and so is a document that no longer folds. A prefix a dotted key
// spelled out holds sub-tables perfectly well; what TOML refuses is a header
// that redefines such a table, which the caller asks about the FINAL key.
func (d *Document) headerTarget(keyPath []string) (Entry, bool, error) {
	root, err := d.readLayer()
	if err != nil {
		return Entry{}, false, err
	}
	cur := root
	for i, part := range keyPath[:len(keyPath)-1] {
		e, ok := cur.Get(part)
		if !ok {
			// Nothing binds this prefix, so nothing binds the final key
			// either: the header creates the whole chain.
			return Entry{}, false, nil
		}
		switch {
		case e.kind == EntryRecords:
			cur = e.records[len(e.records)-1]
		case e.kind != EntryRecord:
			return Entry{}, false, newError(KindConflict,
				"%q holds a value and cannot hold a table", pathFromKeys(keyPath[:i+1]))
		default:
			// A prefix a dotted key spelled out is descended through like any
			// other: TOML refuses only a header that REDEFINES such a table,
			// which is a question about the final key, asked below. An inline
			// table is the one record kind no header may descend into at all --
			// TOML gives one no way to be added to.
			if _, inline := e.record.node.(*InlineTableNode); inline {
				return Entry{}, false, newError(KindConflict,
					"%q is an inline table, and TOML does not allow adding to one with a header",
					pathFromKeys(keyPath[:i+1]))
			}
			cur = e.record
		}
	}
	e, ok := cur.Get(keyPath[len(keyPath)-1])
	return e, ok, nil
}

// checkWritableText refuses text that is not valid UTF-8. A TOML document is
// UTF-8, and the renderers that write a string or a key decode what they are
// handed -- an invalid byte becomes U+FFFD, so the document would read back a
// value, or a name, the caller never wrote. Every write refuses it here instead;
// the exported renderers stay total and are never reached with such input. what
// names the input in the diagnostic.
func checkWritableText(s string, what string) error {
	if utf8.ValidString(s) {
		return nil
	}
	return newError(KindBadInput,
		"the %s %q is not valid UTF-8 and cannot be written into a TOML document", what, s).withValue(s)
}

// checkWritableKeys refuses a path a write cannot spell out. A write creates
// the keys its path names -- the final one, and, for SetCreate, the tables
// along the way -- so each of them has to be a key TOML can carry.
func checkWritableKeys(segments []PathSegment) error {
	for _, seg := range segments {
		if seg.Kind != SegmentKey {
			continue
		}
		if err := checkWritableText(seg.Key, "key"); err != nil {
			return err
		}
	}
	return nil
}

// valueToNode converts a Go value to the appropriate AST node.
// All created nodes are dirty (no raw bytes).
//
// A Node is not a value. One handed in from outside carries a span, a lexeme
// and a parent belonging to wherever it was built, none of which describe the
// document it would be grafted into, and putting it in two documents at once
// would give it two parents. Values enter as Go values and the library renders
// them; a node is refused like any other unsupported type.
func valueToNode(v any) (Node, error) {
	if v == nil {
		return nil, newError(KindBadInput, "unsupported type: nil")
	}

	switch val := v.(type) {
	case string:
		if err := checkWritableText(val, "string value"); err != nil {
			return nil, err
		}
		n := &StringNode{val: scalarOf(val), style: StringBasic}
		n.markDirty()
		return n, nil

	case bool:
		n := &BooleanNode{val: scalarOf(val)}
		n.markDirty()
		return n, nil

	case int:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case int8:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case int16:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case int32:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case int64:
		n := &IntegerNode{val: scalarOf[int64](val), base: IntegerDecimal}
		n.markDirty()
		return n, nil

	case uint:
		return unsignedToNode(uint64(val), val)
	case uint8:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case uint16:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case uint32:
		n := &IntegerNode{val: scalarOf[int64](int64(val)), base: IntegerDecimal}
		n.markDirty()
		return n, nil
	case uint64:
		return unsignedToNode(val, val)

	case float32:
		return floatToNode(float64(val), val)
	case float64:
		return floatToNode(val, val)

	case time.Time:
		n := &DateTimeNode{val: scalarOf(val)}
		n.markDirty()
		return n, nil

	case LocalDateTime:
		n := &LocalDateTimeNode{val: scalarOf(val)}
		n.markDirty()
		return n, nil

	case LocalDate:
		n := &LocalDateNode{val: scalarOf(val)}
		n.markDirty()
		return n, nil

	case LocalTime:
		n := &LocalTimeNode{val: scalarOf(val)}
		n.markDirty()
		return n, nil

	case []any:
		return sliceToArrayNode(val)

	case map[string]any:
		return mapToInlineTableNode(val)

	case []Pair:
		return pairsToInlineTableNode(val)
	}

	// Use reflection for typed slices (e.g., []string, []int).
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice {
		items := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			items[i] = rv.Index(i).Interface()
		}
		return sliceToArrayNode(items)
	}

	return nil, newError(KindBadInput, "unsupported type: %T", v).withValue(v)
}

// floatToNode converts a Go float to a float node, refusing a NaN whose sign bit
// is set. TOML has one NaN spelling and this library writes it, so accepting a
// signed NaN would drop the sign in silence; a caller that means "not a number"
// passes the ordinary one. The infinities need no such rule -- TOML spells both.
// orig is the value as the caller passed it, so the diagnostic reports its own
// type.
func floatToNode(v float64, orig any) (Node, error) {
	if math.IsNaN(v) && math.Signbit(v) {
		return nil, newError(KindBadInput,
			"a NaN with its sign bit set has no TOML spelling of its own: the value would be written \"nan\", dropping the sign").withValue(orig)
	}
	n := &FloatNode{val: scalarOf(v)}
	n.markDirty()
	return n, nil
}

// unsignedToNode converts an unsigned Go value to an integer node, refusing one
// that does not fit in the int64 a TOML integer is. Wrapping it around would
// write a negative number the caller never asked for. orig is the value as the
// caller passed it, so the diagnostic reports its own type.
func unsignedToNode(v uint64, orig any) (Node, error) {
	if v > math.MaxInt64 {
		return nil, newError(KindBadInput,
			"unsigned value %d does not fit in a TOML integer (the maximum is %d)",
			v, int64(math.MaxInt64)).withValue(orig)
	}
	n := &IntegerNode{val: scalarOf[int64](int64(v)), base: IntegerDecimal}
	n.markDirty()
	return n, nil
}

// sliceToArrayNode converts a []any to an ArrayNode with recursive conversion.
func sliceToArrayNode(items []any) (Node, error) {
	arr := &ArrayNode{}
	arr.markDirty()
	for _, item := range items {
		elem, err := valueToNode(item)
		if err != nil {
			return nil, wrapError(err, "array element")
		}
		buildAppend(arr, elem)
	}
	return arr, nil
}

// Pair is one key of an ordered inline table, the input any value-writing
// operation takes as a []Pair: Set, SetCreate, AppendToArray, EnsureDefaults,
// and the element and value positions inside them.
//
// Key is a SINGLE key, taken verbatim -- never a path. "a.b" is one key
// spelled with a dot in it, written quoted, and not a table "a" holding a "b".
// Use a nested []Pair (or a Default's path) to reach into a table. A duplicate
// key and a key that is not valid UTF-8 are each refused with KindBadInput
// when the operation converts the value.
//
// A map[string]any is the unordered alternative: its keys are written in
// sorted order, where a []Pair is written in the order given.
type Pair struct {
	Key   string
	Value any
}

// pairsToInlineTableNode converts an ordered []Pair to an InlineTableNode,
// keeping the order it was given.
func pairsToInlineTableNode(pairs []Pair) (Node, error) {
	tbl := &InlineTableNode{}
	tbl.markDirty()
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		if err := checkWritableText(pair.Key, "key"); err != nil {
			return nil, err
		}
		if _, dup := seen[pair.Key]; dup {
			return nil, newError(KindBadInput,
				"key %q appears twice in the ordered inline table", pair.Key).withValue(pair.Key)
		}
		seen[pair.Key] = struct{}{}
		valNode, err := valueToNode(pair.Value)
		if err != nil {
			return nil, wrapError(err, "inline table key %q", pair.Key)
		}
		buildAppend(tbl, newKeyValueNode(pair.Key, valNode))
	}
	return tbl, nil
}

// mapToInlineTableNode converts a map[string]any to an InlineTableNode.
// Keys are sorted alphabetically for deterministic output.
func mapToInlineTableNode(m map[string]any) (Node, error) {
	tbl := &InlineTableNode{}
	tbl.markDirty()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := checkWritableText(k, "key"); err != nil {
			return nil, err
		}
		valNode, err := valueToNode(m[k])
		if err != nil {
			return nil, wrapError(err, "inline table key %q", k)
		}
		buildAppend(tbl, newKeyValueNode(k, valNode))
	}
	return tbl, nil
}
