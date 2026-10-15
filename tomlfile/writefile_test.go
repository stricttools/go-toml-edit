package tomlfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	tomledit "github.com/stricttools/go-toml-edit"
)

// WriteFile's contract is a list of invariants, and each one has a test here:
// the temporary file is a sibling of the destination, the bytes are checked
// for a round trip before anything is written, a failure at the write or the
// rename leaves the destination untouched and no temporary file behind, an
// existing destination keeps its mode, and a new file is created as an ordinary
// create would make it.
//
// The write path's three seams (renderVerified, writeAll, renameOver) are what
// makes the failure invariants testable without a filesystem that fails on
// demand. Each test restores what it replaced. The round-trip check itself is
// Document.RoundTripVerifiedBytes, and the root package's tests cover its
// refusals; here a refusal is injected to show that nothing is written.

const writeFileDoc = `# a document
title = "example"

[server]
host = "localhost"
port = 8080
`

// parseForWrite parses the fixture and fails the test if it does not parse.
func parseForWrite(t *testing.T) *tomledit.Document {
	t.Helper()
	doc, err := tomledit.Parse([]byte(writeFileDoc))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return doc
}

// dirEntries lists the names in dir, so a test can assert nothing was left
// behind.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Fails if WriteFile stops writing what the document renders, or stops
// producing a file that reads back as the same document.
func TestWriteFile_WritesTheRenderedDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	doc := parseForWrite(t)

	if err := WriteFile(doc, path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if string(got) != writeFileDoc {
		t.Errorf("wrote %q, want %q", got, writeFileDoc)
	}
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "config.toml" {
		t.Errorf("the directory holds %v, want just the destination", names)
	}
	if _, err := ParseFile(path); err != nil {
		t.Errorf("the written file does not parse: %v", err)
	}
}

// Fails if the temporary file stops being a sibling of the destination: a
// rename across a filesystem boundary is not atomic, and only a file in the
// destination's own directory is guaranteed to be on the same one.
func TestWriteFile_TempFileIsASiblingOfTheDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	var from string
	original := renameOver
	renameOver = func(oldpath, newpath string) error {
		from = oldpath
		return original(oldpath, newpath)
	}
	defer func() { renameOver = original }()

	if err := WriteFile(parseForWrite(t), path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if from == "" {
		t.Fatal("nothing was renamed into place")
	}
	if got, want := filepath.Dir(from), filepath.Dir(path); got != want {
		t.Errorf("the temporary file was in %s, want the destination's own directory %s", got, want)
	}
	if filepath.Base(from) == filepath.Base(path) {
		t.Errorf("the temporary file was the destination itself (%s)", from)
	}
}

// Fails if WriteFile stops refusing a document whose rendered bytes fail the
// round-trip check: the check's diagnostic comes back unchanged, and nothing is
// written -- not the destination, and no temporary file.
func TestWriteFile_ARoundTripRefusalWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	var named string
	original := renderVerified
	renderVerified = func(d *tomledit.Document, file string) ([]byte, error) {
		named = file
		_, err := tomledit.Parse([]byte("broken = \n"))
		return nil, err
	}
	defer func() { renderVerified = original }()

	err := WriteFile(parseForWrite(t), path)
	if !errors.Is(err, tomledit.ErrSyntax) {
		t.Fatalf("WriteFile = %v, want the injected refusal", err)
	}
	if named != path {
		t.Errorf("the round-trip check was asked to name %q, want the destination %q", named, path)
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Errorf("the directory holds %v, want nothing written", names)
	}
}

// Fails if a write failure stops leaving the destination as it was, or starts
// leaving the temporary file behind.
func TestWriteFile_AWriteFailureLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const existing = "kept = true\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	injected := errors.New("injected write failure")
	original := writeAll
	writeAll = func(*os.File, []byte) error { return injected }
	defer func() { writeAll = original }()

	if err := WriteFile(parseForWrite(t), path); !errors.Is(err, injected) {
		t.Fatalf("WriteFile = %v, want the injected failure", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the destination: %v", err)
	}
	if string(got) != existing {
		t.Errorf("the destination now holds %q, want the untouched %q", got, existing)
	}
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "config.toml" {
		t.Errorf("the directory holds %v, want just the untouched destination", names)
	}
}

// Fails if a rename failure stops leaving the destination as it was, or starts
// leaving the temporary file behind.
func TestWriteFile_ARenameFailureLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const existing = "kept = true\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	injected := errors.New("injected rename failure")
	original := renameOver
	renameOver = func(string, string) error { return injected }
	defer func() { renameOver = original }()

	if err := WriteFile(parseForWrite(t), path); !errors.Is(err, injected) {
		t.Fatalf("WriteFile = %v, want the injected failure", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the destination: %v", err)
	}
	if string(got) != existing {
		t.Errorf("the destination now holds %q, want the untouched %q", got, existing)
	}
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "config.toml" {
		t.Errorf("the directory holds %v, want just the untouched destination", names)
	}
}

// Fails if an existing destination stops keeping its own file mode: a
// deliberately restricted config must not become world-readable because it was
// rewritten, and a deliberately shared one must not become private.
//
// The two modes are chosen for what they exercise. A restrictive mode is the
// case that motivates the invariant; a permissive one carries bits a typical
// umask clears, so it fails unless the mode is set outright rather than left to
// the temporary file's creation.
func TestWriteFile_AnExistingDestinationKeepsItsMode(t *testing.T) {
	for _, mode := range []fs.FileMode{0o600, 0o666} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte("old = true\n"), mode); err != nil {
				t.Fatalf("seeding the destination: %v", err)
			}
			// os.WriteFile creates through the umask; the mode the invariant
			// concerns is the one the file actually carries.
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("setting the destination's mode: %v", err)
			}

			if err := WriteFile(parseForWrite(t), path); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if got := info.Mode().Perm(); got != mode {
				t.Errorf("the destination is now %v, want the %v it was", got, mode)
			}
		})
	}
}

// Fails if a new file stops being created the way an ordinary create makes one.
// The expected mode is measured rather than written down: a reference file
// created with the same requested mode in the same directory carries whatever
// the process umask leaves of it. How much the comparison distinguishes
// therefore depends on the umask the suite runs under -- under one that clears
// none of 0o644 the two modes agree whatever the implementation does, and under
// one that clears some, a mode set outright instead of created fails here.
func TestWriteFile_ANewFileIsCreatedAsAnOrdinaryCreateWould(t *testing.T) {
	dir := t.TempDir()

	reference := filepath.Join(dir, "reference.toml")
	f, err := os.OpenFile(reference, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatalf("creating the reference file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing the reference file: %v", err)
	}
	refInfo, err := os.Stat(reference)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	path := filepath.Join(dir, "config.toml")
	if err := WriteFile(parseForWrite(t), path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got, want := info.Mode().Perm(), refInfo.Mode().Perm(); got != want {
		t.Errorf("the new file is %v, want the %v an ordinary create of mode 0o644 makes here", got, want)
	}
}

// Fails if WriteFile stops reporting a filesystem failure as the underlying
// error: a directory that does not exist is an *fs.PathError, the same shape
// ParseFile reports a missing file with, not a document diagnostic.
func TestWriteFile_AFilesystemFailureIsTheUnderlyingError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "config.toml")

	err := WriteFile(parseForWrite(t), path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WriteFile = %v, want a not-exist error", err)
	}
	var diag *tomledit.Error
	if errors.As(err, &diag) {
		t.Errorf("WriteFile reported a document diagnostic (%v) for a filesystem failure", diag)
	}
}
