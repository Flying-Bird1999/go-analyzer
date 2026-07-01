package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

const DefaultImpactConfigPath = ".analyzer/go-impact.config.json"

type Config struct {
	AnalyzeModuleChanges *bool    `json:"analyzeModuleChanges,omitempty"`
	IgnoredModuleChanges []string `json:"ignoredModuleChanges,omitempty"`
}

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
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse impact config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

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

func (c Config) ShouldAnalyzeModuleChanges() bool {
	if c.AnalyzeModuleChanges == nil {
		return true
	}
	return *c.AnalyzeModuleChanges
}

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

func isGlobPattern(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}
