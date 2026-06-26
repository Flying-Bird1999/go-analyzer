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
