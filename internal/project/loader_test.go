package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectScansGoFilesAndImports(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	p, err := Load(root)
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

func TestLoadSkipsInvalidGoFileAndRecordsDiagnostic(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/partial\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "valid.go"), []byte("package partial\n\nfunc Valid() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package partial\n\nfunc Broken( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].Code != "package_load_failed" {
		t.Fatalf("diagnostics = %#v", p.Diagnostics)
	}
	pkg := p.Packages["example.com/partial"]
	if pkg == nil || len(pkg.Files) != 1 || !strings.HasSuffix(pkg.Files[0].Path, "valid.go") {
		t.Fatalf("loaded package = %#v", pkg)
	}
}

func TestLoadSkipsGoIgnoredFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/ignored\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid.go", "_ignored.go", ".ignored.go", "_fixtures/ignored.go", ".cache/ignored.go"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package ignored\n\nfunc Value() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	pkg := p.Packages["example.com/ignored"]
	if pkg == nil || len(pkg.Files) != 1 || !strings.HasSuffix(pkg.Files[0].Path, "valid.go") {
		t.Fatalf("loaded package = %#v", pkg)
	}
}
