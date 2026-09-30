package tomledit_test

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	tomledit "github.com/stricttools/go-toml-edit"
)

func ExampleParse() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
port = 8080
`))
	if err != nil {
		panic(err)
	}
	host, err := doc.GetString("server.host")
	if err != nil {
		panic(err)
	}
	port, err := doc.GetInt("server.port")
	if err != nil {
		panic(err)
	}
	fmt.Println(host)
	fmt.Println(port)
	// Output:
	// localhost
	// 8080
}

func ExampleDocument_Set() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
port = 8080
`))
	if err != nil {
		panic(err)
	}
	err = doc.Set("server.port", 9090)
	if err != nil {
		panic(err)
	}
	port, err := doc.GetInt("server.port")
	if err != nil {
		panic(err)
	}
	fmt.Println(port)
	// Output:
	// 9090
}

func ExampleDocument_SetCreate() {
	doc, err := tomledit.Parse([]byte(`title = "example"
`))
	if err != nil {
		panic(err)
	}
	// SetCreate auto-creates intermediate [database] table.
	err = doc.SetCreate("database.host", "db.example.com")
	if err != nil {
		panic(err)
	}
	host, err := doc.GetString("database.host")
	if err != nil {
		panic(err)
	}
	fmt.Println(host)
	// Output:
	// db.example.com
}

func ExampleDocument_Delete() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
port = 8080
debug = true
`))
	if err != nil {
		panic(err)
	}
	err = doc.Delete("server.debug")
	if err != nil {
		panic(err)
	}
	// The key is gone, so reading it is a not-found diagnostic.
	_, err = doc.GetBool("server.debug")
	fmt.Println(errors.Is(err, tomledit.ErrNotFound))
	// Output:
	// true
}

func ExampleDocument_Format() {
	doc, err := tomledit.Parse([]byte(`name="Alice"
[server]
host="localhost"
port=8080
`))
	if err != nil {
		panic(err)
	}
	formatted := doc.Format(tomledit.WithIndentWidth(2))
	fmt.Print(string(formatted))
	// Output:
	// name = "Alice"
	//
	// [server]
	//   host = "localhost"
	//   port = 8080
}

func ExampleDocument_Key() {
	doc, err := tomledit.Parse([]byte(`[database]
host = "localhost"
port = 5432
`))
	if err != nil {
		panic(err)
	}
	host, err := doc.Key("database").Key("host").String()
	if err != nil {
		panic(err)
	}
	fmt.Println(host)

	port, err := doc.Key("database").Key("port").Int()
	if err != nil {
		panic(err)
	}
	fmt.Println(port)
	// Output:
	// localhost
	// 5432
}

func ExampleUnmarshal() {
	type Config struct {
		Title  string `toml:"title"`
		Server struct {
			Host string `toml:"host"`
			Port int    `toml:"port"`
		} `toml:"server"`
	}
	cfg, err := tomledit.Unmarshal[Config]([]byte(`title = "My App"

[server]
host = "0.0.0.0"
port = 443
`))
	if err != nil {
		panic(err)
	}
	fmt.Println(cfg.Title)
	fmt.Println(cfg.Server.Host)
	fmt.Println(cfg.Server.Port)
	// Output:
	// My App
	// 0.0.0.0
	// 443
}

func ExampleDocument_Walk() {
	doc, err := tomledit.Parse([]byte(`name = "example"

[server]
host = "localhost"
port = 8080
`))
	if err != nil {
		panic(err)
	}
	err = doc.Walk(func(path string, node tomledit.Node) error {
		fmt.Printf("%s = %v\n", path, node.(tomledit.Scalar).Value())
		return nil
	}, tomledit.WalkLeaves)
	if err != nil {
		panic(err)
	}
	// Output:
	// name = example
	// server.host = localhost
	// server.port = 8080
}

func ExampleDiff() {
	a, _ := tomledit.Parse([]byte(`name = "Alice"
age = 30
`))
	b, _ := tomledit.Parse([]byte(`name = "Bob"
email = "bob@example.com"
`))
	changes := tomledit.Diff(a, b)
	for _, c := range changes {
		fmt.Printf("%s: %s\n", c.Kind, c.Path)
	}
	// Output:
	// removed: age
	// added: email
	// modified: name
}

func ExampleDocument_Merge() {
	base, _ := tomledit.Parse([]byte(`[server]
host = "localhost"
`))
	defaults, _ := tomledit.Parse([]byte(`[server]
host = "0.0.0.0"
port = 8080
`))
	err := base.Merge(defaults)
	if err != nil {
		panic(err)
	}
	// host was already set, so it keeps "localhost".
	host, err := base.GetString("server.host")
	if err != nil {
		panic(err)
	}
	// port was missing, so it was added from defaults.
	port, err := base.GetInt("server.port")
	if err != nil {
		panic(err)
	}
	fmt.Println(host)
	fmt.Println(port)
	// Output:
	// localhost
	// 8080
}

func ExampleParseFile() {
	dir, err := os.MkdirTemp("", "tomledit-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[server]\nport = \"8080\"\n"), 0o644); err != nil {
		panic(err)
	}

	doc, err := tomledit.ParseFile(path)
	if err != nil {
		panic(err)
	}
	// The document remembers where it was read from, so a later diagnostic
	// names the file, the line and the column.
	_, err = doc.GetInt("server.port")
	var diag *tomledit.Error
	if errors.As(err, &diag) {
		fmt.Println(filepath.Base(diag.File), diag.Pos.Line, diag.Pos.Column, diag.Path, diag.Expected, diag.Got)
	}
	// Output:
	// config.toml 2 8 server.port integer string
}

func ExampleDocument_WriteFile() {
	dir, err := os.MkdirTemp("", "tomledit-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.toml")

	doc, err := tomledit.Parse([]byte("# kept\n[server]\nport = 8080\n"))
	if err != nil {
		panic(err)
	}
	if err := doc.Set("server.port", 9090); err != nil {
		panic(err)
	}
	// The bytes are checked against a re-parse before anything is written,
	// and the file is replaced in one step.
	if err := doc.WriteFile(path); err != nil {
		panic(err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	fmt.Print(string(written))
	// Output:
	// # kept
	// [server]
	// port = 9090
}

func ExampleDocument_Root() {
	doc, err := tomledit.Parse([]byte(`title = "example"
database.host = "localhost"
database.port = 5432

[[server]]
name = "alpha"

[[server]]
name = "beta"
`))
	if err != nil {
		panic(err)
	}

	// The read-layer folds the spellings away: a dotted key and an
	// array-of-tables answer the same questions as anything else.
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
	// Output:
	// title = example
	// database is a table of 2 keys
	// server is an array of 2 tables
}

func ExampleDocument_Validate() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
prot = 8080
`))
	if err != nil {
		panic(err)
	}

	spec := &tomledit.Spec{Fields: map[string]tomledit.Field{
		"server": {Kind: tomledit.FieldKindTable, Required: true, Table: &tomledit.Spec{
			Fields: map[string]tomledit.Field{
				"host": {Kind: tomledit.FieldKindString, Required: true},
				"port": {Kind: tomledit.FieldKindInteger, Required: true},
			},
		}},
	}}

	err = doc.Validate(spec)
	fmt.Println(errors.Is(err, tomledit.ErrUnknownKey))
	fmt.Println(errors.Is(err, tomledit.ErrMissingKey))

	// Every independent violation is collected, in document order.
	var all *tomledit.Errors
	if errors.As(err, &all) {
		for _, e := range all.Unwrap() {
			var diag *tomledit.Error
			if errors.As(e, &diag) {
				fmt.Printf("%s: %s\n", diag.Kind, diag.Path)
			}
		}
	}
	// Output:
	// true
	// true
	// unknown key: server.prot
	// missing key: server.port
}

func ExampleDocument_DecodeSpec() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
port = 8080
`))
	if err != nil {
		panic(err)
	}

	spec := &tomledit.Spec{Fields: map[string]tomledit.Field{
		"server": {Kind: tomledit.FieldKindTable, Table: &tomledit.Spec{
			Fields: map[string]tomledit.Field{
				"host": {Kind: tomledit.FieldKindString},
				"port": {Kind: tomledit.FieldKindInteger},
			},
		}},
	}}

	// A descriptor decode runs no consumer code: the result is exactly what
	// the document says, and it is all-or-nothing.
	values, err := doc.DecodeSpec(spec)
	if err != nil {
		panic(err)
	}
	server := values["server"].(map[string]any)
	fmt.Println(server["host"], server["port"])
	// Output:
	// localhost 8080
}

func ExampleDecode() {
	type Server struct {
		Host string `toml:"host"`
		Port int    `toml:"port"`
	}
	type Config struct {
		Title  string `toml:"title"`
		Server Server `toml:"server"`
	}

	doc, err := tomledit.Parse([]byte(`title = "My App"

[server]
host = "0.0.0.0"
port = 443
`))
	if err != nil {
		panic(err)
	}

	// Decode returns the value it built; the document stays available for
	// comment-preserving edits.
	cfg, err := tomledit.Decode[Config](doc)
	if err != nil {
		panic(err)
	}
	fmt.Println(cfg.Title, cfg.Server.Host, cfg.Server.Port)

	if err := doc.Set("server.port", cfg.Server.Port+1); err != nil {
		panic(err)
	}
	port, err := doc.GetInt("server.port")
	if err != nil {
		panic(err)
	}
	fmt.Println(port)
	// Output:
	// My App 0.0.0.0 443
	// 444
}

func ExampleDecode_strict() {
	type Config struct {
		Title string `toml:"title"`
	}
	doc, err := tomledit.Parse([]byte("title = \"ok\"\ntilte = \"typo\"\n"))
	if err != nil {
		panic(err)
	}

	// Strictness is the only mode: an unknown key is an error, and a failed
	// decode returns no value to inspect.
	cfg, err := tomledit.Decode[Config](doc)
	fmt.Println(cfg == nil)
	fmt.Println(errors.Is(err, tomledit.ErrUnknownKey))
	fmt.Println(err)
	// Output:
	// true
	// true
	// 2:1: tilte: unknown key "tilte"
}

func ExampleDecodeNode() {
	type Server struct {
		Host string `toml:"host"`
		Port int    `toml:"port"`
	}
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
port = 5432
`))
	if err != nil {
		panic(err)
	}

	// One construct decoded on its own.
	node, err := doc.Resolve("server")
	if err != nil {
		panic(err)
	}
	server, err := tomledit.DecodeNode[Server](node)
	if err != nil {
		panic(err)
	}
	fmt.Println(server.Host, server.Port)
	// Output:
	// localhost 5432
}

func ExampleDecodeOver() {
	type Config struct {
		Host    string `toml:"host"`
		Port    int    `toml:"port"`
		Verbose bool   `toml:"verbose"`
	}
	defaults := func() Config {
		return Config{Host: "localhost", Port: 8080, Verbose: false}
	}

	doc, err := tomledit.Parse([]byte("port = 9090\n"))
	if err != nil {
		panic(err)
	}

	// The factory builds the seed, the document overlays it, and the second
	// result names the paths the document supplied.
	cfg, written, err := tomledit.DecodeOver(doc, defaults)
	if err != nil {
		panic(err)
	}
	fmt.Println(cfg.Host, cfg.Port, cfg.Verbose)
	fmt.Println(written)
	// Output:
	// localhost 9090 false
	// [port]
}

func ExampleDocument_EnsureDefaults() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"
`))
	if err != nil {
		panic(err)
	}

	added, err := doc.EnsureDefaults([]tomledit.Default{
		{Path: "server.host", Value: "0.0.0.0"}, // already there, left alone
		{Path: "server.port", Value: 8080},
		{Path: "logging.level", Value: "info"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(added)
	fmt.Print(string(doc.Bytes()))
	// Output:
	// [server.port logging.level]
	// [server]
	// host = "localhost"
	// port = 8080
	// [logging]
	// level = "info"
}

func ExampleDocument_AppendToArray() {
	doc, err := tomledit.Parse([]byte("ports = [8080, 8081]\n"))
	if err != nil {
		panic(err)
	}
	if err := doc.AppendToArray("ports", 8082); err != nil {
		panic(err)
	}
	if err := doc.RemoveFromArray("ports", 0); err != nil {
		panic(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// ports = [8081, 8082]
}

func ExampleDocument_PermuteChildren() {
	doc, err := tomledit.Parse([]byte(`# about b
b = 2
a = 1
c = 3
`))
	if err != nil {
		panic(err)
	}
	// order[i] is the index of the child moving to position i, so this puts
	// the second child first. A child's comments travel with it.
	if err := doc.PermuteChildren("", []int{1, 0, 2}); err != nil {
		panic(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a = 1
	// # about b
	// b = 2
	// c = 3
}

func ExampleDocument_RenameKey() {
	doc, err := tomledit.Parse([]byte(`[server]
host = "localhost"  # bind address

[server.tls]
cert = "/etc/cert.pem"
`))
	if err != nil {
		panic(err)
	}
	// Renaming a table renames the binding: every header that names it,
	// nested ones included.
	if err := doc.RenameKey("server", "listener"); err != nil {
		panic(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// [listener]
	// host = "localhost"  # bind address
	//
	// [listener.tls]
	// cert = "/etc/cert.pem"
}

func ExampleDocument_SetComment() {
	doc, err := tomledit.Parse([]byte("[server]\nport = 8080\n"))
	if err != nil {
		panic(err)
	}
	if err := doc.SetComment("server.port", "default HTTP port"); err != nil {
		panic(err)
	}
	if err := doc.SetLeadingComments("server.port", []string{
		"The port to bind to.",
		"Any value above 1024 needs no privileges.",
	}); err != nil {
		panic(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// [server]
	// # The port to bind to.
	// # Any value above 1024 needs no privileges.
	// port = 8080 # default HTTP port
}

func ExampleDocument_GetComment() {
	doc, err := tomledit.Parse([]byte(
		"# The port to bind to.\n" +
			"# Any value above 1024 needs no privileges.\n" +
			"port = 8080  # default HTTP port\n"))
	if err != nil {
		panic(err)
	}

	inline, err := doc.GetComment("port")
	if err != nil {
		panic(err)
	}
	leading, err := doc.GetLeadingComments("port")
	if err != nil {
		panic(err)
	}

	fmt.Printf("inline: %q\n", inline)
	for _, line := range leading {
		fmt.Printf("leading: %q\n", line)
	}

	// Output:
	// inline: "default HTTP port"
	// leading: "The port to bind to."
	// leading: "Any value above 1024 needs no privileges."
}

func ExampleQuoteString() {
	fmt.Println(tomledit.QuoteString(`say "hi"`))
	fmt.Println(tomledit.QuoteString("tab\there"))
	fmt.Println(tomledit.QuoteKey("bare_key"))
	fmt.Println(tomledit.QuoteKey("not a bare key"))
	// Output:
	// "say \"hi\""
	// "tab\there"
	// bare_key
	// "not a bare key"
}

func ExampleFormatFloat() {
	fmt.Println(tomledit.FormatFloat(1))
	fmt.Println(tomledit.FormatFloat(0.1))
	fmt.Println(tomledit.FormatFloat(1e21))
	fmt.Println(tomledit.FormatFloat(math.Inf(-1)))
	fmt.Println(tomledit.FormatFloat(math.NaN()))
	// Output:
	// 1.0
	// 0.1
	// 1e+21
	// -inf
	// nan
}

func ExampleParsePath() {
	segs, err := tomledit.ParsePath(`servers[0]."host.name"`)
	if err != nil {
		panic(err)
	}
	for _, s := range segs {
		if s.Kind == tomledit.SegmentKey {
			fmt.Printf("key %q\n", s.Key)
		} else {
			fmt.Printf("index %d\n", s.Index)
		}
	}
	// JoinPath is the quoting authority: it writes back what ParsePath read.
	fmt.Println(tomledit.JoinPath(segs))
	// Output:
	// key "servers"
	// index 0
	// key "host.name"
	// servers[0]."host.name"
}

func ExampleDocument_Set_orderedInlineTable() {
	doc, err := tomledit.Parse([]byte("name = \"example\"\n"))
	if err != nil {
		panic(err)
	}
	// A map is written with its keys sorted; []Pair keeps the order given.
	if err := doc.Set("tls", []tomledit.Pair{
		{Key: "cert", Value: "/etc/cert.pem"},
		{Key: "key", Value: "/etc/key.pem"},
	}); err != nil {
		panic(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// name = "example"
	// tls = {cert = "/etc/cert.pem", key = "/etc/key.pem"}
}

func ExampleError() {
	doc, err := tomledit.Parse([]byte("[server]\nport = \"8080\"\n"))
	if err != nil {
		panic(err)
	}
	_, err = doc.GetInt("server.port")

	// Every document-dependent failure is an *Error: match a kind with
	// errors.Is, read the structure with errors.As.
	fmt.Println(errors.Is(err, tomledit.ErrTypeMismatch))
	var diag *tomledit.Error
	if errors.As(err, &diag) {
		fmt.Printf("%d:%d %s: expected %s, got %s\n",
			diag.Pos.Line, diag.Pos.Column, diag.Path, diag.Expected, diag.Got)
	}
	fmt.Println(err)
	// Output:
	// true
	// 2:8 server.port: expected integer, got string
	// 2:8: server.port: expected integer, got string
}

func ExampleDocument_GetTime() {
	doc, err := tomledit.Parse([]byte(`stamp = 1979-05-27T07:32:00Z
written = "1979-05-27T07:32:00Z"
`))
	if err != nil {
		panic(err)
	}
	stamp, err := doc.GetTime("stamp")
	if err != nil {
		panic(err)
	}
	fmt.Println(stamp.Format(time.RFC3339))

	// A string is not a date-time, even one spelling a valid timestamp. A
	// time.Time DECODE target answers the same way: both surfaces read the one
	// conversion table.
	_, err = doc.GetTime("written")
	fmt.Println(errors.Is(err, tomledit.ErrTypeMismatch))

	type Config struct {
		Written time.Time `toml:"written"`
		Stamp   time.Time `toml:"stamp"`
	}
	_, err = tomledit.Decode[Config](doc)
	fmt.Println(errors.Is(err, tomledit.ErrTypeMismatch))
	// Output:
	// 1979-05-27T07:32:00Z
	// true
	// true
}

func ExampleUnmarshal_intoAMap() {
	m, err := tomledit.Unmarshal[map[string]any]([]byte(`title = "My App"

[server]
port = 443
`))
	if err != nil {
		panic(err)
	}
	// The result is a pointer to the value Unmarshal built.
	fmt.Println((*m)["title"])
	fmt.Println((*m)["server"].(map[string]any)["port"])
	// Output:
	// My App
	// 443
}
