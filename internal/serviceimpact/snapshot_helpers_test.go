package serviceimpact

import (
	"context"

	"gopkg.inshopline.com/bff/go-analyzer/internal/analysis"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func AnalyzeTrees(store *facts.Store) TreeResult {
	limits, _ := analysis.DefaultLimits().Normalize()
	result, _ := AnalyzeSnapshotContext(context.Background(), facts.Freeze(store), limits)
	return result
}
