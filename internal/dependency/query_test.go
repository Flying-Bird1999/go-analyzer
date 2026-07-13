package dependency

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestEndpointAndGrpcQueriesShareFormalRelations(t *testing.T) {
	store := queryStore()
	endpoint := Endpoint{Method: "GET", Path: "/stale/orders/:id"}
	registeredEndpoint := Endpoint{Method: "GET", Path: "/orders/:id"}
	assets, err := FindEndpointAssets(store, []Endpoint{endpoint, endpoint})
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
	consumers, err := FindGrpcImpactSources(store, []GrpcMethod{method})
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
	empty, err := FindGrpcImpactSources(store, []GrpcMethod{missing})
	if err != nil || len(empty) != 1 || len(empty[0].Consumers) != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
}

func TestEndpointQueryRejectsUnknownEndpoint(t *testing.T) {
	_, err := FindEndpointAssets(queryStore(), []Endpoint{{Method: "GET", Path: "/missing"}})
	if err == nil {
		t.Fatal("expected endpoint-not-found error")
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
