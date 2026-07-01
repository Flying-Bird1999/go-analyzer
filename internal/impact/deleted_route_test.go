package impact

import (
	"testing"

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

	RecoverDeletedRoutes(changes, nil, store, "git_diff")

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

	result := AnalyzeTrees(store)
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

func TestRecoveredDeletedRouteUsesAnnotationWhenItExtendsRecoveredRoutePath(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	handler := facts.SymbolID("func:example.com/project/controller/sms::SmsRecordPage")
	route := facts.RouteRegistrationFact{
		ID:                "route:deleted:router/sms_router.go:GET:/records:23:0",
		Method:            "GET",
		LocalPath:         "/records",
		ResolvedPath:      "/api/bff-web/sc/message/sms/records",
		GroupVar:          "smsGroup",
		HandlerRaw:        "sms.SmsRecordPage",
		HandlerSymbol:     handler,
		RecoveredFromDiff: true,
		File:              "router/sms_router.go",
		Span:              facts.SourceSpan{File: "router/sms_router.go", StartLine: 23, EndLine: 23},
	}
	store.Routes = append(store.Routes, route)
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:func:example.com/project/controller/sms::SmsRecordPage:GET:/admin/api/bff-web/sc/message/sms/records:0",
		Kind:          "annotation",
		Method:        "GET",
		Path:          "/admin/api/bff-web/sc/message/sms/records",
		Raw:           "@Get /admin/api/bff-web/sc/message/sms/records",
		HandlerSymbol: handler,
		Span:          facts.SourceSpan{File: "controller/sms/sms.go", StartLine: 17, EndLine: 17},
	})
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:         "change:route_deleted:router/sms_router.go:23:0",
		Kind:       facts.ChangeKindRouteDeleted,
		TargetID:   route.ID,
		File:       "router/sms_router.go",
		Source:     "git_diff_deleted_route",
		Confidence: facts.ConfidenceHigh,
	})

	result := AnalyzeTrees(store)
	root := mustTreeRoot(t, result, "change:route_deleted:router/sms_router.go:23:0")
	if len(root.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", root.Endpoints)
	}
	if root.Endpoints[0].Method != "GET" || root.Endpoints[0].Path != "/admin/api/bff-web/sc/message/sms/records" {
		t.Fatalf("endpoint = %#v", root.Endpoints[0])
	}
	if root.Root.Path != "/api/bff-web/sc/message/sms/records" {
		t.Fatalf("route node path = %q", root.Root.Path)
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

	RecoverDeletedRoutes(changes, nil, store, "git_diff")

	if len(store.Routes) != 0 || len(store.Changes) != 0 {
		t.Fatalf("non-Go deleted routes were recovered: routes=%#v changes=%#v", store.Routes, store.Changes)
	}
}
