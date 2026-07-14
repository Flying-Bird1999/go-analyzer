package provider

import "example.com/grpcservice/dubbox"

var (
	firstAPI  = &FirstAPI{}
	secondAPI = &SecondAPI{}
)

type FirstAPI struct{}

type SecondAPI struct{}

// ExportSecondAPI deliberately registers an unrelated provider before the
// ServiceConfig. The config must bind to secondAPI, not firstAPI.
func ExportSecondAPI() {
	dubbox.SetProviderService(firstAPI)
	dubbox.GetRootConfig().Provider.Services["SecondAPI"] = &dubbox.ServiceConfig{
		Interface: "example.second.SecondAPI",
		Version:   "1.0.0",
		Methods:   []*dubbox.MethodConfig{{Name: "second", Retries: "0"}},
	}
	dubbox.SetProviderService(secondAPI)
}

func (s *SecondAPI) MethodMapper() map[string]string {
	return map[string]string{"Second": "second"}
}

func (s *SecondAPI) Second() string { return "second" }
