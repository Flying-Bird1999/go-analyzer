package output

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestRenderJSONIsDeterministicAndSorted(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
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

	first, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("json output is not deterministic")
	}

	var doc Document
	if err := json.Unmarshal(first, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Project.ModulePath != "example.com/mini-bff" {
		t.Fatalf("module path = %q", doc.Project.ModulePath)
	}
	if len(doc.Symbols) == 0 {
		t.Fatal("expected symbols in json output")
	}
	ids := make([]string, 0, len(doc.Symbols))
	for _, symbol := range doc.Symbols {
		ids = append(ids, string(symbol.ID))
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("symbols are not sorted: %v", ids)
	}
}

func TestRenderJSONSortsReferencesAndLinks(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.References = append(store.References,
		facts.ReferenceFact{ID: "ref:b", FromSymbol: "func:example.com/project::B", ToSymbol: "func:example.com/project::A"},
		facts.ReferenceFact{ID: "ref:a", FromSymbol: "func:example.com/project::A", ToSymbol: "func:example.com/project::B"},
	)
	store.Links = append(store.Links,
		facts.LinkFact{ID: "link:b", FromID: "route:b", ToID: "func:b"},
		facts.LinkFact{ID: "link:a", FromID: "route:a", ToID: "func:a"},
	)

	out, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}

	var doc Document
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.References[0].ID; got != "ref:a" {
		t.Fatalf("first reference id = %q", got)
	}
	if got := doc.Links[0].ID; got != "link:a" {
		t.Fatalf("first link id = %q", got)
	}
}
