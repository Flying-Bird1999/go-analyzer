package graph

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
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

func TestEvidenceChainRecordsNodesAndEdges(t *testing.T) {
	chain := NewEvidenceChain("chain:service")
	chain.AddNode("symbol:service", "changed service method", facts.SourceSpan{File: "service/common.go", StartLine: 10, EndLine: 12})
	chain.AddNode("symbol:controller", "controller reference", facts.SourceSpan{File: "controller/common.go", StartLine: 20, EndLine: 22})
	chain.AddEdge("symbol:service", "symbol:controller", "referenced_by")

	if len(chain.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(chain.Nodes))
	}
	if len(chain.Edges) != 1 {
		t.Fatalf("edges = %d", len(chain.Edges))
	}
	if chain.Edges[0].Reason != "referenced_by" {
		t.Fatalf("reason = %q", chain.Edges[0].Reason)
	}
}
