package output

import (
	"encoding/json"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type dependencyProject struct {
	Module       string                 `json:"module"`
	BuildContext dependencyBuildContext `json:"buildContext"`
}
type dependencyBuildContext struct {
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	Tags       []string `json:"tags"`
	CgoEnabled bool     `json:"cgoEnabled"`
}
type dependencyEndpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
type dependencySymbol struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
}
type dependencyClient struct {
	GoPackage  string `json:"goPackage"`
	ClientType string `json:"clientType"`
	GoMethod   string `json:"goMethod"`
}
type dependencyCallSite struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}
type dependencyChain struct {
	Symbols  []dependencySymbol `json:"symbols"`
	CallSite dependencyCallSite `json:"callSite"`
}
type dependencyGrpc struct {
	FullMethod   string             `json:"fullMethod"`
	ProtoPackage string             `json:"protoPackage"`
	Service      string             `json:"service"`
	Method       string             `json:"method"`
	Clients      []dependencyClient `json:"clients"`
	Chains       []dependencyChain  `json:"chains"`
}
type endpointAssetDocument struct {
	Project        dependencyProject `json:"project"`
	EndpointAssets []endpointAsset   `json:"endpointAssets"`
}
type endpointAsset struct {
	Endpoint     dependencyEndpoint `json:"endpoint"`
	Handlers     []dependencySymbol `json:"handlers"`
	Dependencies struct {
		Grpc []dependencyGrpc `json:"grpc"`
	} `json:"dependencies"`
}
type grpcConsumersDocument struct {
	Project       dependencyProject    `json:"project"`
	GrpcConsumers []grpcConsumerResult `json:"grpcConsumers"`
}
type grpcConsumerResult struct {
	Grpc      dependencyGrpcIdentity `json:"grpc"`
	Consumers []grpcConsumer         `json:"consumers"`
}
type dependencyGrpcIdentity struct {
	FullMethod   string `json:"fullMethod"`
	ProtoPackage string `json:"protoPackage"`
	Service      string `json:"service"`
	Method       string `json:"method"`
}
type grpcConsumer struct {
	Endpoint dependencyEndpoint `json:"endpoint"`
	Handlers []dependencySymbol `json:"handlers"`
	Clients  []dependencyClient `json:"clients"`
	Chains   []dependencyChain  `json:"chains"`
}

func RenderEndpointAssets(store *facts.Store, assets []dependency.EndpointAsset) ([]byte, error) {
	doc := endpointAssetDocument{Project: projectForDependency(store), EndpointAssets: []endpointAsset{}}
	for _, asset := range assets {
		item := endpointAsset{Endpoint: endpointForDependency(asset.Endpoint), Handlers: symbolsForDependency(store, asset.Handlers)}
		item.Dependencies.Grpc = []dependencyGrpc{}
		for _, grpc := range asset.Grpc {
			item.Dependencies.Grpc = append(item.Dependencies.Grpc, grpcForDependency(store, grpc))
		}
		doc.EndpointAssets = append(doc.EndpointAssets, item)
	}
	return renderDependency(doc)
}
func RenderGrpcConsumers(store *facts.Store, results []dependency.GrpcConsumerResult) ([]byte, error) {
	doc := grpcConsumersDocument{Project: projectForDependency(store), GrpcConsumers: []grpcConsumerResult{}}
	for _, result := range results {
		item := grpcConsumerResult{Grpc: identityForDependency(result.Grpc), Consumers: []grpcConsumer{}}
		for _, consumer := range result.Consumers {
			item.Consumers = append(item.Consumers, grpcConsumer{Endpoint: endpointForDependency(consumer.Endpoint), Handlers: symbolsForDependency(store, consumer.Handlers), Clients: clientsForDependency(consumer.Clients), Chains: chainsForDependency(store, consumer.Chains)})
		}
		doc.GrpcConsumers = append(doc.GrpcConsumers, item)
	}
	return renderDependency(doc)
}
func projectForDependency(store *facts.Store) dependencyProject {
	return dependencyProject{Module: store.Project.ModulePath, BuildContext: dependencyBuildContext{GOOS: store.Project.BuildContext.GOOS, GOARCH: store.Project.BuildContext.GOARCH, Tags: append([]string(nil), store.Project.BuildContext.Tags...), CgoEnabled: store.Project.BuildContext.CgoEnabled}}
}
func endpointForDependency(value dependency.Endpoint) dependencyEndpoint {
	return dependencyEndpoint{Method: value.Method, Path: value.Path}
}
func identityForDependency(value dependency.GrpcMethod) dependencyGrpcIdentity {
	return dependencyGrpcIdentity{FullMethod: value.FullMethod, ProtoPackage: value.ProtoPackage, Service: value.Service, Method: value.Method}
}
func grpcForDependency(store *facts.Store, value dependency.GrpcDependency) dependencyGrpc {
	return dependencyGrpc{FullMethod: value.Operation.FullMethod, ProtoPackage: value.Operation.ProtoPackage, Service: value.Operation.Service, Method: value.Operation.Method, Clients: clientsForDependency(value.Clients), Chains: chainsForDependency(store, value.Chains)}
}
func clientsForDependency(values []facts.GrpcClientBinding) []dependencyClient {
	out := make([]dependencyClient, 0, len(values))
	for _, value := range values {
		out = append(out, dependencyClient{GoPackage: value.GoPackage, ClientType: value.ClientType, GoMethod: value.GoMethod})
	}
	return out
}
func symbolsForDependency(store *facts.Store, ids []facts.SymbolID) []dependencySymbol {
	byID := map[facts.SymbolID]facts.SymbolFact{}
	for _, symbol := range store.Symbols {
		byID[symbol.ID] = symbol
	}
	out := make([]dependencySymbol, 0, len(ids))
	for _, id := range ids {
		symbol := byID[id]
		out = append(out, dependencySymbol{ID: string(id), Kind: symbol.Kind, Name: symbol.Name, File: symbol.Span.File})
	}
	return out
}
func chainsForDependency(store *facts.Store, values []dependency.Chain) []dependencyChain {
	out := make([]dependencyChain, 0, len(values))
	for _, value := range values {
		out = append(out, dependencyChain{Symbols: symbolsForDependency(store, value.Symbols), CallSite: dependencyCallSite{File: value.Call.Span.File, Line: value.Call.Span.StartLine, Column: value.Call.Span.StartCol}})
	}
	return out
}
func renderDependency(value any) ([]byte, error) {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
