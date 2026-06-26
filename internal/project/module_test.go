package project

import (
	"path/filepath"
	"testing"
)

func TestReadModulePath(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	got, err := ReadModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.com/mini-bff" {
		t.Fatalf("module path = %q", got)
	}
}
