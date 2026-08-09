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
				Identity: result.Grpc.Identity,
				GoMethod: result.Grpc.GoMethod,
			},
			Consumers:         []GrpcConsumerImpact{},
			ImpactedEndpoints: []EndpointSummary{},
		}
		for _, consumer := range result.Consumers {
			source.Consumers = append(source.Consumers, GrpcConsumerImpact{
				Endpoint: endpointForDependency(consumer.Endpoint), Routes: endpointsForDependency(consumer.Routes), Relation: "may_call",
				Handlers: symbolsForDependency(&store, consumer.Handlers), Chains: chainsForDependency(&store, consumer.Chains),
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
	metadata := endpointSourceMetadata{sourceType: "grpc", grpcIdentity: source.Grpc.Identity}
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
				SourceType:   "grpc",
				GrpcIdentity: source.Grpc.Identity,
				RootSymbols:  []EndpointRootSymbolSummary{},
				Chains:       [][]string{},
			}
		}
		for _, chain := range consumer.Chains {
			// identity 本身已经是 <生成包 import 路径>.<Service>/<GoMethod>，不必再单独
			// 拼一条 generated_client 标签重复同样的信息。
			labels := []string{"grpc " + source.Grpc.Identity}
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
