package main

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestMainConsumesPublicBackend(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowedInternal := "github.com/gguage/music-to-bb/internal/cli"
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, "github.com/gguage/music-to-bb/internal/") && path != allowedInternal {
			t.Errorf("main directly imports backend implementation %s", path)
		}
	}
}
