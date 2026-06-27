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
	if got.Analysis.IncludeRawEvidence == nil || !*got.Analysis.IncludeRawEvidence {
		t.Fatalf("default includeRawEvidence was not preserved: %#v", got.Analysis)
	}
	if got.Analysis.IncludeDiff == nil || !*got.Analysis.IncludeDiff {
		t.Fatalf("default includeDiff was not preserved: %#v", got.Analysis)
	}
}

func TestLoadMergesAnalysisConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-analyzer.json")
	body := []byte(`{
  "analysis": {
    "maxDepth": 3,
    "stopPropagation": ["internal/generated/**", "internal/generated/**"],
    "includeRawEvidence": false
  }
}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Analysis.MaxDepth != 3 {
		t.Fatalf("maxDepth = %d", got.Analysis.MaxDepth)
	}
	if len(got.Analysis.StopPropagation) != 1 || got.Analysis.StopPropagation[0] != "internal/generated/**" {
		t.Fatalf("stopPropagation = %#v", got.Analysis.StopPropagation)
	}
	if got.Analysis.IncludeRawEvidence == nil || *got.Analysis.IncludeRawEvidence {
		t.Fatalf("includeRawEvidence = %#v", got.Analysis.IncludeRawEvidence)
	}
	if got.Analysis.IncludeDiff == nil || !*got.Analysis.IncludeDiff {
		t.Fatalf("default includeDiff was not preserved: %#v", got.Analysis.IncludeDiff)
	}
}

func TestLoadRejectsNegativeMaxDepth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-analyzer.json")
	if err := os.WriteFile(path, []byte(`{"analysis":{"maxDepth":-1}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected negative maxDepth to be rejected")
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
