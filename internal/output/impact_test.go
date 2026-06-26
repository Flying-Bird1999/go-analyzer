package output

import (
	"encoding/json"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

func TestRenderImpactJSON(t *testing.T) {
	result := impact.Result{
		ImpactedEndpoints: []impact.EndpointImpact{{
			ID:     "endpoint:b",
			Method: "GET",
			Path:   "/b",
		}, {
			ID:     "endpoint:a",
			Method: "GET",
			Path:   "/a",
		}},
		EvidenceChains: []graph.EvidenceChain{
			graph.NewEvidenceChain("chain:b"),
			graph.NewEvidenceChain("chain:a"),
		},
	}

	out, err := RenderImpactJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	var doc impact.Result
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ImpactedEndpoints[0].ID != "endpoint:a" {
		t.Fatalf("first endpoint = %q", doc.ImpactedEndpoints[0].ID)
	}
	if doc.EvidenceChains[0].ID != "chain:a" {
		t.Fatalf("first chain = %q", doc.EvidenceChains[0].ID)
	}
}
