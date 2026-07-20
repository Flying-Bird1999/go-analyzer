package output

import (
	"encoding/json"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
	"gopkg.inshopline.com/bff/go-analyzer/internal/serviceimpact"
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
			Endpoint: dependency.Endpoint{Method: "GET", Path: "/orders/:id"},
			Routes:   []dependency.Endpoint{{Method: "GET", Path: "/router/orders/:id"}},
			Handlers: []facts.SymbolID{handler},
			Clients:  []facts.GrpcClientBinding{{GoPackage: "example.com/proto", ClientType: "OrderServiceClient", GoMethod: "Get"}},
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
				Relation string               `json:"relation"`
				Routes   []dependencyEndpoint `json:"routes"`
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
	if got := rendered.GrpcSources[0].Consumers[0].Routes; len(got) != 1 || got[0].Path != "/router/orders/:id" {
		t.Fatalf("routes = %#v", got)
	}
	if len(rendered.Summary.ImpactedEndpoints) != 1 || rendered.Summary.ImpactedEndpoints[0].Path != "/orders/:id" {
		t.Fatalf("summary endpoints = %#v", rendered.Summary.ImpactedEndpoints)
	}
	if len(rendered.EndpointSourcesSummary) != 1 || rendered.EndpointSourcesSummary[0].Sources[0].GrpcFullMethod != operation.FullMethod {
		t.Fatalf("endpoint sources = %#v", rendered.EndpointSourcesSummary)
	}
}

// TestBuildGrpcImpactDocumentDedupesContractAcrossRoots 验证同一 contract 被多个变更根
// 命中时，summary 与 entrySourcesSummary 都只保留一份，且反查 source 不是空壳。
func TestBuildGrpcImpactDocumentDedupesContractAcrossRoots(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	contract := serviceimpact.Contract{
		ID:          "http:route:orders",
		Kind:        serviceimpact.ContractHTTPEndpoint,
		Identity:    "GET /orders",
		Relation:    "exposed_http_endpoint",
		EntrySymbol: handler,
		Route:       facts.RouteRegistrationFact{Method: "GET", ResolvedPath: "/orders"},
	}
	// 两个根命中同一 contract。
	tree := serviceimpact.TreeResult{Roots: []serviceimpact.RootImpact{
		{
			Change:    facts.ChangeFact{ID: "change:a", SymbolID: handler, File: "controller/order.go"},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract}},
		},
		{
			Change:    facts.ChangeFact{ID: "change:b", SymbolID: handler, File: "controller/order.go"},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract}},
		},
	}}
	doc := BuildGrpcImpactDocument(nil, tree, GrpcImpactDocumentOptions{})
	if len(doc.Summary.HTTP) != 1 {
		t.Fatalf("expected 1 http contract, got %d", len(doc.Summary.HTTP))
	}
	if len(doc.EntrySourcesSummary.HTTP) != 1 {
		t.Fatalf("expected 1 entrySourcesSummary.HTTP group, got %d", len(doc.EntrySourcesSummary.HTTP))
	}
	entryGroup := doc.EntrySourcesSummary.HTTP[0]
	if len(entryGroup.Sources) == 0 {
		t.Errorf("entrySourcesSummary http group has no sources")
	}
}

// TestBuildGrpcImpactDocumentEntrySourcesCrossFile 验证跨文件场景：a.go 与 b.go 都命中
// 同一 contract 时，entrySourcesSummary 反查能看到两个文件来源。
func TestBuildGrpcImpactDocumentEntrySourcesCrossFile(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	contract := serviceimpact.Contract{
		ID:          "http:route:orders",
		Kind:        serviceimpact.ContractHTTPEndpoint,
		Identity:    "GET /orders",
		Relation:    "exposed_http_endpoint",
		EntrySymbol: handler,
		Route:       facts.RouteRegistrationFact{Method: "GET", ResolvedPath: "/orders"},
	}
	tree := serviceimpact.TreeResult{Roots: []serviceimpact.RootImpact{
		{
			Change:    facts.ChangeFact{ID: "change:a", SymbolID: handler, File: "controller/a.go"},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract}},
		},
		{
			Change:    facts.ChangeFact{ID: "change:b", SymbolID: handler, File: "controller/b.go"},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract}},
		},
	}}
	doc := BuildGrpcImpactDocument(nil, tree, GrpcImpactDocumentOptions{})
	if len(doc.Summary.HTTP) != 1 {
		t.Fatalf("expected 1 http contract, got %d", len(doc.Summary.HTTP))
	}
	if len(doc.EntrySourcesSummary.HTTP) != 1 {
		t.Fatalf("expected 1 entrySourcesSummary.HTTP group, got %d", len(doc.EntrySourcesSummary.HTTP))
	}
	entryGroup := doc.EntrySourcesSummary.HTTP[0]
	// 应有两个 source（a.go + b.go）。
	if len(entryGroup.Sources) != 2 {
		t.Errorf("entrySourcesSummary http sources = %d, want 2 (a.go + b.go)", len(entryGroup.Sources))
	}
}
