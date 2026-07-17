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

// TestBuildGrpcImpactDocumentMergesContractConfidenceWeakest 验证 P1-a 修复：
// 两个变更根以不同 confidence 命中同一 contract（如一个 low file-changed 根与一个 high
// symbol 根都到达同一 handler），summary 的 contract confidence 应取两者中最弱值，
// 而非被后写根覆盖。
//
// 修复前：grpc_service_impact.go 对 globalContracts/builder.contracts 直接赋值（last-write-wins），
// summary confidence 由 root 遍历顺序决定，与 entrySourcesSummary（weakestConfidence(path)）不一致。
// 修复后：mergeContractSummary 按最弱合并 confidence。
func TestBuildGrpcImpactDocumentMergesContractConfidenceWeakest(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	contract := serviceimpact.Contract{
		ID:          "http:route:orders",
		Kind:        serviceimpact.ContractHTTPEndpoint,
		Identity:    "GET /orders",
		Relation:    "exposed_http_endpoint",
		EntrySymbol: handler,
		Confidence:  facts.ConfidenceHigh,
		Route:       facts.RouteRegistrationFact{Method: "GET", ResolvedPath: "/orders"},
	}
	// 两个根命中同一 contract：一个 high，一个 low。
	tree := serviceimpact.TreeResult{Roots: []serviceimpact.RootImpact{
		{
			Change:    facts.ChangeFact{ID: "change:high", SymbolID: handler, File: "controller/order.go", Confidence: facts.ConfidenceHigh},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract, Confidence: facts.ConfidenceHigh}},
		},
		{
			Change:    facts.ChangeFact{ID: "change:low", SymbolID: handler, File: "controller/order.go", Confidence: facts.ConfidenceLow},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract, Confidence: facts.ConfidenceLow}},
		},
	}}
	// 即便 low 根在前（被 high 覆盖风险）也应得到 low；调换顺序也应得到 low。
	for _, order := range []string{"low-first", "high-first"} {
		roots := tree.Roots
		if order == "high-first" {
			roots = []serviceimpact.RootImpact{tree.Roots[1], tree.Roots[0]}
		}
		doc := BuildGrpcImpactDocument(nil, serviceimpact.TreeResult{Roots: roots}, GrpcImpactDocumentOptions{})
		if len(doc.Summary.HTTP) != 1 {
			t.Fatalf("%s: expected 1 http contract, got %d", order, len(doc.Summary.HTTP))
		}
		if got := doc.Summary.HTTP[0].Confidence; got != facts.ConfidenceLow {
			t.Errorf("%s: summary http contract confidence = %q, want low (weakest of high+low roots)", order, got)
		}
		// entrySourcesSummary 反查：contract.confidence 必须与 summary 一致（low），
		// 且 source 必须存在（验证反查不是空壳）。
		if len(doc.EntrySourcesSummary.HTTP) != 1 {
			t.Fatalf("%s: expected 1 entrySourcesSummary.HTTP group, got %d", order, len(doc.EntrySourcesSummary.HTTP))
		}
		entryGroup := doc.EntrySourcesSummary.HTTP[0]
		if entryGroup.Contract.Confidence != facts.ConfidenceLow {
			t.Errorf("%s: entrySourcesSummary http contract confidence = %q, want low (consistent with summary)", order, entryGroup.Contract.Confidence)
		}
		if len(entryGroup.Sources) == 0 {
			t.Errorf("%s: entrySourcesSummary http group has no sources", order)
		}
	}
}

// TestBuildGrpcImpactDocumentEntrySourcesCrossFileConfidence 验证跨文件场景：
// a.go（高置信）与 b.go（低置信）命中同一 contract，summary 与 entrySourcesSummary 的
// contract.confidence 都应为 low（最弱）。修复前 entrySourcesSummary 因按文件排序
// 先处理 a.go 而保留 high（first-write-wins），与 summary 不一致。
func TestBuildGrpcImpactDocumentEntrySourcesCrossFileConfidence(t *testing.T) {
	handler := facts.SymbolID("func:example.com/project/controller::Get")
	contract := serviceimpact.Contract{
		ID:          "http:route:orders",
		Kind:        serviceimpact.ContractHTTPEndpoint,
		Identity:    "GET /orders",
		Relation:    "exposed_http_endpoint",
		EntrySymbol: handler,
		Confidence:  facts.ConfidenceHigh,
		Route:       facts.RouteRegistrationFact{Method: "GET", ResolvedPath: "/orders"},
	}
	// a.go（字典序在前）高置信、b.go 低置信命中同一 contract。
	tree := serviceimpact.TreeResult{Roots: []serviceimpact.RootImpact{
		{
			Change:    facts.ChangeFact{ID: "change:a", SymbolID: handler, File: "controller/a.go", Confidence: facts.ConfidenceHigh},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract, Confidence: facts.ConfidenceHigh}},
		},
		{
			Change:    facts.ChangeFact{ID: "change:b", SymbolID: handler, File: "controller/b.go", Confidence: facts.ConfidenceLow},
			Root:      impact.Node{ID: string(handler), Kind: "func", Level: 0, Children: []impact.Node{}},
			Contracts: []serviceimpact.ContractImpact{{Contract: contract, Confidence: facts.ConfidenceLow}},
		},
	}}
	doc := BuildGrpcImpactDocument(nil, tree, GrpcImpactDocumentOptions{})
	if len(doc.Summary.HTTP) != 1 {
		t.Fatalf("expected 1 http contract, got %d", len(doc.Summary.HTTP))
	}
	if got := doc.Summary.HTTP[0].Confidence; got != facts.ConfidenceLow {
		t.Errorf("summary http confidence = %q, want low", got)
	}
	if len(doc.EntrySourcesSummary.HTTP) != 1 {
		t.Fatalf("expected 1 entrySourcesSummary.HTTP group, got %d", len(doc.EntrySourcesSummary.HTTP))
	}
	entryGroup := doc.EntrySourcesSummary.HTTP[0]
	// 修复前：a.go 先处理，contract.confidence 保留 high；修复后：合并为 low。
	if entryGroup.Contract.Confidence != facts.ConfidenceLow {
		t.Errorf("entrySourcesSummary http contract confidence = %q, want low (cross-file weakest, consistent with summary)", entryGroup.Contract.Confidence)
	}
	// 应有两个 source（a.go + b.go）。
	if len(entryGroup.Sources) != 2 {
		t.Errorf("entrySourcesSummary http sources = %d, want 2 (a.go + b.go)", len(entryGroup.Sources))
	}
}

// TestWeakestPathConfidenceToTakesWeakestAcrossAllPaths 验证 per-source confidence 取
// 同一 root 到 contract 的所有路径中最弱者（与 contract.confidence 的跨路径最弱一致），
// 而非仅按最短路径。修复前 shortestContractPath 只按长度选路，若同长的两条路径一强一弱
// 而先命中强路径，source.confidence 会被高估为 high，与 contract.confidence 自相矛盾。
func TestWeakestPathConfidenceToTakesWeakestAcrossAllPaths(t *testing.T) {
	const contractID = "http:route:orders"
	// 菱形：root S -> A(high) -> H -> C 与 root S -> B(medium) -> H -> C，两条到 C 的路径同长。
	root := ImpactNode{
		ID: "func:S", Kind: "func", Confidence: facts.ConfidenceHigh,
		Children: []ImpactNode{
			{ID: "func:A", Kind: "func", Confidence: facts.ConfidenceHigh, Children: []ImpactNode{
				{ID: contractID, Kind: "http_endpoint", Confidence: facts.ConfidenceHigh},
			}},
			{ID: "func:B", Kind: "func", Confidence: facts.ConfidenceMedium, Children: []ImpactNode{
				{ID: contractID, Kind: "http_endpoint", Confidence: facts.ConfidenceMedium},
			}},
		},
	}
	if got := weakestPathConfidenceTo(root, contractID); got != facts.ConfidenceMedium {
		t.Fatalf("weakestPathConfidenceTo = %q, want medium (weakest across both paths)", got)
	}
	// 无路径到达时返回空。
	if got := weakestPathConfidenceTo(root, "http:route:missing"); got != "" {
		t.Fatalf("weakestPathConfidenceTo(missing) = %q, want empty", got)
	}
}
