package tomledit

// The typed setters write arrays and inline tables from statically typed Go
// values. Set and SetCreate take any, and the array and inline-table values
// they accept are []any, map[string]any, and []Pair; a caller whose rules
// forbid it to build such a value -- one checked by strictgo, which refuses the
// empty interface as a value type -- writes the same values through these
// methods instead.
//
// Each setter converts its typed value and hands it to Set or SetCreate, so it
// refuses exactly what they refuse, with the same error kinds, is a no-op on
// exactly the same terms (see Set's equality rule), and creates intermediate
// tables only in its SetCreate form.

// SetStrings writes an array of strings at the path, as Set does.
func (d *Document) SetStrings(path string, values []string) error {
	return d.Set(path, stringsValue(values))
}

// SetCreateStrings writes an array of strings at the path, as SetCreate does.
func (d *Document) SetCreateStrings(path string, values []string) error {
	return d.SetCreate(path, stringsValue(values))
}

// SetInts writes an array of integers at the path, as Set does.
func (d *Document) SetInts(path string, values []int64) error {
	return d.Set(path, intsValue(values))
}

// SetCreateInts writes an array of integers at the path, as SetCreate does.
func (d *Document) SetCreateInts(path string, values []int64) error {
	return d.SetCreate(path, intsValue(values))
}

// SetFloats writes an array of floats at the path, as Set does. A NaN whose
// sign bit is set is refused with KindBadInput, as Set refuses one.
func (d *Document) SetFloats(path string, values []float64) error {
	return d.Set(path, floatsValue(values))
}

// SetCreateFloats writes an array of floats at the path, as SetCreate does.
func (d *Document) SetCreateFloats(path string, values []float64) error {
	return d.SetCreate(path, floatsValue(values))
}

// SetBools writes an array of booleans at the path, as Set does.
func (d *Document) SetBools(path string, values []bool) error {
	return d.Set(path, boolsValue(values))
}

// SetCreateBools writes an array of booleans at the path, as SetCreate does.
func (d *Document) SetCreateBools(path string, values []bool) error {
	return d.SetCreate(path, boolsValue(values))
}

// SetInlineTable writes the inline table at the path, as Set does, with its
// keys in the order they were added.
func (d *Document) SetInlineTable(path string, table InlineTable) error {
	return d.Set(path, table.pairsCopy())
}

// SetCreateInlineTable writes the inline table at the path, as SetCreate does,
// with its keys in the order they were added.
func (d *Document) SetCreateInlineTable(path string, table InlineTable) error {
	return d.SetCreate(path, table.pairsCopy())
}

// InlineTable is an ordered inline table built from typed values, the input of
// SetInlineTable and SetCreateInlineTable. The zero value is an empty table.
// Each method adds one key and returns the table, so additions chain.
//
// A key is a SINGLE key, taken verbatim -- never a path, as a Pair's Key is
// not. A duplicate key and a key or string that is not valid UTF-8 are not
// refused when added: the write that takes the table refuses them with
// KindBadInput, and writes nothing.
type InlineTable struct {
	pairs []Pair
}

func (t *InlineTable) add(key string, value any) *InlineTable {
	t.pairs = append(t.pairs, Pair{Key: key, Value: value})
	return t
}

// String adds a string value under the key.
func (t *InlineTable) String(key, value string) *InlineTable { return t.add(key, value) }

// Int adds an integer value under the key.
func (t *InlineTable) Int(key string, value int64) *InlineTable { return t.add(key, value) }

// Float adds a float value under the key.
func (t *InlineTable) Float(key string, value float64) *InlineTable { return t.add(key, value) }

// Bool adds a boolean value under the key.
func (t *InlineTable) Bool(key string, value bool) *InlineTable { return t.add(key, value) }

// Strings adds an array of strings under the key.
func (t *InlineTable) Strings(key string, values []string) *InlineTable {
	return t.add(key, stringsValue(values))
}

// Ints adds an array of integers under the key.
func (t *InlineTable) Ints(key string, values []int64) *InlineTable {
	return t.add(key, intsValue(values))
}

// Floats adds an array of floats under the key.
func (t *InlineTable) Floats(key string, values []float64) *InlineTable {
	return t.add(key, floatsValue(values))
}

// Bools adds an array of booleans under the key.
func (t *InlineTable) Bools(key string, values []bool) *InlineTable {
	return t.add(key, boolsValue(values))
}

// Table adds a nested inline table under the key. The table is copied: keys
// added to it afterwards do not reach this one.
func (t *InlineTable) Table(key string, table InlineTable) *InlineTable {
	return t.add(key, table.pairsCopy())
}

// pairsCopy returns the table's pairs in a slice of their own, never nil, so
// an empty table is written as an empty inline table.
func (t InlineTable) pairsCopy() []Pair {
	out := make([]Pair, len(t.pairs))
	copy(out, t.pairs)
	return out
}

// The conversions below build the []any Set converts, never nil, so an empty
// slice is written as an empty array.

func stringsValue(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func intsValue(values []int64) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func floatsValue(values []float64) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func boolsValue(values []bool) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
