package output

import (
	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func RenderJSON(store *facts.Store) ([]byte, error) {
	return RenderSnapshotJSON(facts.Freeze(store))
}

func RenderEndpointAssets(store *facts.Store, assets []dependency.EndpointAsset) ([]byte, error) {
	return RenderEndpointAssetsSnapshot(facts.Freeze(store), assets)
}

func AddGrpcSources(doc *ImpactDocument, store *facts.Store, results []dependency.GrpcImpactSource) {
	AddGrpcSourcesSnapshot(doc, facts.Freeze(store), results)
}
