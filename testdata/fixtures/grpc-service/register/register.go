package register

import (
	"example.com/grpcservice/api"
	"example.com/grpcservice/provider"
)

func provide[T any](factory func() T) T { return factory() }

func RegisterProviders(server api.Registrar) {
	api.RegisterEchoServiceServer(server, provide[api.EchoServiceServer](provider.NewEchoServer))
	provider.ExportReplyAPI()
}
