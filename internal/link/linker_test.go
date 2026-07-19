// linker_test.go 验证 handler 与 middleware 符号解析，以及 Run 建立的 route-handler-annotation
// 与 middleware 符号关联，覆盖普通函数、包级 var 方法、constructor 初始化、struct field 等常见 BFF 写法。

package link

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	annotationextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestResolveFunctionHandlerSymbol 验证普通函数型 handler 能解析到 func 符号。
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

// TestResolvePackageVarMethodHandlerSymbol 验证包级变量上的方法（handler-method-var fixture）能解析到 method 符号。
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

// TestResolveLocalPackageVarMethodHandlerSymbol 验证同包内包级变量上的方法（Var.Method）能解析到 method 符号。
func TestResolveLocalPackageVarMethodHandlerSymbol(t *testing.T) {
	root := t.TempDir()
	writeLinkTestFile(t, root, "go.mod", "module example.com/local-handler\n\ngo 1.24\n")
	writeLinkTestFile(t, root, "router/router.go", `package router

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}

type API struct{}

var HandlerAPI API

func (API) List() {}

func Init(g *RouterGroup) {
	g.GET("/orders", HandlerAPI.List)
}
`)
	_, idx, store := loadAndExtract(t, root)
	if len(store.Routes) != 1 {
		t.Fatalf("routes = %#v", store.Routes)
	}

	got, ok := ResolveHandlerSymbol(idx, store.Routes[0])
	if !ok {
		t.Fatal("handler did not resolve")
	}
	want := facts.SymbolID("method:example.com/local-handler/router:API:List")
	if got != want {
		t.Fatalf("handler symbol = %q, want %q", got, want)
	}
}

// TestRunLinksRouteHandlerAndAnnotation 验证 Run 写回 route 的 handler 符号，并生成 route_to_handler 与 handler_to_annotation 关联。
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

// TestLinkRouteAcrossCallsDoesNotDuplicateHandlerToAnnotation 验证多次独立调用
// LinkRoute（而非单次 Run 内部批量遍历）时，共享同一 handler 的多条 route 不会
// 产生重复的 handler_to_annotation LinkFact。deleted_route.go 会对同一 store 多次
// 调用 LinkRoute（每条恢复出来的路由各调用一次；relinkUnresolvedRoutesForDeletedHandler
// 也逐条路由调用），修复前每次调用都从空白 linkedHandlers 出发，重复生成同 ID 的
// handler_to_annotation 记录；facts.Store 对 Links 不做去重，重复记录会直接进入
// facts JSON 的 links 数组。
func TestLinkRouteAcrossCallsDoesNotDuplicateHandlerToAnnotation(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "route-annotation-link"))

	route1 := store.Routes[0]
	route1.ID = "route:shared:1"
	route2 := store.Routes[0]
	route2.ID = "route:shared:2"

	// 模拟 deleted_route.go 的调用模式：对同一 store 分两次独立调用包级 LinkRoute，
	// 而非通过 Run 在一次批量遍历内共享 linkedHandlers。
	if !LinkRoute(idx, store, &route1) {
		t.Fatal("first LinkRoute call failed to resolve handler")
	}
	if !LinkRoute(idx, store, &route2) {
		t.Fatal("second LinkRoute call failed to resolve handler")
	}

	handlerToAnnotationCount := 0
	seenIDs := map[string]int{}
	for _, link := range store.Links {
		seenIDs[link.ID]++
		if link.Kind == facts.LinkKindHandlerToAnnotation {
			handlerToAnnotationCount++
		}
	}
	if handlerToAnnotationCount != 1 {
		t.Fatalf("handler_to_annotation links = %d, want 1 (shared handler across 2 routes must dedupe): %#v", handlerToAnnotationCount, store.Links)
	}
	for id, count := range seenIDs {
		if count > 1 {
			t.Fatalf("link ID %q duplicated %d times in store.Links", id, count)
		}
	}
}

// TestRunLinksMiddlewareSymbols 验证 Run 能把多种中间件原始表达式解析为符号。
func TestRunLinksMiddlewareSymbols(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "middleware-order"))

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	assertMiddlewareSymbol(t, store, "Auth()", "func:example.com/middleware-order/router::Auth")
	assertMiddlewareSymbol(t, store, "h1", "func:example.com/middleware-order/router::h1")
	assertMiddlewareSymbol(t, store, "Audit()", "func:example.com/middleware-order/router::Audit")
}

// TestRunLinksConstructorInitializedPackageVarMiddlewareSymbol 验证 constructor 初始化的包级 var 的方法（auth.Default.Middleware）能解析为 method 符号。
func TestRunLinksConstructorInitializedPackageVarMiddlewareSymbol(t *testing.T) {
	root := t.TempDir()
	writeLinkTestFile(t, root, "go.mod", "module example.com/constructor-middleware\n\ngo 1.24\n")
	writeLinkTestFile(t, root, "auth/auth.go", `package auth

var Default = NewAuth()

type Auth struct{}

func NewAuth() *Auth {
	return &Auth{}
}

func (a *Auth) Middleware() {}
`)
	writeLinkTestFile(t, root, "router/router.go", `package router

import auth "example.com/constructor-middleware/auth"

type RouterGroup struct{}

func (g *RouterGroup) Use(middleware any) {}
func (g *RouterGroup) GET(path string, handler any) {}

func Handler() {}

func InitRouter(g *RouterGroup) {
	g.Use(auth.Default.Middleware)
	g.GET("/x", Handler)
}
`)
	_, idx, store := loadAndExtract(t, root)

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	assertMiddlewareSymbol(t, store, "auth.Default.Middleware", "method:example.com/constructor-middleware/auth:Auth:Middleware")
}

// TestRunLinksPackageVarWithImportedExplicitTypeMiddlewareSymbol 验证显式导入类型的包级 var（provider.Default，类型为 auth.Auth）的方法能跨包解析。
func TestRunLinksPackageVarWithImportedExplicitTypeMiddlewareSymbol(t *testing.T) {
	root := t.TempDir()
	writeLinkTestFile(t, root, "go.mod", "module example.com/imported-type-middleware\n\ngo 1.24\n")
	writeLinkTestFile(t, root, "auth/auth.go", `package auth

type Auth struct{}

func (a *Auth) Middleware() {}
`)
	writeLinkTestFile(t, root, "provider/provider.go", `package provider

import "example.com/imported-type-middleware/auth"

var Default auth.Auth
`)
	writeLinkTestFile(t, root, "router/router.go", `package router

import provider "example.com/imported-type-middleware/provider"

type RouterGroup struct{}

func (g *RouterGroup) Use(middleware any) {}
func (g *RouterGroup) GET(path string, handler any) {}

func Handler() {}

func InitRouter(g *RouterGroup) {
	g.Use(provider.Default.Middleware)
	g.GET("/x", Handler)
}
`)
	_, idx, store := loadAndExtract(t, root)

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	assertMiddlewareSymbol(t, store, "provider.Default.Middleware", "method:example.com/imported-type-middleware/auth:Auth:Middleware")
}

// TestRunLinksPackageVarStructFieldMiddlewareSymbol 验证包级 var 的多层 struct field（provider.Default.Auth.Middleware）能解析为 method 符号。
func TestRunLinksPackageVarStructFieldMiddlewareSymbol(t *testing.T) {
	root := t.TempDir()
	writeLinkTestFile(t, root, "go.mod", "module example.com/struct-field-middleware\n\ngo 1.24\n")
	writeLinkTestFile(t, root, "auth/auth.go", `package auth

type Auth struct{}

func (a *Auth) Middleware() {}
`)
	writeLinkTestFile(t, root, "provider/provider.go", `package provider

import "example.com/struct-field-middleware/auth"

type Dependencies struct {
	Auth auth.Auth
}

var Default = Dependencies{}
`)
	writeLinkTestFile(t, root, "router/router.go", `package router

import provider "example.com/struct-field-middleware/provider"

type RouterGroup struct{}

func (g *RouterGroup) Use(middleware any) {}
func (g *RouterGroup) GET(path string, handler any) {}

func Handler() {}

func InitRouter(g *RouterGroup) {
	g.Use(provider.Default.Auth.Middleware)
	g.GET("/x", Handler)
}
`)
	_, idx, store := loadAndExtract(t, root)

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	assertMiddlewareSymbol(t, store, "provider.Default.Auth.Middleware", "method:example.com/struct-field-middleware/auth:Auth:Middleware")
}

// assertMiddlewareSymbol 断言指定原始表达式的中间件绑定解析到期望符号。
func assertMiddlewareSymbol(t *testing.T, store *facts.Store, raw string, want facts.SymbolID) {
	t.Helper()
	for _, binding := range store.Middleware {
		if binding.MiddlewareRaw != raw {
			continue
		}
		for _, symbol := range binding.MiddlewareSymbols {
			if symbol == want {
				return
			}
		}
		t.Fatalf("middleware %q symbols = %#v, want %q", raw, binding.MiddlewareSymbols, want)
	}
	t.Fatalf("middleware %q not found: %#v", raw, store.Middleware)
}

// loadAndExtract 加载 fixture 并构建到含符号/注解/路由的 facts.Store，供 linker 测试使用。
func loadAndExtract(t *testing.T, root string) (*project.Project, *astindex.Index, *facts.Store) {
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

// writeLinkTestFile 在测试临时目录下写入指定相对路径的文件，自动创建父目录。
func writeLinkTestFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
