// grpc.go 定义 generated gRPC operation 与项目内调用的原子事实。
package facts

import (
	"strconv"
	"strings"
)

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

// GrpcProviderFact describes one canonical operation exposed by a concrete
// server registration in the analyzed project. HandlerSymbol can be empty
// when the registered implementation inherits the generated unimplemented
// method or its concrete type cannot be proven statically.
type GrpcProviderFact struct {
	ID                      string         `json:"id"`
	OperationID             string         `json:"operation_id"`
	GeneratedGoPackage      string         `json:"generated_go_package"`
	RegisterFunction        string         `json:"register_function"`
	ServerInterface         string         `json:"server_interface"`
	ImplementationGoPackage string         `json:"implementation_go_package,omitempty"`
	ImplementationType      string         `json:"implementation_type,omitempty"`
	ImplementationSymbol    SymbolID       `json:"implementation_symbol,omitempty"`
	HandlerSymbol           SymbolID       `json:"handler_symbol,omitempty"`
	RegistrationSymbol      SymbolID       `json:"registration_symbol"`
	Span                    SourceSpan     `json:"span"`
	Evidence                []EvidenceFact `json:"evidence,omitempty"`
	Confidence              Confidence     `json:"confidence"`
}

// GrpcOperationID 返回 canonical full method 的稳定事实 ID。
func GrpcOperationID(fullMethod string) string {
	return "grpc:" + strings.TrimSpace(fullMethod)
}

// GrpcProviderID returns a stable ID for one operation registration site.
func GrpcProviderID(operationID string, registration SourceSpan) string {
	return "grpc_provider:" + strings.TrimSpace(operationID) + ":" + registration.File + ":" +
		strconv.Itoa(registration.StartLine) + ":" + strconv.Itoa(registration.StartCol)
}
