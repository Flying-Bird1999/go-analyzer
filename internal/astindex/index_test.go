package astindex

import (
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

func mustSymbol(t *testing.T, idx *Index, id facts.SymbolID) facts.SymbolFact {
	t.Helper()
	symbol, ok := idx.Symbols[id]
	if !ok {
		t.Fatalf("symbol %s not found", id)
	}
	return symbol
}
