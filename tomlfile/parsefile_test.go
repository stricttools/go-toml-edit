package tomlfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tomledit "github.com/stricttools/go-toml-edit"
)

// ParseFile reads a document from disk and the document remembers where it came
// from, so that every diagnostic it produces afterwards names the file. That the
// name reaches every later diagnostic is tomledit.ParseWithFileName's contract,
// and the root package's tests cover it; these tests cover the read.

// writeTOML writes content to a file in a temporary directory and returns its
// path.
func writeTOML(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// Fails if ParseFile stops producing the same document Parse produces for the
// same bytes.
func TestParseFile(t *testing.T) {
	const content = "# a config\ntitle = \"hello\"\n\n[server]\nhost = \"localhost\"\n"
	path := writeTOML(t, "config.toml", content)

	doc, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if got := string(doc.Bytes()); got != content {
		t.Errorf("round-trip = %q, want %q", got, content)
	}
	if host, err := doc.GetString("server.host"); err != nil || host != "localhost" {
		t.Errorf("server.host = %q, %v; want \"localhost\", true", host, err)
	}
}

// Fails if an unreadable file stops being reported as the underlying read
// error -- nothing was parsed, so there is nothing to diagnose.
func TestParseFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.toml")
	doc, err := ParseFile(path)
	if err == nil {
		t.Fatalf("ParseFile on a missing file returned %v, want an error", doc)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false, want true (err: %v)", err)
	}
	var diag *tomledit.Error
	if errors.As(err, &diag) {
		t.Errorf("a read failure must not be reported as a diagnostic, got %v", diag)
	}
}

// Fails if a parse failure from ParseFile stops naming the file it came from.
func TestParseFileSyntaxErrorNamesTheFile(t *testing.T) {
	path := writeTOML(t, "broken.toml", "a = 1\na = 2\n")

	_, err := ParseFile(path)
	if err == nil {
		t.Fatalf("expected a parse error")
	}
	var diag *tomledit.Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *tomledit.Error, got %T: %v", err, err)
	}
	if diag.File != path {
		t.Errorf("File = %q, want %q", diag.File, path)
	}
	if !strings.HasPrefix(err.Error(), path+":") {
		t.Errorf("rendered error %q does not start with the file name", err.Error())
	}
}

// Fails if a later diagnostic stops naming the file the document was read from.
func TestParseFileDiagnosticsNameTheFile(t *testing.T) {
	path := writeTOML(t, "config.toml", "[server]\nhost = \"localhost\"\n")
	doc, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	_, err = doc.Resolve("server.port")
	var diag *tomledit.Error
	if !errors.As(err, &diag) {
		t.Fatalf("expected *tomledit.Error, got %T: %v", err, err)
	}
	if diag.File != path {
		t.Errorf("File = %q, want %q (err: %v)", diag.File, path, err)
	}
}
