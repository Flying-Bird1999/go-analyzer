// grpc.go 定义 generated gRPC operation 与项目内调用的原子事实。
package facts

import "strings"

// GrpcStreamingMode 描述 gRPC method 的流模式。
type GrpcStreamingMode string

const (
	GrpcStreamingUnary         GrpcStreamingMode = "unary"
	GrpcStreamingClient        GrpcStreamingMode = "client_streaming"
	GrpcStreamingServer        GrpcStreamingMode = "server_streaming"
	GrpcStreamingBidirectional GrpcStreamingMode = "bidirectional_streaming"
)

// GrpcClientBinding 是 generated Go client method 到 canonical operation 的精确绑定键。
type GrpcClientBinding struct {
	GoPackage  string `json:"go_package"`
	ClientType string `json:"client_type"`
	GoMethod   string `json:"go_method"`
}

// GrpcOperationFact 描述从当前依赖图中 generated client source 证明的 gRPC operation。
type GrpcOperationFact struct {
	ID             string              `json:"id"`
	FullMethod     string              `json:"full_method"`
	ProtoPackage   string              `json:"proto_package"`
	Service        string              `json:"service"`
	Method         string              `json:"method"`
	StreamingMode  GrpcStreamingMode   `json:"streaming_mode"`
	ClientBindings []GrpcClientBinding `json:"client_bindings"`
	Evidence       []EvidenceFact      `json:"evidence"`
}

// GrpcCallFact 描述项目内一次已被精确证明的 generated client 调用。
type GrpcCallFact struct {
	ID            string            `json:"id"`
	CallerSymbol  SymbolID          `json:"caller_symbol"`
	OperationID   string            `json:"operation_id"`
	ClientBinding GrpcClientBinding `json:"client_binding"`
	Span          SourceSpan        `json:"span"`
	Evidence      []EvidenceFact    `json:"evidence"`
}

// GrpcOperationID 返回 canonical full method 的稳定事实 ID。
func GrpcOperationID(fullMethod string) string {
	return "grpc:" + strings.TrimSpace(fullMethod)
}
