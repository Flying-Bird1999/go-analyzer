// query.go 在可执行调用图和 route/annotation 图之上提供 endpoint 与 gRPC 双向查询。
package dependency

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

type Endpoint struct {
	Method string
	Path   string
}
type GrpcMethod struct {
	FullMethod   string
	ProtoPackage string
	Service      string
	Method       string
}
type Chain struct {
	Symbols []facts.SymbolID
	Call    facts.GrpcCallFact
}
type GrpcDependency struct {
	Operation facts.GrpcOperationFact
	Clients   []facts.GrpcClientBinding
	Chains    []Chain
}
type EndpointAsset struct {
	Endpoint Endpoint
	Routes   []Endpoint
	Handlers []facts.SymbolID
	Grpc     []GrpcDependency
}
type GrpcImpactConsumer struct {
	Endpoint Endpoint
	Routes   []Endpoint
	Handlers []facts.SymbolID
	Clients  []facts.GrpcClientBinding
	Chains   []Chain
}
type GrpcImpactSource struct {
	Grpc      GrpcMethod
	Consumers []GrpcImpactConsumer
}

func ParseEndpoint(raw string) (Endpoint, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "/") {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q", raw)
	}
	return Endpoint{Method: strings.ToUpper(fields[0]), Path: fields[1]}, nil
}
func ParseGrpcMethod(raw string) (GrpcMethod, error) {
	if !strings.HasPrefix(raw, "/") {
		return GrpcMethod{}, fmt.Errorf("invalid gRPC method %q", raw)
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return GrpcMethod{}, fmt.Errorf("invalid gRPC method %q", raw)
	}
	service := strings.Split(parts[0], ".")
	if len(service) < 2 {
		return GrpcMethod{}, fmt.Errorf("invalid gRPC method %q", raw)
	}
	return GrpcMethod{FullMethod: raw, ProtoPackage: strings.Join(service[:len(service)-1], "."), Service: service[len(service)-1], Method: parts[1]}, nil
}

func FindEndpointAssets(store *facts.Store, inputs []Endpoint) ([]EndpointAsset, error) {
	routes := graph.NewRouteGraph(store)
	calls := graph.NewCallGraph(store)
	handlers := endpointHandlers(store, routes)
	operations := map[string]facts.GrpcOperationFact{}
	for _, operation := range store.GrpcOperations {
		operations[operation.ID] = operation
	}
	var out []EndpointAsset
	for _, input := range uniqueEndpoints(inputs) {
		matched := handlers[input]
		if len(matched) == 0 {
			return nil, fmt.Errorf("endpoint not found: %s %s", input.Method, input.Path)
		}
		asset := EndpointAsset{
			Endpoint: input,
			Routes:   routesForHandlers(routes, matched),
			Handlers: append([]facts.SymbolID(nil), matched...),
		}
		byOperation := map[string]*GrpcDependency{}
		for _, handler := range matched {
			for _, chain := range forwardChains(calls, handler) {
				operation, ok := operations[chain.Call.OperationID]
				if !ok {
					return nil, fmt.Errorf("gRPC call references missing operation %s", chain.Call.OperationID)
				}
				dependency := byOperation[operation.ID]
				if dependency == nil {
					dependency = &GrpcDependency{Operation: operation}
					byOperation[operation.ID] = dependency
				}
				dependency.Clients = appendBinding(dependency.Clients, chain.Call.ClientBinding)
				dependency.Chains = append(dependency.Chains, chain)
			}
		}
		for _, dependency := range byOperation {
			sortDependency(dependency)
			asset.Grpc = append(asset.Grpc, *dependency)
		}
		sort.Slice(asset.Grpc, func(i, j int) bool { return asset.Grpc[i].Operation.FullMethod < asset.Grpc[j].Operation.FullMethod })
		out = append(out, asset)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Endpoint.Method != out[j].Endpoint.Method {
			return out[i].Endpoint.Method < out[j].Endpoint.Method
		}
		return out[i].Endpoint.Path < out[j].Endpoint.Path
	})
	return out, nil
}

// FindGrpcImpactSources maps changed upstream gRPC methods to BFF HTTP consumers.
func FindGrpcImpactSources(store *facts.Store, inputs []GrpcMethod) ([]GrpcImpactSource, error) {
	handlers := endpointHandlers(store, graph.NewRouteGraph(store))
	endpoints := make([]Endpoint, 0, len(handlers))
	for endpoint := range handlers {
		endpoints = append(endpoints, endpoint)
	}
	assets, err := FindEndpointAssets(store, endpoints)
	if err != nil {
		return nil, err
	}
	var out []GrpcImpactSource
	for _, input := range uniqueGrpc(inputs) {
		result := GrpcImpactSource{Grpc: input}
		for _, asset := range assets {
			for _, dependency := range asset.Grpc {
				if dependency.Operation.FullMethod == input.FullMethod {
					result.Consumers = append(result.Consumers, GrpcImpactConsumer{Endpoint: asset.Endpoint, Routes: asset.Routes, Handlers: asset.Handlers, Clients: dependency.Clients, Chains: dependency.Chains})
				}
			}
		}
		sort.Slice(result.Consumers, func(i, j int) bool {
			if result.Consumers[i].Endpoint.Method != result.Consumers[j].Endpoint.Method {
				return result.Consumers[i].Endpoint.Method < result.Consumers[j].Endpoint.Method
			}
			return result.Consumers[i].Endpoint.Path < result.Consumers[j].Endpoint.Path
		})
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Grpc.FullMethod < out[j].Grpc.FullMethod })
	return out, nil
}

func endpointHandlers(store *facts.Store, routes *graph.RouteGraph) map[Endpoint][]facts.SymbolID {
	out := map[Endpoint][]facts.SymbolID{}
	for handler, registered := range routes.RoutesByHandler {
		annotations := routes.AnnotationsForHandler(handler)
		if len(annotations) > 0 {
			for _, annotation := range annotations {
				endpoint := Endpoint{Method: strings.ToUpper(annotation.Method), Path: annotation.Path}
				out[endpoint] = appendSymbol(out[endpoint], handler)
			}
			continue
		}
		for _, route := range registered {
			if route.ResolvedPath != "" {
				endpoint := Endpoint{Method: strings.ToUpper(route.Method), Path: route.ResolvedPath}
				out[endpoint] = appendSymbol(out[endpoint], handler)
			}
		}
	}
	return out
}
func routesForHandlers(routes *graph.RouteGraph, handlers []facts.SymbolID) []Endpoint {
	var out []Endpoint
	for _, handler := range handlers {
		for _, route := range routes.RoutesByHandler[handler] {
			if route.ResolvedPath != "" {
				out = append(out, Endpoint{Method: strings.ToUpper(route.Method), Path: route.ResolvedPath})
			}
		}
	}
	out = uniqueEndpoints(out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}
func forwardChains(calls *graph.CallGraph, handler facts.SymbolID) []Chain {
	type state struct {
		symbol facts.SymbolID
		path   []facts.SymbolID
	}
	queue := []state{{handler, []facts.SymbolID{handler}}}
	visited := map[facts.SymbolID]bool{handler: true}
	var out []Chain
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, call := range calls.GrpcCalls(current.symbol) {
			out = append(out, Chain{Symbols: append([]facts.SymbolID(nil), current.path...), Call: call})
		}
		for _, next := range calls.Callees(current.symbol) {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, state{next, append(append([]facts.SymbolID(nil), current.path...), next)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Call.ID < out[j].Call.ID })
	return out
}
func uniqueEndpoints(values []Endpoint) []Endpoint {
	seen := map[Endpoint]bool{}
	var out []Endpoint
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
func uniqueGrpc(values []GrpcMethod) []GrpcMethod {
	seen := map[string]bool{}
	var out []GrpcMethod
	for _, value := range values {
		if !seen[value.FullMethod] {
			seen[value.FullMethod] = true
			out = append(out, value)
		}
	}
	return out
}
func appendSymbol(values []facts.SymbolID, value facts.SymbolID) []facts.SymbolID {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func appendBinding(values []facts.GrpcClientBinding, value facts.GrpcClientBinding) []facts.GrpcClientBinding {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func sortDependency(value *GrpcDependency) {
	sort.Slice(value.Clients, func(i, j int) bool {
		return value.Clients[i].GoPackage+value.Clients[i].ClientType+value.Clients[i].GoMethod < value.Clients[j].GoPackage+value.Clients[j].ClientType+value.Clients[j].GoMethod
	})
	sort.Slice(value.Chains, func(i, j int) bool { return value.Chains[i].Call.ID < value.Chains[j].Call.ID })
}
