package output

import (
	"encoding/json"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

func TestAddGrpcSourcesMergesConsumersIntoImpactDocument(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	remote := facts.SymbolID("func:example.com/project/remote::Get")
	store.Symbols = []facts.SymbolFact{
		{ID: handler, Kind: "func", Name: "Get", Span: facts.SourceSpan{File: "controller/order.go"}},
		{ID: remote, Kind: "func", Name: "Get", Span: facts.SourceSpan{File: "remote/order.go"}},
	}
	operation := dependency.GrpcMethod{FullMethod: "/shop.order.v1.OrderService/Get", ProtoPackage: "shop.order.v1", Service: "OrderService", Method: "Get"}
	doc := BuildImpactDocument(nil, impact.TreeResult{}, ImpactDocumentOptions{})
	AddGrpcSources(&doc, store, []dependency.GrpcImpactSource{{
		Grpc: operation,
		Consumers: []dependency.GrpcImpactConsumer{{
			Endpoint:            dependency.Endpoint{Method: "GET", Path: "/orders/:id"},
			RegisteredEndpoints: []dependency.Endpoint{{Method: "GET", Path: "/router/orders/:id"}},
			Handlers:            []facts.SymbolID{handler},
			Clients:             []facts.GrpcClientBinding{{GoPackage: "example.com/proto", ClientType: "OrderServiceClient", GoMethod: "Get"}},
			Chains: []dependency.Chain{{
				Symbols: []facts.SymbolID{handler, remote},
				Call:    facts.GrpcCallFact{Span: facts.SourceSpan{File: "remote/order.go", StartLine: 18, StartCol: 9}},
			}},
		}},
	}})

	out, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Summary struct {
			ImpactedEndpoints []EndpointSummary `json:"impactedEndpoints"`
		} `json:"summary"`
		GrpcSources []struct {
			Grpc struct {
				FullMethod string `json:"fullMethod"`
			} `json:"grpc"`
			Consumers []struct {
				Relation            string               `json:"relation"`
				RegisteredEndpoints []dependencyEndpoint `json:"registeredEndpoints"`
			} `json:"consumers"`
		} `json:"grpcSources"`
		EndpointSourcesSummary []EndpointSourceSummary `json:"endpointSourcesSummary"`
	}
	if err := json.Unmarshal(out, &rendered); err != nil {
		t.Fatal(err)
	}
	if len(rendered.GrpcSources) != 1 || rendered.GrpcSources[0].Grpc.FullMethod != operation.FullMethod {
		t.Fatalf("grpc sources = %#v", rendered.GrpcSources)
	}
	if len(rendered.GrpcSources[0].Consumers) != 1 || rendered.GrpcSources[0].Consumers[0].Relation != "may_call" {
		t.Fatalf("consumers = %#v", rendered.GrpcSources[0].Consumers)
	}
	if got := rendered.GrpcSources[0].Consumers[0].RegisteredEndpoints; len(got) != 1 || got[0].Path != "/router/orders/:id" {
		t.Fatalf("registered endpoints = %#v", got)
	}
	if len(rendered.Summary.ImpactedEndpoints) != 1 || rendered.Summary.ImpactedEndpoints[0].Path != "/orders/:id" {
		t.Fatalf("summary endpoints = %#v", rendered.Summary.ImpactedEndpoints)
	}
	if len(rendered.EndpointSourcesSummary) != 1 || rendered.EndpointSourcesSummary[0].Sources[0].GrpcFullMethod != operation.FullMethod {
		t.Fatalf("endpoint sources = %#v", rendered.EndpointSourcesSummary)
	}
}
