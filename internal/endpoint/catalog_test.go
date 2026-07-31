package endpoint

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestCatalogPreservesAnnotationDriftAndIndependentRouteAlias(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project::Handler")
	store := facts.NewStore("/project", "example.com/project")
	store.Annotations = []facts.AnnotationFact{{
		ID: "annotation:handler", Method: "POST", Path: "/public/orders", HandlerSymbol: handler,
	}}
	store.Routes = []facts.RouteRegistrationFact{
		{ID: "route:primary", Method: "POST", ResolvedPath: "/public/orders", HandlerSymbol: handler},
		{ID: "route:alias", Method: "POST", ResolvedPath: "/legacy/orders", HandlerSymbol: handler},
	}

	catalog := Build(facts.Freeze(store))
	if _, ok := catalog.Lookup(Key{Method: "POST", Path: "/public/orders"}); !ok {
		t.Fatal("annotation endpoint missing")
	}
	if alias, ok := catalog.Lookup(Key{Method: "POST", Path: "/legacy/orders"}); !ok || len(alias.Routes) != 2 {
		t.Fatalf("route alias missing or route evidence lost: %#v, %v", alias, ok)
	}
	if got := catalog.ForHandler(handler); len(got) != 2 {
		t.Fatalf("handler resolutions = %#v, want 2", got)
	}

	store.Annotations[0].Path = "/documented/orders"
	drifted := Build(facts.Freeze(store))
	if _, ok := drifted.Lookup(Key{Method: "POST", Path: "/legacy/orders"}); ok {
		t.Fatal("unclaimed route must not become an alias during annotation drift")
	}
	if _, ok := drifted.Lookup(Key{Method: "POST", Path: "/documented/orders"}); !ok {
		t.Fatal("drifted annotation endpoint missing")
	}
}

func TestCatalogMergesHandlersAndRoutesForSameEndpoint(t *testing.T) {
	first := facts.SymbolID("func:example.com/project::First")
	second := facts.SymbolID("func:example.com/project::Second")
	store := facts.NewStore("/project", "example.com/project")
	store.Annotations = []facts.AnnotationFact{
		{ID: "annotation:first", Method: "GET", Path: "/orders", HandlerSymbol: first},
		{ID: "annotation:second", Method: "GET", Path: "/orders", HandlerSymbol: second},
	}
	store.Routes = []facts.RouteRegistrationFact{
		{ID: "route:first", Method: "GET", ResolvedPath: "/orders", HandlerSymbol: first},
		{ID: "route:second", Method: "GET", ResolvedPath: "/v1/orders", HandlerSymbol: second},
	}

	entry, ok := Build(facts.Freeze(store)).Lookup(Key{Method: "GET", Path: "/orders"})
	if !ok || len(entry.Handlers) != 2 || len(entry.Routes) != 2 {
		t.Fatalf("merged entry = %#v, found=%v", entry, ok)
	}
}
