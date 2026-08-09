// grpc.go 定义 gRPC operation 与项目内调用/注册的原子事实。
//
// Identity 完全由分析项目自身的源码推出：<生成包 import 路径>.<Service>/<GoMethod>，
// 不读取任何依赖包或生成代码的源码——既不解析 client stub 里的 Invoke 调用，也不解析
// server 侧的 ServiceDesc。Service/GoMethod 依据 protoc-gen-go-grpc 的命名契约从本仓库
// 代码里的标识符推导（client 侧：接收者类型名去掉 Client 后缀；server 侧：Register 函数名
// 去掉 Register 前缀、Server 后缀）。两条链路各自只看自己仓库的代码，靠这套共享的命名
// 契约在没有交换信息的情况下推出同一个 Identity 字符串，从而对得上。
package facts

import (
	"strconv"
	"strings"
)

// GrpcIdentity 拼出 canonical identity 字符串。
func GrpcIdentity(goPackage, service, goMethod string) string {
	return goPackage + "." + service + "/" + goMethod
}

// GrpcOperationFact 描述从当前项目源码证明存在的一个 gRPC 方法。
type GrpcOperationFact struct {
	ID        string         `json:"id"`
	Identity  string         `json:"identity"`
	GoPackage string         `json:"go_package"`
	Service   string         `json:"service"`
	GoMethod  string         `json:"go_method"`
	Evidence  []EvidenceFact `json:"evidence,omitempty"`
}

// GrpcCallFact 描述项目内一次已被精确证明的 gRPC client 调用。
type GrpcCallFact struct {
	ID           string         `json:"id"`
	CallerSymbol SymbolID       `json:"caller_symbol"`
	OperationID  string         `json:"operation_id"`
	Span         SourceSpan     `json:"span"`
	Evidence     []EvidenceFact `json:"evidence,omitempty"`
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
}

// GrpcOperationID 返回 canonical identity 的稳定事实 ID。
func GrpcOperationID(identity string) string {
	return "grpc:" + strings.TrimSpace(identity)
}

// GrpcProviderID returns a stable ID for one operation registration site.
func GrpcProviderID(operationID string, registration SourceSpan) string {
	return "grpc_provider:" + strings.TrimSpace(operationID) + ":" + registration.File + ":" +
		strconv.Itoa(registration.StartLine) + ":" + strconv.Itoa(registration.StartCol)
}
