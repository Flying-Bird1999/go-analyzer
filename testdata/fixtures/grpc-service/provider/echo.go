package provider

import (
	"context"

	"example.com/grpcservice/api"
	"example.com/grpcservice/service"
)

type EchoServer struct{}

func NewEchoServer() api.EchoServiceServer { return &EchoServer{} }

func (s *EchoServer) Ping(ctx context.Context, req *api.PingRequest) (*api.PingResponse, error) {
	return &api.PingResponse{Value: service.BuildReply()}, nil
}

func (s *EchoServer) Health(ctx context.Context, req *api.HealthRequest) (*api.HealthResponse, error) {
	return &api.HealthResponse{Status: "ok"}, nil
}
