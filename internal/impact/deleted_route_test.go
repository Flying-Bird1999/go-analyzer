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

	RecoverDeletedRoutes(changes, store, config.Default(), "git_diff")

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
}
