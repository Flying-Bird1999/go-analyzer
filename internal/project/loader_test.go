// loader_test.go 验证 project 加载器对目录扫描、构建约束过滤、构建标签以及
// 解析失败诊断行为是否正确。

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 测试场景：加载 mini-bff fixture，应正确扫描 .go 文件、解析 alias import，并跳过 _test.go。
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

// 测试场景：单个普通源码解析失败时不应中断加载，需记录 package_load_failed 诊断并保留可解析文件。
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

// 测试场景：_ 或 . 前缀的文件与目录应被整体跳过，仅保留 normal .go 文件。
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

// 测试场景：被 `//go:build ignore` 排除的文件不应被加载，正常文件保留。
func TestLoadSkipsFilesExcludedByBuildConstraints(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/build-tags\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "update_env.go"), []byte(`//go:build ignore
// +build ignore

package main

func main() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	pkg := p.Packages["example.com/build-tags"]
	if pkg == nil {
		t.Fatalf("main package not loaded: %#v", p.Packages)
	}
	if len(pkg.Files) != 1 {
		t.Fatalf("loaded files = %#v", pkg.Files)
	}
	if strings.HasSuffix(pkg.Files[0].Path, "update_env.go") {
		t.Fatalf("build-ignored file should be skipped: %s", pkg.Files[0].Path)
	}
}

// 测试场景：传入显式构建标签时，仅保留满足该标签的文件，并反映在 Project.BuildContext 上。
func TestLoadWithOptionsHonorsExplicitBuildTags(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/build-context\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLoaderTestFile(t, root, "default.go", `//go:build !customtag

package buildcontext

func DefaultOnly() {}
`)
	writeLoaderTestFile(t, root, "tagged.go", `//go:build customtag

package buildcontext

func TaggedOnly() {}
`)

	p, err := LoadWithOptions(root, LoadOptions{
		BuildContext: BuildContextOptions{Tags: []string{"customtag"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	pkg := p.Packages["example.com/build-context"]
	if pkg == nil || len(pkg.Files) != 1 {
		t.Fatalf("loaded package = %#v", pkg)
	}
	if got := filepath.Base(pkg.Files[0].Path); got != "tagged.go" {
		t.Fatalf("loaded file = %q", got)
	}
	if len(p.BuildContext.Tags) != 1 || p.BuildContext.Tags[0] != "customtag" {
		t.Fatalf("project build context = %#v", p.BuildContext)
	}
}

// writeLoaderTestFile 是加载器测试中向临时目录写入测试源码文件的辅助函数。
func writeLoaderTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
