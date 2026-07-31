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
	if len(assets) != 1 || len(assets[0].Grpc) != 1 || assets[0].Grpc[0].Operation.FullMethod != "/shop.order.v1.OrderService/Get" {
		t.Fatalf("assets=%#v", assets)
	}
	if len(assets[0].Grpc[0].Chains) != 1 || len(assets[0].Grpc[0].Chains[0].Symbols) != 2 {
		t.Fatalf("chains=%#v", assets[0].Grpc[0].Chains)
	}
	if len(assets[0].Routes) != 1 || assets[0].Routes[0] != registeredEndpoint {
		t.Fatalf("routes=%#v", assets[0].Routes)
	}
	method, err := ParseGrpcMethod("/shop.order.v1.OrderService/Get")
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
	missing, err := ParseGrpcMethod("/shop.order.v1.OrderService/Missing")
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

func TestEndpointAliasKeepsGrpcBidirectionalInvariant(t *testing.T) {
	store := queryStore()
	handler := store.Routes[0].HandlerSymbol
	store.Annotations[0].Path = "/orders/:id"
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID: "route:legacy", Method: "GET", ResolvedPath: "/legacy/orders/:id", HandlerSymbol: handler,
	})

	alias := Endpoint{Method: "GET", Path: "/legacy/orders/:id"}
	assets, err := findEndpointAssetsForTest(store, []Endpoint{alias})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || len(assets[0].Grpc) != 1 {
		t.Fatalf("alias assets = %#v", assets)
	}
	method, err := ParseGrpcMethod("/shop.order.v1.OrderService/Get")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := findGrpcImpactSourcesForTest(store, []GrpcMethod{method})
	if err != nil {
		t.Fatal(err)
	}
	foundAlias := false
	for _, consumer := range sources[0].Consumers {
		foundAlias = foundAlias || consumer.Endpoint == alias
	}
	if !foundAlias {
		t.Fatalf("grpc reverse query omitted alias: %#v", sources)
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
	operation := facts.GrpcOperationFact{ID: facts.GrpcOperationID("/shop.order.v1.OrderService/Get"), FullMethod: "/shop.order.v1.OrderService/Get", ProtoPackage: "shop.order.v1", Service: "OrderService", Method: "Get"}
	store.GrpcOperations = []facts.GrpcOperationFact{operation}
	store.GrpcCalls = []facts.GrpcCallFact{{ID: "grpc_call:get", CallerSymbol: helper, OperationID: operation.ID, ClientBinding: facts.GrpcClientBinding{GoPackage: "example.com/proto", ClientType: "OrderClient", GoMethod: "Get"}}}

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
	operation := facts.GrpcOperationFact{ID: facts.GrpcOperationID("/shop.order.v1.OrderService/Get"), FullMethod: "/shop.order.v1.OrderService/Get", ProtoPackage: "shop.order.v1", Service: "OrderService", Method: "Get"}
	store.GrpcOperations = []facts.GrpcOperationFact{operation}
	store.GrpcCalls = []facts.GrpcCallFact{{ID: "grpc_call:get", CallerSymbol: service, OperationID: operation.ID, ClientBinding: facts.GrpcClientBinding{GoPackage: "example.com/proto", ClientType: "OrderClient", GoMethod: "Get"}}}
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
