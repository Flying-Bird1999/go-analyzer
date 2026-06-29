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

	result := AnalyzeTrees(store, TreeOptions{})
	root := mustTreeRoot(t, result, "change:service")
	path := firstEndpointPath(t, root.Root)
	assertNodeKinds(t, path, "func", "func", "route", "annotation", "endpoint")
	endpoint := path[len(path)-1]
	if endpoint.Method != "GET" || endpoint.Path != "/api/bff-web/common/checkIn" {
		t.Fatalf("endpoint = %#v", endpoint)
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

	result := AnalyzeTrees(store, TreeOptions{})
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

	result := AnalyzeTrees(store, TreeOptions{})
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

	result := AnalyzeTrees(store, TreeOptions{})
	if len(result.Roots) != 2 {
		t.Fatalf("roots = %#v", result.Roots)
	}
	service := mustTreeRoot(t, result, "change:service")
	if len(service.Endpoints) != 2 {
		t.Fatalf("service endpoints = %#v", service.Endpoints)
	}
}

func TestAnalyzeStopsAtBoundary(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:service",
		Kind:     facts.ChangeKindSymbolChanged,
		SymbolID: serviceSymbol,
	})

	result := AnalyzeTrees(store, TreeOptions{StopPropagation: []string{"controller/**"}})
	root := mustTreeRoot(t, result, "change:service")
	controller := findTreeNode(t, root.Root, string(controllerSymbol))
	if !controller.StopBoundary {
		t.Fatalf("controller boundary = %#v", controller)
	}
	if len(controller.Children) != 0 {
		t.Fatalf("boundary children = %#v", controller.Children)
	}
	if len(root.Endpoints) != 0 {
		t.Fatalf("boundary endpoints = %#v", root.Endpoints)
	}
}

func TestAnalyzeHonorsMaxDepth(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:service",
		Kind:     facts.ChangeKindSymbolChanged,
		SymbolID: serviceSymbol,
	})

	result := AnalyzeTrees(store, TreeOptions{MaxDepth: 1})
	root := mustTreeRoot(t, result, "change:service")
	controller := findTreeNode(t, root.Root, string(controllerSymbol))
	if len(controller.Children) != 0 {
		t.Fatalf("depth-limited children = %#v", controller.Children)
	}
	assertTreeDiagnostic(t, result, "propagation_depth_truncated")
}

func TestAnalyzeFiltersDiagnosticsOutsideCurrentImpactTree(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:service",
		Kind:     facts.ChangeKindSymbolChanged,
		SymbolID: serviceSymbol,
		File:     "service/common.go",
	})
	store.Diagnostics = append(store.Diagnostics,
		facts.DiagnosticFact{
			ID:             "diagnostic:related",
			Code:           "symbol_reference_unresolved",
			RelatedFactIDs: []string{string(controllerSymbol)},
		},
		facts.DiagnosticFact{
			ID:             "diagnostic:unrelated",
			Code:           "symbol_reference_unresolved",
			RelatedFactIDs: []string{"func:example.com/project/unrelated::Load"},
			Span:           facts.SourceSpan{File: "unrelated/load.go"},
		},
	)

	result := AnalyzeTrees(store, TreeOptions{})
	assertTreeDiagnostic(t, result, "symbol_reference_unresolved")
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.ID == "diagnostic:unrelated" {
			t.Fatalf("unrelated diagnostic leaked into impact result: %#v", result.Diagnostics)
		}
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

	result := AnalyzeTrees(store, TreeOptions{})
	root := mustTreeRoot(t, result, "change:middleware-symbol")
	path := firstEndpointPath(t, root.Root)
	assertNodeKinds(t, path, "method", "middleware", "route", "endpoint")
	endpoint := path[len(path)-1]
	if endpoint.Method != "GET" || endpoint.Path != "/api/checkIn" {
		t.Fatalf("endpoint = %#v", endpoint)
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

func findTreeNode(t *testing.T, root Node, id string) Node {
	t.Helper()
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if got, ok := findTreeNodeOptional(child, id); ok {
			return got
		}
	}
	t.Fatalf("node %q not found: %#v", id, root)
	return Node{}
}

func findTreeNodeOptional(root Node, id string) (Node, bool) {
	if root.ID == id {
		return root, true
	}
	for _, child := range root.Children {
		if got, ok := findTreeNodeOptional(child, id); ok {
			return got, true
		}
	}
	return Node{}, false
}

func assertTreeDiagnostic(t *testing.T, result TreeResult, code string) {
	t.Helper()
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %q not found: %#v", code, result.Diagnostics)
}
