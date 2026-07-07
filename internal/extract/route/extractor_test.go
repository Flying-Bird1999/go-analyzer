// extractor_test.go 对 route 提取的核心场景进行端到端验证。
package route

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestExtractDirectRouteRegistration 验证直接在根组上注册的单条路由能被正确提取并解析出完整路径。
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
	if len(route.Evidence) != 1 {
		t.Fatalf("route evidence = %#v", route.Evidence)
	}
	if route.Evidence[0].Kind != "route_call" || route.Evidence[0].Raw == "" {
		t.Fatalf("route evidence = %#v", route.Evidence)
	}
}

// TestExtractWrapperStackAndFinalHandler 验证 handler 包装器栈被正确拆解，保留最内层 handler 与按外到内顺序的包装器。
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

// TestExtractRouteGroupAssignedThroughBuiltInWrapper 验证把组传入项目内包装器函数返回的新组上仍能注册路由。
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

// TestExtractRouteGroupAssignedThroughWrapperOfExistingGroup 验证对已存在组变量再套一层包装器得到的新组上注册的路由能被解析。
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

// TestExtractMiddlewareBindingStatementOrder 验证中间件绑定的语句序号位于其前后路由之间，且函数值与组级中间件均被记录。
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

// TestExtractGeneratedNestedRoute 验证代码生成风格 fixture 中嵌套组下的路由能被提取。
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

// TestExtractDynamicRoutePathKeepsRawExpression 验证无法静态解析的动态路径保留原始表达式文本并发出诊断。
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

// TestExtractUnresolvedHandlerEmitsDiagnostic 验证 handler 表达式无法精确解析时发出对应诊断。
func TestExtractUnresolvedHandlerEmitsDiagnostic(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "unresolved-handler")
	store := extractFixture(t, root)

	assertDiagnosticCode(t, store, "route_unresolved_handler")
}

// TestExtractRouteInsideIfFromMethodRouteFunc 验证方法形式路由函数的 if 分支内注册的路由能被提取并归属到该方法符号。
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

// TestExtractRouteInsideSwitchAndSelect 验证 switch 与 select 分支内注册的路由能被提取。
func TestExtractRouteInsideSwitchAndSelect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/route-branches\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

type Group struct{}

func (g *Group) GET(path string, handler any) {}
func CheckIn() {}
func Poll() {}

func Init(g *Group, mode string, done <-chan struct{}) {
	switch mode {
	case "check":
		g.GET("/checkIn", CheckIn)
	}
	select {
	case <-done:
		g.GET("/poll", Poll)
	default:
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	if len(store.Routes) != 2 {
		t.Fatalf("routes = %#v", store.Routes)
	}
	if findRoute(t, store, "/checkIn").HandlerRaw != "CheckIn" {
		t.Fatalf("check route = %#v", store.Routes)
	}
	if findRoute(t, store, "/poll").HandlerRaw != "Poll" {
		t.Fatalf("poll route = %#v", store.Routes)
	}
}

// TestExtractRecursivelyFindsLegoHandlerInsideBusinessWrapper 验证递归拆解多层业务包装器后能定位最内层 handler。
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

// TestExtractEmptyRoutePathResolvesToGroupPrefix 验证空字符串路径的路由会解析为所在组的前缀。
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

// TestExtractRouteGroupPathFromConstConcatenation 验证由多个常量拼接成的组路径能被还原为完整解析路径。
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

// TestUniqueRouteCallPrefixesDoesNotSeedIntermediateRouteFunctions 验证前缀传播不会把中间层路由函数当作终点播种，前缀正确穿透到最深层。
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

// TestExtractNestedRouteOrderBeforeFollowingTopLevelRoute 验证嵌套分支内的路由语句序号早于其后同函数的顶层路由。
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

// TestExtractRejectsNonLegoCamelCaseHTTPMethod 验证非 lego 风格的驼峰方法名（如 client.Get）不会被误识别为路由。
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

// TestExtractRejectsProjectAddFunctionReturningNonGroup 验证返回非路由组类型的 Add* 函数结果不会被当作路由组。
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

// TestExtractRejectsInlineProjectAddFunctionReturningNonGroup 验证内联调用返回非路由组类型的 Add* 函数不会被当作路由组。
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

// TestExtractRejectsUnresolvedExternalAddWrapper 验证来自项目外、无法解析的 Add* 包装器不会被当作路由组。
func TestExtractRejectsUnresolvedExternalAddWrapper(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/external-wrapper\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "router.go"), []byte(`package router

import guard "example.com/external/guard"

type RouterGroup struct{}

func Handler() {}

func Init(g *RouterGroup) {
	guard.AddCount(g).GET("/not-a-route", Handler)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixture(t, root)
	if len(store.Routes) != 0 {
		t.Fatalf("unresolved external AddCount result was parsed as a route group: %#v", store.Routes)
	}
}

// TestExtractUnwrapsParenthesizedHandler 验证被括号包裹的 handler 表达式仍能正确拆解到最内层 handler。
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

// extractFixture 加载 fixture 项目并运行 route 提取，返回填充好的 facts.Store。
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

// findRoute 按本地路径或解析路径查找并返回唯一路由，找不到则失败。
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

// assertDiagnosticCode 断言 store 中存在指定 code 的诊断。
func assertDiagnosticCode(t *testing.T, store *facts.Store, code string) {
	t.Helper()
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %s not found: %#v", code, store.Diagnostics)
}
