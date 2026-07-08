// route.go 实现路由图视图：把 facts.Store 中的路由注册、路由分组、中间件绑定、
// 注解以及它们与 handler/依赖的关系，预先索引成多张查询表，供影响传播阶段
// 从 handler/group/middleware 快速定位受影响的 routes/annotations。
package graph

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// RouteGraph 是路由图，预先建立多维索引以支持下列查询：
//   - 处理函数 -> 以其为 handler 的路由（RoutesByHandler）。
//   - 依赖 symbol -> 在其 reference span 内注册的路由（RoutesByDependency）。
//   - 中间件绑定 -> 同 group 且语句顺序更靠后的路由（RoutesAffectedByMiddleware）。
//   - 注解 -> 按 handler 聚合（AnnotationsByHandler）。
type RouteGraph struct {
	// RoutesByID 按路由 ID 索引的路由注册事实。
	RoutesByID map[string]facts.RouteRegistrationFact
	// GroupsByID 按组 ID 索引的路由分组事实。
	GroupsByID map[string]facts.RouteGroupFact
	// MiddlewareByID 按绑定 ID 索引的中间件绑定事实。
	MiddlewareByID map[string]facts.MiddlewareBindingFact
	// MiddlewareBySymbol 按中间件 symbol 聚合绑定事实。
	MiddlewareBySymbol map[facts.SymbolID][]facts.MiddlewareBindingFact
	// RoutesByGroupID 按有效 group ID 聚合的路由列表。
	RoutesByGroupID map[string][]facts.RouteRegistrationFact
	// ChildGroupsByID 按父组 ID 聚合的直接子组 ID（含 ParentGroupID 字段与 RouteGroupFlow）。
	ChildGroupsByID map[string][]string
	// RoutesByHandler 按处理函数 symbol 聚合的路由列表。
	RoutesByHandler map[facts.SymbolID][]facts.RouteRegistrationFact
	// RoutesByDependency 按依赖 symbol 聚合的路由列表（含 route-scoped 与 assigned-group 两类）。
	RoutesByDependency map[facts.SymbolID][]facts.RouteRegistrationFact
	// AnnotationsByHandler 按处理函数 symbol 聚合的注解列表。
	AnnotationsByHandler map[facts.SymbolID][]facts.AnnotationFact
}

// NewRouteGraph 扫描 store，构建路由图的全部索引。流程：
//  1. 索引路由分组，建立 parent->child 关系（来源：ParentGroupID 字段、RouteGroupFlow）。
//  2. 索引路由，按 group、handler 聚合。
//  3. 扫描 references：若 reference span 落在某条 route 的 span 内，则建立
//     route-scoped 依赖；若落在某 group 创建表达式 span 内，则把该 group 及其
//     descendant group 的全部路由纳入 assigned-group 依赖。
//  4. 索引中间件绑定、注解，并最终统一排序保证输出稳定。
func NewRouteGraph(store *facts.Store) *RouteGraph {
	// routesByFunc / groupsByFunc 为内部辅助索引，按路由函数 symbol 聚合 route/group，
	// 用于后续按 reference 的 FromSymbol 快速定位候选 route/group。
	routesByFunc := map[facts.SymbolID][]facts.RouteRegistrationFact{}
	groupsByFunc := map[facts.SymbolID][]facts.RouteGroupFact{}
	g := &RouteGraph{
		RoutesByID:           map[string]facts.RouteRegistrationFact{},
		GroupsByID:           map[string]facts.RouteGroupFact{},
		MiddlewareByID:       map[string]facts.MiddlewareBindingFact{},
		MiddlewareBySymbol:   map[facts.SymbolID][]facts.MiddlewareBindingFact{},
		RoutesByGroupID:      map[string][]facts.RouteRegistrationFact{},
		ChildGroupsByID:      map[string][]string{},
		RoutesByHandler:      map[facts.SymbolID][]facts.RouteRegistrationFact{},
		RoutesByDependency:   map[facts.SymbolID][]facts.RouteRegistrationFact{},
		AnnotationsByHandler: map[facts.SymbolID][]facts.AnnotationFact{},
	}
	// 第 1 步：建立 parent->child 索引，来源为 group 自带的 ParentGroupID 字段。
	for _, group := range store.RouteGroups {
		g.GroupsByID[group.ID] = group
		groupsByFunc[group.RouteFunc] = append(groupsByFunc[group.RouteFunc], group)
		if group.ParentGroupID != "" {
			g.ChildGroupsByID[group.ParentGroupID] = append(g.ChildGroupsByID[group.ParentGroupID], group.ID)
		}
	}
	// 第 1 步补充：跨函数的 group flow（父组在另一函数中被注册为子组）也并入同一索引。
	for _, flow := range store.RouteGroupFlows {
		if flow.ParentGroupID == "" || flow.ChildGroupID == "" {
			continue
		}
		g.ChildGroupsByID[flow.ParentGroupID] = append(g.ChildGroupsByID[flow.ParentGroupID], flow.ChildGroupID)
	}
	// 第 2 步：索引路由，按有效 group ID 与 handler symbol 聚合。
	for _, route := range store.Routes {
		g.RoutesByID[route.ID] = route
		routesByFunc[route.RouteFunc] = append(routesByFunc[route.RouteFunc], route)
		groupID := effectiveGroupID(route.GroupID, route.RouteFunc, route.GroupVar)
		g.RoutesByGroupID[groupID] = append(g.RoutesByGroupID[groupID], route)
		if route.HandlerSymbol != "" {
			g.RoutesByHandler[route.HandlerSymbol] = append(g.RoutesByHandler[route.HandlerSymbol], route)
		}
	}
	// 第 3 步：把每条 reference 关联到其 span 所在的 route（route-scoped）或 group
	// （assigned-group），建立依赖 symbol -> 路由的映射。
	for _, ref := range store.References {
		if ref.ToSymbol == "" {
			continue
		}
		// route-scoped：reference span 落在 route span 内，且引用的不是该 route 的
		// handler 本身（handler 走 RoutesByHandler 而非依赖）。
		for _, route := range routesByFunc[ref.FromSymbol] {
			if ref.ToSymbol == route.HandlerSymbol || !spanContains(route.Span, ref.Span) {
				continue
			}
			g.RoutesByDependency[ref.ToSymbol] = appendRouteOnce(g.RoutesByDependency[ref.ToSymbol], route)
		}
		// assigned-group：reference span 落在 group 创建表达式 span 内，则该依赖
		// （通常是 guard/factory）影响整个 group 的全部路由，包括 descendant group。
		for _, group := range groupsByFunc[ref.FromSymbol] {
			if !spanContains(group.Span, ref.Span) {
				continue
			}
			for _, route := range g.RoutesForGroup(group.ID) {
				g.RoutesByDependency[ref.ToSymbol] = appendRouteOnce(g.RoutesByDependency[ref.ToSymbol], route)
			}
		}
	}
	// 第 4 步：索引中间件绑定与注解。
	for _, binding := range store.Middleware {
		g.MiddlewareByID[binding.ID] = binding
		for _, symbol := range binding.MiddlewareSymbols {
			if symbol == "" {
				continue
			}
			g.MiddlewareBySymbol[symbol] = append(g.MiddlewareBySymbol[symbol], binding)
		}
	}
	for _, annotation := range store.Annotations {
		g.AnnotationsByHandler[annotation.HandlerSymbol] = append(g.AnnotationsByHandler[annotation.HandlerSymbol], annotation)
	}
	g.sort()
	return g
}

// sort 对所有切片型索引统一排序，保证查询输出稳定可复现。
func (g *RouteGraph) sort() {
	for group := range g.RoutesByGroupID {
		sortRoutes(g.RoutesByGroupID[group])
	}
	for group := range g.ChildGroupsByID {
		sort.Strings(g.ChildGroupsByID[group])
	}
	for handler := range g.RoutesByHandler {
		sortRoutes(g.RoutesByHandler[handler])
	}
	for dependency := range g.RoutesByDependency {
		sortRoutes(g.RoutesByDependency[dependency])
	}
	for symbol := range g.MiddlewareBySymbol {
		sortMiddlewareBindings(g.MiddlewareBySymbol[symbol])
	}
	for handler := range g.AnnotationsByHandler {
		sort.Slice(g.AnnotationsByHandler[handler], func(i, j int) bool {
			return g.AnnotationsByHandler[handler][i].ID < g.AnnotationsByHandler[handler][j].ID
		})
	}
}

// RoutesForHandler 返回以 handler 为处理函数的全部路由。返回副本，调用方可安全修改。
func (g *RouteGraph) RoutesForHandler(handler facts.SymbolID) []facts.RouteRegistrationFact {
	return append([]facts.RouteRegistrationFact(nil), g.RoutesByHandler[handler]...)
}

// RoutesForDependency 返回依赖 symbol 影响的全部路由（route-scoped 与 assigned-group 合并）。
// 返回副本，调用方可安全修改。
func (g *RouteGraph) RoutesForDependency(dependency facts.SymbolID) []facts.RouteRegistrationFact {
	return append([]facts.RouteRegistrationFact(nil), g.RoutesByDependency[dependency]...)
}

// MiddlewareBindingsForSymbol 返回引用指定中间件 symbol 的全部中间件绑定。返回副本，调用方可安全修改。
func (g *RouteGraph) MiddlewareBindingsForSymbol(symbol facts.SymbolID) []facts.MiddlewareBindingFact {
	return append([]facts.MiddlewareBindingFact(nil), g.MiddlewareBySymbol[symbol]...)
}

// AnnotationsForHandler 返回绑定到 handler 的全部注解。返回副本，调用方可安全修改。
func (g *RouteGraph) AnnotationsForHandler(handler facts.SymbolID) []facts.AnnotationFact {
	return append([]facts.AnnotationFact(nil), g.AnnotationsByHandler[handler]...)
}

// RoutesForGroup 递归收集 groupID 及其全部后代组（descendant group）下注册的路由。
// 递归时同时通过 effectiveGroupID 把“同一 route 函数内由 groupVar 标识的匿名组”
// 也并入，避免遗漏同函数内的兄弟组路由。结果按语句顺序排序后返回副本。
func (g *RouteGraph) RoutesForGroup(groupID string) []facts.RouteRegistrationFact {
	var routes []facts.RouteRegistrationFact
	// seenGroups / seenRoutes 防止环状 group 关系或重复注册导致死循环与重复路由。
	seenGroups := map[string]bool{}
	seenRoutes := map[string]bool{}
	var collect func(string)
	collect = func(current string) {
		if seenGroups[current] {
			return
		}
		seenGroups[current] = true
		for _, route := range g.RoutesByGroupID[current] {
			if !seenRoutes[route.ID] {
				seenRoutes[route.ID] = true
				routes = append(routes, route)
			}
		}
		// 递归处理子组：descendant group 下的路由同样受祖先组中间件/依赖影响。
		for _, child := range g.ChildGroupsByID[current] {
			collect(child)
		}
	}
	collect(groupID)
	// 若 groupID 对应一个真实的 RouteGroupFact，则把同函数内由 groupVar 标识的
	// 匿名组路由也一并纳入（InitRouter 中常见 g := Group(...) 模式）。
	if group, ok := g.GroupsByID[groupID]; ok {
		collect(effectiveGroupID("", group.RouteFunc, group.GroupVar))
	}
	sortRoutes(routes)
	return append([]facts.RouteRegistrationFact(nil), routes...)
}

// RoutesAffectedByMiddleware 返回受某条中间件绑定影响的路由。命中条件：
// 路由与绑定属于同一有效 group，且路由的语句顺序（StatementIndex）严格大于
// 绑定的语句顺序——中间件只作用于在其之后注册的路由。跨函数路由（RouteFunc 不同）
// 也属于同一 group 时同样受影响。
func (g *RouteGraph) RoutesAffectedByMiddleware(bindingID string) []facts.RouteRegistrationFact {
	binding, ok := g.MiddlewareByID[bindingID]
	if !ok {
		return nil
	}
	var out []facts.RouteRegistrationFact
	groupID := effectiveGroupID(binding.GroupID, binding.RouteFunc, binding.GroupVar)
	for _, route := range g.RoutesForGroup(groupID) {
		// 语句顺序判定：仅当 route 的 StatementIndex 严格大于 binding 时才算“之后”；
		// 跨函数场景下（RouteFunc 不同）只要 group 一致即视为受影响。
		if binding.RouteFunc != route.RouteFunc || binding.StatementIndex < route.StatementIndex {
			out = append(out, route)
		}
	}
	sortRoutes(out)
	return out
}

// effectiveGroupID 计算路由/中间件所属的有效 group ID：显式 GroupID 优先；
// 否则用 routeFunc + "::" + groupVar 合成匿名组的逻辑键。
func effectiveGroupID(groupID string, routeFunc facts.SymbolID, groupVar string) string {
	if groupID != "" {
		return groupID
	}
	return string(routeFunc) + "::" + groupVar
}

// sortRoutes 按语句顺序（StatementIndex）排序，相同顺序时按路由 ID 兜底，保证稳定。
func sortRoutes(routes []facts.RouteRegistrationFact) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].StatementIndex != routes[j].StatementIndex {
			return routes[i].StatementIndex < routes[j].StatementIndex
		}
		return routes[i].ID < routes[j].ID
	})
}

func sortMiddlewareBindings(bindings []facts.MiddlewareBindingFact) {
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Span.File != bindings[j].Span.File {
			return bindings[i].Span.File < bindings[j].Span.File
		}
		if bindings[i].StatementIndex != bindings[j].StatementIndex {
			return bindings[i].StatementIndex < bindings[j].StatementIndex
		}
		return bindings[i].ID < bindings[j].ID
	})
}

// appendRouteOnce 把 candidate 追加到 routes（仅当其 ID 尚未存在），避免同一依赖
// 被多条 reference 重复计入同一路由。
func appendRouteOnce(routes []facts.RouteRegistrationFact, candidate facts.RouteRegistrationFact) []facts.RouteRegistrationFact {
	for _, route := range routes {
		if route.ID == candidate.ID {
			return routes
		}
	}
	return append(routes, candidate)
}

// spanContains 判断 inner 位置区间是否完全落在 outer 内。要求同文件且起止点满足
// 包含关系，用于把 reference 关联到其所在的 route/group span。
func spanContains(outer, inner facts.SourceSpan) bool {
	if outer.File == "" || outer.File != inner.File {
		return false
	}
	return positionLessOrEqual(outer.StartLine, outer.StartCol, inner.StartLine, inner.StartCol) &&
		positionLessOrEqual(inner.EndLine, inner.EndCol, outer.EndLine, outer.EndCol)
}

// positionLessOrEqual 比较两个行列位置：先比行，行相同则比列。
func positionLessOrEqual(leftLine, leftCol, rightLine, rightCol int) bool {
	if leftLine != rightLine {
		return leftLine < rightLine
	}
	return leftCol <= rightCol
}
