package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestReverseGraphLookupByTarget(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:controller-service",
		Kind:       facts.ReferenceKindCall,
		FromSymbol: "func:example.com/project/controller::CheckIn",
		ToSymbol:   "func:example.com/project/service::WebApiForwardGray",
		Confidence: facts.ConfidenceHigh,
	})

	g := NewReverseGraph(store)
	refs := g.ReferencesTo("func:example.com/project/service::WebApiForwardGray")
	if len(refs) != 1 {
		t.Fatalf("refs = %d", len(refs))
	}
	if refs[0].FromSymbol != "func:example.com/project/controller::CheckIn" {
		t.Fatalf("from = %q", refs[0].FromSymbol)
	}
}

func TestRouteGraphMiddlewareAffectsOnlyLaterRoutes(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Routes = append(store.Routes,
		facts.RouteRegistrationFact{ID: "route:a", GroupVar: "g", StatementIndex: 1},
		facts.RouteRegistrationFact{ID: "route:b", GroupVar: "g", StatementIndex: 3},
	)
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:             "middleware:auth",
		GroupVar:       "g",
		StatementIndex: 2,
	})

	g := NewRouteGraph(store)
	affected := g.RoutesAffectedByMiddleware("middleware:auth")
	if len(affected) != 1 {
		t.Fatalf("affected routes = %d", len(affected))
	}
	if affected[0].ID != "route:b" {
		t.Fatalf("affected route = %q", affected[0].ID)
	}
}

func TestRouteGraphScopesGroupsByRouteFunction(t *testing.T) {
	store := extractAndLinkFixture(t, "group-scope")
	graph := NewRouteGraph(store)

	var bindingID string
	for _, binding := range store.Middleware {
		if strings.HasSuffix(string(binding.RouteFunc), "::InitA") {
			bindingID = binding.ID
			break
		}
	}
	if bindingID == "" {
		t.Fatalf("InitA middleware not found: %#v", store.Middleware)
	}

	routes := graph.RoutesAffectedByMiddleware(bindingID)
	if len(routes) != 1 || routes[0].ResolvedPath != "/a/one" {
		t.Fatalf("affected routes = %#v", routes)
	}
}

func TestRouteGraphIncludesDescendantGroupRoutes(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.RouteGroups = append(store.RouteGroups,
		facts.RouteGroupFact{ID: "group:parent", GroupVar: "parent"},
		facts.RouteGroupFact{ID: "group:child", GroupVar: "child", ParentGroupID: "group:parent"},
	)
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:      "route:child",
		GroupID: "group:child",
	})

	graph := NewRouteGraph(store)
	routes := graph.RoutesForGroup("group:parent")
	if len(routes) != 1 || routes[0].ID != "route:child" {
		t.Fatalf("descendant routes = %#v", routes)
	}
}

func extractAndLinkFixture(t *testing.T, fixture string) *facts.Store {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "fixtures", fixture)
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	if err := routeextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if err := link.Run(idx, store); err != nil {
		t.Fatal(err)
	}
	return store
}
