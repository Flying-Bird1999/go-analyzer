// config_test.go 验证 impact 配置的加载、严格字段校验以及模块变更过滤行为。

package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// 测试场景：项目内不存在默认配置文件时返回零值配置，且默认仍分析模块变更。
func TestLoadImpactConfigReturnsDefaultWhenProjectConfigMissing(t *testing.T) {
	cfg, err := LoadImpactConfig(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ShouldAnalyzeModuleChanges() {
		t.Fatal("module changes should be analyzed by default")
	}
}

// 测试场景：glob 模式应正确过滤掉匹配的模块变更，保留未命中的模块变更。
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

// 测试场景：显式 analyzeModuleChanges=false 时应整体关闭模块变更分析。
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

// 测试场景：ignoredModuleChanges 中的非法 glob 模式应在加载阶段被拒绝。
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

// 测试场景：旧的 route schema 字段不在 impact 配置范围内，应被严格校验拒绝。
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

// 测试场景：拼错的字段名应被严格校验拒绝，避免配置被静默忽略。
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
