package tomledit

import (
	"errors"
	"testing"
	"time"
)

// The accessor families read the conversion table, so the getters answer two
// widening rows: an integer a float target holds exactly, and the local
// date-time flavors a time.Time target accepts.

// Fails if the float accessors stop accepting an integer the target holds
// exactly -- the conversion table's integer-into-float row.
func TestAccessorWidening_ExactIntegerReadsAsFloat(t *testing.T) {
	doc, err := Parse([]byte("val = 42\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	got, err := doc.GetFloat("val")
	if err != nil {
		t.Errorf("GetFloat on an exactly representable integer refused it")
	}
	if got != 42 {
		t.Errorf("GetFloat read %v, want 42", got)
	}

	cur, err := doc.Key("val").Float()
	if err != nil {
		t.Errorf("Cursor.Float on an exactly representable integer refused it")
	}
	if cur != 42 {
		t.Errorf("Cursor.Float read %v, want 42", cur)
	}
}

// Fails if the time accessors stop following the conversion table's time.Time
// rows: a local date-time and a local date convert, because the declared
// target expresses the intent.
func TestAccessorWidening_LocalFlavorsReadAsTime(t *testing.T) {
	doc, err := Parse([]byte("dt = 1979-05-27T07:32:00\nd = 1979-05-27\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	got, err := doc.GetTime("dt")
	if err != nil {
		t.Errorf("GetTime on a local date-time refused it")
	}
	if want := time.Date(1979, 5, 27, 7, 32, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("GetTime read %v, want %v", got, want)
	}

	got, err = doc.GetTime("d")
	if err != nil {
		t.Errorf("GetTime on a local date refused it")
	}
	if want := time.Date(1979, 5, 27, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("GetTime read %v, want %v", got, want)
	}
}

// --- the three stages, at the three surfaces ---
//
// A typed read runs navigate, then type-check, then convert, and each stage
// owns its diagnostic kinds. The tests below pin one refusal per stage at every
// surface that has the stage: the path-level getters and the Cursor terminals
// run all three, and the node-level As* family has no navigate stage, because
// the caller already holds the node.

// stagesDoc is the fixture the stage tests read: a scalar, a header table, an
// array-of-tables (a position no single node stands for), and a table implied
// by a longer header (another such position).
const stagesDoc = `title = "doc"

[server]
host = "localhost"
port = 8080

[[products]]
name = "widget"

[deep.nested]
x = 1
`

// Fails if the navigate stage stops reporting its own kinds: a path that does
// not parse is KindBadPath, one naming nothing is KindNotFound, and a step that
// does not apply to what it addresses -- including a position no single node
// stands for -- is KindWrongContainer.
func TestAccessorStage_Navigate(t *testing.T) {
	cases := []struct {
		name string
		path string
		// cursor spells the same navigation for the Cursor surface, or is nil
		// where the surface cannot express it.
		cursor func(*Document) *Cursor
		want   error
	}{
		{
			name: "a path that does not parse",
			path: "server.[",
			// The Cursor takes key names rather than path text, so it has no
			// path to fail to parse.
			want: ErrBadPath,
		},
		{
			name:   "a key the table does not carry",
			path:   "server.missing",
			cursor: func(d *Document) *Cursor { return d.Key("server").Key("missing") },
			want:   ErrNotFound,
		},
		{
			name:   "a key step into a scalar",
			path:   "server.host.deeper",
			cursor: func(d *Document) *Cursor { return d.Key("server").Key("host").Key("deeper") },
			want:   ErrWrongContainer,
		},
		{
			name:   "an index into a table",
			path:   "server[0]",
			cursor: func(d *Document) *Cursor { return d.Key("server").At(0) },
			want:   ErrWrongContainer,
		},
		{
			name:   "an array-of-tables, which no single node stands for",
			path:   "products",
			cursor: func(d *Document) *Cursor { return d.Key("products") },
			want:   ErrWrongContainer,
		},
		{
			name:   "a table implied by a longer header",
			path:   "deep",
			cursor: func(d *Document) *Cursor { return d.Key("deep") },
			want:   ErrWrongContainer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(stagesDoc))
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			if _, err := doc.GetString(tc.path); !errors.Is(err, tc.want) {
				t.Errorf("GetString(%q) = %v, want %v", tc.path, err, tc.want)
			}
			if tc.cursor == nil {
				return
			}
			cur := tc.cursor(doc)
			if _, err := cur.String(); !errors.Is(err, tc.want) {
				t.Errorf("the Cursor terminal reported %v, want %v", err, tc.want)
			}
			// A navigation-terminated chain reports the same failure through
			// Err, which is what a caller who never reaches a terminal asks.
			if err := cur.Err(); !errors.Is(err, tc.want) {
				t.Errorf("Err() reported %v, want %v", err, tc.want)
			}
		})
	}
}

// Fails if the type-check stage stops reporting KindTypeMismatch, or stops
// naming both sides of the mismatch, at any of the three surfaces.
func TestAccessorStage_TypeCheck(t *testing.T) {
	doc, err := Parse([]byte(stagesDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	node, err := doc.Resolve("server.host")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	check := func(surface string, err error) {
		t.Helper()
		if !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("%s reported %v, want %v", surface, err, ErrTypeMismatch)
			return
		}
		var diag *Error
		if !errors.As(err, &diag) {
			t.Fatalf("%s reported %v (%T), want a *Error", surface, err, err)
		}
		if diag.Expected == "" || diag.Got == "" {
			t.Errorf("%s reported expected=%q got=%q, want both named", surface, diag.Expected, diag.Got)
		}
		if !diag.Span.IsValid() {
			t.Errorf("%s reported the zero span, want the value's own range", surface)
		}
	}

	_, perr := doc.GetInt("server.host")
	check("the path-level getter", perr)
	if diag := (*Error)(nil); errors.As(perr, &diag) && diag.Path != "server.host" {
		t.Errorf("the path-level getter reported path %q, want %q", diag.Path, "server.host")
	}

	_, nerr := node.(Scalar).AsInt()
	check("the node-level accessor", nerr)

	_, cerr := doc.Key("server").Key("host").Int()
	check("the Cursor terminal", cerr)
}

// Fails if the convert stage stops reporting KindInexact with the offending
// value at any of the three surfaces: an integer no float64 holds exactly
// passes the type check, because the table accepts an integer for a float
// target, and is refused by the value.
func TestAccessorStage_Convert(t *testing.T) {
	doc, err := Parse([]byte("big = 9007199254740993\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	node, err := doc.Resolve("big")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	check := func(surface string, err error) {
		t.Helper()
		if !errors.Is(err, ErrInexact) {
			t.Errorf("%s reported %v, want %v", surface, err, ErrInexact)
			return
		}
		var diag *Error
		if !errors.As(err, &diag) {
			t.Fatalf("%s reported %v (%T), want a *Error", surface, err, err)
		}
		if diag.Value != int64(9007199254740993) {
			t.Errorf("%s carried value %#v, want the offending integer", surface, diag.Value)
		}
	}

	_, perr := doc.GetFloat("big")
	check("the path-level getter", perr)

	_, nerr := node.(Scalar).AsFloat()
	check("the node-level accessor", nerr)

	_, cerr := doc.Key("big").Float()
	check("the Cursor terminal", cerr)
}

// Fails if the node-level family stops following the two widened rows the path
// level and the Cursor already pin: an integer a float64 holds exactly, and the
// local date-time flavors a time.Time accepts.
func TestAccessorWidening_NodeLevel(t *testing.T) {
	doc, err := Parse([]byte("n = 42\ndt = 1979-05-27T07:32:00\nd = 1979-05-27\nlt = 07:32:00\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	scalar := func(path string) Scalar {
		t.Helper()
		node, err := doc.Resolve(path)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", path, err)
		}
		s, ok := node.(Scalar)
		if !ok {
			t.Fatalf("%q is %T, want a Scalar", path, node)
		}
		return s
	}

	if got, err := scalar("n").AsFloat(); err != nil || got != 42 {
		t.Errorf("AsFloat on an exactly representable integer = (%v, %v), want 42", got, err)
	}
	if got, err := scalar("dt").AsTime(); err != nil || !got.Equal(time.Date(1979, 5, 27, 7, 32, 0, 0, time.UTC)) {
		t.Errorf("AsTime on a local date-time = (%v, %v)", got, err)
	}
	if got, err := scalar("d").AsTime(); err != nil || !got.Equal(time.Date(1979, 5, 27, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("AsTime on a local date = (%v, %v)", got, err)
	}
	// A local time carries no date, so the table does not list it for a
	// time.Time target.
	if _, err := scalar("lt").AsTime(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsTime on a local time = %v, want %v", err, ErrTypeMismatch)
	}
}

// --- the accessor families against the conversion table ---

// accessorFamily is one target type of the accessor family, read through each
// surface that offers it. The Go type is the same one a decode target of that
// type would be, which is what lets the test below compare the two without
// restating a single row of the conversion table.
type accessorFamily struct {
	name string
	// decode reads a node through the decode front end as this family's Go
	// type: what it answers is what every accessor surface is compared against.
	decode decodeSide
	// node reads a scalar node; nil is never returned, the surface always
	// offers every target.
	node func(Scalar) (any, error)
	// path and cursor are nil for the three Local targets, which the record
	// gives to the node level only.
	path   func(*Document, string) (any, error)
	cursor func(*Cursor) (any, error)
}

// decodeSide is the decode front end for one Go target type.
type decodeSide struct {
	read func(Node) (any, error)
}

// decodeAs builds the decode side for the target type T.
func decodeAs[T any]() decodeSide {
	return decodeSide{
		read: func(n Node) (any, error) {
			v, err := DecodeNode[T](n)
			if err != nil {
				return nil, err
			}
			return *v, nil
		},
	}
}

func accessorFamilies() []accessorFamily {
	return []accessorFamily{
		{
			name:   "string",
			decode: decodeAs[string](),
			node:   func(s Scalar) (any, error) { return s.AsString() },
			path:   func(d *Document, p string) (any, error) { return d.GetString(p) },
			cursor: func(c *Cursor) (any, error) { return c.String() },
		},
		{
			name:   "int64",
			decode: decodeAs[int64](),
			node:   func(s Scalar) (any, error) { return s.AsInt() },
			path:   func(d *Document, p string) (any, error) { return d.GetInt(p) },
			cursor: func(c *Cursor) (any, error) { return c.Int() },
		},
		{
			name:   "float64",
			decode: decodeAs[float64](),
			node:   func(s Scalar) (any, error) { return s.AsFloat() },
			path:   func(d *Document, p string) (any, error) { return d.GetFloat(p) },
			cursor: func(c *Cursor) (any, error) { return c.Float() },
		},
		{
			name:   "bool",
			decode: decodeAs[bool](),
			node:   func(s Scalar) (any, error) { return s.AsBool() },
			path:   func(d *Document, p string) (any, error) { return d.GetBool(p) },
			cursor: func(c *Cursor) (any, error) { return c.Bool() },
		},
		{
			name:   "time.Time",
			decode: decodeAs[time.Time](),
			node:   func(s Scalar) (any, error) { return s.AsTime() },
			path:   func(d *Document, p string) (any, error) { return d.GetTime(p) },
			cursor: func(c *Cursor) (any, error) { return c.Time() },
		},
		{
			name:   "LocalDateTime",
			decode: decodeAs[LocalDateTime](),
			node:   func(s Scalar) (any, error) { return s.AsLocalDateTime() },
		},
		{
			name:   "LocalDate",
			decode: decodeAs[LocalDate](),
			node:   func(s Scalar) (any, error) { return s.AsLocalDate() },
		},
		{
			name:   "LocalTime",
			decode: decodeAs[LocalTime](),
			node:   func(s Scalar) (any, error) { return s.AsLocalTime() },
		},
	}
}

// Fails if an accessor family stops answering what a decode target of the same
// Go type answers, for the same document value. The comparison is the whole
// assertion: the decode targets are already pinned to the conversion table by
// TestConversionTable_BothConsumers, so agreeing with them is agreeing with the
// table -- and no row of it is restated here.
//
// Nothing is excluded. Both surfaces read the one conversion table and no
// target decodes itself, so every pairing agrees -- a string read as a
// time.Time is a type mismatch on either surface.
func TestConversionTable_AccessorFamilies(t *testing.T) {
	for _, tc := range conversionCases() {
		for _, family := range accessorFamilies() {
			t.Run(tc.name+" as "+family.name, func(t *testing.T) {
				doc, err := Parse([]byte("val = " + tc.toml + "\n"))
				if err != nil {
					t.Fatalf("parse error: %v", err)
				}
				node, err := doc.Resolve("val")
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}

				want, wantErr := family.decode.read(node)
				var wantKind ErrorKind
				if wantErr != nil {
					wantKind = diagnosticsOf(t, wantErr)[0].Kind
				}

				agree := func(surface string, read func() (any, error)) {
					t.Helper()
					got, err := read()
					switch {
					case wantErr != nil && err == nil:
						t.Errorf("%s read %#v where a %s decode target reported %s",
							surface, got, family.name, wantKind)
					case wantErr != nil:
						if kind := diagnosticsOf(t, err)[0].Kind; kind != wantKind {
							t.Errorf("%s reported %s where a %s decode target reported %s",
								surface, kind, family.name, wantKind)
						}
					case err != nil:
						t.Errorf("%s refused a value a %s decode target accepted: %v",
							surface, family.name, err)
					case !sameDecoded(got, want):
						t.Errorf("%s read %#v, want %#v", surface, got, want)
					}
				}

				if scalar, ok := node.(Scalar); ok {
					agree("the node-level accessor", func() (any, error) { return family.node(scalar) })
				}
				if family.path != nil {
					agree("the path-level getter", func() (any, error) { return family.path(doc, "val") })
					agree("the Cursor terminal", func() (any, error) { return family.cursor(doc.Key("val")) })
				}
			})
		}
	}
}

// --- the comma-ok existence surface's allocation budget ---

// Fails if Lookup or Has starts allocating beyond the path parse. They are the
// existence surface a caller polls, so their cost is a contract: parsing the
// path text is the only allocation either is allowed, and walking a warm
// read-layer to the node adds none.
//
// The budget is measured rather than written down: whatever ParsePath allocates
// for this path is what Lookup and Has may allocate for it.
func TestLookupAndHasAllocateOnlyTheirPath(t *testing.T) {
	doc, err := Parse([]byte(stagesDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	const path = "server.host"
	// Warm the read-layer: the first read folds the document, and the fold is
	// what the cache exists to avoid repeating.
	if !doc.Has(path) {
		t.Fatalf("Has(%q) = false, want true", path)
	}

	budget := testing.AllocsPerRun(100, func() {
		if _, err := ParsePath(path); err != nil {
			t.Fatalf("ParsePath: %v", err)
		}
	})

	lookup := testing.AllocsPerRun(100, func() {
		if _, ok := doc.Lookup(path); !ok {
			t.Fatalf("Lookup(%q) reported false", path)
		}
	})
	if lookup > budget {
		t.Errorf("Lookup allocates %.0f times, want no more than the %.0f of its path parse", lookup, budget)
	}

	has := testing.AllocsPerRun(100, func() {
		if !doc.Has(path) {
			t.Fatalf("Has(%q) = false", path)
		}
	})
	if has > budget {
		t.Errorf("Has allocates %.0f times, want no more than the %.0f of its path parse", has, budget)
	}
}
