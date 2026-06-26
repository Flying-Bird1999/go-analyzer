package project

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectScansGoFilesAndImports(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	p, err := Load(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if p.ModulePath != "example.com/mini-bff" {
		t.Fatalf("module path = %q", p.ModulePath)
	}
	if _, ok := p.Packages["example.com/mini-bff/controller"]; !ok {
		t.Fatalf("controller package not loaded: %#v", p.Packages)
	}
	routerPkg := p.Packages["example.com/mini-bff/router"]
	if routerPkg == nil {
		t.Fatal("router package not loaded")
	}
	if len(routerPkg.Files) != 1 {
		t.Fatalf("router files = %d", len(routerPkg.Files))
	}
	if got := routerPkg.Files[0].Imports["ctl"]; got != "example.com/mini-bff/controller" {
		t.Fatalf("alias import ctl = %q", got)
	}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			if strings.HasSuffix(file.Path, "_test.go") {
				t.Fatalf("test file should be skipped: %s", file.Path)
			}
		}
	}
}
