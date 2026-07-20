// link.go 实现 route-handler 与 handler-annotation 关联事实类型，由 internal/link 产出。
// link 解决的是路由域身份对齐：把 route 文件 handler 表达式、controller 声明、handler 注释归并到同一 symbol。

package facts

// LinkKind 枚举关联事实的种类。
type LinkKind string

const (
	// LinkKindRouteToHandler 表示 route 注册到 handler symbol 的关联。
	LinkKindRouteToHandler LinkKind = "route_to_handler"
	// LinkKindHandlerToAnnotation 表示 handler symbol 到其 HTTP 注解的关联。
	LinkKindHandlerToAnnotation LinkKind = "handler_to_annotation"
)

// LinkFact 描述一条 route/handler/annotation 之间的关联边，由 linker 写入。
// 这些关联让 RouteGraph 能从 handler 反查 route、从 route 找到 annotation 确认 endpoint。
type LinkFact struct {
	// ID 是该关联事实的唯一标识。
	ID string `json:"id"`
	// Kind 是关联种类（route_to_handler / handler_to_annotation）。
	Kind LinkKind `json:"kind"`
	// FromID 是关联起点的 fact ID（route 注册或 handler symbol）。
	FromID string `json:"from_id"`
	// ToID 是关联终点的 fact ID（handler symbol 或 annotation）。
	ToID string `json:"to_id"`
}
