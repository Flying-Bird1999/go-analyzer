package impact

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestRecoverDeletedRoutesAddsRouteDeletedChangeAndEndpoint(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
		ID:        "route_group:router:init:api",
		GroupVar:  "api",
		Prefix:    "/api",
		RouteFunc: "func:example.com/project/router::Init",
		Span:      facts.SourceSpan{File: "router/router.go", StartLine: 10, EndLine: 10},
	})
	changes := []diff.FileChange{{
		OldPath: "router/router.go",
		NewPath: "router/router.go",
		Status:  diff.StatusModified,
		DeletedBlocks: []diff.DeletedBlock{{
			OldStartLine:  21,
			NewAnchorLine: 21,
			Lines: []string{
				"\tapi.POST(\"/legacy\", legacy.Delete)",
			},
		}},
	}}

	RecoverDeletedRoutes(changes, nil, store, config.Default(), "git_diff")

	if len(store.Routes) != 1 {
		t.Fatalf("routes = %#v", store.Routes)
	}
	route := store.Routes[0]
	if route.Method != "POST" || route.LocalPath != "/legacy" || route.ResolvedPath != "/api/legacy" {
		t.Fatalf("route = %#v", route)
	}
	if route.HandlerRaw != "legacy.Delete" || route.GroupID != "route_group:router:init:api" {
		t.Fatalf("route linkage = %#v", route)
	}
	if len(store.Changes) != 1 {
		t.Fatalf("changes = %#v", store.Changes)
	}
	change := store.Changes[0]
	if change.Kind != facts.ChangeKindRouteDeleted || change.TargetID != route.ID {
		t.Fatalf("change = %#v", change)
	}

	result := AnalyzeTrees(store, TreeOptions{})
	root := mustTreeRoot(t, result, change.ID)
	if root.Root.Kind != "route" {
		t.Fatalf("root = %#v", root.Root)
	}
	if len(root.Endpoints) != 1 || root.Endpoints[0].Method != "POST" || root.Endpoints[0].Path != "/api/legacy" {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
	if len(root.Root.Children) != 1 {
		t.Fatalf("route children = %#v", root.Root.Children)
	}
	endpoint := root.Root.Children[0]
	if endpoint.Relation != "deleted_route_endpoint" || endpoint.Confidence != facts.ConfidenceMedium {
		t.Fatalf("deleted route endpoint evidence = %#v", endpoint)
	}
}

func TestRecoverDeletedRoutesIgnoresNonGoFiles(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	changes := []diff.FileChange{{
		OldPath: "web/router.ts",
		NewPath: "web/router.ts",
		Status:  diff.StatusModified,
		DeletedBlocks: []diff.DeletedBlock{{
			OldStartLine:  10,
			NewAnchorLine: 10,
			Lines:         []string{`api.GET("/orders", handler)`},
		}},
	}}

	RecoverDeletedRoutes(changes, nil, store, config.Default(), "git_diff")

	if len(store.Routes) != 0 || len(store.Changes) != 0 {
		t.Fatalf("non-Go deleted routes were recovered: routes=%#v changes=%#v", store.Routes, store.Changes)
	}
}
