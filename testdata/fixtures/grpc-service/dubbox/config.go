package dubbox

type MethodConfig struct {
	Name    string
	Retries string
}

type ServiceConfig struct {
	Interface string
	Version   string
	Methods   []*MethodConfig
}

type ProviderConfig struct {
	Services map[string]*ServiceConfig
}

type RootConfig struct {
	Provider ProviderConfig
}

var root = RootConfig{Provider: ProviderConfig{Services: map[string]*ServiceConfig{}}}

func GetRootConfig() *RootConfig { return &root }

func SetProviderService(any) {}
