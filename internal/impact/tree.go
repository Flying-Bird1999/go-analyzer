// tree.go 实现影响树的外层数据结构：递归节点、根包装与最终结果。
package impact

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

// Node 是影响树中的递归节点，对应 ARCHITECTURE 第 10 节描述的传播节点。
// 节点可以是符号、路由、注解、中间件、端点、IM 事件等多种领域类型。
type Node struct {
	// ID 是节点稳定标识（符号 ID、路由 ID、端点字符串等）。
	ID string `json:"id"`
	// Kind 是节点类型，如 func/method/route/annotation/middleware/endpoint/im_event。
	Kind string `json:"kind"`
	// Name 是用于展示的人类可读名称。
	Name string `json:"name,omitempty"`
	// File 是节点所在的项目相对路径。
	File string `json:"file,omitempty"`
	// Package 是符号所属的 package path，仅符号节点填写。
	Package string `json:"package,omitempty"`
	// Relation 描述本节点相对父节点的关系，如 call/type_ref/registered_handler 等。
	Relation string `json:"relation,omitempty"`
	// Raw 保留原始证据文本（如 handler 表达式、注解原文、事件表达式）。
	Raw string `json:"raw,omitempty"`
	// Span 是节点对应的源码 span，供 review 定位。
	Span facts.SourceSpan `json:"span,omitempty"`
	// Level 是节点在树中的深度，根为 0。
	Level int `json:"level"`
	// Cycle 标记当前 DFS 路径上重复出现的节点，用于环路检测。
	Cycle bool `json:"cycle,omitempty"`
	// Method 用于端点/路由/注解节点，表示 HTTP method。
	Method string `json:"method,omitempty"`
	// Path 用于端点/路由/注解节点，表示 HTTP path。
	Path string `json:"path,omitempty"`
	// FullMethod is set on canonical gRPC operation terminal nodes.
	FullMethod string `json:"full_method,omitempty"`
	// Children 是递归子节点。
	Children []Node `json:"children"`
}

// RootImpact 是单个 ChangeFact 对应的传播结果：根节点加上该根下命中的端点与 IM 事件摘要。
type RootImpact struct {
	// Change 是触发本棵树的变更事实。
	Change facts.ChangeFact `json:"change"`
	// Root 是本棵树的根节点（递归结构）。
	Root Node `json:"root"`
	// Endpoints 是该根下命中的端点摘要（去重并按 method/path 排序）。
	Endpoints []EndpointImpact `json:"endpoints"`
	// IMEvents 是该根下命中的已解析 IM 事件摘要（去重并按事件名排序）。
	IMEvents []IMEventImpact `json:"im_events"`
}

// TreeResult 是 AnalyzeTrees 的最终结果，包含全部变更根对应的 RootImpact。
type TreeResult struct {
	// Roots 是按变更 ID 排序的传播根列表。
	Roots []RootImpact `json:"roots"`
}

// IMEventImpact 是单个已解析 IM 事件的摘要，公开输出只暴露事件字符串。
type IMEventImpact struct {
	// Event 是 IM 事件名（topic/类型）。
	Event string `json:"event"`
}
