package output

import (
	"bytes"
	"encoding/json"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestRenderEndpointAssetsIsDeterministicForShuffledInput(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	firstHandler := facts.SymbolID("func:example.com/project::A")
	secondHandler := facts.SymbolID("func:example.com/project::B")
	store.Symbols = []facts.SymbolFact{
		{ID: secondHandler, Kind: "func", Name: "B"},
		{ID: firstHandler, Kind: "func", Name: "A"},
	}
	firstAsset := dependency.EndpointAsset{
		Endpoint: dependency.Endpoint{Method: "POST", Path: "/orders"},
		Routes: []dependency.Endpoint{
			{Method: "POST", Path: "/v1/orders"},
			{Method: "POST", Path: "/orders"},
		},
		Handlers: []facts.SymbolID{secondHandler, firstHandler},
	}
	secondAsset := dependency.EndpointAsset{Endpoint: dependency.Endpoint{Method: "GET", Path: "/orders"}}

	first, err := RenderEndpointAssets(store, []dependency.EndpointAsset{firstAsset, secondAsset})
	if err != nil {
		t.Fatal(err)
	}
	firstAsset.Routes[0], firstAsset.Routes[1] = firstAsset.Routes[1], firstAsset.Routes[0]
	firstAsset.Handlers[0], firstAsset.Handlers[1] = firstAsset.Handlers[1], firstAsset.Handlers[0]
	second, err := RenderEndpointAssets(store, []dependency.EndpointAsset{secondAsset, firstAsset})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("shuffled endpoint-assets output differs:\n%s\n%s", first, second)
	}
}

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

func TestRenderEndpointAssetsIncludesRoutes(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	out, err := RenderEndpointAssets(store, []dependency.EndpointAsset{{
		Endpoint: dependency.Endpoint{Method: "GET", Path: "/orders/:id"},
		Routes:   []dependency.Endpoint{{Method: "GET", Path: "/api/orders/:id"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		EndpointAssets []struct {
			Endpoint dependencyEndpoint   `json:"endpoint"`
			Routes   []dependencyEndpoint `json:"routes"`
		} `json:"endpointAssets"`
	}
	if err := json.Unmarshal(out, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.EndpointAssets) != 1 || document.EndpointAssets[0].Endpoint.Path != "/orders/:id" {
		t.Fatalf("endpoint assets=%#v", document.EndpointAssets)
	}
	if got := document.EndpointAssets[0].Routes; len(got) != 1 || got[0].Path != "/api/orders/:id" {
		t.Fatalf("routes=%#v", got)
	}
}
