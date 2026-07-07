// config.go 实现 impact 配置的加载与校验，仅用于模块版本变更的过滤。

// Package config 负责加载可选的 impact 配置。
//
// 该配置只影响 go.mod 模块变更（module change）的过滤，例如业务方明确不需要
// 按版本升级传播的 proto 包；它不是 route/annotation/middleware 语法配置。
// 配置文件使用严格字段校验（DisallowUnknownFields），未知字段、拼错字段或旧的
// route/annotation/middleware schema 都会直接报错，避免业务方误以为配置已生效。
// 配置文件可选，缺失时按默认行为（分析全部模块变更）继续。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// DefaultImpactConfigPath 是项目内默认的 impact 配置文件相对路径。
const DefaultImpactConfigPath = ".analyzer/go-impact.config.json"

// Config 表示 impact 配置。AnalyzeModuleChanges 使用指针以区分“未设置（默认开启）”
// 与“显式关闭”。
type Config struct {
	AnalyzeModuleChanges *bool    `json:"analyzeModuleChanges,omitempty"`
	IgnoredModuleChanges []string `json:"ignoredModuleChanges,omitempty"`
}

// LoadImpactConfig 加载 impact 配置。explicitPath 为空时回退到项目内默认路径，
// 默认路径不存在视为空配置；存在则严格解析并校验。
func LoadImpactConfig(projectRoot, explicitPath string) (Config, error) {
	configPath := strings.TrimSpace(explicitPath)
	if configPath == "" {
		configPath = filepath.Join(projectRoot, DefaultImpactConfigPath)
		if _, err := os.Stat(configPath); err != nil {
			if os.IsNotExist(err) {
				return Config{}, nil
			}
			return Config{}, fmt.Errorf("stat impact config: %w", err)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("read impact config: %w", err)
	}
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	// 严格校验未知字段，旧 schema 或拼写错误必须失败而非被静默忽略。
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse impact config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate 校验 ignoredModuleChanges 中每个模式的合法性：
// 不允许空字符串，包含通配字符的模式必须是合法的 glob。
func (c Config) Validate() error {
	for _, pattern := range c.IgnoredModuleChanges {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return fmt.Errorf("ignoredModuleChanges contains empty module pattern")
		}
		if isGlobPattern(pattern) {
			if _, err := path.Match(pattern, "module"); err != nil {
				return fmt.Errorf("invalid ignoredModuleChanges pattern %q: %w", pattern, err)
			}
		}
	}
	return nil
}

// ShouldAnalyzeModuleChanges 表示是否应分析模块变更；未显式设置时默认为 true。
func (c Config) ShouldAnalyzeModuleChanges() bool {
	if c.AnalyzeModuleChanges == nil {
		return true
	}
	return *c.AnalyzeModuleChanges
}

// FilterModuleChanges 按配置过滤模块变更列表：
// 关闭模块分析时全部丢弃，否则剔除被 ignoredModuleChanges 命中的模块变更。
func (c Config) FilterModuleChanges(changes []facts.ModuleChangeFact) []facts.ModuleChangeFact {
	if !c.ShouldAnalyzeModuleChanges() {
		return nil
	}
	if len(c.IgnoredModuleChanges) == 0 {
		return changes
	}
	out := make([]facts.ModuleChangeFact, 0, len(changes))
	for _, change := range changes {
		if c.IgnoresModule(change.Path) {
			continue
		}
		out = append(out, change)
	}
	return out
}

// IgnoresModule 判断给定 module path 是否应被忽略：
// 精确匹配命中即忽略，glob 模式按 path.Match 命中即忽略。
func (c Config) IgnoresModule(modulePath string) bool {
	for _, pattern := range c.IgnoredModuleChanges {
		pattern = strings.TrimSpace(pattern)
		if pattern == modulePath {
			return true
		}
		if !isGlobPattern(pattern) {
			continue
		}
		if matched, err := path.Match(pattern, modulePath); err == nil && matched {
			return true
		}
	}
	return false
}

// isGlobPattern 判断模式是否包含通配元字符，从而需要按 glob 进行匹配。
func isGlobPattern(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}
