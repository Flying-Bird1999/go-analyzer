package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestLoadImpactConfigReturnsDefaultWhenProjectConfigMissing(t *testing.T) {
	cfg, err := LoadImpactConfig(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ShouldAnalyzeModuleChanges() {
		t.Fatal("module changes should be analyzed by default")
	}
}

func TestLoadImpactConfigFiltersIgnoredModuleChangeGlob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-impact.config.json")
	if err := os.WriteFile(path, []byte(`{
  "ignoredModuleChanges": [
    "gopkg.inshopline.com/sc1/app/modules/*/proto"
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadImpactConfig("", path)
	if err != nil {
		t.Fatal(err)
	}
	changes := cfg.FilterModuleChanges([]facts.ModuleChangeFact{
		{Path: "gopkg.inshopline.com/sc1/app/modules/medium/proto"},
		{Path: "github.com/shopspring/decimal"},
	})
	if len(changes) != 1 || changes[0].Path != "github.com/shopspring/decimal" {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestLoadImpactConfigCanDisableAllModuleChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-impact.config.json")
	if err := os.WriteFile(path, []byte(`{
  "analyzeModuleChanges": false
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadImpactConfig("", path)
	if err != nil {
		t.Fatal(err)
	}
	changes := cfg.FilterModuleChanges([]facts.ModuleChangeFact{{Path: "github.com/shopspring/decimal"}})
	if len(changes) != 0 {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestLoadImpactConfigRejectsInvalidIgnoredModulePattern(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-impact.config.json")
	if err := os.WriteFile(path, []byte(`{
  "ignoredModuleChanges": ["gopkg.inshopline.com/sc1/app/modules/[*/proto"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadImpactConfig("", path); err == nil {
		t.Fatal("expected invalid pattern to fail")
	}
}

func TestLoadImpactConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-impact.config.json")
	if err := os.WriteFile(path, []byte(`{
  "route": {
    "handlerWrappers": ["ControllerWithReqResp"]
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadImpactConfig("", path); err == nil {
		t.Fatal("expected stale route config to fail")
	}
}

func TestLoadImpactConfigRejectsMisspelledFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go-impact.config.json")
	if err := os.WriteFile(path, []byte(`{
  "ignoredModuleChange": ["github.com/example/module"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadImpactConfig("", path); err == nil {
		t.Fatal("expected misspelled config field to fail")
	}
}
