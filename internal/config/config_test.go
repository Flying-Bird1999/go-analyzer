package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesProjectOverridesWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-analyzer.json")
	body := []byte(`{
  "project": {
    "skipDirs": ["fixtures"]
  },
  "route": {
    "httpMethods": ["SEARCH"],
    "handlerWrappers": ["CustomController"],
    "routeGroupWrappers": [{"contains": "Shield"}]
  },
  "annotation": {
    "methods": ["Search"]
  }
}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, got.Project.SkipDirs, "vendor")
	assertContains(t, got.Project.SkipDirs, "fixtures")
	assertContains(t, got.Route.HTTPMethods, "GET")
	assertContains(t, got.Route.HTTPMethods, "SEARCH")
	assertContains(t, got.Route.HandlerWrappers, "ControllerWithResp")
	assertContains(t, got.Route.HandlerWrappers, "CustomController")
	assertContains(t, got.Annotation.Methods, "GET")
	assertContains(t, got.Annotation.Methods, "SEARCH")
	if !got.IsRouteGroupWrapper("TenantShield") {
		t.Fatalf("TenantShield should match configured route group wrapper: %#v", got.Route.RouteGroupWrappers)
	}
}

func assertContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Fatalf("%q not found in %#v", want, items)
}
