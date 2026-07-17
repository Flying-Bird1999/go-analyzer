// analyzer_test.go 测试 AnalyzeTrees 在符号传播、领域根、环路、IM 终端等场景下的影响树构造。
package impact

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// TestAnalyzeBuildsCompleteSymbolToEndpointTree 验证从 service 符号反向传播到 controller、
// 再到路由与注解，最终命中完整 HTTP endpoint 的标准链路。
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

// TestConfidencePropagatesWeakestAlongPath 验证 P1-4 置信度合并：
// 一个 low confidence 的 change 根（模拟 file_changed fallback）经 high 边反向传播
// 到 endpoint，endpoint 终节点的置信度应为 low（取链路最弱），而非被静默升级为 high。
//
// 修复前：endpoint/annotation/route 终节点用硬编码 ConfidenceHigh/ConfidenceMedium，
// 与 change 根置信度无关，弱根经 high 边到达后结论被夸大。
// 修复后：终节点 confidence = CombineConfidence(父链路累积, 终节点证据)，取最弱。
func TestConfidencePropagatesWeakestAlongPath(t *testing.T) {
	store := referenceImpactStore()
	// low confidence 根：模拟无法精确定位符号的 file_changed fallback。
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:file-low",
		Kind:       facts.ChangeKindFileChanged,
		SymbolID:   serviceSymbol,
		File:       "service/common.go",
		Confidence: facts.ConfidenceLow,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:file-low")
	path := firstEndpointPath(t, root.Root)
	if len(path) < 2 {
		t.Fatalf("path too short: %#v", path)
	}

	// 根节点应继承 change 的 low 置信度。
	if root.Root.Confidence != facts.ConfidenceLow {
		t.Errorf("root confidence = %q, want low", root.Root.Confidence)
	}
	// 沿链路各跳（symbol→symbol 边为 high）应取最弱，故所有中间节点为 low。
	for i, node := range path {
		if node.Confidence != facts.ConfidenceLow {
			t.Errorf("path[%d] (kind=%s) confidence = %q, want low (low root + high edge = low)", i, node.Kind, node.Confidence)
		}
	}
	// endpoint 终节点同样应为 low（修复前会因硬编码 ConfidenceHigh 而为 high）。
	endpoint := path[len(path)-1]
	if endpoint.Kind != "endpoint" {
		t.Fatalf("last node kind = %q, want endpoint", endpoint.Kind)
	}
	if endpoint.Confidence != facts.ConfidenceLow {
		t.Errorf("endpoint confidence = %q, want low (P1-4: low root must downgrade terminal)", endpoint.Confidence)
	}
}

// TestConfidenceHighRootKeepsHighEndpoint 验证 high confidence 根的正例：
// high 根经 high 边到达 endpoint，置信度保持 high（不被错误降级）。
func TestConfidenceHighRootKeepsHighEndpoint(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:service-high",
		Kind:       facts.ChangeKindSymbolChanged,
		SymbolID:   serviceSymbol,
		File:       "service/common.go",
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:service-high")
	path := firstEndpointPath(t, root.Root)
	endpoint := path[len(path)-1]
	if endpoint.Confidence != facts.ConfidenceHigh {
		t.Errorf("endpoint confidence = %q, want high (high root + high edge)", endpoint.Confidence)
	}
}

// TestAnalyzeBuildsEndpointAndIMEventExitsFromSamePath 验证同一传播路径上既产生 HTTP endpoint，
// 也产生已解析 IM 事件，且动态事件保留为 im_event_unresolved 终端但不计入 IM 摘要。
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

// TestAnalyzePrefersChangedRouteDomainRootOverHandlerSymbol 验证当变更直接命中路由时，
// 优先以路由领域根展开，而不是退化为 handler 符号传播。
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

// TestAnalyzeAnnotationRootKeepsAnnotationEndpoint 验证注解领域根展开时，完整 annotation
// 保持正式 endpoint identity；注册路由只作为树中的辅助证据。
func TestAnalyzeAnnotationRootKeepsAnnotationEndpoint(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	handler := facts.SymbolID("func:example.com/project/controller/common::CheckIn")
	annotation := facts.AnnotationFact{
		ID:            "annotation:func:example.com/project/controller/common::CheckIn:POST:/api/bff-web/common/checkInV2:0",
		Kind:          "annotation",
		Method:        "POST",
		Path:          "/api/bff-web/common/checkInV2",
		Raw:           "@Post /api/bff-web/common/checkInV2",
		HandlerSymbol: handler,
		Span:          facts.SourceSpan{File: "controller/common/common.go", StartLine: 19, EndLine: 19},
	}
	store.Annotations = append(store.Annotations, annotation)
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:            "route:func:example.com/project/router/common::Init:POST:/checkIn:0",
		Method:        "POST",
		LocalPath:     "/checkIn",
		ResolvedPath:  "/api/bff-web/common/checkIn",
		GroupVar:      "group",
		HandlerRaw:    "common.CheckIn",
		HandlerSymbol: handler,
		RouteFunc:     "func:example.com/project/router/common::Init",
		File:          "router/common/common.go",
		Span:          facts.SourceSpan{File: "router/common/common.go", StartLine: 21, EndLine: 21},
	})
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:annotation:controller/common/common.go:19",
		Kind:       facts.ChangeKindAnnotationChanged,
		TargetID:   annotation.ID,
		File:       "controller/common/common.go",
		Source:     "git_diff",
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:annotation:controller/common/common.go:19")
	if len(root.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
	if root.Endpoints[0].Method != "POST" || root.Endpoints[0].Path != "/api/bff-web/common/checkInV2" {
		t.Fatalf("endpoint = %#v", root.Endpoints[0])
	}
	if len(root.Root.Children) != 1 || root.Root.Children[0].Kind != "route" {
		t.Fatalf("annotation root children = %#v", root.Root.Children)
	}
}

func TestAnalyzeKeepsAliasRouteSeparateFromAnnotatedEndpoint(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	handler := facts.SymbolID("func:example.com/project/controller::GetCustomer")
	service := facts.SymbolID("func:example.com/project/service::GetCustomer")
	store.Symbols = append(store.Symbols,
		facts.SymbolFact{ID: handler, Kind: "func", Span: facts.SourceSpan{File: "controller/customer.go", StartLine: 10, EndLine: 12}},
		facts.SymbolFact{ID: service, Kind: "func", Span: facts.SourceSpan{File: "service/customer.go", StartLine: 10, EndLine: 12}},
	)
	store.References = append(store.References, facts.ReferenceFact{ID: "ref:customer", Kind: facts.ReferenceKindCall, FromSymbol: handler, ToSymbol: service, Confidence: facts.ConfidenceHigh})
	store.Annotations = append(store.Annotations, facts.AnnotationFact{ID: "annotation:customer", Method: "GET", Path: "/api/customers/:id", HandlerSymbol: handler, Span: facts.SourceSpan{File: "controller/customer.go", StartLine: 9, EndLine: 9}})
	store.Routes = append(store.Routes,
		facts.RouteRegistrationFact{ID: "route:customer:current", Method: "GET", ResolvedPath: "/api/customers/:id", HandlerSymbol: handler, Span: facts.SourceSpan{File: "router/customer.go", StartLine: 20, EndLine: 20}},
		facts.RouteRegistrationFact{ID: "route:customer:legacy", Method: "GET", ResolvedPath: "/uc/customers/:customerId", HandlerSymbol: handler, Span: facts.SourceSpan{File: "router/customer.go", StartLine: 21, EndLine: 21}},
	)
	store.Changes = append(store.Changes, facts.ChangeFact{ID: "change:customer-service", Kind: facts.ChangeKindSymbolChanged, SymbolID: service, Confidence: facts.ConfidenceHigh})

	root := mustTreeRoot(t, AnalyzeTrees(store), "change:customer-service")
	if len(root.Endpoints) != 2 {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
	got := map[string]string{}
	for _, endpoint := range root.Endpoints {
		got[endpoint.Path] = endpoint.AnnotationID
	}
	if got["/api/customers/:id"] != "annotation:customer" || got["/uc/customers/:customerId"] != "" {
		t.Fatalf("endpoint identities = %#v", got)
	}
}

func TestAnalyzeSingleRouteAnnotationDriftKeepsAnnotationIdentity(t *testing.T) {
	store := referenceImpactStore()
	store.Routes[0].ResolvedPath = "/api/check-in"
	store.Routes[0].LocalPath = "/check-in"
	store.Annotations[0].Path = "/api/check-in-v2"
	store.Changes = append(store.Changes, facts.ChangeFact{ID: "change:service-drift", Kind: facts.ChangeKindSymbolChanged, SymbolID: serviceSymbol, Confidence: facts.ConfidenceHigh})

	root := mustTreeRoot(t, AnalyzeTrees(store), "change:service-drift")
	if len(root.Endpoints) != 1 || root.Endpoints[0].Path != "/api/check-in-v2" || root.Endpoints[0].AnnotationID == "" {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
}

// TestAnalyzeMarksCycles 验证当反向引用形成环路（service <-> controller）时，
// 重复出现的节点被正确标记 Cycle 而不无限递归。
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

// TestAnalyzeKeepsMultipleEndpointsAndSeparateRoots 验证多个变更根互不覆盖、各自独立展开，
// 且同一根能同时命中多个端点。
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

// TestAnalyzePropagatesMiddlewareSymbolToEndpoint 验证中间件符号变更能传播到挂载该中间件、
// 且 statement order 靠后的路由，最终命中 HTTP endpoint。
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

// TestAnalyzePropagatesRouteScopedDependencyToOnlyItsRoute 验证 inline 路由作用域依赖
// （route 注册表达式 span 内引用的 helper）只影响它所在的那条路由，不会波及同函数的其他路由。
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

// 测试中复用的符号 ID 常量，分别表示 service 与 controller 层的 CheckIn 函数。
const (
	serviceSymbol    facts.SymbolID = "func:example.com/project/service::CheckIn"
	controllerSymbol facts.SymbolID = "func:example.com/project/controller::CheckIn"
)

// referenceImpactStore 构造一个基础 store：service 被 controller 调用，
// controller 注册为路由的 handler 并带有注解，是多数传播测试的起点。
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

// mustTreeRoot 在结果中查找指定变更 ID 对应的根，找不到则 fail。
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

// firstEndpointPath 在树中深度优先找到第一条到 endpoint 的路径并返回路径上所有节点。
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

// assertNodeKinds 校验节点路径上各节点的 Kind 依次匹配期望值。
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

// containsCycle 递归判断树中是否存在被标记 Cycle 的节点。
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

// containsNodeKind 递归判断树中是否包含指定 Kind 的节点。
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
