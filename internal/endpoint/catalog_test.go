package endpoint

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// TestCatalogAnnotationClaimsEveryRouteOfTheHandler 锁定接口身份口径：
// 只要 handler 上有接口注释，注释就是这个 handler 唯一的对外身份来源——
// 注释没覆盖到的路由不再自成一个接口，只作为注册证据留在 Routes 里。
func TestCatalogAnnotationClaimsEveryRouteOfTheHandler(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project::Handler")
	store := facts.NewStore("/project", "example.com/project")
	store.Annotations = []facts.AnnotationFact{{
		ID: "annotation:handler", Method: "POST", Path: "/public/orders", HandlerSymbol: handler,
	}}
	store.Routes = []facts.RouteRegistrationFact{
		{ID: "route:primary", Method: "POST", ResolvedPath: "/public/orders", HandlerSymbol: handler},
		{ID: "route:uncovered", Method: "POST", ResolvedPath: "/legacy/orders", HandlerSymbol: handler},
	}

	catalog := Build(facts.Freeze(store))
	entries := catalog.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want exactly 1 annotation-backed endpoint", entries)
	}
	if entries[0].Endpoint != (Key{Method: "POST", Path: "/public/orders"}) {
		t.Errorf("endpoint = %#v, want the annotated identity", entries[0].Endpoint)
	}
	// 两条注册证据都必须保留，否则回归范围会漏掉 /legacy/orders 这条真实可调用路径。
	if len(entries[0].Routes) != 2 {
		t.Errorf("routes = %#v, want both registrations as evidence", entries[0].Routes)
	}
	if got := catalog.ForHandler(handler); len(got) != 1 {
		t.Errorf("handler resolutions = %#v, want 1", got)
	}
}

// TestCatalogAnnotationDriftStillYieldsOnlyTheAnnotatedEndpoint 覆盖注释漂移：
// 注释路径与任何一条注册路由都对不上时，身份仍取注释，路由仍只是证据。
func TestCatalogAnnotationDriftStillYieldsOnlyTheAnnotatedEndpoint(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project::Handler")
	store := facts.NewStore("/project", "example.com/project")
	store.Annotations = []facts.AnnotationFact{{
		ID: "annotation:handler", Method: "POST", Path: "/documented/orders", HandlerSymbol: handler,
	}}
	store.Routes = []facts.RouteRegistrationFact{
		{ID: "route:primary", Method: "POST", ResolvedPath: "/public/orders", HandlerSymbol: handler},
		{ID: "route:uncovered", Method: "POST", ResolvedPath: "/legacy/orders", HandlerSymbol: handler},
	}

	entries := Build(facts.Freeze(store)).Entries()
	if len(entries) != 1 || entries[0].Endpoint.Path != "/documented/orders" {
		t.Fatalf("entries = %#v, want only the drifted annotation endpoint", entries)
	}
	if len(entries[0].Routes) != 2 {
		t.Errorf("routes = %#v, want both registrations as drift evidence", entries[0].Routes)
	}
}

// TestCatalogFallsBackToRoutesWhenHandlerHasNoAnnotation 确认路由兜底只在
// handler 完全没有注释时生效——这是接口身份的唯一兜底路径。
func TestCatalogFallsBackToRoutesWhenHandlerHasNoAnnotation(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project::Handler")
	store := facts.NewStore("/project", "example.com/project")
	store.Routes = []facts.RouteRegistrationFact{
		{ID: "route:web", Method: "POST", ResolvedPath: "/web/orders", HandlerSymbol: handler},
		{ID: "route:app", Method: "POST", ResolvedPath: "/app/orders", HandlerSymbol: handler},
	}

	entries := Build(facts.Freeze(store)).Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want one endpoint per route", entries)
	}
}

// TestLookupFallsBackToRegisteredRoute 确认调用方拿真实 URL 仍查得到：
// 该 URL 不是接口身份时，按注册证据回退，返回它所属的那个正式接口。
func TestLookupFallsBackToRegisteredRoute(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project::Handler")
	store := facts.NewStore("/project", "example.com/project")
	store.Annotations = []facts.AnnotationFact{{
		ID: "annotation:handler", Method: "POST", Path: "/public/orders", HandlerSymbol: handler,
	}}
	store.Routes = []facts.RouteRegistrationFact{
		{ID: "route:primary", Method: "POST", ResolvedPath: "/public/orders", HandlerSymbol: handler},
		{ID: "route:uncovered", Method: "POST", ResolvedPath: "/legacy/orders", HandlerSymbol: handler},
	}

	catalog := Build(facts.Freeze(store))
	entry, ok := catalog.Lookup(Key{Method: "POST", Path: "/legacy/orders"})
	if !ok {
		t.Fatal("registered route must stay queryable")
	}
	// 返回的是正式接口身份，而不是把查询用的路径原样回显。
	if entry.Endpoint != (Key{Method: "POST", Path: "/public/orders"}) {
		t.Errorf("entry.Endpoint = %#v, want the canonical annotated identity", entry.Endpoint)
	}
	if _, ok := catalog.Lookup(Key{Method: "POST", Path: "/never/registered"}); ok {
		t.Error("unknown path must not resolve")
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
