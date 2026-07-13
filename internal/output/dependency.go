package output

import (
	"encoding/json"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type dependencyProject struct {
	Module string `json:"module"`
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
	Endpoint     dependencyEndpoint   `json:"endpoint"`
	Routes       []dependencyEndpoint `json:"routes"`
	Handlers     []dependencySymbol   `json:"handlers"`
	Dependencies struct {
		Grpc []dependencyGrpc `json:"grpc"`
	} `json:"dependencies"`
}

func RenderEndpointAssets(store *facts.Store, assets []dependency.EndpointAsset) ([]byte, error) {
	doc := endpointAssetDocument{Project: projectForDependency(store), EndpointAssets: []endpointAsset{}}
	for _, asset := range assets {
		item := endpointAsset{Endpoint: endpointForDependency(asset.Endpoint), Routes: endpointsForDependency(asset.Routes), Handlers: symbolsForDependency(store, asset.Handlers)}
		item.Dependencies.Grpc = []dependencyGrpc{}
		for _, grpc := range asset.Grpc {
			item.Dependencies.Grpc = append(item.Dependencies.Grpc, grpcForDependency(store, grpc))
		}
		doc.EndpointAssets = append(doc.EndpointAssets, item)
	}
	return renderDependency(doc)
}
func projectForDependency(store *facts.Store) dependencyProject {
	return dependencyProject{Module: store.Project.ModulePath}
}
func endpointForDependency(value dependency.Endpoint) dependencyEndpoint {
	return dependencyEndpoint{Method: value.Method, Path: value.Path}
}
func endpointsForDependency(values []dependency.Endpoint) []dependencyEndpoint {
	out := make([]dependencyEndpoint, 0, len(values))
	for _, value := range values {
		out = append(out, endpointForDependency(value))
	}
	return out
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
