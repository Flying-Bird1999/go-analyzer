package output

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// AddGrpcSourcesSnapshot 将 gRPC 变更源及其静态 BFF 消费关系合入 impact 文档。
func AddGrpcSourcesSnapshot(doc *ImpactDocument, snapshot facts.Snapshot, results []dependency.GrpcImpactSource) {
	store := snapshot.Store()
	for _, result := range results {
		source := GrpcSourceImpact{
			Grpc: GrpcOperationSummary{
				FullMethod:   result.Grpc.FullMethod,
				ProtoPackage: result.Grpc.ProtoPackage,
				Service:      result.Grpc.Service,
				Method:       result.Grpc.Method,
			},
			Consumers:         []GrpcConsumerImpact{},
			ImpactedEndpoints: []EndpointSummary{},
		}
		for _, consumer := range result.Consumers {
			source.Consumers = append(source.Consumers, GrpcConsumerImpact{
				Endpoint: endpointForDependency(consumer.Endpoint), Routes: endpointsForDependency(consumer.Routes), Relation: "may_call",
				Handlers: symbolsForDependency(&store, consumer.Handlers), Clients: clientsForDependency(consumer.Clients), Chains: chainsForDependency(&store, consumer.Chains),
			})
			summary := EndpointSummary{Method: consumer.Endpoint.Method, Path: consumer.Endpoint.Path, Routes: endpointsForDependency(consumer.Routes)}
			source.ImpactedEndpoints = append(source.ImpactedEndpoints, summary)
			doc.Summary.ImpactedEndpoints = append(doc.Summary.ImpactedEndpoints, summary)
		}
		normalizeGrpcSource(&source)
		doc.GrpcSources = append(doc.GrpcSources, source)
	}
	doc.EndpointSourcesSummary = buildEndpointSourcesSummary(*doc)
}

func normalizeGrpcSource(source *GrpcSourceImpact) {
	sort.Slice(source.Consumers, func(i, j int) bool {
		left, right := source.Consumers[i].Endpoint, source.Consumers[j].Endpoint
		if left.Method != right.Method {
			return left.Method < right.Method
		}
		return left.Path < right.Path
	})
	sortEndpointSummaries(source.ImpactedEndpoints)
	source.ImpactedEndpoints = uniqueEndpointSummaries(source.ImpactedEndpoints)
}

func addEndpointGrpcSource(builders map[string]*endpointSourceSummaryBuilder, source GrpcSourceImpact) {
	metadata := endpointSourceMetadata{sourceType: "grpc", grpcFullMethod: source.Grpc.FullMethod}
	for _, consumer := range source.Consumers {
		endpoint := EndpointSummary{Method: consumer.Endpoint.Method, Path: consumer.Endpoint.Path, Routes: consumer.Routes}
		endpointID := endpointKey(endpoint)
		builder := builders[endpointID]
		if builder == nil {
			builder = &endpointSourceSummaryBuilder{
				summary: EndpointSourceSummary{Method: endpoint.Method, Path: endpoint.Path, Sources: []EndpointImpactSource{}},
				sources: map[string]EndpointImpactSource{},
			}
			builders[endpointID] = builder
		}
		sourceKey := endpointImpactSourceKey(metadata)
		impactSource := builder.sources[sourceKey]
		if impactSource.SourceType == "" {
			impactSource = EndpointImpactSource{
				SourceType:     "grpc",
				GrpcFullMethod: source.Grpc.FullMethod,
				RootSymbols:    []EndpointRootSymbolSummary{},
				Chains:         [][]string{},
			}
		}
		for _, chain := range consumer.Chains {
			labels := []string{"grpc " + source.Grpc.FullMethod}
			client := chain.client
			if client.GoPackage == "" && client.ClientType == "" && client.GoMethod == "" && len(consumer.Clients) == 1 {
				client = consumer.Clients[0]
			}
			if client.GoPackage != "" || client.ClientType != "" || client.GoMethod != "" {
				labels = append(labels, strings.TrimSpace("generated_client "+client.GoPackage+" "+client.ClientType+"."+client.GoMethod))
			}
			if chain.CallSite.File != "" {
				labels = append(labels, fmt.Sprintf("call_site %s:%d:%d", chain.CallSite.File, chain.CallSite.Line, chain.CallSite.Column))
			}
			for index := len(chain.Symbols) - 1; index >= 0; index-- {
				symbol := chain.Symbols[index]
				labels = append(labels, strings.TrimSpace(symbol.Kind+" "+symbol.Name))
			}
			labels = append(labels, endpoint.Method+" "+endpoint.Path)
			impactSource.Chains = append(impactSource.Chains, labels)
		}
		builder.sources[sourceKey] = impactSource
	}
}
