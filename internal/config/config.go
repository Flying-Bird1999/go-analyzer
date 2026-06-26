package config

type Config struct {
	Project    ProjectConfig    `json:"project,omitempty"`
	Route      RouteConfig      `json:"route,omitempty"`
	Annotation AnnotationConfig `json:"annotation,omitempty"`
}

type ProjectConfig struct {
	SkipDirs []string `json:"skipDirs,omitempty"`
}

type RouteConfig struct {
	HTTPMethods         []string      `json:"httpMethods,omitempty"`
	HandlerWrappers     []string      `json:"handlerWrappers,omitempty"`
	RouteGroupWrappers  []WrapperRule `json:"routeGroupWrappers,omitempty"`
	GeneratedRouteCalls []string      `json:"generatedRouteCalls,omitempty"`
}

type WrapperRule struct {
	Exact    string `json:"exact,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Contains string `json:"contains,omitempty"`
}

type AnnotationConfig struct {
	Methods []string `json:"methods,omitempty"`
}
