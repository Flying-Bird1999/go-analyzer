package config

type Config struct {
	HTTPMethods         []string
	HandlerWrappers     []string
	GeneratedRouteCalls []string
	SkipDirs            []string
}
