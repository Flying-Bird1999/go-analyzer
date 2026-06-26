package output

import (
	"encoding/json"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

func RenderImpactJSON(result impact.Result) ([]byte, error) {
	doc := result
	sort.Slice(doc.ImpactedEndpoints, func(i, j int) bool {
		return doc.ImpactedEndpoints[i].ID < doc.ImpactedEndpoints[j].ID
	})
	sort.Slice(doc.EvidenceChains, func(i, j int) bool {
		return doc.EvidenceChains[i].ID < doc.EvidenceChains[j].ID
	})
	sort.Slice(doc.ModuleImpacts, func(i, j int) bool {
		if doc.ModuleImpacts[i].ModulePath != doc.ModuleImpacts[j].ModulePath {
			return doc.ModuleImpacts[i].ModulePath < doc.ModuleImpacts[j].ModulePath
		}
		return doc.ModuleImpacts[i].Basis < doc.ModuleImpacts[j].Basis
	})
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
