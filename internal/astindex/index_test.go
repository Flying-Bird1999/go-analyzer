package astindex

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestBuildIndexesDeclarationSymbols(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}

	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"const:example.com/mini-bff/controller::DefaultChannel",
		"func:example.com/mini-bff/service::CheckIn",
		"func:example.com/mini-bff/router::Register",
		"method:example.com/mini-bff/controller:CommonController:CheckIn",
		"type:example.com/mini-bff/controller::CommonController",
		"var:example.com/mini-bff/controller::Common",
	}
	for _, id := range want {
		symbolID := facts.SymbolID(id)
		if _, ok := idx.Symbols[symbolID]; !ok {
			t.Fatalf("symbol %s not found; got %#v", id, idx.Symbols)
		}
		if idx.Symbols[symbolID].Span.File == "" {
			t.Fatalf("symbol %s has empty source file", id)
		}
	}
}

func TestBuildUsesCompleteDeclarationSpans(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "declaration-spans")
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}

	typeSymbol := mustSymbol(t, idx, "type:example.com/declaration-spans::Request")
	if typeSymbol.Span.EndLine <= typeSymbol.Span.StartLine {
		t.Fatalf("type span does not cover body: %#v", typeSymbol.Span)
	}

	valueSymbol := mustSymbol(t, idx, "var:example.com/declaration-spans::DefaultRequest")
	if valueSymbol.Span.EndLine <= valueSymbol.Span.StartLine {
		t.Fatalf("value span does not cover declaration: %#v", valueSymbol.Span)
	}
}

func TestBuildIndexesNewBuiltinReceiverType(t *testing.T) {
	idx, file := buildValueTypeFixture(t)

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"DefaultCache", "Read"})
	if !ok {
		t.Fatal("DefaultCache.Read was not resolved")
	}
	want := facts.SymbolID("method:example.com/value-types:Cache:Read")
	if resolved.ID != want || resolved.Confidence != facts.ConfidenceHigh {
		t.Fatalf("DefaultCache.Read = %#v, want %s with high confidence", resolved, want)
	}
}

func TestBuildIndexesTypedConstReceiverType(t *testing.T) {
	idx, file := buildValueTypeFixture(t)

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"DefaultCode", "String"})
	if !ok {
		t.Fatal("DefaultCode.String was not resolved")
	}
	want := facts.SymbolID("method:example.com/value-types:Code:String")
	if resolved.ID != want || resolved.Confidence != facts.ConfidenceHigh {
		t.Fatalf("DefaultCode.String = %#v, want %s with high confidence", resolved, want)
	}
}

func buildValueTypeFixture(t *testing.T) (*Index, *project.File) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/value-types\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package valuetypes

type Cache struct{}

func (*Cache) Read() {}

type Code string

func (Code) String() string { return "" }

var DefaultCache = new(Cache)

const DefaultCode Code = "default"
`
	if err := os.WriteFile(filepath.Join(root, "values.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	pkg := p.Packages[p.ModulePath]
	if pkg == nil || len(pkg.Files) != 1 {
		t.Fatalf("fixture files = %#v", pkg)
	}
	return idx, pkg.Files[0]
}

func mustSymbol(t *testing.T, idx *Index, id facts.SymbolID) facts.SymbolFact {
	t.Helper()
	symbol, ok := idx.Symbols[id]
	if !ok {
		t.Fatalf("symbol %s not found", id)
	}
	return symbol
}
