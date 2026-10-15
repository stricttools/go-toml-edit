package tomledit

import (
	"errors"
	"strings"
	"testing"
)

// A document parsed with ParseWithFileName remembers the name it was given, so
// that every diagnostic it produces afterwards names the file. Reading the file
// itself is the tomlfile subpackage's, and its tests cover it.

// parseWithFileName parses content under the file name file and fails the test
// if it does not parse.
func parseWithFileName(t *testing.T, file, content string) *Document {
	t.Helper()
	doc, err := ParseWithFileName([]byte(content), file)
	if err != nil {
		t.Fatalf("ParseWithFileName(%s): %v", file, err)
	}
	return doc
}

// Fails if ParseWithFileName stops producing the same document Parse produces
// for the same bytes.
func TestParseWithFileName(t *testing.T) {
	const content = "# a config\ntitle = \"hello\"\n\n[server]\nhost = \"localhost\"\n"
	doc := parseWithFileName(t, "configs/config.toml", content)

	if got := string(doc.Bytes()); got != content {
		t.Errorf("round-trip = %q, want %q", got, content)
	}
	if host, err := doc.GetString("server.host"); err != nil || host != "localhost" {
		t.Errorf("server.host = %q, %v; want \"localhost\", true", host, err)
	}
}

// Fails if a parse failure from ParseWithFileName stops naming the file.
func TestParseWithFileNameSyntaxErrorNamesTheFile(t *testing.T) {
	const path = "configs/broken.toml"
	_, err := ParseWithFileName([]byte("a = 1\na = 2\n"), path)
	if err == nil {
		t.Fatalf("expected a parse error")
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if diag.File != path {
		t.Errorf("File = %q, want %q", diag.File, path)
	}
	if !strings.HasPrefix(err.Error(), path+":") {
		t.Errorf("rendered error %q does not start with the file name", err.Error())
	}
}

// Fails if a later diagnostic -- from access, editing or commenting -- stops
// naming the file the document was parsed under.
func TestParseWithFileNameDiagnosticsNameTheFile(t *testing.T) {
	const content = "title = \"hello\"\npoint = { x = 1 }\n\n[server]\nhost = \"localhost\"\n"
	const path = "configs/config.toml"

	tests := []struct {
		name string
		run  func(d *Document) error
	}{
		{"resolve", func(d *Document) error { _, err := d.Resolve("server.port"); return err }},
		{"set", func(d *Document) error { return d.Set("nope.deep", 1) }},
		{"delete with a bad path", func(d *Document) error { return d.Delete("a[") }},
		{"rename", func(d *Document) error { return d.RenameKey("server.port", "p") }},
		{"new table", func(d *Document) error { return d.NewTable("server") }},
		{"comment", func(d *Document) error { return d.SetComment("point.x", "no") }},
		{"cursor", func(d *Document) error { return d.Key("server").Key("port").Err() }},
		{"ensure defaults", func(d *Document) error {
			_, err := d.EnsureDefaults([]Default{{Path: "fresh", Value: make(chan int)}})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseWithFileName(t, path, content)
			err := tt.run(doc)
			if err == nil {
				t.Fatalf("expected an error")
			}
			var diag *Error
			if !errors.As(err, &diag) {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			if diag.File != path {
				t.Errorf("File = %q, want %q (err: %v)", diag.File, path, err)
			}
			if !strings.HasPrefix(err.Error(), path+":") {
				t.Errorf("rendered error %q does not start with the file name", err.Error())
			}
		})
	}
}

// Fails if a document parsed from bytes starts claiming a file.
func TestParseCarriesNoFile(t *testing.T) {
	doc, err := Parse([]byte("title = \"hello\"\n"))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	err = doc.Set("nope.deep", 1)
	if err == nil {
		t.Fatalf("expected an error")
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if diag.File != "" {
		t.Errorf("File = %q, want empty for a document parsed from bytes", diag.File)
	}
}

// Fails if a DecodeNode diagnostic stops naming the file its node came from.
// A node reaches its document through the parent links the structure funnel
// maintains, which is how a decode scoped to one construct still knows where
// the construct was read from.
func TestParseWithFileNameDecodeNodeDiagnosticsNameTheFile(t *testing.T) {
	const path = "configs/config.toml"
	doc := parseWithFileName(t, path, "[server]\nport = \"eighty\"\n")
	node, ok := doc.Lookup("server")
	if !ok {
		t.Fatal("server not found")
	}

	type target struct {
		Port int `toml:"port"`
	}
	_, err := DecodeNode[target](node)
	if err == nil {
		t.Fatal("expected an error decoding a string into an int field")
	}
	var diag *Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if diag.File != path {
		t.Errorf("File = %q, want %q (err: %v)", diag.File, path, err)
	}
	if !strings.HasPrefix(err.Error(), path+":") {
		t.Errorf("Error() = %q, want it to start with %q", err.Error(), path+":")
	}
}
