package provider

import (
	"example.com/grpcservice/api"
	"example.com/grpcservice/service"
)

type EchoServer struct{}

func NewEchoServer() api.EchoServiceServer { return &EchoServer{} }

func (s *EchoServer) Ping(_ *api.PingRequest) *api.PingResponse {
	return &api.PingResponse{Value: service.BuildReply()}
}

func (s *EchoServer) Health() string {
	return "ok"
}
