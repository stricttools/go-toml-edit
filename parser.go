package tomledit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Parse lexes and parses TOML source bytes into a Document AST.
//
// The returned Document preserves all whitespace, comments, and formatting
// from the original source. Serializing it back with Bytes produces the exact
// original bytes (round-trip fidelity).
//
// Every lexing or parsing failure -- including duplicate key detection and
// invalid TOML syntax -- is reported as an *Error of kind KindSyntax, carrying
// the position, span and source line of the offending construct. Parsing stops
// at the first failure.
func Parse(src []byte) (*Document, error) {
	return parseSource(src, "")
}

// ParseWithFileName parses src as Parse does, and records file as the
// document's origin, so every diagnostic it later produces -- from parsing,
// access or editing -- names that file. It reads nothing: file is only the name
// the diagnostics carry. The tomlfile subpackage reads a file and parses it
// through this function.
func ParseWithFileName(src []byte, file string) (*Document, error) {
	return parseSource(src, file)
}

// parseSource lexes and parses src, recording file as the document's origin
// and on every diagnostic the parse itself reports.
func parseSource(src []byte, file string) (*Document, error) {
	tokens, err := lex(src)
	if err != nil {
		return nil, stampFile(err, file)
	}
	p := &parser{
		tokens: tokens,
		pos:    0,
		src:    src,
	}
	doc, err := p.parseDocument()
	if err != nil {
		return nil, stampFile(err, file)
	}
	doc.file = file
	return doc, nil
}

// parser is the internal recursive-descent parser.
type parser struct {
	tokens []token
	pos    int
	src    []byte
}

// --- token helpers ---

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{Type: tokenEOF}
}

func (p *parser) peekType() tokenType {
	return p.peek().Type
}

func (p *parser) advance() token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *parser) expect(typ tokenType) (token, error) {
	tok := p.advance()
	if tok.Type != typ {
		return tok, p.errorfAt(tok, "expected %s, got %s", typ.describe(), tok.Type.describe())
	}
	return tok, nil
}

// errorfAt reports a parse failure at tok, carrying tok's position, the span
// of tok itself, and the source line it sits on.
func (p *parser) errorfAt(tok token, format string, args ...any) *Error {
	return syntaxErrorAt(p.src, tokenStart(tok), format, args...).within(spanFromToken(tok))
}

// errorf reports a parse failure at the token the parser is looking at.
func (p *parser) errorf(format string, args ...any) *Error {
	tok := p.peek()
	return p.errorfAt(tok, format, args...)
}

// errorFrom reports err -- a failure from one of the value or string decoding
// helpers, which know no positions -- as a parse-stage diagnostic at tok.
func (p *parser) errorFrom(tok token, err error) *Error {
	return p.errorfAt(tok, "%s", err.Error()).wrapping(err)
}

// --- trivia collection ---

// leadingTrivia is everything consumed before a content node: the blank-line
// runs and comment lines that precede it, and the whitespace on its own line.
//
// blankRuns holds the blank lines AROUND the comments -- entry i the run before
// comments[i], and the entry after the last comment the run between that
// comment and the content node -- so it always carries len(comments)+1 entries.
// Keeping the bytes rather than a bare count preserves a blank line that
// carries whitespace of its own.
type leadingTrivia struct {
	whitespace []byte   // whitespace on the same line as the coming content
	comments   [][]byte // full comment lines (leading whitespace + comment + newline)
	blankRuns  [][]byte // the blank-line runs around the comments
	blankSpans []Span   // parallel to blankRuns; the zero Span for an empty run
	raw        []byte   // every consumed byte, concatenated
	orphan     orphanTrivia
}

// blankRun returns the bytes of blank-line run i, or nil when there is none.
func (lt leadingTrivia) blankRun(i int) []byte {
	if i < 0 || i >= len(lt.blankRuns) {
		return nil
	}
	return lt.blankRuns[i]
}

// blankSpan returns the source range of blank-line run i, or the zero Span.
func (lt leadingTrivia) blankSpan(i int) Span {
	if i < 0 || i >= len(lt.blankSpans) {
		return Span{}
	}
	return lt.blankSpans[i]
}

// applyTo copies the parts a node keeps in its own trivia.
func (lt leadingTrivia) applyTo(t *trivia) {
	t.LeadingWhitespace = lt.whitespace
	t.LeadingComments = lt.comments
	t.blankRuns = lt.blankRuns
}

// collectLeadingTrivia consumes the whitespace, comments, and newlines that
// precede a content node.
func (p *parser) collectLeadingTrivia() leadingTrivia {
	// Accumulate lines. A "line" may be: whitespace, comment+newline, blank newline.
	// The last whitespace run (if not followed by newline or comment) is the
	// inline leading whitespace for the coming node.

	lt := leadingTrivia{blankRuns: [][]byte{nil}, blankSpans: []Span{{}}}
	var accumulated []byte // all bytes consumed
	var pendingWS []byte   // whitespace not yet assigned

	// track records the span of every consumed trivia token so orphan
	// CommentNodes (emitted at end of input) can carry positions.
	track := func(tok token) {
		sp := spanFromToken(tok)
		if !lt.orphan.span.IsValid() {
			lt.orphan.span.Start = sp.Start
		}
		lt.orphan.span.End = sp.End
	}

	// addBlank appends one blank line to the run currently being built, and
	// grows that run's span to cover it.
	addBlank := func(line []byte, span Span) {
		last := len(lt.blankRuns) - 1
		lt.blankRuns[last] = append(lt.blankRuns[last], line...)
		if !lt.blankSpans[last].IsValid() {
			lt.blankSpans[last].Start = span.Start
		}
		lt.blankSpans[last].End = span.End
	}

	for {
		tt := p.peekType()
		switch tt {
		case tokenWhitespace:
			tok := p.advance()
			track(tok)
			pendingWS = append(pendingWS, tok.Raw...)
			continue

		case tokenNewline:
			tok := p.advance()
			track(tok)
			// The pending whitespace + this newline is a blank line.
			blank := make([]byte, 0, len(pendingWS)+len(tok.Raw))
			blank = append(blank, pendingWS...)
			blank = append(blank, tok.Raw...)
			pendingWS = nil
			accumulated = append(accumulated, blank...)
			addBlank(blank, spanFromToken(tok))
			continue

		case tokenComment:
			tok := p.advance()
			track(tok)
			// A comment line: pendingWS is its leading whitespace
			commentLine := make([]byte, 0, len(pendingWS)+len(tok.Raw))
			commentLine = append(commentLine, pendingWS...)
			commentLine = append(commentLine, tok.Raw...)
			pendingWS = nil
			lt.orphan.commentSpans = append(lt.orphan.commentSpans, spanFromToken(tok))

			// Consume the newline after the comment if present
			if p.peekType() == tokenNewline {
				nl := p.advance()
				track(nl)
				commentLine = append(commentLine, nl.Raw...)
			}
			accumulated = append(accumulated, commentLine...)
			lt.comments = append(lt.comments, commentLine)
			// The next blank lines belong to the run after this comment.
			lt.blankRuns = append(lt.blankRuns, nil)
			lt.blankSpans = append(lt.blankSpans, Span{})
			continue

		default:
			// Not trivia. pendingWS is the leading whitespace for the content node.
			lt.whitespace = pendingWS
			lt.raw = append(accumulated, pendingWS...)
			return lt
		}
	}
}

// orphanTrivia carries position data for trivia consumed by
// collectLeadingTrivia, used when the trivia becomes orphan CommentNodes at
// end of input.
type orphanTrivia struct {
	commentSpans []Span // parallel to leadingComments; each covers only the '#...' token
	span         Span   // the entire consumed trivia region; zero if nothing was consumed
}

// consumeInlineTrivia consumes optional whitespace + comment on the same line
// after a value (before newline/EOF).
func (p *parser) consumeInlineTrivia() (ws []byte, comment []byte) {
	if p.peekType() == tokenWhitespace {
		tok := p.advance()
		ws = tok.Raw
	}
	if p.peekType() == tokenComment {
		tok := p.advance()
		comment = tok.Raw
	}
	return
}

// consumeTrailingNewline consumes a newline or checks for EOF.
func (p *parser) consumeTrailingNewline() []byte {
	if p.peekType() == tokenNewline {
		tok := p.advance()
		return tok.Raw
	}
	return nil
}

// --- document parsing ---

func (p *parser) parseDocument() (*Document, error) {
	doc := &Document{}
	tracker := newDefinitionTracker(p.src)

	for p.peekType() != tokenEOF {
		lt := p.collectLeadingTrivia()

		tt := p.peekType()
		switch {
		case tt == tokenEOF:
			// Any remaining trivia becomes orphan comment nodes
			p.emitOrphanTrivia(doc, lt)

		case tt == tokenLeftBracket:
			tbl, err := p.parseTable(lt, tracker)
			if err != nil {
				return nil, err
			}
			buildAppend(doc, tbl)

		case tt == tokenDoubleLeftBracket:
			atbl, err := p.parseArrayTable(lt, tracker)
			if err != nil {
				return nil, err
			}
			buildAppend(doc, atbl)

		case isKeyToken(tt):
			kv, err := p.parseKeyValue(lt, tracker, nil)
			if err != nil {
				return nil, err
			}
			buildAppend(doc, kv)

		default:
			return nil, p.errorf("%s is not valid here", tt.describe())
		}
	}

	// The document spans from the first byte to the EOF position.
	eof := p.peek()
	doc.setSpan(Span{
		Start: Position{Line: 1, Column: 1, Offset: 0},
		End:   tokenStart(eof),
	})

	return doc, nil
}

func (p *parser) emitOrphanTrivia(parent interface{ addChild(Node) }, lt leadingTrivia) {
	if len(lt.comments) == 0 && len(lt.raw) == 0 {
		return
	}
	// If we have comment lines, emit them as CommentNodes, each preceded by the
	// blank-line run written before it. Nothing follows this trivia that could
	// carry those blank lines, so they become nodes of their own.
	if len(lt.comments) > 0 {
		for i, c := range lt.comments {
			emitBlankRun(parent, lt.blankRun(i), lt.blankSpan(i))
			cn := &CommentNode{text: string(c)}
			cn.setRaw(c)
			if i < len(lt.orphan.commentSpans) {
				cn.setSpan(lt.orphan.commentSpans[i])
			}
			parent.addChild(cn)
		}
		// The run after the last comment, plus any whitespace that never
		// reached a content node because the input ended.
		last := len(lt.comments)
		tail := make([]byte, 0, len(lt.blankRun(last))+len(lt.whitespace))
		tail = append(tail, lt.blankRun(last)...)
		tail = append(tail, lt.whitespace...)
		emitBlankRun(parent, tail, lt.blankSpan(last))
		return
	}
	// If only blank lines (raw without comments), attach as a comment node with empty text
	if len(lt.raw) > 0 {
		cn := &CommentNode{text: ""}
		cn.setRaw(lt.raw)
		cn.setSpan(lt.orphan.span)
		parent.addChild(cn)
	}
}

// emitBlankRun adds a node standing for a run of blank lines, which is how the
// document holds blank lines that no following construct can carry.
func emitBlankRun(parent interface{ addChild(Node) }, run []byte, span Span) {
	if len(run) == 0 {
		return
	}
	cn := &CommentNode{text: ""}
	cn.setRaw(run)
	cn.setSpan(span)
	parent.addChild(cn)
}

// childAdder is implemented by Document, TableNode, ArrayTableNode.
type childAdder interface {
	addChild(Node)
}

func (n *Document) addChild(c Node)       { buildAppend(n, c) }
func (n *TableNode) addChild(c Node)      { buildAppend(n, c) }
func (n *ArrayTableNode) addChild(c Node) { buildAppend(n, c) }

func isKeyToken(tt tokenType) bool {
	return tt == tokenBareKey || tt == tokenBasicString || tt == tokenLiteralString
}

// --- table parsing ---

func (p *parser) parseTable(lt leadingTrivia, tracker *definitionTracker) (*TableNode, error) {
	startPos := p.pos

	// consume [
	openTok, err := p.expect(tokenLeftBracket)
	if err != nil {
		return nil, err
	}

	// optional whitespace inside bracket
	p.skipWhitespace()

	// parse key path
	keyPath, headerKey, keySpans, keyPos, err := p.parseKeyPath(startPos)
	if err != nil {
		return nil, err
	}

	afterKey := p.pos
	p.skipWhitespace()

	// consume ]
	closeTok, err := p.expect(tokenRightBracket)
	if err != nil {
		return nil, err
	}
	// The closing bracket belongs to the gap after the last key part, so a
	// header that re-renders writes its brackets back as they were.
	headerKey.extendEnd(p.rawFromTokenRange(afterKey, p.pos))

	// inline comment
	inlineGap, inlineComment := p.consumeInlineTrivia()

	// After the table header, only a newline or EOF is allowed
	if tt := p.peekType(); tt != tokenNewline && tt != tokenEOF {
		return nil, p.errorf("unexpected content after table header")
	}

	// trailing newline
	trailingNL := p.consumeTrailingNewline()

	// Build raw bytes for the header line
	headerRaw := p.rawFromTokenRange(startPos, p.pos)
	headerRaw = append(lt.raw, headerRaw...)

	tbl := &TableNode{
		keyPath:  keyPath,
		keySpans: keySpans,
	}
	buildHeaderKey(tbl, headerKey)
	tbl.setRaw(headerRaw)
	// The table span covers only the [header], not the children.
	tbl.setSpan(Span{Start: tokenStart(openTok), End: spanFromToken(closeTok).End})
	lt.applyTo(&tbl.nodeTrivia)
	tbl.nodeTrivia.inlineGap = inlineGap
	tbl.nodeTrivia.InlineComment = inlineComment
	tbl.nodeTrivia.TrailingNewline = trailingNL

	// Check for redefinition
	if err := tracker.defineTable(keyPath, tbl, keyPos); err != nil {
		return nil, err
	}

	// Parse children (key-value pairs and orphan comments) until next table or EOF
	childTracker := tracker.tableScope(keyPath)
	if err := p.parseTableChildren(tbl, childTracker, tracker); err != nil {
		return nil, err
	}

	return tbl, nil
}

func (p *parser) parseArrayTable(lt leadingTrivia, tracker *definitionTracker) (*ArrayTableNode, error) {
	startPos := p.pos

	// consume [[
	openTok, err := p.expect(tokenDoubleLeftBracket)
	if err != nil {
		return nil, err
	}

	p.skipWhitespace()

	// parse key path
	keyPath, headerKey, keySpans, keyPos, err := p.parseKeyPath(startPos)
	if err != nil {
		return nil, err
	}

	afterKey := p.pos
	p.skipWhitespace()

	// consume ]]
	closeTok, err := p.expect(tokenDoubleRightBracket)
	if err != nil {
		return nil, err
	}
	headerKey.extendEnd(p.rawFromTokenRange(afterKey, p.pos))

	// inline comment
	inlineGap, inlineComment := p.consumeInlineTrivia()

	// After the array table header, only a newline or EOF is allowed
	if tt := p.peekType(); tt != tokenNewline && tt != tokenEOF {
		return nil, p.errorf("unexpected content after array table header")
	}

	// trailing newline
	trailingNL := p.consumeTrailingNewline()

	// Build raw bytes for the header line
	headerRaw := p.rawFromTokenRange(startPos, p.pos)
	headerRaw = append(lt.raw, headerRaw...)

	atbl := &ArrayTableNode{
		keyPath:  keyPath,
		keySpans: keySpans,
	}
	buildHeaderKey(atbl, headerKey)
	atbl.setRaw(headerRaw)
	// The array-table span covers only the [[header]], not the children.
	atbl.setSpan(Span{Start: tokenStart(openTok), End: spanFromToken(closeTok).End})
	lt.applyTo(&atbl.nodeTrivia)
	atbl.nodeTrivia.inlineGap = inlineGap
	atbl.nodeTrivia.InlineComment = inlineComment
	atbl.nodeTrivia.TrailingNewline = trailingNL

	// Check for redefinition
	if err := tracker.defineArrayTable(keyPath, atbl, keyPos); err != nil {
		return nil, err
	}

	// Parse children
	childTracker := tracker.arrayTableScope(keyPath)
	if err := p.parseTableChildren(atbl, childTracker, tracker); err != nil {
		return nil, err
	}

	return atbl, nil
}

func (p *parser) parseTableChildren(parent childAdder, childTracker *definitionTracker, rootTracker *definitionTracker) error {
	for {
		// Peek ahead past trivia to see what's next
		savedPos := p.pos
		lt := p.collectLeadingTrivia()

		tt := p.peekType()
		switch {
		case tt == tokenEOF:
			p.emitOrphanTrivia(parent, lt)
			return nil

		case tt == tokenLeftBracket || tt == tokenDoubleLeftBracket:
			// End of this table's children. Restore position so the outer loop handles it.
			p.pos = savedPos
			return nil

		case isKeyToken(tt):
			kv, err := p.parseKeyValue(lt, childTracker, nil)
			if err != nil {
				return err
			}
			parent.addChild(kv)

		default:
			return p.errorf("%s is not valid in a table body", tt.describe())
		}
	}
}

// --- key-value parsing ---

func (p *parser) parseKeyValue(lt leadingTrivia, tracker *definitionTracker, parentPath []string) (*KeyValueNode, error) {
	startPos := p.pos

	// Parse key (possibly dotted)
	keyNode, keyPos, sepStart, err := p.parseKey()
	if err != nil {
		return nil, err
	}

	// Everything from the key's last part to the value is the separator
	// fragment: the "=" and the whitespace around it, spliced back whenever the
	// pair re-renders.
	p.skipWhitespace()

	// Expect =
	_, err = p.expect(tokenEquals)
	if err != nil {
		return nil, err
	}

	p.skipWhitespace()
	sep := p.rawFromTokenRange(sepStart, p.pos)

	// Parse value
	val, err := p.parseValue()
	if err != nil {
		return nil, err
	}

	// Inline trivia
	inlineGap, inlineComment := p.consumeInlineTrivia()

	// Trailing newline
	trailingNL := p.consumeTrailingNewline()

	// Build raw bytes
	kvRaw := p.rawFromTokenRange(startPos, p.pos)
	kvRaw = append(lt.raw, kvRaw...)

	kv := newPair(keyNode, val)
	kv.buildSep(sep)
	kv.setRaw(kvRaw)
	// The key-value span runs from the key's first byte to the value's last
	// byte, excluding leading trivia, inline comment, and trailing newline.
	kv.setSpan(Span{Start: keyNode.Span().Start, End: val.Span().End})
	lt.applyTo(&kv.nodeTrivia)
	kv.nodeTrivia.inlineGap = inlineGap
	kv.nodeTrivia.InlineComment = inlineComment
	kv.nodeTrivia.TrailingNewline = trailingNL

	// Track definition
	fullPath := append(parentPath, keyNode.parts...)
	if err := tracker.defineKey(fullPath, kv, keyPos); err != nil {
		return nil, err
	}

	return kv, nil
}

// --- key parsing ---

// parseKey parses a (possibly dotted) key, returning the KeyNode, the source
// position of the first key token, and the index of the first token after the
// key's last part -- where the separator fragment begins. The key's own
// fragments end at that part: the whitespace before the "=" is the separator's,
// not the key's.
func (p *parser) parseKey() (*KeyNode, Position, int, error) {
	key := &KeyNode{}
	startPos := p.pos

	// Capture position of the first key token
	firstTok := p.peek()

	// mark tracks the first token not yet accounted for by a fragment, so the
	// bytes standing between the parts -- dots and whitespace -- are captured
	// as the gaps that render between them.
	mark := p.pos

	// First part
	part, rawPart, style, err := p.parseSimpleKey()
	if err != nil {
		return nil, Position{}, 0, err
	}
	key.buildPart(part, rawPart, p.rawFromTokenRange(mark, p.pos-1), style)
	// parseSimpleKey consumes exactly one token; track the last key token so
	// the span ends at the final key part (excluding trailing whitespace).
	lastTok := p.tokens[p.pos-1]
	key.partSpans = append(key.partSpans, spanFromToken(lastTok))
	mark = p.pos

	// Additional dotted parts
	for {
		// Optional whitespace before dot
		p.skipWhitespace()
		if p.peekType() != tokenDot {
			break
		}
		p.advance() // consume dot
		p.skipWhitespace()

		part, rawPart, style, err = p.parseSimpleKey()
		if err != nil {
			return nil, Position{}, 0, err
		}
		key.buildPart(part, rawPart, p.rawFromTokenRange(mark, p.pos-1), style)
		lastTok = p.tokens[p.pos-1]
		key.partSpans = append(key.partSpans, spanFromToken(lastTok))
		mark = p.pos
	}

	key.buildKeyEnd(nil)
	key.setRaw(p.rawFromTokenRange(startPos, p.pos))
	key.setSpan(Span{Start: tokenStart(firstTok), End: spanFromToken(lastTok).End})
	return key, tokenStart(firstTok), mark, nil
}

func (p *parser) parseSimpleKey() (decoded string, raw []byte, style StringStyle, err error) {
	tok := p.peek()
	switch tok.Type {
	case tokenBareKey:
		p.advance()
		return string(tok.Raw), tok.Raw, StringBasic, nil
	case tokenBasicString:
		p.advance()
		decoded, err = decodeBasicString(tok.Raw)
		if err != nil {
			return "", nil, 0, p.errorFrom(tok, err)
		}
		return decoded, tok.Raw, StringBasic, nil
	case tokenLiteralString:
		p.advance()
		decoded = decodeLiteralString(tok.Raw)
		return decoded, tok.Raw, StringLiteral, nil
	default:
		return "", nil, 0, p.errorf("expected a key, got %s", tok.Type.describe())
	}
}

// parseKeyPath parses a dotted key for table headers (without consuming =).
// Returns the decoded parts, raw parts, the source range of each part, the
// source position of the first key token, and any error.
func (p *parser) parseKeyPath(mark int) ([]string, keyFragments, []Span, Position, error) {
	var parts []string
	var frag keyFragments
	var partSpans []Span

	firstTok := p.peek()
	part, rawPart, _, err := p.parseSimpleKey()
	if err != nil {
		return nil, keyFragments{}, nil, Position{}, err
	}
	parts = append(parts, part)
	frag.buildPart(rawPart, p.rawFromTokenRange(mark, p.pos-1))
	partSpans = append(partSpans, spanFromToken(p.tokens[p.pos-1]))
	mark = p.pos

	for {
		p.skipWhitespace()
		if p.peekType() != tokenDot {
			break
		}
		p.advance() // consume dot
		p.skipWhitespace()

		part, rawPart, _, err = p.parseSimpleKey()
		if err != nil {
			return nil, keyFragments{}, nil, Position{}, err
		}
		parts = append(parts, part)
		frag.buildPart(rawPart, p.rawFromTokenRange(mark, p.pos-1))
		partSpans = append(partSpans, spanFromToken(p.tokens[p.pos-1]))
		mark = p.pos
	}

	frag.buildKeyEnd(p.rawFromTokenRange(mark, p.pos))
	return parts, frag, partSpans, tokenStart(firstTok), nil
}

// --- value parsing ---

func (p *parser) parseValue() (Node, error) {
	tok := p.peek()
	switch tok.Type {
	case tokenBasicString:
		return p.parseStringValue()
	case tokenLiteralString:
		return p.parseLiteralStringValue()
	case tokenMultiLineBasicString:
		return p.parseMultiLineBasicStringValue()
	case tokenMultiLineLiteralString:
		return p.parseMultiLineLiteralStringValue()
	case tokenInteger:
		return p.parseIntegerValue()
	case tokenFloat:
		return p.parseFloatValue()
	case tokenBoolean:
		return p.parseBooleanValue()
	case tokenOffsetDateTime:
		return p.parseDateTimeValue()
	case tokenLocalDateTime:
		return p.parseLocalDateTimeValue()
	case tokenLocalDate:
		return p.parseLocalDateValue()
	case tokenLocalTime:
		return p.parseLocalTimeValue()
	case tokenLeftBracket:
		return p.parseArrayValue()
	case tokenLeftBrace:
		return p.parseInlineTableValue()
	default:
		return nil, p.errorf("expected a value, got %s", tok.Type.describe())
	}
}

func (p *parser) parseStringValue() (Node, error) {
	tok := p.advance()
	decoded, err := decodeBasicString(tok.Raw)
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &StringNode{val: lexed(decoded, tok.Raw), style: StringBasic}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseLiteralStringValue() (Node, error) {
	tok := p.advance()
	decoded := decodeLiteralString(tok.Raw)
	n := &StringNode{val: lexed(decoded, tok.Raw), style: StringLiteral}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseMultiLineBasicStringValue() (Node, error) {
	tok := p.advance()
	decoded, err := decodeMultiLineBasicString(tok.Raw)
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &StringNode{val: lexed(decoded, tok.Raw), style: StringMultiLineBasic}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseMultiLineLiteralStringValue() (Node, error) {
	tok := p.advance()
	decoded := decodeMultiLineLiteralString(tok.Raw)
	n := &StringNode{val: lexed(decoded, tok.Raw), style: StringMultiLineLiteral}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseIntegerValue() (Node, error) {
	tok := p.advance()
	val, base, err := parseInteger(tok.Raw)
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &IntegerNode{val: lexed[int64](val, tok.Raw), base: base}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseFloatValue() (Node, error) {
	tok := p.advance()
	val, err := parseFloat(tok.Raw)
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &FloatNode{val: lexed(val, tok.Raw)}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseBooleanValue() (Node, error) {
	tok := p.advance()
	val := string(tok.Raw) == "true"
	n := &BooleanNode{val: lexed(val, tok.Raw)}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseDateTimeValue() (Node, error) {
	tok := p.advance()
	val, err := parseOffsetDateTime(string(tok.Raw))
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &DateTimeNode{val: lexed(val, tok.Raw)}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseLocalDateTimeValue() (Node, error) {
	tok := p.advance()
	val, err := parseLocalDateTime(string(tok.Raw))
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &LocalDateTimeNode{val: lexed(val, tok.Raw)}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseLocalDateValue() (Node, error) {
	tok := p.advance()
	val, err := parseLocalDate(string(tok.Raw))
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &LocalDateNode{val: lexed(val, tok.Raw)}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseLocalTimeValue() (Node, error) {
	tok := p.advance()
	val, err := parseLocalTime(string(tok.Raw))
	if err != nil {
		return nil, p.errorFrom(tok, err)
	}
	n := &LocalTimeNode{val: lexed(val, tok.Raw)}
	n.setSpan(spanFromToken(tok))
	return n, nil
}

func (p *parser) parseArrayValue() (Node, error) {
	startPos := p.pos
	openTok := p.advance() // consume [

	arr := &ArrayNode{}

	// gaps collects the bytes standing before, between and after the elements:
	// the brackets, the commas, the whitespace, the newlines and the interior
	// comments. mark is the first token they have not accounted for yet.
	var gaps [][]byte
	mark := startPos

	for {
		// Collect leading trivia (whitespace, newlines, comments) before the
		// next element or closing bracket.
		leadingWS, leadingComments := p.collectArrayTrivia()

		if p.peekType() == tokenRightBracket {
			// Comments collected here belong after the last element (or are the
			// only content in an empty array). Store them as trailing comments.
			arr.buildTrailingComments(leadingComments)
			closeTok := p.advance() // consume ]
			gaps = append(gaps, p.rawFromTokenRange(mark, p.pos))
			buildGaps(arr, gaps)
			arr.setRaw(p.rawFromTokenRange(startPos, p.pos))
			arr.setSpan(Span{Start: tokenStart(openTok), End: spanFromToken(closeTok).End})
			return arr, nil
		}

		gaps = append(gaps, p.rawFromTokenRange(mark, p.pos))

		elem, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		mark = p.pos

		// Attach leading trivia to this element.
		t := elem.trivia()
		t.LeadingWhitespace = leadingWS
		t.LeadingComments = leadingComments

		buildAppend(arr, elem)

		// After the value, there may be:
		//   value, # comment    (comma first, then inline comment)
		//   value # comment \n, (inline comment first, then comma on next line)
		//   value,              (just comma)
		//   value ]             (last element, no comma)
		//   value # comment ]   (last element with comment)
		//
		// First, try to find comma or inline comment or ] by skipping whitespace.
		p.skipArrayWhitespace()

		switch p.peekType() {
		case tokenComma:
			p.advance() // consume comma
			// Inline comment after the comma on the same line.
			_, inlineComment := p.collectArrayInlineTrivia()
			if len(inlineComment) > 0 {
				t.InlineComment = inlineComment
			}
			continue

		case tokenComment:
			// Inline comment without a preceding comma (e.g., `2#,9\n,`).
			_, inlineComment := p.collectArrayInlineTrivia()
			if len(inlineComment) > 0 {
				t.InlineComment = inlineComment
			}
			// After the comment, skip whitespace/newlines and look for comma or ].
			p.skipArrayTrivia()
			if p.peekType() == tokenComma {
				p.advance()
				continue
			}
			if p.peekType() == tokenRightBracket {
				continue // will be consumed at top of loop
			}
			return nil, p.errorf("expected ',' or ']' in array, got %s", p.peekType().describe())

		case tokenRightBracket:
			continue // will be consumed at top of loop

		default:
			return nil, p.errorf("expected ',' or ']' in array, got %s", p.peekType().describe())
		}
	}
}

func (p *parser) parseInlineTableValue() (Node, error) {
	startPos := p.pos
	openTok := p.advance() // consume {

	tbl := &InlineTableNode{}
	tracker := newDefinitionTracker(p.src)

	// gaps collects the braces, commas and whitespace standing around the
	// pairs; mark is the first token they have not accounted for yet. A pair
	// itself decomposes further, into its key, its separator and its value.
	var gaps [][]byte
	mark := startPos

	p.skipWhitespace()

	if p.peekType() == tokenRightBrace {
		closeTok := p.advance() // consume }
		buildGaps(tbl, [][]byte{p.rawFromTokenRange(mark, p.pos)})
		tbl.setRaw(p.rawFromTokenRange(startPos, p.pos))
		tbl.setSpan(Span{Start: tokenStart(openTok), End: spanFromToken(closeTok).End})
		return tbl, nil
	}

	for {
		p.skipWhitespace()
		gaps = append(gaps, p.rawFromTokenRange(mark, p.pos))

		keyNode, keyPos, sepStart, err := p.parseKey()
		if err != nil {
			return nil, err
		}

		p.skipWhitespace()

		_, err = p.expect(tokenEquals)
		if err != nil {
			return nil, err
		}

		p.skipWhitespace()
		sep := p.rawFromTokenRange(sepStart, p.pos)

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		mark = p.pos

		kv := newPair(keyNode, val)
		kv.buildSep(sep)
		kv.setSpan(Span{Start: keyNode.Span().Start, End: val.Span().End})

		// Check for duplicate keys within the inline table
		if err := tracker.defineKey(keyNode.parts, kv, keyPos); err != nil {
			return nil, err
		}

		// raw for inline table kv is set at inline table level
		buildAppend(tbl, kv)

		p.skipWhitespace()

		if p.peekType() == tokenComma {
			p.advance()
			continue
		}

		break
	}

	p.skipWhitespace()

	closeTok, err := p.expect(tokenRightBrace)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, p.rawFromTokenRange(mark, p.pos))
	buildGaps(tbl, gaps)

	tbl.setRaw(p.rawFromTokenRange(startPos, p.pos))
	tbl.setSpan(Span{Start: tokenStart(openTok), End: spanFromToken(closeTok).End})
	return tbl, nil
}

// --- helpers ---

func (p *parser) skipWhitespace() {
	for p.peekType() == tokenWhitespace {
		p.advance()
	}
}

func (p *parser) skipArrayTrivia() {
	for {
		tt := p.peekType()
		if tt == tokenWhitespace || tt == tokenNewline || tt == tokenComment {
			p.advance()
			continue
		}
		break
	}
}

// skipArrayWhitespace consumes whitespace and newlines (but NOT comments)
// inside an array.
func (p *parser) skipArrayWhitespace() {
	for {
		tt := p.peekType()
		if tt == tokenWhitespace || tt == tokenNewline {
			p.advance()
			continue
		}
		break
	}
}

// collectArrayTrivia collects whitespace, newlines, and comments before an
// array element or closing bracket. It returns the final leading whitespace
// (indentation before the element) and any full comment lines collected.
func (p *parser) collectArrayTrivia() (leadingWS []byte, leadingComments [][]byte) {
	var pendingWS []byte

	for {
		tt := p.peekType()
		switch tt {
		case tokenWhitespace:
			tok := p.advance()
			pendingWS = append(pendingWS, tok.Raw...)
			continue

		case tokenNewline:
			tok := p.advance()
			// Pending whitespace + newline is just a blank line; discard
			// the whitespace (it's not comment content).
			_ = tok
			pendingWS = nil
			continue

		case tokenComment:
			tok := p.advance()
			// Build a comment line: indentation + comment text
			commentLine := make([]byte, 0, len(pendingWS)+len(tok.Raw))
			commentLine = append(commentLine, pendingWS...)
			commentLine = append(commentLine, tok.Raw...)
			pendingWS = nil

			// Consume the newline after the comment if present.
			if p.peekType() == tokenNewline {
				nl := p.advance()
				commentLine = append(commentLine, nl.Raw...)
			}
			leadingComments = append(leadingComments, commentLine)
			continue

		default:
			// Not trivia. pendingWS is the leading whitespace (indentation)
			// for the coming element.
			leadingWS = pendingWS
			return
		}
	}
}

// collectArrayInlineTrivia collects optional whitespace and a comment on the
// same line after an array element value (before a comma or newline).
func (p *parser) collectArrayInlineTrivia() (ws []byte, comment []byte) {
	if p.peekType() == tokenWhitespace {
		tok := p.advance()
		ws = tok.Raw
	}
	if p.peekType() == tokenComment {
		tok := p.advance()
		comment = tok.Raw
	}
	return
}

func (p *parser) rawFromTokenRange(startIdx, endIdx int) []byte {
	if startIdx >= endIdx {
		return nil
	}
	var raw []byte
	for i := startIdx; i < endIdx && i < len(p.tokens); i++ {
		raw = append(raw, p.tokens[i].Raw...)
	}
	return raw
}

// --- string decoding ---

// decodeBasicString decodes a TOML basic string (including quotes).
func decodeBasicString(raw []byte) (string, error) {
	s := string(raw)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("invalid basic string: %q", s)
	}
	inner := s[1 : len(s)-1]
	return decodeBasicStringEscapes(inner)
}

func decodeBasicStringEscapes(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' {
			i++
			if i >= len(s) {
				return "", fmt.Errorf("trailing backslash in string")
			}
			switch s[i] {
			case 'b':
				b.WriteByte('\b')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case 'n':
				b.WriteByte('\n')
				i++
			case 'f':
				b.WriteByte('\f')
				i++
			case 'r':
				b.WriteByte('\r')
				i++
			case '"':
				b.WriteByte('"')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case 'u':
				i++
				if i+4 > len(s) {
					return "", fmt.Errorf("incomplete \\u escape")
				}
				r, err := strconv.ParseUint(s[i:i+4], 16, 32)
				if err != nil {
					return "", fmt.Errorf("invalid \\u escape: %s", s[i:i+4])
				}
				if r >= 0xD800 && r <= 0xDFFF {
					return "", fmt.Errorf("surrogate code point not allowed in unicode escape: U+%04X", r)
				}
				if r > 0x10FFFF {
					return "", fmt.Errorf("unicode code point out of range: U+%08X", r)
				}
				b.WriteRune(rune(r))
				i += 4
			case 'U':
				i++
				if i+8 > len(s) {
					return "", fmt.Errorf("incomplete \\U escape")
				}
				r, err := strconv.ParseUint(s[i:i+8], 16, 32)
				if err != nil {
					return "", fmt.Errorf("invalid \\U escape: %s", s[i:i+8])
				}
				if r >= 0xD800 && r <= 0xDFFF {
					return "", fmt.Errorf("surrogate code point not allowed in unicode escape: U+%04X", r)
				}
				if r > 0x10FFFF {
					return "", fmt.Errorf("unicode code point out of range: U+%08X", r)
				}
				b.WriteRune(rune(r))
				i += 8
			default:
				return "", fmt.Errorf("invalid escape: \\%c", s[i])
			}
		} else {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
		}
	}
	return b.String(), nil
}

func decodeLiteralString(raw []byte) string {
	s := string(raw)
	if len(s) < 2 {
		return ""
	}
	// strip quotes
	return s[1 : len(s)-1]
}

func decodeMultiLineBasicString(raw []byte) (string, error) {
	s := string(raw)
	if len(s) < 6 {
		return "", fmt.Errorf("invalid multi-line basic string")
	}
	// Strip """ from both ends. But closing may have up to 5 quotes total.
	inner := s[3:]
	// find the last """ and strip from there
	// The raw includes everything from """ to """
	// Count trailing quotes
	endQuotes := 0
	for i := len(inner) - 1; i >= 0 && inner[i] == '"'; i-- {
		endQuotes++
	}
	if endQuotes < 3 {
		return "", fmt.Errorf("invalid multi-line basic string: missing closing quotes")
	}
	// The extra quotes (beyond 3) are part of the content
	extra := endQuotes - 3
	inner = inner[:len(inner)-3] // remove closing """
	// But the 'extra' quotes at the end are content, which are already in inner
	_ = extra

	// Strip the first newline if present (TOML spec: newline immediately after opening """ is trimmed)
	if len(inner) > 0 && inner[0] == '\n' {
		inner = inner[1:]
	} else if len(inner) > 1 && inner[0] == '\r' && inner[1] == '\n' {
		inner = inner[2:]
	}

	// Process escape sequences and line-ending backslashes
	return decodeMultiLineBasicEscapes(inner)
}

func decodeMultiLineBasicEscapes(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' {
			i++
			if i >= len(s) {
				return "", fmt.Errorf("trailing backslash")
			}
			// Line-ending backslash: \ may be followed by optional
			// spaces/tabs before the newline (all trimmed per TOML spec).
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j < len(s) && (s[j] == '\n' || (s[j] == '\r' && j+1 < len(s) && s[j+1] == '\n')) {
				i = j
				// skip newline
				if s[i] == '\r' {
					i += 2
				} else {
					i++
				}
				// skip following whitespace and newlines
				for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || (s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n')) {
					if s[i] == '\r' {
						i += 2
					} else {
						i++
					}
				}
				continue
			}
			switch s[i] {
			case 'b':
				b.WriteByte('\b')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case 'n':
				b.WriteByte('\n')
				i++
			case 'f':
				b.WriteByte('\f')
				i++
			case 'r':
				b.WriteByte('\r')
				i++
			case '"':
				b.WriteByte('"')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case 'u':
				i++
				if i+4 > len(s) {
					return "", fmt.Errorf("incomplete \\u escape")
				}
				r, err := strconv.ParseUint(s[i:i+4], 16, 32)
				if err != nil {
					return "", fmt.Errorf("invalid \\u escape")
				}
				if r >= 0xD800 && r <= 0xDFFF {
					return "", fmt.Errorf("surrogate code point not allowed in unicode escape: U+%04X", r)
				}
				if r > 0x10FFFF {
					return "", fmt.Errorf("unicode code point out of range: U+%08X", r)
				}
				b.WriteRune(rune(r))
				i += 4
			case 'U':
				i++
				if i+8 > len(s) {
					return "", fmt.Errorf("incomplete \\U escape")
				}
				r, err := strconv.ParseUint(s[i:i+8], 16, 32)
				if err != nil {
					return "", fmt.Errorf("invalid \\U escape")
				}
				if r >= 0xD800 && r <= 0xDFFF {
					return "", fmt.Errorf("surrogate code point not allowed in unicode escape: U+%04X", r)
				}
				if r > 0x10FFFF {
					return "", fmt.Errorf("unicode code point out of range: U+%08X", r)
				}
				b.WriteRune(rune(r))
				i += 8
			default:
				return "", fmt.Errorf("invalid escape: \\%c", s[i])
			}
		} else {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
		}
	}
	return b.String(), nil
}

func decodeMultiLineLiteralString(raw []byte) string {
	s := string(raw)
	if len(s) < 6 {
		return ""
	}
	inner := s[3:]
	// Count trailing quotes
	endQuotes := 0
	for i := len(inner) - 1; i >= 0 && inner[i] == '\''; i-- {
		endQuotes++
	}
	if endQuotes < 3 {
		return ""
	}
	inner = inner[:len(inner)-3]

	// Strip the first newline if present
	if len(inner) > 0 && inner[0] == '\n' {
		inner = inner[1:]
	} else if len(inner) > 1 && inner[0] == '\r' && inner[1] == '\n' {
		inner = inner[2:]
	}

	return inner
}

// --- number parsing ---

func parseInteger(raw []byte) (int64, IntegerBase, error) {
	s := string(raw)
	// strip underscores
	s = strings.ReplaceAll(s, "_", "")

	if len(s) == 0 {
		return 0, IntegerDecimal, fmt.Errorf("empty integer")
	}

	// Check for sign
	sign := ""
	rest := s
	if s[0] == '+' || s[0] == '-' {
		sign = s[:1]
		rest = s[1:]
	}

	// Check for prefix
	if len(rest) > 2 {
		prefix := rest[:2]
		switch prefix {
		case "0x", "0X":
			v, err := strconv.ParseInt(sign+rest[2:], 16, 64)
			return v, IntegerHex, err
		case "0o", "0O":
			v, err := strconv.ParseInt(sign+rest[2:], 8, 64)
			return v, IntegerOctal, err
		case "0b", "0B":
			v, err := strconv.ParseInt(sign+rest[2:], 2, 64)
			return v, IntegerBinary, err
		}
	}

	v, err := strconv.ParseInt(s, 10, 64)
	return v, IntegerDecimal, err
}

func parseFloat(raw []byte) (float64, error) {
	s := string(raw)
	s = strings.ReplaceAll(s, "_", "")

	// Special values
	switch s {
	case "inf", "+inf":
		return math.Inf(1), nil
	case "-inf":
		return math.Inf(-1), nil
	case "nan", "+nan":
		return math.NaN(), nil
	case "-nan":
		return math.NaN(), nil
	}

	return strconv.ParseFloat(s, 64)
}

// --- date/time parsing ---

func parseOffsetDateTime(s string) (time.Time, error) {
	// TOML allows 'T', 't', or ' ' as separator, and 'z'/'Z' for UTC
	normalized := strings.Replace(s, "t", "T", 1)
	normalized = strings.Replace(normalized, " ", "T", 1)
	// Normalize lowercase 'z' to 'Z' for Go's time.Parse
	if strings.HasSuffix(normalized, "z") {
		normalized = normalized[:len(normalized)-1] + "Z"
	}
	// Validate timezone offset range (Go's time.Parse silently accepts invalid offsets)
	if err := validateDateTimeOffset(normalized); err != nil {
		return time.Time{}, fmt.Errorf("invalid offset date-time %q: %v", s, err)
	}

	// Try various formats
	layouts := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.999999999Z",
	}

	var lastErr error
	for _, layout := range layouts {
		t, err := time.Parse(layout, normalized)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("invalid offset date-time %q: %v", s, lastErr)
}

func parseLocalDateTime(s string) (LocalDateTime, error) {
	// normalize separator
	normalized := strings.Replace(s, "t", "T", 1)
	normalized = strings.Replace(normalized, " ", "T", 1)

	layouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.999999999",
	}

	for _, layout := range layouts {
		t, err := time.Parse(layout, normalized)
		if err == nil {
			return LocalDateTime{
				Year: t.Year(), Month: int(t.Month()), Day: t.Day(),
				Hour: t.Hour(), Minute: t.Minute(), Second: t.Second(),
				Nanosecond: t.Nanosecond(),
			}, nil
		}
	}
	return LocalDateTime{}, fmt.Errorf("invalid local date-time %q", s)
}

func parseLocalDate(s string) (LocalDate, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return LocalDate{}, fmt.Errorf("invalid local date %q: %v", s, err)
	}
	return LocalDate{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}, nil
}

func parseLocalTime(s string) (LocalTime, error) {
	layouts := []string{
		"15:04:05",
		"15:04:05.999999999",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return LocalTime{
				Hour: t.Hour(), Minute: t.Minute(), Second: t.Second(),
				Nanosecond: t.Nanosecond(),
			}, nil
		}
	}
	return LocalTime{}, fmt.Errorf("invalid local time %q", s)
}

// validateDateTimeOffset checks that a timezone offset (e.g. +12:30) has valid
// hour and minute values. Go's time.Parse silently accepts invalid offsets
// like +12:60.
func validateDateTimeOffset(s string) error {
	// Find the offset part: look for +/- after the time portion
	// The format is YYYY-MM-DDTHH:MM:SS[.frac]+HH:MM or Z
	// Find last + or - that's part of the offset (not sign of year or exponent)
	idx := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '+' || s[i] == '-' {
			idx = i
			break
		}
		if s[i] == 'Z' {
			return nil // Z offset is always valid
		}
	}
	if idx == -1 {
		return nil // no offset found
	}
	// Verify it looks like an offset (has :)
	rest := s[idx+1:]
	if len(rest) != 5 || rest[2] != ':' {
		return nil // not a standard offset format
	}
	hours, err := strconv.Atoi(rest[0:2])
	if err != nil {
		return fmt.Errorf("invalid offset hours: %s", rest[0:2])
	}
	minutes, err := strconv.Atoi(rest[3:5])
	if err != nil {
		return fmt.Errorf("invalid offset minutes: %s", rest[3:5])
	}
	if hours > 23 {
		return fmt.Errorf("offset hours out of range: %d", hours)
	}
	if minutes > 59 {
		return fmt.Errorf("offset minutes out of range: %d", minutes)
	}
	return nil
}

// --- definition tracking (duplicate/conflict detection) ---

// tableEntry tracks what has been defined at a given key path.
type tableEntry struct {
	kind entryKind
	node Node
	sub  map[string]*tableEntry
}

type entryKind int

const (
	entryImplicit       entryKind = iota // implicitly created by sub-table header path
	entryDottedImplicit                  // implicitly created by dotted key (cannot be reopened)
	entryExplicit                        // explicitly defined via [table]
	entryValue                           // defined as a key = value
	entryArrayTable                      // defined via [[array-table]]
)

type definitionTracker struct {
	root *tableEntry
	src  []byte // the document source, for the snippets of its diagnostics
}

func newDefinitionTracker(src []byte) *definitionTracker {
	return &definitionTracker{
		root: &tableEntry{kind: entryImplicit, sub: make(map[string]*tableEntry)},
		src:  src,
	}
}

// ensurePath creates implicit table entries along the path, returning the
// final entry. When fromDottedKey is true, new entries are marked as
// entryDottedImplicit (cannot be reopened by a [table] header).
// pos is the source position of the key being defined, used for error
// reporting.
func (dt *definitionTracker) ensurePath(path []string, fromDottedKey bool, pos Position) (*tableEntry, error) {
	cur := dt.root
	for _, key := range path {
		if cur.sub == nil {
			cur.sub = make(map[string]*tableEntry)
		}
		child, ok := cur.sub[key]
		if !ok {
			kind := entryImplicit
			if fromDottedKey {
				kind = entryDottedImplicit
			}
			child = &tableEntry{kind: kind, sub: make(map[string]*tableEntry)}
			cur.sub[key] = child
			cur = child
			continue
		}
		if child.kind == entryValue {
			return nil, syntaxErrorAt(dt.src, pos, "key %q is already defined as a value", key)
		}
		// Dotted keys cannot traverse through explicitly defined tables
		// or array tables (you can't add to a table that was already
		// defined with a [table] or [[array-table]] header)
		if fromDottedKey && (child.kind == entryExplicit || child.kind == entryArrayTable) {
			return nil, syntaxErrorAt(dt.src, pos, "cannot extend %q via dotted key (already explicitly defined)", key)
		}
		cur = child
	}
	return cur, nil
}

func (dt *definitionTracker) defineTable(path []string, node Node, pos Position) error {
	if len(path) == 0 {
		return nil
	}
	parent, err := dt.ensurePath(path[:len(path)-1], false, pos)
	if err != nil {
		return err
	}
	key := path[len(path)-1]
	if parent.sub == nil {
		parent.sub = make(map[string]*tableEntry)
	}
	existing, ok := parent.sub[key]
	if ok {
		if existing.kind == entryExplicit {
			return syntaxErrorAt(dt.src, pos, "table [%s] already defined", pathFromKeys(path))
		}
		if existing.kind == entryValue {
			return syntaxErrorAt(dt.src, pos, "key %q is already defined as a value", key)
		}
		if existing.kind == entryArrayTable {
			return syntaxErrorAt(dt.src, pos, "cannot define [%s] as a table, already defined as array table", pathFromKeys(path))
		}
		if existing.kind == entryDottedImplicit {
			return syntaxErrorAt(dt.src, pos, "cannot define [%s] as a table, already implicitly defined via dotted key", pathFromKeys(path))
		}
		// Was implicit (from sub-table path), promote to explicit
		existing.kind = entryExplicit
		existing.node = node
		return nil
	}
	parent.sub[key] = &tableEntry{kind: entryExplicit, node: node, sub: make(map[string]*tableEntry)}
	return nil
}

func (dt *definitionTracker) defineArrayTable(path []string, node Node, pos Position) error {
	if len(path) == 0 {
		return nil
	}
	parent, err := dt.ensurePath(path[:len(path)-1], false, pos)
	if err != nil {
		return err
	}
	key := path[len(path)-1]
	if parent.sub == nil {
		parent.sub = make(map[string]*tableEntry)
	}
	existing, ok := parent.sub[key]
	if ok {
		if existing.kind == entryExplicit {
			return syntaxErrorAt(dt.src, pos, "cannot define [[%s]] as array table, already defined as table", pathFromKeys(path))
		}
		if existing.kind == entryValue {
			return syntaxErrorAt(dt.src, pos, "key %q is already defined as a value", key)
		}
		if existing.kind == entryArrayTable {
			// Multiple [[array-table]] entries are fine -- reset children for the new element
			existing.node = node
			existing.sub = make(map[string]*tableEntry)
			return nil
		}
		if existing.kind == entryDottedImplicit {
			return syntaxErrorAt(dt.src, pos, "cannot define [[%s]] as array table, already implicitly defined via dotted key", pathFromKeys(path))
		}
		// Was implicit (from sub-table path). Can only promote if no
		// sub-tables were already defined under it (which would conflict
		// with array-table semantics that reset children per element).
		if len(existing.sub) > 0 {
			return syntaxErrorAt(dt.src, pos, "cannot define [[%s]] as array table, already used as table with sub-entries", pathFromKeys(path))
		}
		existing.kind = entryArrayTable
		existing.node = node
		existing.sub = make(map[string]*tableEntry)
		return nil
	}
	parent.sub[key] = &tableEntry{kind: entryArrayTable, node: node, sub: make(map[string]*tableEntry)}
	return nil
}

func (dt *definitionTracker) defineKey(path []string, node Node, pos Position) error {
	if len(path) == 0 {
		return nil
	}
	// Navigate/create intermediate entries. Dotted keys create
	// entryDottedImplicit entries that cannot be reopened by [table] headers.
	parent, err := dt.ensurePath(path[:len(path)-1], true, pos)
	if err != nil {
		return err
	}
	key := path[len(path)-1]
	if parent.sub == nil {
		parent.sub = make(map[string]*tableEntry)
	}
	existing, ok := parent.sub[key]
	if ok {
		if existing.kind == entryValue {
			return syntaxErrorAt(dt.src, pos, "duplicate key %s", pathFromKeys(path))
		}
		if existing.kind == entryExplicit || existing.kind == entryArrayTable {
			return syntaxErrorAt(dt.src, pos, "key %s conflicts with table definition", pathFromKeys(path))
		}
		// Implicit: promote to value only if it has no children (sub-tables)
		if len(existing.sub) > 0 {
			return syntaxErrorAt(dt.src, pos, "key %s is already defined as a table", pathFromKeys(path))
		}
		existing.kind = entryValue
		existing.node = node
		return nil
	}
	parent.sub[key] = &tableEntry{kind: entryValue, node: node}
	return nil
}

// tableScope returns a tracker that scopes key definitions to the given table path.
func (dt *definitionTracker) tableScope(path []string) *definitionTracker {
	cur := dt.root
	for _, key := range path {
		if cur.sub == nil {
			cur.sub = make(map[string]*tableEntry)
		}
		child, ok := cur.sub[key]
		if !ok {
			child = &tableEntry{kind: entryImplicit, sub: make(map[string]*tableEntry)}
			cur.sub[key] = child
		}
		cur = child
	}
	return &definitionTracker{root: cur, src: dt.src}
}

// arrayTableScope returns a tracker scoped to the latest element of the array table.
func (dt *definitionTracker) arrayTableScope(path []string) *definitionTracker {
	// For array tables, each occurrence gets fresh children, so the existing
	// entry's children map is already reset in defineArrayTable.
	return dt.tableScope(path)
}
