package impact

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestAnalyzeBuildsCompleteSymbolToEndpointTree(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:service",
		Kind:       facts.ChangeKindSymbolChanged,
		SymbolID:   serviceSymbol,
		File:       "service/common.go",
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:service")
	path := firstEndpointPath(t, root.Root)
	assertNodeKinds(t, path, "func", "func", "route", "annotation", "endpoint")
	endpoint := path[len(path)-1]
	if endpoint.Method != "GET" || endpoint.Path != "/api/bff-web/common/checkIn" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
}

func TestAnalyzeBuildsEndpointAndIMEventExitsFromSamePath(t *testing.T) {
	store := referenceImpactStore()
	store.IMEvents = append(store.IMEvents,
		facts.IMEventFact{
			ID:           "im_event:check_in",
			Event:        "check_in",
			SenderSymbol: controllerSymbol,
			Dependencies: []facts.IMEventDependency{{
				SymbolID:   serviceSymbol,
				Relation:   facts.IMRelationPayload,
				Confidence: facts.ConfidenceHigh,
			}},
			Confidence: facts.ConfidenceHigh,
			Resolved:   true,
		},
		facts.IMEventFact{
			ID:           "im_event:dynamic",
			EventRaw:     "event",
			SenderSymbol: controllerSymbol,
			Dependencies: []facts.IMEventDependency{{
				SymbolID:   serviceSymbol,
				Relation:   facts.IMRelationPayload,
				Confidence: facts.ConfidenceHigh,
			}},
			Confidence: facts.ConfidenceHigh,
			Resolved:   false,
		},
	)
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:service",
		Kind:       facts.ChangeKindSymbolChanged,
		SymbolID:   serviceSymbol,
		File:       "service/common.go",
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:service")
	if len(root.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
	if len(root.IMEvents) != 1 || root.IMEvents[0].Event != "check_in" {
		t.Fatalf("im events = %#v", root.IMEvents)
	}
	if !containsNodeKind(root.Root, "im_event") {
		t.Fatalf("resolved IM event node missing: %#v", root.Root)
	}
	if !containsNodeKind(root.Root, "im_event_unresolved") {
		t.Fatalf("unresolved IM event node missing: %#v", root.Root)
	}
}

func TestAnalyzePrefersChangedRouteDomainRootOverHandlerSymbol(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:route",
		Kind:     facts.ChangeKindRouteChanged,
		TargetID: "route:checkIn",
		SymbolID: controllerSymbol,
		File:     "router/router.go",
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:route")
	if root.Root.Kind != "route" || root.Root.ID != "route:checkIn" {
		t.Fatalf("route root = %#v", root.Root)
	}
}

func TestAnalyzeMarksCycles(t *testing.T) {
	store := referenceImpactStore()
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:service-controller",
		Kind:       facts.ReferenceKindCall,
		FromSymbol: serviceSymbol,
		ToSymbol:   controllerSymbol,
		Confidence: facts.ConfidenceHigh,
	})
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:service",
		Kind:     facts.ChangeKindSymbolChanged,
		SymbolID: serviceSymbol,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:service")
	if !containsCycle(root.Root) {
		t.Fatalf("cycle marker not found: %#v", root.Root)
	}
}

func TestAnalyzeKeepsMultipleEndpointsAndSeparateRoots(t *testing.T) {
	store := referenceImpactStore()
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:            "route:second",
		Method:        "POST",
		ResolvedPath:  "/second",
		HandlerSymbol: controllerSymbol,
		Span:          facts.SourceSpan{File: "router/router.go", StartLine: 21, EndLine: 21},
	})
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:second",
		Method:        "POST",
		Path:          "/second",
		HandlerSymbol: controllerSymbol,
		Span:          facts.SourceSpan{File: "controller/common.go", StartLine: 8, EndLine: 8},
	})
	store.Changes = append(store.Changes,
		facts.ChangeFact{ID: "change:service", Kind: facts.ChangeKindSymbolChanged, SymbolID: serviceSymbol},
		facts.ChangeFact{ID: "change:controller", Kind: facts.ChangeKindSymbolChanged, SymbolID: controllerSymbol},
	)

	result := AnalyzeTrees(store)
	if len(result.Roots) != 2 {
		t.Fatalf("roots = %#v", result.Roots)
	}
	service := mustTreeRoot(t, result, "change:service")
	if len(service.Endpoints) != 2 {
		t.Fatalf("service endpoints = %#v", service.Endpoints)
	}
}

func TestAnalyzePropagatesMiddlewareSymbolToEndpoint(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	middlewareSymbol := facts.SymbolID("method:example.com/project/auth:Auth:Middleware")
	store.Symbols = append(store.Symbols, facts.SymbolFact{
		ID:          middlewareSymbol,
		Kind:        "method",
		Name:        "Middleware",
		PackagePath: "example.com/project/auth",
		Receiver:    "Auth",
		Span:        facts.SourceSpan{File: "auth/auth.go", StartLine: 10, EndLine: 12},
	})
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:                "middleware:auth",
		GroupID:           "route_group:api",
		GroupVar:          "api",
		MiddlewareRaw:     "auth.Default.Middleware",
		MiddlewareSymbols: []facts.SymbolID{middlewareSymbol},
		StatementIndex:    10,
		Span:              facts.SourceSpan{File: "router/router.go", StartLine: 20, EndLine: 20},
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:             "route:checkIn",
		Method:         "GET",
		ResolvedPath:   "/api/checkIn",
		GroupID:        "route_group:api",
		GroupVar:       "api",
		StatementIndex: 11,
		Span:           facts.SourceSpan{File: "router/router.go", StartLine: 21, EndLine: 21},
	})
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:middleware-symbol",
		Kind:       facts.ChangeKindSymbolChanged,
		SymbolID:   middlewareSymbol,
		File:       "auth/auth.go",
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:middleware-symbol")
	path := firstEndpointPath(t, root.Root)
	assertNodeKinds(t, path, "method", "middleware", "route", "endpoint")
	endpoint := path[len(path)-1]
	if endpoint.Method != "GET" || endpoint.Path != "/api/checkIn" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
}

func TestAnalyzePropagatesRouteScopedDependencyToOnlyItsRoute(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	guard := facts.SymbolID("func:example.com/project/router::Guard")
	routeFunc := facts.SymbolID("func:example.com/project/router::InitRouter")
	store.Symbols = append(store.Symbols,
		facts.SymbolFact{ID: guard, Kind: "func", Span: facts.SourceSpan{File: "router/router.go", StartLine: 10, EndLine: 10}},
		facts.SymbolFact{ID: routeFunc, Kind: "func", Span: facts.SourceSpan{File: "router/router.go", StartLine: 15, EndLine: 22}},
	)
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:guard",
		Kind:       facts.ReferenceKindCall,
		FromSymbol: routeFunc,
		ToSymbol:   guard,
		Confidence: facts.ConfidenceHigh,
		Span:       facts.SourceSpan{File: "router/router.go", StartLine: 20, StartCol: 2, EndLine: 20, EndCol: 10},
	})
	store.Routes = append(store.Routes,
		facts.RouteRegistrationFact{
			ID:           "route:guarded",
			Method:       "GET",
			ResolvedPath: "/guarded",
			RouteFunc:    routeFunc,
			Span:         facts.SourceSpan{File: "router/router.go", StartLine: 20, StartCol: 2, EndLine: 20, EndCol: 42},
		},
		facts.RouteRegistrationFact{
			ID:           "route:plain",
			Method:       "GET",
			ResolvedPath: "/plain",
			RouteFunc:    routeFunc,
			Span:         facts.SourceSpan{File: "router/router.go", StartLine: 21, StartCol: 2, EndLine: 21, EndCol: 35},
		},
	)
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:guard",
		Kind:       facts.ChangeKindSymbolChanged,
		SymbolID:   guard,
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:guard")
	if len(root.Endpoints) != 1 || root.Endpoints[0].Path != "/guarded" {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
}

const (
	serviceSymbol    facts.SymbolID = "func:example.com/project/service::CheckIn"
	controllerSymbol facts.SymbolID = "func:example.com/project/controller::CheckIn"
)

func referenceImpactStore() *facts.Store {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols,
		facts.SymbolFact{ID: serviceSymbol, Kind: "func", Span: facts.SourceSpan{File: "service/common.go", StartLine: 1, EndLine: 3}},
		facts.SymbolFact{ID: controllerSymbol, Kind: "func", Span: facts.SourceSpan{File: "controller/common.go", StartLine: 10, EndLine: 14}},
	)
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:controller-service",
		Kind:       facts.ReferenceKindCall,
		FromSymbol: controllerSymbol,
		ToSymbol:   serviceSymbol,
		Confidence: facts.ConfidenceHigh,
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:            "route:checkIn",
		Method:        "GET",
		LocalPath:     "/checkIn",
		HandlerSymbol: controllerSymbol,
		Span:          facts.SourceSpan{File: "router/router.go", StartLine: 20, EndLine: 20},
	})
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:checkIn",
		Method:        "GET",
		Path:          "/api/bff-web/common/checkIn",
		HandlerSymbol: controllerSymbol,
		Span:          facts.SourceSpan{File: "controller/common.go", StartLine: 9, EndLine: 9},
	})
	return store
}

func mustTreeRoot(t *testing.T, result TreeResult, changeID string) RootImpact {
	t.Helper()
	for _, root := range result.Roots {
		if root.Change.ID == changeID {
			return root
		}
	}
	t.Fatalf("root %q not found: %#v", changeID, result.Roots)
	return RootImpact{}
}

func firstEndpointPath(t *testing.T, root Node) []Node {
	t.Helper()
	var visit func(Node, []Node) []Node
	visit = func(node Node, path []Node) []Node {
		path = append(path, node)
		if node.Kind == "endpoint" {
			return path
		}
		for _, child := range node.Children {
			if got := visit(child, path); len(got) > 0 {
				return got
			}
		}
		return nil
	}
	got := visit(root, nil)
	if len(got) == 0 {
		t.Fatalf("endpoint path not found: %#v", root)
	}
	return got
}

func assertNodeKinds(t *testing.T, nodes []Node, want ...string) {
	t.Helper()
	if len(nodes) != len(want) {
		t.Fatalf("node path length = %d, want %d: %#v", len(nodes), len(want), nodes)
	}
	for i := range want {
		if nodes[i].Kind != want[i] {
			t.Fatalf("node %d kind = %q, want %q: %#v", i, nodes[i].Kind, want[i], nodes)
		}
	}
}

func containsCycle(node Node) bool {
	if node.Cycle {
		return true
	}
	for _, child := range node.Children {
		if containsCycle(child) {
			return true
		}
	}
	return false
}

func containsNodeKind(node Node, kind string) bool {
	if node.Kind == kind {
		return true
	}
	for _, child := range node.Children {
		if containsNodeKind(child, kind) {
			return true
		}
	}
	return false
}
