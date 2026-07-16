// endpoint.go 实现端点摘要数据结构。
package impact

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

// EndpointImpact 是单个受影响 HTTP 端点的摘要，由 AnalyzeTrees 在展开过程中收集并去重。
// 公开输出契约只暴露这些字段，不包含原始传播路径。
type EndpointImpact struct {
	// ID 是端点的稳定标识，形如 "endpoint:<method>:<path>"。
	ID string `json:"id"`
	// Method 是 HTTP method。
	Method string `json:"method"`
	// Path 是完整 HTTP path（综合路由与注解决定）。
	Path string `json:"path"`
	// AnnotationID 是产生该端点的注解 ID；路由 method/path fallback 端点为空。
	AnnotationID string `json:"annotation_id"`
	// HandlerSymbol 是端点对应的处理函数符号。
	HandlerSymbol facts.SymbolID `json:"handler_symbol"`
	// Routes 是该端点 handler 已静态解析出的路由候选（method + resolved path），
	// 作为辅助证据随端点输出（ARCHITECTURE 第 5 节）。它可能为空或与注解路径漂移，
	// 但不改变注解 endpoint 的正式身份。
	Routes []EndpointRoute `json:"routes"`
}

// EndpointRoute 是端点 handler 关联的一条已解析路由候选。
type EndpointRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
