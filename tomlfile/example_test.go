package tomlfile_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tomledit "github.com/stricttools/go-toml-edit"
	"github.com/stricttools/go-toml-edit/tomlfile"
)

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

	doc, err := tomlfile.ParseFile(path)
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

func ExampleWriteFile() {
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
	if err := tomlfile.WriteFile(doc, path); err != nil {
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
