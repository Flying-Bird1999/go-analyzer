package output

import (
	"encoding/json"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestDependencyRenderersDoNotExposeBuildContext(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project", facts.BuildContextFact{GOOS: "linux", GOARCH: "amd64", Tags: []string{"internal"}, CgoEnabled: false})
	renders := []struct {
		name string
		run  func() ([]byte, error)
	}{
		{
			name: "endpoint assets",
			run: func() ([]byte, error) {
				return RenderEndpointAssets(store, []dependency.EndpointAsset{{Endpoint: dependency.Endpoint{Method: "GET", Path: "/orders"}}})
			},
		},
	}
	for _, tt := range renders {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.run()
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Project map[string]any `json:"project"`
			}
			if err := json.Unmarshal(out, &document); err != nil {
				t.Fatal(err)
			}
			if document.Project["module"] != "example.com/project" {
				t.Fatalf("project=%#v", document.Project)
			}
			if _, ok := document.Project["buildContext"]; ok {
				t.Fatalf("buildContext leaked: %#v", document.Project)
			}
		})
	}
}
