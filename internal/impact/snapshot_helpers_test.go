package impact

import (
	"gopkg.inshopline.com/bff/go-analyzer/internal/endpoint"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func AnalyzeTrees(store *facts.Store) TreeResult {
	snapshot := facts.Freeze(store)
	return AnalyzeSnapshot(snapshot, endpoint.Build(snapshot))
}
