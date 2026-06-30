package gomod

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractModuleDependencies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "gomod-change", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	deps, err := ExtractDependencies(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Fatalf("deps = %d: %#v", len(deps), deps)
	}
	gin := findDep(t, deps, "github.com/gin-gonic/gin")
	if gin.Version != "v1.10.0" {
		t.Fatalf("gin version = %q", gin.Version)
	}
	if gin.Indirect {
		t.Fatal("gin should be direct")
	}
	if gin.ReplacePath != "github.com/gin-gonic/gin" || gin.ReplaceVersion != "v1.10.1" {
		t.Fatalf("gin replace = %#v", gin)
	}
	lego := findDep(t, deps, "gopkg.inshopline.com/commons/lego/core")
	if !lego.Indirect {
		t.Fatal("lego should be indirect")
	}
}

func TestExtractModuleDependenciesSupportsReplaceBlock(t *testing.T) {
	data := []byte(`module example.com/app

go 1.24

require (
	example.com/one v1.0.0
	example.com/two v2.0.0
)

replace (
	example.com/one => example.com/one-fork v1.1.0
	example.com/two => ../two
)
`)
	deps, err := ExtractDependencies(data)
	if err != nil {
		t.Fatal(err)
	}
	one := findDep(t, deps, "example.com/one")
	if one.ReplacePath != "example.com/one-fork" || one.ReplaceVersion != "v1.1.0" {
		t.Fatalf("one replace = %#v", one)
	}
	two := findDep(t, deps, "example.com/two")
	if two.ReplacePath != "../two" || two.ReplaceVersion != "" {
		t.Fatalf("two replace = %#v", two)
	}
}

func TestCompareVersionUsesSemanticPrereleaseOrdering(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "release after prerelease", left: "v1.0.0", right: "v1.0.0-rc.2", want: 1},
		{name: "prerelease before release", left: "v1.0.0-beta.1", right: "v1.0.0", want: -1},
		{name: "numeric prerelease", left: "v1.0.0-rc.10", right: "v1.0.0-rc.2", want: 1},
		{name: "build metadata ignored", left: "v1.0.0+incompatible", right: "v1.0.0", want: 0},
		{name: "pseudo version timestamp", left: "v0.0.0-20250102030405-bbbbbbbbbbbb", right: "v0.0.0-20240102030405-aaaaaaaaaaaa", want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compareVersion(tc.left, tc.right)
			switch {
			case tc.want < 0 && got >= 0:
				t.Fatalf("compareVersion(%q, %q) = %d, want < 0", tc.left, tc.right, got)
			case tc.want == 0 && got != 0:
				t.Fatalf("compareVersion(%q, %q) = %d, want 0", tc.left, tc.right, got)
			case tc.want > 0 && got <= 0:
				t.Fatalf("compareVersion(%q, %q) = %d, want > 0", tc.left, tc.right, got)
			}
		})
	}
}

func TestDiffModulesFromFileChangesDetectsBlockRequireWithoutBlockHeader(t *testing.T) {
	fileChanges := []diff.FileChange{{
		OldPath: "go.mod",
		NewPath: "go.mod",
		Status:  diff.StatusModified,
		Raw: "diff --git a/go.mod b/go.mod\n" +
			"--- a/go.mod\n" +
			"+++ b/go.mod\n" +
			"@@ -12 +12 @@ require (\n" +
			"-\texample.com/jsonx v1.0.0\n" +
			"+\texample.com/jsonx v1.1.0\n",
	}}

	changes, err := DiffModulesFromFileChanges(fileChanges)
	if err != nil {
		t.Fatal(err)
	}
	assertModuleChange(t, changes, "example.com/jsonx", facts.ModuleChangeUpgraded)
}

func TestDiffModulesFromFileChangesDetectsReplaceOnlyHunk(t *testing.T) {
	fileChanges := []diff.FileChange{{
		OldPath: "go.mod",
		NewPath: "go.mod",
		Status:  diff.StatusModified,
		Raw: "diff --git a/go.mod b/go.mod\n" +
			"--- a/go.mod\n" +
			"+++ b/go.mod\n" +
			"@@ -20 +20 @@\n" +
			"-replace example.com/jsonx => example.com/jsonx v1.0.0\n" +
			"+replace example.com/jsonx => example.com/jsonx v1.1.0\n",
	}}

	changes, err := DiffModulesFromFileChanges(fileChanges)
	if err != nil {
		t.Fatal(err)
	}
	assertModuleChange(t, changes, "example.com/jsonx", facts.ModuleChangeReplaced)
}

func TestMapModuleUsagePrecise(t *testing.T) {
	store := mapUsageFixture(t, "gomod-precise")
	usage := findUsage(t, store.ModuleUsages, "gopkg.inshopline.com/sc1/commons/utils")
	if usage.Basis != facts.ModuleUsagePrecise {
		t.Fatalf("basis = %q", usage.Basis)
	}
	if usage.SymbolID == "" {
		t.Fatal("expected precise usage to include symbol id")
	}
}

func TestMapModuleUsageFileFallback(t *testing.T) {
	store := mapUsageFixture(t, "gomod-file-fallback")
	usage := findUsage(t, store.ModuleUsages, "gopkg.inshopline.com/sc1/commons/utils")
	if usage.Basis != facts.ModuleUsageFileFallback {
		t.Fatalf("basis = %q", usage.Basis)
	}
	if usage.File == "" {
		t.Fatal("expected fallback usage to include file")
	}
	assertGomodDiagnosticCode(t, store, "module_usage_file_fallback")
}

func TestMapModuleUsageUnreferenced(t *testing.T) {
	store := mapUsageFixture(t, "gomod-unreferenced")
	usage := findUsage(t, store.ModuleUsages, "gopkg.inshopline.com/sc1/commons/utils")
	if usage.Basis != facts.ModuleUsageUnreferenced {
		t.Fatalf("basis = %q", usage.Basis)
	}
	assertGomodDiagnosticCode(t, store, "module_unreferenced")
}

func TestMapModuleUsageGenericReceiverIsPrecise(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/generic-usage\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(`package service

import utils "gopkg.inshopline.com/sc1/commons/utils"

type Client[T any] struct{}

func (c *Client[T]) Trace() string {
	return utils.TraceID()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	usages := MapModuleUsage(p, idx, store, []facts.ModuleChangeFact{{
		Path: "gopkg.inshopline.com/sc1/commons/utils",
		Kind: facts.ModuleChangeUpgraded,
	}})
	usage := findUsage(t, usages, "gopkg.inshopline.com/sc1/commons/utils")
	if usage.Basis != facts.ModuleUsagePrecise {
		t.Fatalf("basis = %q, want precise: %#v", usage.Basis, usage)
	}
	want := facts.SymbolID("method:example.com/generic-usage:Client:Trace")
	if usage.SymbolID != want {
		t.Fatalf("symbol = %q, want %q", usage.SymbolID, want)
	}
}

func findDep(t *testing.T, deps []facts.ModuleDependencyFact, path string) facts.ModuleDependencyFact {
	t.Helper()
	for _, dep := range deps {
		if dep.Path == path {
			return dep
		}
	}
	t.Fatalf("dependency %s not found: %#v", path, deps)
	return facts.ModuleDependencyFact{}
}

func assertModuleChange(t *testing.T, changes []facts.ModuleChangeFact, path string, kind facts.ModuleChangeKind) {
	t.Helper()
	for _, change := range changes {
		if change.Path == path && change.Kind == kind {
			return
		}
	}
	t.Fatalf("module change %s %s not found: %#v", path, kind, changes)
}

func mapUsageFixture(t *testing.T, name string) *facts.Store {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", name)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	for _, symbol := range idx.Symbols {
		store.AddSymbol(symbol)
	}
	changes := []facts.ModuleChangeFact{{Path: "gopkg.inshopline.com/sc1/commons/utils", Kind: facts.ModuleChangeUpgraded}}
	usages := MapModuleUsage(p, idx, store, changes)
	store.ModuleUsages = append(store.ModuleUsages, usages...)
	return store
}

func assertGomodDiagnosticCode(t *testing.T, store *facts.Store, code string) {
	t.Helper()
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %s not found: %#v", code, store.Diagnostics)
}

func findUsage(t *testing.T, usages []facts.ModuleUsageFact, module string) facts.ModuleUsageFact {
	t.Helper()
	for _, usage := range usages {
		if usage.ModulePath == module {
			return usage
		}
	}
	t.Fatalf("module usage %s not found: %#v", module, usages)
	return facts.ModuleUsageFact{}
}
