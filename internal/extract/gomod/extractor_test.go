package gomod

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
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

func TestDiffModulesDetectsDependencyChanges(t *testing.T) {
	oldMod := []byte(`module example.com/app

go 1.24

require (
	example.com/removed v1.0.0
	example.com/upgraded v1.0.0
	example.com/downgraded v1.2.0
	example.com/replaced v1.0.0
)

replace example.com/replaced => example.com/replaced v1.0.1
`)
	newMod := []byte(`module example.com/app

go 1.24

require (
	example.com/added v1.0.0
	example.com/upgraded v1.1.0
	example.com/downgraded v1.1.0
	example.com/replaced v1.0.0
)

replace example.com/replaced => example.com/replaced v1.0.2
`)

	changes, err := DiffModules(oldMod, newMod)
	if err != nil {
		t.Fatal(err)
	}

	assertModuleChange(t, changes, "example.com/added", facts.ModuleChangeAdded)
	assertModuleChange(t, changes, "example.com/removed", facts.ModuleChangeRemoved)
	assertModuleChange(t, changes, "example.com/upgraded", facts.ModuleChangeUpgraded)
	assertModuleChange(t, changes, "example.com/downgraded", facts.ModuleChangeDowngraded)
	assertModuleChange(t, changes, "example.com/replaced", facts.ModuleChangeReplaced)
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
}

func TestMapModuleUsageUnreferenced(t *testing.T) {
	store := mapUsageFixture(t, "gomod-unreferenced")
	usage := findUsage(t, store.ModuleUsages, "gopkg.inshopline.com/sc1/commons/utils")
	if usage.Basis != facts.ModuleUsageUnreferenced {
		t.Fatalf("basis = %q", usage.Basis)
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
	p, err := project.Load(root, project.Options{})
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
	usages := MapModuleUsage(p, idx, changes)
	store.ModuleUsages = append(store.ModuleUsages, usages...)
	return store
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
