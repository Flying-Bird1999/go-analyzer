// schema.go 定义 facts JSON 的顶层 Document 结构（Go 侧的序列化模型）。
// Document 直接由 json.go 的 RenderJSON 序列化，与 contract.go 中 facts schema 的字段一一对应。
package output

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

// Document 是 facts 命令的顶层 JSON 文档模型。
// 字段顺序与 JSON tag 决定了 facts 输出的稳定结构，新增字段需同步 contract.go 与文档。
type Document struct {
	// Project 是项目根、module path 与 build context。
	Project facts.ProjectFact `json:"project"`
	// Symbols 是所有声明符号（function/method/type/var/const）。
	Symbols []facts.SymbolFact `json:"symbols"`
	// Annotations 是 controller handler 上的 HTTP 注释事实。
	Annotations []facts.AnnotationFact `json:"annotations"`
	// RouteGroups 是 route group / prefix / parent 关系。
	RouteGroups []facts.RouteGroupFact `json:"route_groups"`
	// Routes 是 route registration（method/path/handler/wrapper）。
	Routes []facts.RouteRegistrationFact `json:"routes"`
	// Middleware 是 group middleware 绑定与顺序。
	Middleware []facts.MiddlewareBindingFact `json:"middleware"`
	// References 是 call/type/value 反向引用边。
	References []facts.ReferenceFact `json:"references"`
	// Modules 是 go.mod dependency/replace。
	Modules []facts.ModuleDependencyFact `json:"modules"`
	// IMEvents 是出站 IM event、sender 与精确依赖事实。
	IMEvents []facts.IMEventFact `json:"im_events"`
	// GrpcOperations 是从 generated dependency client 提取的 operation facts。
	GrpcOperations []facts.GrpcOperationFact `json:"grpc_operations"`
	// GrpcCalls 是项目内已证明的 generated client 调用事实。
	GrpcCalls []facts.GrpcCallFact `json:"grpc_calls"`
	// GrpcProviders are project-local gRPC server registration bindings.
	GrpcProviders []facts.GrpcProviderFact `json:"grpc_providers"`
	// Links 是 route-handler / handler-annotation / middleware symbol 关联。
	Links []facts.LinkFact `json:"links"`
	// Diagnostics 是可恢复的不确定性诊断。
	Diagnostics []facts.DiagnosticFact `json:"diagnostics"`
}
