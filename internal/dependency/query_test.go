package dependency

import (
	"context"
	"errors"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/analysis"
	endpointcatalog "gopkg.inshopline.com/bff/go-analyzer/internal/endpoint"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestEndpointAndGrpcQueriesShareFormalRelations(t *testing.T) {
	store := queryStore()
	endpoint := Endpoint{Method: "GET", Path: "/stale/orders/:id"}
	registeredEndpoint := Endpoint{Method: "GET", Path: "/orders/:id"}
	assets, err := findEndpointAssetsForTest(store, []Endpoint{endpoint, endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || len(assets[0].Grpc) != 1 || assets[0].Grpc[0].Operation.Identity != "example.com/proto.OrderService/Get" {
		t.Fatalf("assets=%#v", assets)
	}
	if len(assets[0].Grpc[0].Chains) != 1 || len(assets[0].Grpc[0].Chains[0].Symbols) != 2 {
		t.Fatalf("chains=%#v", assets[0].Grpc[0].Chains)
	}
	if len(assets[0].Routes) != 1 || assets[0].Routes[0] != registeredEndpoint {
		t.Fatalf("routes=%#v", assets[0].Routes)
	}
	method, err := ParseGrpcMethod("example.com/proto.OrderService/Get")
	if err != nil {
		t.Fatal(err)
	}
	consumers, err := findGrpcImpactSourcesForTest(store, []GrpcMethod{method})
	if err != nil {
		t.Fatal(err)
	}
	if len(consumers) != 1 || len(consumers[0].Consumers) != 1 || consumers[0].Consumers[0].Endpoint != endpoint {
		t.Fatalf("consumers=%#v", consumers)
	}
	if len(consumers[0].Consumers[0].Routes) != 1 || consumers[0].Consumers[0].Routes[0] != registeredEndpoint {
		t.Fatalf("consumer routes=%#v", consumers[0].Consumers[0].Routes)
	}
	missing, err := ParseGrpcMethod("example.com/proto.OrderService/Missing")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := findGrpcImpactSourcesForTest(store, []GrpcMethod{missing})
	if err != nil || len(empty) != 1 || len(empty[0].Consumers) != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
}

func TestEndpointQueryRejectsUnknownEndpoint(t *testing.T) {
	_, err := findEndpointAssetsForTest(queryStore(), []Endpoint{{Method: "GET", Path: "/missing"}})
	if err == nil {
		t.Fatal("expected endpoint-not-found error")
	}
}

// TestUncoveredRouteKeepsGrpcBidirectionalInvariant 守住双向不变量在“注释没覆盖到
// 的注册路径”上仍然成立。这类路径不是接口身份（身份以注释为准），但它是真实可调用的
// URL，调用方常常只有它。因此要求：
//   - 正查：用该路径查得到，且回报的是它所属的那个正式接口，而不是把入参原样回显；
//   - 反查：该正式接口出现在 consumer 里，且这条路径作为注册证据没有丢。
//
// 两个方向必须落在同一个接口身份上，否则同一份代码关系会因查询方向不同而分叉。
func TestUncoveredRouteKeepsGrpcBidirectionalInvariant(t *testing.T) {
	store := queryStore()
	handler := store.Routes[0].HandlerSymbol
	store.Annotations[0].Path = "/orders/:id"
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID: "route:legacy", Method: "GET", ResolvedPath: "/legacy/orders/:id", HandlerSymbol: handler,
	})

	uncovered := Endpoint{Method: "GET", Path: "/legacy/orders/:id"}
	canonical := Endpoint{Method: "GET", Path: "/orders/:id"}

	assets, err := findEndpointAssetsForTest(store, []Endpoint{uncovered})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || len(assets[0].Grpc) != 1 {
		t.Fatalf("uncovered route assets = %#v", assets)
	}
	if assets[0].Endpoint != canonical {
		t.Errorf("forward endpoint = %#v, want canonical %#v", assets[0].Endpoint, canonical)
	}

	method, err := ParseGrpcMethod("example.com/proto.OrderService/Get")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := findGrpcImpactSourcesForTest(store, []GrpcMethod{method})
	if err != nil {
		t.Fatal(err)
	}
	var matched *GrpcImpactConsumer
	for i, consumer := range sources[0].Consumers {
		if consumer.Endpoint == canonical {
			matched = &sources[0].Consumers[i]
		}
		if consumer.Endpoint == uncovered {
			t.Errorf("uncovered route must not surface as its own endpoint: %#v", consumer)
		}
	}
	if matched == nil {
		t.Fatalf("grpc reverse query omitted the canonical endpoint: %#v", sources)
	}
	foundEvidence := false
	for _, route := range matched.Routes {
		foundEvidence = foundEvidence || route == uncovered
	}
	if !foundEvidence {
		t.Errorf("reverse query lost the uncovered route evidence: %#v", matched.Routes)
	}
}

func TestEndpointQueryEnforcesTraversalBudget(t *testing.T) {
	store := queryStore()
	service := store.References[0].ToSymbol
	helper := facts.SymbolID("func:example.com/project/service::Helper")
	store.References = append(store.References, facts.ReferenceFact{
		ID: "call:helper", Kind: facts.ReferenceKindCall, FromSymbol: service, ToSymbol: helper,
	})
	store.GrpcCalls[0].CallerSymbol = helper
	snapshot := facts.Freeze(store)
	limits := analysis.DefaultLimits()
	limits.MaxDepth = 1
	_, err := FindEndpointAssets(context.Background(), snapshot, endpointcatalog.Build(snapshot), limits, []Endpoint{{
		Method: "GET", Path: "/stale/orders/:id",
	}})
	var budgetErr *analysis.BudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("query error = %v, want BudgetError", err)
	}
}

// TestForwardChainsRecordsAllPathsToSharedGrpcHelper 验证 handler 经两条不同路径
// （A 与 B）都到达同一个发起 gRPC 调用的 helper C 时，两条 H->...->C->Op 调用链都应
// 被记录，而不是只保留先到达 C 的那条路径。修复前 forwardChains 用单个全局 visited
// 门控 callee 展开，C 只在首次被访问时展开一次，另一条路径的链路证据永久丢失。
func TestForwardChainsRecordsAllPathsToSharedGrpcHelper(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	pathA := facts.SymbolID("func:example.com/project/service::ViaA")
	pathB := facts.SymbolID("func:example.com/project/service::ViaB")
	helper := facts.SymbolID("func:example.com/project/service::Helper")
	store.Routes = []facts.RouteRegistrationFact{{ID: "route:get", Method: "GET", ResolvedPath: "/orders/:id", HandlerSymbol: handler}}
	store.References = []facts.ReferenceFact{
		{ID: "call:h_a", Kind: facts.ReferenceKindCall, FromSymbol: handler, ToSymbol: pathA},
		{ID: "call:h_b", Kind: facts.ReferenceKindCall, FromSymbol: handler, ToSymbol: pathB},
		{ID: "call:a_helper", Kind: facts.ReferenceKindCall, FromSymbol: pathA, ToSymbol: helper},
		{ID: "call:b_helper", Kind: facts.ReferenceKindCall, FromSymbol: pathB, ToSymbol: helper},
	}
	operation := facts.GrpcOperationFact{ID: facts.GrpcOperationID("example.com/proto.OrderService/Get"), Identity: "example.com/proto.OrderService/Get", GoPackage: "example.com/proto", Service: "OrderService", GoMethod: "Get"}
	store.GrpcOperations = []facts.GrpcOperationFact{operation}
	store.GrpcCalls = []facts.GrpcCallFact{{ID: "grpc_call:get", CallerSymbol: helper, OperationID: operation.ID}}

	assets, err := findEndpointAssetsForTest(store, []Endpoint{{Method: "GET", Path: "/orders/:id"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || len(assets[0].Grpc) != 1 {
		t.Fatalf("assets=%#v", assets)
	}
	chains := assets[0].Grpc[0].Chains
	if len(chains) != 2 {
		t.Fatalf("expected 2 chains (one via each path to the shared helper), got %d: %#v", len(chains), chains)
	}
	seenPrefix := map[facts.SymbolID]bool{}
	for _, chain := range chains {
		if len(chain.Symbols) != 3 {
			t.Fatalf("chain symbols = %#v, want length 3 (handler, path, helper)", chain.Symbols)
		}
		seenPrefix[chain.Symbols[1]] = true
	}
	if !seenPrefix[pathA] || !seenPrefix[pathB] {
		t.Fatalf("expected chains via both pathA and pathB, got prefixes=%#v", seenPrefix)
	}
}

func queryStore() *facts.Store {
	store := facts.NewStore("/tmp/project", "example.com/project")
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	service := facts.SymbolID("func:example.com/project/service::Get")
	store.Routes = []facts.RouteRegistrationFact{{ID: "route:get", Method: "GET", ResolvedPath: "/orders/:id", HandlerSymbol: handler}}
	store.Annotations = []facts.AnnotationFact{{ID: "annotation:get", Method: "GET", Path: "/stale/orders/:id", HandlerSymbol: handler}}
	store.References = []facts.ReferenceFact{{ID: "call:handler", Kind: facts.ReferenceKindCall, FromSymbol: handler, ToSymbol: service}, {ID: "type:ignored", Kind: facts.ReferenceKindType, FromSymbol: handler, ToSymbol: "func:example.com/project/other::Ignored"}}
	operation := facts.GrpcOperationFact{ID: facts.GrpcOperationID("example.com/proto.OrderService/Get"), Identity: "example.com/proto.OrderService/Get", GoPackage: "example.com/proto", Service: "OrderService", GoMethod: "Get"}
	store.GrpcOperations = []facts.GrpcOperationFact{operation}
	store.GrpcCalls = []facts.GrpcCallFact{{ID: "grpc_call:get", CallerSymbol: service, OperationID: operation.ID}}
	return store
}

func findEndpointAssetsForTest(store *facts.Store, inputs []Endpoint) ([]EndpointAsset, error) {
	snapshot := facts.Freeze(store)
	return FindEndpointAssets(context.Background(), snapshot, endpointcatalog.Build(snapshot), analysis.DefaultLimits(), inputs)
}

func findGrpcImpactSourcesForTest(store *facts.Store, inputs []GrpcMethod) ([]GrpcImpactSource, error) {
	snapshot := facts.Freeze(store)
	return FindGrpcImpactSources(context.Background(), snapshot, endpointcatalog.Build(snapshot), analysis.DefaultLimits(), inputs)
}

// TestParseGrpcMethodSplitsOnLastSlashThenLastDot 锁定新身份格式的解析规则：
// <生成包 import 路径>.<Service>/<GoMethod>。import 路径本身可以含多段 "/"，
// 所以必须先按最后一个 "/" 切出 GoMethod，再在剩下的前缀里按最后一个 "." 切出
// Service——顺序反了会把 import 路径错误地切碎。
func TestParseGrpcMethodSplitsOnLastSlashThenLastDot(t *testing.T) {
	got, err := ParseGrpcMethod("gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_config.SalesConfigService/GetSlLiveCommentSync")
	if err != nil {
		t.Fatal(err)
	}
	want := GrpcMethod{
		Identity:  "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_config.SalesConfigService/GetSlLiveCommentSync",
		GoPackage: "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_config",
		Service:   "SalesConfigService",
		GoMethod:  "GetSlLiveCommentSync",
	}
	if got != want {
		t.Fatalf("ParseGrpcMethod() = %#v, want %#v", got, want)
	}
}

func TestParseGrpcMethodRejectsMalformedInput(t *testing.T) {
	for _, raw := range []string{
		"",
		"NoSlashAtAll",
		"trailing/slash/",
		"onlypath/Method",                 // 前缀里没有 "." 分隔 Service
		"/shop.order.v1.OrderService/Get", // 旧的 wire full-method 格式：goPackage 会以 "/" 开头，不再合法
	} {
		if _, err := ParseGrpcMethod(raw); err == nil {
			t.Errorf("ParseGrpcMethod(%q) = nil error, want error", raw)
		}
	}
}
