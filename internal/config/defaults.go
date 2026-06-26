package config

func Default() Config {
	return Config{
		HTTPMethods:         []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
		HandlerWrappers:     []string{"ControllerWithReqResp", "ControllerWithResp", "MiddlewareController"},
		GeneratedRouteCalls: []string{"RegisterRouters", "RegisterRouter"},
		SkipDirs:            []string{".git", ".cache", "vendor", "node_modules", "testdata"},
	}
}
