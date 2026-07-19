// graph_test.go 测试反向引用图与路由图的查询视图构造与命中逻辑。
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

// TestReverseGraphLookupByTarget 场景：按被依赖 symbol 查询反向引用，应返回唯一引用者。
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

// TestRouteGraphMiddlewareAffectsOnlyLaterRoutes 场景：中间件仅影响语句顺序在其之后注册的同组路由。
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

func TestRouteGraphIndexesMiddlewareBySymbol(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	auth := facts.SymbolID("method:example.com/project/auth:Auth:Middleware")
	audit := facts.SymbolID("func:example.com/project/audit::Middleware")
	store.Middleware = append(store.Middleware,
		facts.MiddlewareBindingFact{
			ID:                "middleware:b",
			MiddlewareSymbols: []facts.SymbolID{auth, ""},
			StatementIndex:    20,
			Span:              facts.SourceSpan{File: "router/b.go"},
		},
		facts.MiddlewareBindingFact{
			ID:                "middleware:a",
			MiddlewareSymbols: []facts.SymbolID{auth, audit},
			StatementIndex:    10,
			Span:              facts.SourceSpan{File: "router/a.go"},
		},
		facts.MiddlewareBindingFact{
			ID:                "middleware:c",
			MiddlewareSymbols: []facts.SymbolID{auth},
			StatementIndex:    5,
			Span:              facts.SourceSpan{File: "router/a.go"},
		},
	)

	graph := NewRouteGraph(store)
	authBindings := graph.MiddlewareBindingsForSymbol(auth)
	gotIDs := middlewareBindingIDs(authBindings)
	wantIDs := []string{"middleware:c", "middleware:a", "middleware:b"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("auth bindings = %#v, want %#v", gotIDs, wantIDs)
	}
	auditBindings := graph.MiddlewareBindingsForSymbol(audit)
	if len(auditBindings) != 1 || auditBindings[0].ID != "middleware:a" {
		t.Fatalf("audit bindings = %#v", auditBindings)
	}
	if bindings := graph.MiddlewareBindingsForSymbol(""); len(bindings) != 0 {
		t.Fatalf("empty symbol bindings = %#v", bindings)
	}
	authBindings[0].ID = "mutated"
	again := graph.MiddlewareBindingsForSymbol(auth)
	if again[0].ID != "middleware:c" {
		t.Fatalf("middleware bindings query did not return a copy: %#v", again)
	}
}

// TestRouteGraphScopesGroupsByRouteFunction 场景：相同 groupVar 但不同路由函数的组不应串扰，中间件只命中同函数的组路由。
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

// TestRouteGraphIncludesDescendantGroupRoutes 场景：查询父组路由时应递归包含后代组中注册的路由。
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

// TestRouteGraphMiddlewareAffectsDescendantGroupRoutes 场景：父组上的中间件同样影响后代组中的路由。
func TestRouteGraphMiddlewareAffectsDescendantGroupRoutes(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.RouteGroups = append(store.RouteGroups,
		facts.RouteGroupFact{ID: "group:parent", GroupVar: "parent"},
		facts.RouteGroupFact{ID: "group:child", GroupVar: "child", ParentGroupID: "group:parent"},
	)
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:             "middleware:auth",
		GroupID:        "group:parent",
		GroupVar:       "parent",
		StatementIndex: 10,
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:             "route:child",
		GroupID:        "group:child",
		StatementIndex: 11,
	})

	graph := NewRouteGraph(store)
	routes := graph.RoutesAffectedByMiddleware("middleware:auth")
	if len(routes) != 1 || routes[0].ID != "route:child" {
		t.Fatalf("middleware descendant routes = %#v", routes)
	}
}

// TestRouteGraphMiddlewareAffectsCrossFunctionGroupFlowRoutes 场景：跨函数 group flow 建立的父子关系下，父组中间件同样影响子组路由。
func TestRouteGraphMiddlewareAffectsCrossFunctionGroupFlowRoutes(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.RouteGroupFlows = append(store.RouteGroupFlows, facts.RouteGroupFlowFact{
		ID:            "flow:helper-child",
		ParentGroupID: "group:helper",
		ChildGroupID:  "group:child-root",
	})
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:             "middleware:auth",
		GroupID:        "group:helper",
		RouteFunc:      "func:example.com/project/router::AddAuth",
		StatementIndex: 10,
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:             "route:child",
		GroupID:        "group:child-root",
		RouteFunc:      "func:example.com/project/router::Register",
		StatementIndex: 1,
	})

	graph := NewRouteGraph(store)
	routes := graph.RoutesAffectedByMiddleware("middleware:auth")
	if len(routes) != 1 || routes[0].ID != "route:child" {
		t.Fatalf("cross-function middleware routes = %#v", routes)
	}
}

// TestRouteGraphChildGroupsByIDDedupesAcrossParentFieldAndFlow 验证同一 (parent, child)
// 父子组关系若同时被 RouteGroupFact.ParentGroupID 与 RouteGroupFlowFact 两处记录，
// ChildGroupsByID 只保留一条子组 ID，不产生重复条目。
// 修复前两处各自 append 不去重：RoutesForGroup 的递归靠 seenGroups 兜底不受影响，
// 但直接读取 ChildGroupsByID 的消费方会看到重复子组 ID，且递归会对同一子组做多余
// 的重复调用。
func TestRouteGraphChildGroupsByIDDedupesAcrossParentFieldAndFlow(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.RouteGroups = append(store.RouteGroups,
		facts.RouteGroupFact{ID: "group:parent", GroupVar: "parent"},
		// child 组自身的 ParentGroupID 字段已经指向 parent……
		facts.RouteGroupFact{ID: "group:child", GroupVar: "child", ParentGroupID: "group:parent"},
	)
	// ……同一条父子关系又被一条 RouteGroupFlow 重复记录（例如子组在另一函数中
	// 也被显式传递注册）。
	store.RouteGroupFlows = append(store.RouteGroupFlows, facts.RouteGroupFlowFact{
		ID:            "flow:parent-child",
		ParentGroupID: "group:parent",
		ChildGroupID:  "group:child",
	})

	graph := NewRouteGraph(store)
	children := graph.ChildGroupsByID["group:parent"]
	if len(children) != 1 || children[0] != "group:child" {
		t.Fatalf("ChildGroupsByID[parent] = %#v, want exactly one entry [\"group:child\"]", children)
	}
}

// TestRouteGraphMapsRouteScopedDependenciesOnlyToContainingRoute 场景：依赖引用 span 落在某条路由 span 内时，只关联到该条路由而非同函数的其他路由。
func TestRouteGraphMapsRouteScopedDependenciesOnlyToContainingRoute(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	routeFunc := facts.SymbolID("func:example.com/project/router::InitRouter")
	guard := facts.SymbolID("func:example.com/project/router::Guard")
	store.Routes = append(store.Routes,
		facts.RouteRegistrationFact{
			ID:        "route:guarded",
			RouteFunc: routeFunc,
			Span:      facts.SourceSpan{File: "router/router.go", StartLine: 20, StartCol: 2, EndLine: 20, EndCol: 42},
		},
		facts.RouteRegistrationFact{
			ID:        "route:plain",
			RouteFunc: routeFunc,
			Span:      facts.SourceSpan{File: "router/router.go", StartLine: 21, StartCol: 2, EndLine: 21, EndCol: 35},
		},
	)
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:guard",
		FromSymbol: routeFunc,
		ToSymbol:   guard,
		Span:       facts.SourceSpan{File: "router/router.go", StartLine: 20, StartCol: 2, EndLine: 20, EndCol: 10},
	})

	graph := NewRouteGraph(store)
	routes := graph.RoutesForDependency(guard)
	if len(routes) != 1 || routes[0].ID != "route:guarded" {
		t.Fatalf("dependency routes = %#v", routes)
	}
}

// TestRouteGraphMapsAssignedGroupHelperDependencyToGroupRoutes 场景：依赖引用 span 落在 group 创建表达式内时，影响该 group（含后代组）的全部路由。
func TestRouteGraphMapsAssignedGroupHelperDependencyToGroupRoutes(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	routeFunc := facts.SymbolID("func:example.com/project/router::InitRouter")
	guard := facts.SymbolID("func:example.com/project/router::AddReadGuard")
	store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
		ID:             "group:guarded",
		GroupVar:       "guarded",
		RouteFunc:      routeFunc,
		StatementIndex: 1,
		Span:           facts.SourceSpan{File: "router/router.go", StartLine: 10, StartCol: 2, EndLine: 10, EndCol: 42},
	})
	store.Routes = append(store.Routes,
		facts.RouteRegistrationFact{
			ID:             "route:guarded",
			GroupID:        "group:guarded",
			GroupVar:       "guarded",
			RouteFunc:      routeFunc,
			StatementIndex: 2,
			Span:           facts.SourceSpan{File: "router/router.go", StartLine: 11, StartCol: 2, EndLine: 11, EndCol: 42},
		},
		facts.RouteRegistrationFact{
			ID:             "route:plain",
			GroupVar:       "root",
			RouteFunc:      routeFunc,
			StatementIndex: 3,
			Span:           facts.SourceSpan{File: "router/router.go", StartLine: 12, StartCol: 2, EndLine: 12, EndCol: 35},
		},
	)
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:guard-assignment",
		FromSymbol: routeFunc,
		ToSymbol:   guard,
		Span:       facts.SourceSpan{File: "router/router.go", StartLine: 10, StartCol: 13, EndLine: 10, EndCol: 32},
	})

	graph := NewRouteGraph(store)
	routes := graph.RoutesForDependency(guard)
	if len(routes) != 1 || routes[0].ID != "route:guarded" {
		t.Fatalf("assigned group dependency routes = %#v", routes)
	}
}

func middlewareBindingIDs(bindings []facts.MiddlewareBindingFact) []string {
	out := make([]string, len(bindings))
	for i, binding := range bindings {
		out[i] = binding.ID
	}
	return out
}

// extractAndLinkFixture 加载 testdata fixture，执行路由抽取与 link，返回填充好的 Store。
func extractAndLinkFixture(t *testing.T, fixture string) *facts.Store {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "fixtures", fixture)
	p, err := project.Load(root)
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
