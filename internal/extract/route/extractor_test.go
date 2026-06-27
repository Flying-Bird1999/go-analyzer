package route

import (
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractDirectRouteRegistration(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "controller-wrapper")
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	if len(store.RouteGroups) != 1 {
		t.Fatalf("route groups = %d", len(store.RouteGroups))
	}
	if len(store.Routes) != 1 {
		t.Fatalf("routes = %d", len(store.Routes))
	}
	route := store.Routes[0]
	if route.Method != "POST" {
		t.Fatalf("method = %q", route.Method)
	}
	if route.LocalPath != "/checkIn" {
		t.Fatalf("local path = %q", route.LocalPath)
	}
	if route.ResolvedPath != "/api/bff-web/common/checkIn" {
		t.Fatalf("resolved path = %q", route.ResolvedPath)
	}
	if route.HandlerRaw != "common.CheckIn" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
	}
}

func TestExtractWrapperStackAndFinalHandler(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "route-wrapper")
	store := extractFixture(t, root)

	if len(store.Routes) != 2 {
		t.Fatalf("routes = %d", len(store.Routes))
	}
	wrapped := findRoute(t, store, "/wrapped")
	if wrapped.HandlerRaw != "common.CheckIn" {
		t.Fatalf("wrapped handler raw = %q", wrapped.HandlerRaw)
	}
	if len(wrapped.Wrappers) != 2 {
		t.Fatalf("wrapper count = %d", len(wrapped.Wrappers))
	}
	if wrapped.Wrappers[0].Name != "MiddlewareController" || wrapped.Wrappers[1].Name != "ControllerWithResp" {
		t.Fatalf("wrappers = %#v", wrapped.Wrappers)
	}

	guarded := findRoute(t, store, "/guarded")
	if guarded.HandlerRaw != "common.CheckIn" {
		t.Fatalf("guarded handler raw = %q", guarded.HandlerRaw)
	}
	if len(guarded.Wrappers) != 1 || guarded.Wrappers[0].Name != "Guard" {
		t.Fatalf("guarded wrappers = %#v", guarded.Wrappers)
	}
}

func TestExtractMiddlewareBindingStatementOrder(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "middleware-order")
	store := extractFixture(t, root)

	if len(store.Middleware) != 3 {
		t.Fatalf("middleware bindings = %d", len(store.Middleware))
	}
	before := findRoute(t, store, "/a")
	after := findRoute(t, store, "/b")
	binding := store.Middleware[0]
	if !(before.StatementIndex < binding.StatementIndex && binding.StatementIndex < after.StatementIndex) {
		t.Fatalf("statement order before=%d middleware=%d after=%d", before.StatementIndex, binding.StatementIndex, after.StatementIndex)
	}
	if binding.MiddlewareRaw != "Auth()" {
		t.Fatalf("middleware raw = %q", binding.MiddlewareRaw)
	}
	if store.Middleware[1].MiddlewareRaw != "h1" {
		t.Fatalf("function-value middleware raw = %q", store.Middleware[1].MiddlewareRaw)
	}
	groupBinding := store.Middleware[2]
	if groupBinding.MiddlewareRaw != "Audit()" {
		t.Fatalf("group middleware raw = %q", groupBinding.MiddlewareRaw)
	}
}

func TestExtractGeneratedNestedRoute(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "generated-nexus")
	store := extractFixture(t, root)

	route := findRoute(t, store, "/generated/checkIn")
	if route.Method != "GET" {
		t.Fatalf("method = %q", route.Method)
	}
	if route.HandlerRaw != "common.CheckIn" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
	}
	if route.SourceFamily != "generated" {
		t.Fatalf("source family = %q", route.SourceFamily)
	}
}

func TestExtractDynamicRoutePathKeepsRawExpression(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "dynamic-route-path")
	store := extractFixture(t, root)

	if len(store.Routes) != 1 {
		t.Fatalf("routes = %d", len(store.Routes))
	}
	route := store.Routes[0]
	if route.LocalPath != "" {
		t.Fatalf("local path = %q", route.LocalPath)
	}
	if route.ResolvedPath != "" {
		t.Fatalf("resolved path = %q", route.ResolvedPath)
	}
	if route.PathRaw != `"/api" + suffix` {
		t.Fatalf("path raw = %q", route.PathRaw)
	}
	assertDiagnosticCode(t, store, "route_dynamic_path")
}

func TestExtractUnresolvedHandlerEmitsDiagnostic(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "unresolved-handler")
	store := extractFixture(t, root)

	assertDiagnosticCode(t, store, "route_unresolved_handler")
}

func TestExtractUsesConfiguredRouteRules(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "configurable-rules")
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	cfg := config.Default()
	cfg.Route.HTTPMethods = append(cfg.Route.HTTPMethods, "SEARCH")
	cfg.Route.HandlerWrappers = append(cfg.Route.HandlerWrappers, "CustomController")
	cfg.Route.RouteGroupWrappers = append(cfg.Route.RouteGroupWrappers, config.WrapperRule{Contains: "Shield"})

	if err := ExtractWithConfig(p, idx, store, cfg); err != nil {
		t.Fatal(err)
	}

	if len(store.Routes) != 1 {
		t.Fatalf("routes = %d: %#v", len(store.Routes), store.Routes)
	}
	route := store.Routes[0]
	if route.Method != "SEARCH" {
		t.Fatalf("method = %q", route.Method)
	}
	if route.HandlerRaw != "common.CheckIn" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
	}
	if len(route.Wrappers) != 2 || route.Wrappers[0].Name != "TenantShield" || route.Wrappers[1].Name != "CustomController" {
		t.Fatalf("wrappers = %#v", route.Wrappers)
	}
}

func extractFixture(t *testing.T, root string) *facts.Store {
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
	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	return store
}

func findRoute(t *testing.T, store *facts.Store, path string) facts.RouteRegistrationFact {
	t.Helper()
	for _, route := range store.Routes {
		if route.LocalPath == path || route.ResolvedPath == path {
			return route
		}
	}
	t.Fatalf("route %s not found: %#v", path, store.Routes)
	return facts.RouteRegistrationFact{}
}

func assertDiagnosticCode(t *testing.T, store *facts.Store, code string) {
	t.Helper()
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %s not found: %#v", code, store.Diagnostics)
}
