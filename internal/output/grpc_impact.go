package output

import (
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// AddGrpcSources 将 gRPC 变更源及其静态 BFF 消费关系合入 impact 文档。
func AddGrpcSources(doc *ImpactDocument, store *facts.Store, results []dependency.GrpcImpactSource) {
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
				Handlers: symbolsForDependency(store, consumer.Handlers), Clients: clientsForDependency(consumer.Clients), Chains: chainsForDependency(store, consumer.Chains),
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
				Confidence:     facts.ConfidenceHigh,
			}
		}
		for _, chain := range consumer.Chains {
			labels := make([]string, 0, len(chain.Symbols)+1)
			for index, symbol := range chain.Symbols {
				labels = append(labels, strings.TrimSpace(symbol.Kind+" "+symbol.Name))
				if index == 0 {
					impactSource.RootSymbols = append(impactSource.RootSymbols, EndpointRootSymbolSummary{ID: symbol.ID, Kind: symbol.Kind, Name: symbol.Name, File: symbol.File})
				}
			}
			labels = append(labels, "grpc "+source.Grpc.FullMethod)
			impactSource.Chains = append(impactSource.Chains, labels)
		}
		builder.sources[sourceKey] = impactSource
	}
}
