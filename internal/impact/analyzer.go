package impact

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

type Result struct {
	ImpactedEndpoints []EndpointImpact      `json:"impacted_endpoints"`
	EvidenceChains    []graph.EvidenceChain `json:"evidence_chains"`
	ModuleImpacts     []ModuleImpact        `json:"module_impacts"`
	Diagnostics       []string              `json:"diagnostics"`
}

func Analyze(store *facts.Store) Result {
	a := newAnalyzer(store)
	result := a.run()
	sort.Slice(result.ImpactedEndpoints, func(i, j int) bool {
		return result.ImpactedEndpoints[i].ID < result.ImpactedEndpoints[j].ID
	})
	sort.Slice(result.EvidenceChains, func(i, j int) bool {
		return result.EvidenceChains[i].ID < result.EvidenceChains[j].ID
	})
	return result
}
