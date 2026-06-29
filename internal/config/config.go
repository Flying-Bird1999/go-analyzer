package config

type Config struct {
	Analysis AnalysisConfig `json:"analysis,omitempty"`
}

type WrapperRule struct {
	Exact    string `json:"exact,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Contains string `json:"contains,omitempty"`
}

type AnalysisConfig struct {
	MaxDepth        int      `json:"maxDepth,omitempty"`
	StopPropagation []string `json:"stopPropagation,omitempty"`
}
