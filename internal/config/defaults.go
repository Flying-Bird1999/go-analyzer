package config

import (
	"encoding/json"
	"os"
	"strings"
)

func Default() Config {
	return Config{
		Project: ProjectConfig{
			SkipDirs: []string{".git", ".cache", "vendor", "node_modules", "testdata"},
		},
		Route: RouteConfig{
			HTTPMethods:         []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
			HandlerWrappers:     []string{"ControllerWithReqResp", "AppControllerWithReqResp", "ControllerWithResp", "Controller", "MiddlewareController"},
			RouteGroupWrappers:  []WrapperRule{{Prefix: "Add"}, {Contains: "Guard"}, {Contains: "Validator"}},
			GeneratedRouteCalls: []string{"RegisterRouters", "RegisterRouter"},
		},
		Annotation: AnnotationConfig{
			Methods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
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
	return merge(cfg, override), nil
}

func (c Config) IsHTTPMethod(name string) bool {
	return containsFold(c.Route.HTTPMethods, name)
}

func (c Config) IsAnnotationMethod(name string) bool {
	return containsFold(c.Annotation.Methods, name)
}

func (c Config) IsHandlerWrapper(name string) bool {
	return containsFold(c.Route.HandlerWrappers, name)
}

func (c Config) IsRouteGroupWrapper(name string) bool {
	for _, rule := range c.Route.RouteGroupWrappers {
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
	base.Project.SkipDirs = appendUniqueFold(base.Project.SkipDirs, override.Project.SkipDirs...)
	base.Route.HTTPMethods = appendUniqueUpper(base.Route.HTTPMethods, override.Route.HTTPMethods...)
	base.Route.HandlerWrappers = appendUniqueFold(base.Route.HandlerWrappers, override.Route.HandlerWrappers...)
	base.Route.RouteGroupWrappers = append(base.Route.RouteGroupWrappers, override.Route.RouteGroupWrappers...)
	base.Route.GeneratedRouteCalls = appendUniqueFold(base.Route.GeneratedRouteCalls, override.Route.GeneratedRouteCalls...)
	base.Annotation.Methods = appendUniqueUpper(base.Annotation.Methods, override.Annotation.Methods...)
	return base
}

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
