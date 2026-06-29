package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIgnoresBusinessSyntaxOverrides(t *testing.T) {
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

	if got.Analysis.MaxDepth != 0 || len(got.Analysis.StopPropagation) != 0 {
		t.Fatalf("unexpected analysis defaults: %#v", got.Analysis)
	}
}

func TestLoadMergesAnalysisConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-analyzer.json")
	body := []byte(`{
  "analysis": {
    "maxDepth": 3,
    "stopPropagation": ["internal/generated/**", "internal/generated/**"]
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
}

func TestDefaultConfigDoesNotExposeOutputEvidenceSwitches(t *testing.T) {
	data, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range [][]byte{[]byte("includeDiff"), []byte("includeRawEvidence")} {
		if bytes.Contains(data, retired) {
			t.Fatalf("retired output config %q remains: %s", retired, data)
		}
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
