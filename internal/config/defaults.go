package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func Default() Config {
	return Config{
		Analysis: AnalysisConfig{
			MaxDepth:        0,
			StopPropagation: []string{},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var override Config
	if err := json.Unmarshal(data, &override); err != nil {
		return Config{}, err
	}
	if override.Analysis.MaxDepth < 0 {
		return Config{}, fmt.Errorf("analysis.maxDepth must be greater than or equal to zero")
	}
	return merge(cfg, override), nil
}

func DefaultSkipDirs() []string {
	return []string{".git", ".cache", "vendor", "node_modules", "testdata", ".analyzer"}
}

func (c Config) IsHTTPMethod(name string) bool {
	return containsFold(defaultHTTPMethods, name)
}

func (c Config) IsAnnotationMethod(name string) bool {
	return containsFold(defaultAnnotationMethods, name)
}

func (c Config) IsHandlerWrapper(name string) bool {
	return containsFold(defaultHandlerWrappers, name)
}

func (c Config) IsRouteGroupWrapper(name string) bool {
	for _, rule := range defaultRouteGroupWrappers {
		if rule.Exact != "" && strings.EqualFold(rule.Exact, name) {
			return true
		}
		if rule.Prefix != "" && strings.HasPrefix(strings.ToLower(name), strings.ToLower(rule.Prefix)) {
			return true
		}
		if rule.Contains != "" && strings.Contains(strings.ToLower(name), strings.ToLower(rule.Contains)) {
			return true
		}
	}
	return false
}

func merge(base Config, override Config) Config {
	if override.Analysis.MaxDepth != 0 {
		base.Analysis.MaxDepth = override.Analysis.MaxDepth
	}
	base.Analysis.StopPropagation = appendUniqueFold(base.Analysis.StopPropagation, override.Analysis.StopPropagation...)
	return base
}

var defaultHTTPMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
var defaultAnnotationMethods = defaultHTTPMethods
var defaultHandlerWrappers = []string{"ControllerWithReqResp", "AppControllerWithReqResp", "ControllerWithResp", "Controller", "MiddlewareController"}
var defaultRouteGroupWrappers = []WrapperRule{{Prefix: "Add"}, {Contains: "Guard"}, {Contains: "Validator"}}

func containsFold(items []string, want string) bool {
	for _, item := range items {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

func appendUniqueFold(base []string, extras ...string) []string {
	out := append([]string(nil), base...)
	for _, extra := range extras {
		extra = strings.TrimSpace(extra)
		if extra == "" || containsFold(out, extra) {
			continue
		}
		out = append(out, extra)
	}
	return out
}

func appendUniqueUpper(base []string, extras ...string) []string {
	out := append([]string(nil), base...)
	for _, extra := range extras {
		extra = strings.ToUpper(strings.TrimSpace(extra))
		if extra == "" || containsFold(out, extra) {
			continue
		}
		out = append(out, extra)
	}
	return out
}
