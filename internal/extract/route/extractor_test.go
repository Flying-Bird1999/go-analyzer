package route

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractDirectRouteRegistration(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "controller-wrapper")
	p, err := project.Load(root)
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

func TestExtractRouteGroupAssignedThroughBuiltInWrapper(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wrapped-route-group\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) Group(path string) *Group { return g }
func (g *Group) GET(path string, handler any) {}

func AddStaffFlowControl(g *Group) *Group { return g }
func GetConversations() {}

func Init(oldPathGroup *Group) {
	officialMsgRouter := AddStaffFlowControl(oldPathGroup.Group("/officialmsg/v1/admin"))
	officialMsgRouter.GET("/conversations", GetConversations)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	route := findRoute(t, store, "/officialmsg/v1/admin/conversations")
	if route.HandlerRaw != "GetConversations" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
	}
}

func TestExtractRouteGroupAssignedThroughWrapperOfExistingGroup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wrapped-existing-group\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) Group(path string) *Group { return g }
func (g *Group) GET(path string, handler any) {}

func AddReadGuard(g *Group) *Group { return g }
func GetStatistics() {}

func Init(root *Group) {
	saleGroup := root.Group("/live/sale/:salesId")
	guarded := AddReadGuard(saleGroup)
	guarded.GET("/statistics", GetStatistics)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	route := findRoute(t, store, "/live/sale/:salesId/statistics")
	if route.HandlerRaw != "GetStatistics" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
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

func TestExtractRouteInsideIfFromMethodRouteFunc(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/route-method\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) GET(path string, handler any) {}

type Router struct{}

func CheckIn() {}

func (r *Router) Init(g *Group) {
	if true {
		g.GET("/checkIn", CheckIn)
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	if len(store.Routes) != 1 {
		t.Fatalf("routes = %#v", store.Routes)
	}
	route := store.Routes[0]
	if route.LocalPath != "/checkIn" {
		t.Fatalf("route path = %q", route.LocalPath)
	}
	if route.RouteFunc != "method:example.com/route-method:Router:Init" {
		t.Fatalf("route func = %q", route.RouteFunc)
	}
}

func TestExtractRecursivelyFindsLegoHandlerInsideBusinessWrapper(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lego-wrapper\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) PUT(path string, handler any) {}

func ControllerWithReqResp(handler any) any { return handler }
func AppendDisplayMessageOnControllerWithReqRespHandler(handler any) any { return handler }

func UpdateCustomer() {}

func Init(g *Group) {
	g.PUT("/:customerId", ControllerWithReqResp(AppendDisplayMessageOnControllerWithReqRespHandler(UpdateCustomer)))
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	route := findRoute(t, store, "/:customerId")
	if route.HandlerRaw != "UpdateCustomer" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
	}
	if len(route.Wrappers) != 2 {
		t.Fatalf("wrappers = %#v", route.Wrappers)
	}
	if route.Wrappers[0].Name != "ControllerWithReqResp" || route.Wrappers[1].Name != "AppendDisplayMessageOnControllerWithReqRespHandler" {
		t.Fatalf("wrappers = %#v", route.Wrappers)
	}
}

func TestExtractEmptyRoutePathResolvesToGroupPrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/empty-route-path\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) Group(path string) *Group { return g }
func (g *Group) GET(path string, handler any) {}

func Handler() {}

func Init(g *Group) {
	api := g.Group("/api")
	api.GET("", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	route := findRoute(t, store, "/api")
	if route.LocalPath != "" {
		t.Fatalf("local path = %q", route.LocalPath)
	}
	if route.ResolvedPath != "/api" {
		t.Fatalf("resolved path = %q", route.ResolvedPath)
	}
}

func TestExtractRouteGroupPathFromConstConcatenation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/const-route-path\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

const BasePath = "/admin/api/bff-web/sc"
const baseChannelPath = BasePath + "/channel"

type Group struct{}

func (g *Group) Group(path string) *Group { return g }
func (g *Group) GET(path string, handler any) {}

func Handler() {}

func Init(g *Group) {
	countGroup := g.Group(baseChannelPath + "/count")
	countGroup.GET("/:type", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	route := findRoute(t, store, "/:type")
	if route.ResolvedPath != "/admin/api/bff-web/sc/channel/count/:type" {
		t.Fatalf("resolved path = %q", route.ResolvedPath)
	}
}

func TestUniqueRouteCallPrefixesDoesNotSeedIntermediateRouteFunctions(t *testing.T) {
	contexts := []routeCallContext{
		{
			caller: facts.SymbolID("func:example.com/router/app::InitAppAllRouter"),
			callee: facts.SymbolID("func:example.com/router/app/common::InitAppCommonRouter"),
			params: map[string]routeParamContext{
				"adminAppGroup": {
					prefix:        "",
					callerRootVar: "adminAppGroup",
				},
			},
		},
		{
			caller: facts.SymbolID("func:example.com/router::InitRouter"),
			callee: facts.SymbolID("func:example.com/router/app::InitAppAllRouter"),
			params: map[string]routeParamContext{
				"adminAppGroup": {
					prefix:        "/admin/api/bff-app",
					callerRootVar: "g",
				},
			},
		},
	}

	prefixes := uniqueRouteCallPrefixes(contexts)
	got := prefixes[facts.SymbolID("func:example.com/router/app/common::InitAppCommonRouter")]["adminAppGroup"]
	if got != "/admin/api/bff-app" {
		t.Fatalf("prefix = %q", got)
	}
}

func TestExtractNestedRouteOrderBeforeFollowingTopLevelRoute(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/route-order\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) GET(path string, handler any) {}

func Handler() {}

func Init(g *Group) {
	if true {
		g.GET("/inside", Handler)
	}
	g.GET("/after", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	inside := findRoute(t, store, "/inside")
	after := findRoute(t, store, "/after")
	if !(inside.StatementIndex < after.StatementIndex) {
		t.Fatalf("statement order inside=%d after=%d", inside.StatementIndex, after.StatementIndex)
	}
}

func TestExtractRejectsNonLegoCamelCaseHTTPMethod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/non-route-get\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client.go"), []byte(`package client

type Client struct{}

func Handler() {}

func Load(client *Client) {
	client.Get("/cache-key", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	if len(store.Routes) != 0 {
		t.Fatalf("camel-case client.Get was parsed as a route: %#v", store.Routes)
	}
}

func TestExtractRejectsProjectAddFunctionReturningNonGroup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/non-group-helper\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type RouterGroup struct{}

func Handler() {}

func AddCount(g *RouterGroup) int {
	return 1
}

func Init(g *RouterGroup) {
	value := AddCount(g)
	value.GET("/not-a-route", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	if len(store.Routes) != 0 {
		t.Fatalf("non-group AddCount result was parsed as a route group: %#v", store.Routes)
	}
}

func TestExtractRejectsInlineProjectAddFunctionReturningNonGroup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/non-group-inline-helper\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type RouterGroup struct{}
type Counter struct{}

func (Counter) GET(path string, handler any) {}

func Handler() {}

func AddCount(g *RouterGroup) Counter {
	return Counter{}
}

func Init(g *RouterGroup) {
	AddCount(g).GET("/not-a-route", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	if len(store.Routes) != 0 {
		t.Fatalf("inline non-group AddCount result was parsed as a route group: %#v", store.Routes)
	}
}

func TestExtractUnwrapsParenthesizedHandler(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/parenthesized-handler\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) GET(path string, handler any) {}
func ControllerWithResp(handler any) any { return handler }
func Handler() {}

func Init(g *Group) {
	g.GET("/orders", ControllerWithResp((Handler)))
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	route := findRoute(t, store, "/orders")
	if route.HandlerRaw != "Handler" {
		t.Fatalf("handler raw = %q", route.HandlerRaw)
	}
}

func extractFixture(t *testing.T, root string) *facts.Store {
	t.Helper()
	p, err := project.Load(root)
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
