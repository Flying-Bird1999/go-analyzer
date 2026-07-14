// context.go 定义单个路由组在提取过程中的上下文信息：组 ID、变量名、累积前缀和根组变量。
package route

// groupContext 描述当前作用域内一个路由组变量的解析状态。
type groupContext struct {
	id        string // 路由组事实 ID，root 组使用 rootGroupID，普通组使用 routeGroupID
	varName   string // 该组在源码中的变量名
	prefix    string // 静态可解析的完整路径前缀
	prefixRaw string // 无法静态解析的完整前缀表达式
	rootVar   string // 该组所属路由函数的根组变量名，用于跨函数前缀传播
}
