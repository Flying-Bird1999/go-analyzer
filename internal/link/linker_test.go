package link

import (
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	annotationextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestResolveFunctionHandlerSymbol(t *testing.T) {
	p, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "controller-wrapper"))
	_ = p
	got, ok := ResolveHandlerSymbol(idx, store.Routes[0])
	if !ok {
		t.Fatal("handler did not resolve")
	}
	want := facts.SymbolID("func:example.com/controller-wrapper/controller::CheckIn")
	if got != want {
		t.Fatalf("handler symbol = %q, want %q", got, want)
	}
}

func TestResolvePackageVarMethodHandlerSymbol(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "handler-method-var"))
	got, ok := ResolveHandlerSymbol(idx, store.Routes[0])
	if !ok {
		t.Fatal("handler did not resolve")
	}
	want := facts.SymbolID("method:example.com/handler-method-var/controller/uc:merchantSettingApi:UpdateSubMerchantSettingByCode")
	if got != want {
		t.Fatalf("handler symbol = %q, want %q", got, want)
	}
}

func TestRunLinksRouteHandlerAndAnnotation(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "route-annotation-link"))

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	if got := store.Routes[0].HandlerSymbol; got != "func:example.com/route-annotation-link/controller::CheckIn" {
		t.Fatalf("route handler symbol = %q", got)
	}
	if len(store.Links) != 2 {
		t.Fatalf("links = %d: %#v", len(store.Links), store.Links)
	}
	kinds := map[facts.LinkKind]bool{}
	for _, link := range store.Links {
		kinds[link.Kind] = true
	}
	if !kinds[facts.LinkKindRouteToHandler] {
		t.Fatal("missing route_to_handler link")
	}
	if !kinds[facts.LinkKindHandlerToAnnotation] {
		t.Fatal("missing handler_to_annotation link")
	}
}

func loadAndExtract(t *testing.T, root string) (*project.Project, *astindex.Index, *facts.Store) {
	t.Helper()
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
	if err := annotationextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if err := routeextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	return p, idx, store
}
