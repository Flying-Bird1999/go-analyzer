// route_flow.go 实现路由分组跨函数流向事实 RouteGroupFlowFact，
// 用于传播 group 参数/返回值在多个 route function 之间的静态流向。

package facts

// RouteGroupFlowFact 描述父级 group 与子级 group 之间的跨函数流向关系，
// 用于在 group 创建/返回表达式引用的 guard/factory 与 descendant group routes 之间建立传播边。
// 该事实仅供内部传播使用（json:"-"），不进入公开 facts JSON。
type RouteGroupFlowFact struct {
	// ID 是该流向事实的唯一标识。
	ID string `json:"id"`
	// ParentGroupID 是上游父级 group 的事实 ID。
	ParentGroupID string `json:"parent_group_id"`
	// ChildGroupID 是下游子级 group 的事实 ID。
	ChildGroupID string `json:"child_group_id"`
}
