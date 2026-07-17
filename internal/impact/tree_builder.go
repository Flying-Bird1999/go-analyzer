// tree_builder.go 实现影响树的构造：从 ChangeFact 出发，沿反向引用图、路由图、IM 图传播，
// 输出受影响的端点与 IM 事件。
//
// 该文件还承载本包的包文档。

// Package impact 从 ChangeFact 出发构造影响树，沿反向引用图、路由图、IM 图传播，
// 输出受影响的 HTTP 端点与 IM 事件，并恢复 diff 中被删除的路由注册。
//
// 主入口为 AnalyzeTrees 与 RecoverDeletedRoutes：前者按变更事实逐棵展开影响树，
// 在符号上递归反向查找引用者，并经过路由图落到端点注解或路由 method/path 降级端点，
// 同时通过 IM 图把命中传播路径的 IM 事实投影为 im_event 或 im_event_unresolved 终端；
// 后者把 diff 删除块中的路由注册语句解析出来，补充合成路由事实与 route_deleted 根。
// 多个变更根互不覆盖，每个根独立生成一棵传播树。
package impact

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

// treeBuilder 负责为单个 ChangeFact 构造一棵影响树，并在展开过程中收集命中的
// 端点与 IM 事件。它内嵌 treeContext，共享跨根的图与索引查询视图。
type treeBuilder struct {
	*treeContext
	// endpoints 收集本棵树展开过程中命中的端点，按 "method\x00path" 去重。
	endpoints map[string]EndpointImpact
	// imEvents 收集本棵树展开过程中命中的已解析 IM 事件，按事件名去重。
	imEvents map[string]IMEventImpact
	// change 是当前正在展开的变更事实。
	change facts.ChangeFact
}

// treeContext 是跨多个 ChangeFact 复用的查询上下文，封装三类图与符号/注解索引，
// 避免在每次根展开时重复构造查询视图。
type treeContext struct {
	// store 是共享的事实总线，提供路由组、中间件绑定等查询来源。
	store *facts.Store
	// reverse 是反向引用图，按 ToSymbol -> []FromSymbol 提供引用者查询。
	reverse *graph.ReverseGraph
	// routes 是路由图，提供 handler/中间件/路由组到路由与注解的查询。
	routes *graph.RouteGraph
	// im 是 IM 图，按当前传播 path 上的依赖精确匹配 IM 事件。
	im *graph.IMGraph
	// symbols 是按 SymbolID 索引的符号事实，用于补全节点元信息。
	symbols map[facts.SymbolID]facts.SymbolFact
	// annotations 是按注解 ID 索引的注解事实，用于注解根展开。
	annotations map[string]facts.AnnotationFact
	// jobs 是按 job ID 索引的 job 注册事实，用于 job 注册变更根直接命中。
	jobs map[string]facts.JobRegistrationFact
}

// AnalyzeTrees 是影响分析的主入口：为 Store 中每个 ChangeFact 独立展开一棵影响树，
// 同时收集命中的端点与已解析 IM 事件，并按变更 ID 稳定排序输出。
//
// 多个变更根互不覆盖：每个根各自生成一棵传播树及对应的端点/IM 事件摘要，
// 不在跨根之间做合并或裁剪。
func AnalyzeTrees(store *facts.Store) TreeResult {
	result := TreeResult{
		Roots: []RootImpact{},
	}
	// 构造一次跨根共享的查询上下文（三类图 + 符号/注解索引）。
	context := newTreeContext(store)
	// 复制变更切片并按 ID 排序，保证输出顺序与变更在 Store 中的位置无关。
	changes := append([]facts.ChangeFact(nil), store.Changes...)
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].ID < changes[j].ID
	})
	for _, change := range changes {
		builder := newTreeBuilder(context, change)
		root := builder.buildRoot()
		// 把本棵树收集的端点 map 转为切片，并按 method/path 稳定排序。
		endpoints := make([]EndpointImpact, 0, len(builder.endpoints))
		for _, endpoint := range builder.endpoints {
			endpoints = append(endpoints, endpoint)
		}
		sort.Slice(endpoints, func(i, j int) bool {
			if endpoints[i].Method != endpoints[j].Method {
				return endpoints[i].Method < endpoints[j].Method
			}
			return endpoints[i].Path < endpoints[j].Path
		})
		// 把本棵树收集的 IM 事件 map 转为切片，并按事件名稳定排序。
		imEvents := make([]IMEventImpact, 0, len(builder.imEvents))
		for _, event := range builder.imEvents {
			imEvents = append(imEvents, event)
		}
		sort.Slice(imEvents, func(i, j int) bool {
			return imEvents[i].Event < imEvents[j].Event
		})
		result.Roots = append(result.Roots, RootImpact{
			Change:    change,
			Root:      root,
			Endpoints: endpoints,
			IMEvents:  imEvents,
		})
	}
	return result
}

// newTreeContext 基于 facts.Store 构造跨根复用的查询上下文：建立反向引用图、路由图、
// IM 图，并对符号与注解建立按 ID 的索引以便快速补全节点元信息。
func newTreeContext(store *facts.Store) *treeContext {
	context := &treeContext{
		store:       store,
		reverse:     graph.NewReverseGraph(store),
		routes:      graph.NewRouteGraph(store),
		im:          graph.NewIMGraph(store),
		symbols:     map[facts.SymbolID]facts.SymbolFact{},
		annotations: map[string]facts.AnnotationFact{},
		jobs:        map[string]facts.JobRegistrationFact{},
	}
	for _, symbol := range store.Symbols {
		context.symbols[symbol.ID] = symbol
	}
	for _, annotation := range store.Annotations {
		context.annotations[annotation.ID] = annotation
	}
	for _, job := range store.JobRegistrations {
		context.jobs[job.ID] = job
	}
	return context
}

// newTreeBuilder 为单个变更事实创建一棵独立影响树的构造器，初始化端点与 IM 事件的去重容器。
func newTreeBuilder(context *treeContext, change facts.ChangeFact) *treeBuilder {
	return &treeBuilder{
		treeContext: context,
		endpoints:   map[string]EndpointImpact{},
		imEvents:    map[string]IMEventImpact{},
		change:      change,
	}
}

// buildRoot 按领域优先级把 ChangeFact 映射成对应类型的根节点并展开。
//
// 优先级顺序：路由 > 路由组 > 中间件 > 注解 > Job 注册 > 符号 > 文件降级。
// 这样可以让领域根（如直接 diff 命中路由/注解）优先进入对应子图，
// 避免落入纯符号传播导致 endpoint 漏报。
func (b *treeBuilder) buildRoot() Node {
	// 1) 路由领域根：直接命中某条路由注册。
	if route, ok := b.routes.RoutesByID[b.change.TargetID]; ok {
		return b.routeNode(route, 0, "", b.change.Confidence)
	}
	// 2) 路由组领域根：展开组内及其子组的全部路由。
	if group, ok := b.routes.GroupsByID[b.change.TargetID]; ok {
		root := Node{
			ID:         group.ID,
			Kind:       "route_group",
			Name:       group.GroupVar,
			File:       group.Span.File,
			Relation:   "",
			Span:       group.Span,
			Confidence: b.change.Confidence,
			Level:      0,
			Children:   []Node{},
		}
		for _, route := range b.routes.RoutesForGroup(group.ID) {
			root.Children = append(root.Children, b.routeNode(route, 1, "route_group_contains", b.change.Confidence))
		}
		root.Children = mergeAndSortChildren(root.Children)
		return root
	}
	// 3) 中间件领域根：展开挂载该中间件且顺序靠后的路由。
	if middleware, ok := b.routes.MiddlewareByID[b.change.TargetID]; ok {
		root := b.middlewareNode(middleware, 0, "", b.change.Confidence)
		root.Confidence = b.change.Confidence
		return root
	}
	// 4) 注解领域根：通过注解直接定位 endpoint。
	if annotation, ok := b.annotations[b.change.TargetID]; ok {
		return b.annotationRootNode(annotation)
	}
	// 5) Job 注册领域根：直接命中某条 job 注册语句。
	if job, ok := b.jobs[b.change.TargetID]; ok {
		return Node{
			ID:         job.ID,
			Kind:       "job",
			Name:       job.Name,
			File:       job.Span.File,
			Relation:   "registered_job",
			Span:       job.Span,
			Confidence: b.change.Confidence,
			Level:      0,
			Children:   []Node{},
		}
	}
	// 6) 符号根：沿反向引用图递归展开。
	if b.change.SymbolID != "" {
		root := b.symbolNode(b.change.SymbolID, 0)
		root.Confidence = b.change.Confidence
		// path 记录当前 DFS 已访问的符号，用于环路检测。
		path := map[facts.SymbolID]bool{b.change.SymbolID: true}
		b.expandSymbol(&root, path)
		return root
	}
	// 7) 文件降级根：无法映射到任何语义事实时保留为文件级根，无子节点。
	return Node{
		ID:         b.change.File,
		Kind:       "file",
		Name:       b.change.File,
		File:       b.change.File,
		Confidence: b.change.Confidence,
		Level:      0,
		Children:   []Node{},
	}
}

// expandSymbol 沿反向引用图、路由图、IM 图递归展开一个符号节点。
//
// 它综合以下五类子节点来源（与 ARCHITECTURE 第 10 节描述的 symbol 展开规则对应）：
//  1. 反向引用：通过 ReverseGraph 找到引用当前符号的其他符号；
//  2. handler 路由：通过 RouteGraph 找到以当前符号为 handler 的路由；
//  3. 路由作用域依赖：找到路由注册表达式 span 内引用当前符号的路由
//     （例如 AddGuard(g).POST(...) 这种 inline helper 调用）；
//  4. 中间件绑定：找到引用当前符号的中间件绑定；
//  5. IM 事件：通过 IMGraph 在当前传播 path 上精确匹配 payload/event/control 依赖。
//
// path 用于环路检测：若下一个符号已在当前 DFS 路径中，则不再递归、只标记 Cycle。
// 这保证了传播始终能终止，而不需要深度或目录裁剪配置。
func (b *treeBuilder) expandSymbol(node *Node, path map[facts.SymbolID]bool) {
	symbolID := facts.SymbolID(node.ID)
	references := b.reverse.ReferencesTo(symbolID)
	routes := b.routes.RoutesForHandler(symbolID)
	dependencyRoutes := b.routes.RoutesForDependency(symbolID)
	middlewareBindings := b.middlewareBindingsForSymbol(symbolID)
	for _, ref := range references {
		child := b.symbolNode(ref.FromSymbol, node.Level+1)
		child.Relation = referenceRelation(ref.Kind)
		child.Raw = ref.ToRaw
		child.Span = ref.Span
		child.File = b.symbolFile(ref.FromSymbol, child.File)
		child.Confidence = facts.CombineConfidence(node.Confidence, ref.Confidence)
		if path[ref.FromSymbol] {
			// 命中环路：标记后不再递归展开，避免无限循环。
			child.Cycle = true
		} else {
			// 就地回溯：进入子分支前标记、返回后清除，避免每条边复制整张 path map。
			// 环路检测与 EventsForPath 行为与复制版完全等价，但每条边从 O(L) 拷贝降到 O(1)。
			path[ref.FromSymbol] = true
			b.expandSymbol(&child, path)
			delete(path, ref.FromSymbol)
		}
		node.Children = append(node.Children, child)
	}
	for _, route := range routes {
		node.Children = append(node.Children, b.routeNode(route, node.Level+1, "registered_handler", node.Confidence))
	}
	for _, route := range dependencyRoutes {
		node.Children = append(node.Children, b.routeNode(route, node.Level+1, "route_dependency", node.Confidence))
	}
	for _, middleware := range middlewareBindings {
		node.Children = append(node.Children, b.middlewareNode(middleware, node.Level+1, "middleware_symbol", node.Confidence))
	}
	for _, match := range b.im.EventsForPath(symbolID, path, b.change) {
		node.Children = append(node.Children, b.imEventNode(match, node.Level+1, node.Confidence))
		// 只有已解析且事件名非空的 IM 事实才计入 IM 摘要；
		// 动态/未解析事件保留为 im_event_unresolved 终端，但不进入摘要。
		if match.Fact.Resolved && match.Fact.Event != "" {
			b.imEvents[match.Fact.Event] = IMEventImpact{Event: match.Fact.Event}
		}
	}
	node.Children = mergeAndSortChildren(node.Children)
}

// imEventNode 把一个 IMGraph 匹配结果转换成树中的 IM 事件终端节点。
//
// 已解析的事件使用 im_event，按事件名标识；未解析或事件名为空的事件降级为
// im_event_unresolved，使用 IM 事实自身 ID 与原始事件表达式，保留在树中但不计入摘要。
// imEventNode 把一个 IMGraph 匹配结果转换成树中的 IM 事件终端节点。
// parentConfidence 是传播路径上累积的置信度；im_event 终节点与它 combine。
func (b *treeBuilder) imEventNode(match graph.IMEventMatch, level int, parentConfidence facts.Confidence) Node {
	kind := "im_event"
	id := "im_event:" + match.Fact.Event
	name := match.Fact.Event
	if !match.Fact.Resolved || match.Fact.Event == "" {
		kind = "im_event_unresolved"
		id = match.Fact.ID
		name = match.Fact.EventRaw
	}
	// match.Fact.Span 为 *SourceSpan（可能 nil），解引用为值；nil 时 span 为零值 SourceSpan。
	span := facts.SourceSpan{}
	if match.Fact.Span != nil {
		span = *match.Fact.Span
	}
	return Node{
		ID:         id,
		Kind:       kind,
		Name:       name,
		File:       span.File,
		Relation:   string(match.Relation),
		Raw:        match.Fact.EventRaw,
		Span:       span,
		Confidence: facts.CombineConfidence(parentConfidence, match.Fact.Confidence),
		Level:      level,
		Children:   []Node{},
	}
}

// middlewareBindingsForSymbol 返回所有引用了指定符号的中间件绑定事实。
func (b *treeBuilder) middlewareBindingsForSymbol(symbolID facts.SymbolID) []facts.MiddlewareBindingFact {
	return b.routes.MiddlewareBindingsForSymbol(symbolID)
}

// symbolNode 构造一个符号节点。若符号在 facts 中存在，则补全 file/package/span 等元信息；
// 否则只通过 ID 拆分出 kind 与 name，仍保留节点以保证传播链路完整。
func (b *treeBuilder) symbolNode(id facts.SymbolID, level int) Node {
	symbol, ok := b.symbols[id]
	if !ok {
		return Node{
			ID:       string(id),
			Kind:     symbolKindFromID(id),
			Name:     symbolNameFromID(id),
			Level:    level,
			Children: []Node{},
		}
	}
	return Node{
		ID:       string(symbol.ID),
		Kind:     symbol.Kind,
		Name:     symbol.Name,
		File:     symbol.Span.File,
		Package:  symbol.PackagePath,
		Span:     symbol.Span,
		Level:    level,
		Children: []Node{},
	}
}

// symbolFile 返回符号所在文件；符号缺失时退回 fallback，避免丢失文件信息。
func (b *treeBuilder) symbolFile(id facts.SymbolID, fallback string) string {
	if symbol, ok := b.symbols[id]; ok {
		return symbol.Span.File
	}
	return fallback
}

// routeNode 构造路由节点，并按优先级补齐 endpoint 子节点。
//
// endpoint 来源优先级：handler 注解优先；缺失注解时降级为路由的 method/path fallback
// （被删除路由使用 deleted_route_endpoint 关系，置信度为 medium）。
// parentConfidence 是传播路径上累积的置信度；route/endpoint 终节点与它 combine，
// 避免弱根经 high 边到达后结论被夸大。这与 ARCHITECTURE 第 9 节描述的端点语义一致。
func (b *treeBuilder) routeNode(route facts.RouteRegistrationFact, level int, relation string, parentConfidence facts.Confidence) Node {
	path := route.ResolvedPath
	if path == "" {
		path = route.LocalPath
	}
	node := Node{
		ID:         route.ID,
		Kind:       "route",
		Name:       strings.TrimSpace(route.Method + " " + path),
		File:       route.Span.File,
		Relation:   relation,
		Raw:        route.HandlerRaw,
		Span:       route.Span,
		Confidence: facts.CombineConfidence(parentConfidence, facts.ConfidenceHigh),
		Level:      level,
		Method:     route.Method,
		Path:       path,
		Children:   []Node{},
	}
	annotations := b.routes.AnnotationsForHandler(route.HandlerSymbol)
	if len(annotations) == 0 {
		// 无注解时使用路由 method/path 降级端点。
		if route.Method != "" && path != "" {
			endpointRelation := "route_endpoint"
			if route.RecoveredFromDiff {
				endpointRelation = "deleted_route_endpoint"
			}
			node.Children = append(node.Children, b.endpointNode(
				route.Method,
				path,
				"",
				route.HandlerSymbol,
				route.Span,
				level+1,
				endpointRelation,
				facts.CombineConfidence(parentConfidence, facts.ConfidenceMedium),
			))
		}
		return node
	}
	// 别名注册判定（按 route 而非按 handler）：同一 handler 注册多条路径时（典型为
	// 新 bff 路径 + 旧路径别名），只有与注解 method+path 对应的那条 route 才归属注解
	// 端点身份。当本 route 不与任何注解对应、且 handler 的每条注解都已被其他 route
	// 认领时，本 route 是独立的第二条 URL（别名注册）：端点取其自身 method/path
	// （与无注解 fallback 同规格），避免被注解身份吞并造成旧路径接口漏报。
	// 注解路径漂移不受影响：只要还有注解未被任何 route 认领（漂移注解的注册正是
	// 当前这类不对应 route），就维持注解身份，不判别名。
	if !routeMatchesAnyAnnotation(route, annotations) && b.annotationsClaimedByOtherRoutes(route, annotations) {
		if route.Method != "" && path != "" {
			endpointRelation := "route_endpoint"
			if route.RecoveredFromDiff {
				endpointRelation = "deleted_route_endpoint"
			}
			node.Children = append(node.Children, b.endpointNode(
				route.Method,
				path,
				"",
				route.HandlerSymbol,
				route.Span,
				level+1,
				endpointRelation,
				facts.CombineConfidence(parentConfidence, facts.ConfidenceMedium),
			))
		}
		return node
	}
	for _, annotation := range annotations {
		node.Children = append(node.Children, b.annotationNode(annotation, route, level+1, "handler_annotation", parentConfidence))
	}
	node.Children = mergeAndSortChildren(node.Children)
	return node
}

// routeMatchesAnyAnnotation 判断 route 是否与其中某条注解对应。
func routeMatchesAnyAnnotation(route facts.RouteRegistrationFact, annotations []facts.AnnotationFact) bool {
	for _, annotation := range annotations {
		if routeMatchesAnnotation(route, annotation) {
			return true
		}
	}
	return false
}

// routeMatchesAnnotation 判断 route 与 annotation 是否指向同一条 endpoint 注册：
// HTTP method 相同（大小写归一），且注解路径等于 route 的 resolved path 或 local path。
// 同时接受 local path 是刻意保守：能对应上的 route 维持注解身份（现行为），
// 只有确定不对应的才走别名分支。
func routeMatchesAnnotation(route facts.RouteRegistrationFact, annotation facts.AnnotationFact) bool {
	if annotation.Path == "" || !strings.EqualFold(route.Method, annotation.Method) {
		return false
	}
	if annotation.Path == route.ResolvedPath {
		return true
	}
	return route.LocalPath != "" && annotation.Path == route.LocalPath
}

// annotationsClaimedByOtherRoutes 判断 handler 的每条注解是否都已被除当前 route 以外的
// 其他 route 认领（method+path 对应）。全部认领时说明注解身份均有各自的注册承载，
// 当前不对应的 route 是额外的别名注册；只要有一条注解未被认领（可能是漂移注解，
// 其注册正是当前 route），就不判别名，保守维持注解身份。
func (b *treeBuilder) annotationsClaimedByOtherRoutes(route facts.RouteRegistrationFact, annotations []facts.AnnotationFact) bool {
	siblings := b.routes.RoutesForHandler(route.HandlerSymbol)
	for _, annotation := range annotations {
		claimed := false
		for _, sibling := range siblings {
			if sibling.ID != route.ID && routeMatchesAnnotation(sibling, annotation) {
				claimed = true
				break
			}
		}
		if !claimed {
			return false
		}
	}
	return true
}

// annotationRootNode 构造注解领域根节点。如果该 handler 同时注册了路由，
// 则把注册路由挂到根下，便于 review 时追踪注解与注册的对应关系；
// 否则直接退化为单注解节点。
// 注解领域根是 diff 直接命中注解，证据确凿，confidence 取 change.Confidence。
func (b *treeBuilder) annotationRootNode(annotation facts.AnnotationFact) Node {
	routes := b.routes.RoutesForHandler(annotation.HandlerSymbol)
	if len(routes) == 0 {
		return b.annotationNode(annotation, facts.RouteRegistrationFact{}, 0, "", b.change.Confidence)
	}
	root := Node{
		ID:         annotation.ID,
		Kind:       "annotation",
		Name:       strings.TrimSpace(annotation.Method + " " + annotation.Path),
		File:       annotation.Span.File,
		Raw:        annotation.Raw,
		Span:       annotation.Span,
		Confidence: b.change.Confidence,
		Level:      0,
		Method:     annotation.Method,
		Path:       annotation.Path,
		Children:   []Node{},
	}
	for _, route := range routes {
		root.Children = append(root.Children, b.routeNode(route, 1, "registered_route", b.change.Confidence))
	}
	root.Children = mergeAndSortChildren(root.Children)
	return root
}

// middlewareNode 构造中间件节点，并展开受其影响的路由。
//
// 受影响路由通过 RouteGraph.RoutesAffectedByMiddleware 计算：同一 group 内、
// 且 statement_index 严格大于该中间件的路由才会被纳入。
// parentConfidence 是传播路径上累积的置信度；middleware 终节点与它 combine。
func (b *treeBuilder) middlewareNode(middleware facts.MiddlewareBindingFact, level int, relation string, parentConfidence facts.Confidence) Node {
	node := Node{
		ID:         middleware.ID,
		Kind:       "middleware",
		Name:       middleware.MiddlewareRaw,
		File:       middleware.Span.File,
		Relation:   relation,
		Raw:        middleware.MiddlewareRaw,
		Span:       middleware.Span,
		Confidence: facts.CombineConfidence(parentConfidence, facts.ConfidenceHigh),
		Level:      level,
		Children:   []Node{},
	}
	routes := b.routes.RoutesAffectedByMiddleware(middleware.ID)
	for _, route := range routes {
		node.Children = append(node.Children, b.routeNode(route, level+1, "middleware_applies_to", node.Confidence))
	}
	node.Children = mergeAndSortChildren(node.Children)
	return node
}

// annotationNode 构造注解节点并补齐 endpoint。注解是正式 endpoint identity；只有注解缺少
// method 或 path 时，才使用已解析 route 补齐对应字段。route 节点仍保留完整解析路径，供
// review 与辅助校验，不得覆盖完整 annotation。
// parentConfidence 是传播路径上累积的置信度；annotation/endpoint 终节点与它 combine，
// 避免弱根经 high 边到达后结论被夸大。
func (b *treeBuilder) annotationNode(annotation facts.AnnotationFact, route facts.RouteRegistrationFact, level int, relation string, parentConfidence facts.Confidence) Node {
	method := strings.ToUpper(annotation.Method)
	path := annotation.Path
	endpointRelation := "annotation_endpoint"
	endpointSpan := annotation.Span
	endpointEvidence := facts.ConfidenceHigh
	if method == "" {
		method = strings.ToUpper(route.Method)
		endpointRelation = "route_endpoint"
		endpointSpan = route.Span
		endpointEvidence = facts.ConfidenceMedium
	}
	if path == "" {
		path = route.ResolvedPath
		if path == "" {
			path = route.LocalPath
		}
		endpointRelation = "route_endpoint"
		endpointSpan = route.Span
		endpointEvidence = facts.ConfidenceMedium
	}
	if endpointRelation == "route_endpoint" && route.RecoveredFromDiff {
		endpointRelation = "deleted_route_endpoint"
	}
	combined := facts.CombineConfidence(parentConfidence, facts.ConfidenceHigh)
	node := Node{
		ID:         annotation.ID,
		Kind:       "annotation",
		Name:       strings.TrimSpace(method + " " + path),
		File:       annotation.Span.File,
		Relation:   relation,
		Raw:        annotation.Raw,
		Span:       annotation.Span,
		Confidence: combined,
		Level:      level,
		Method:     method,
		Path:       path,
		Children:   []Node{},
	}
	if method != "" && path != "" {
		node.Children = append(node.Children, b.endpointNode(
			method,
			path,
			annotation.ID,
			annotation.HandlerSymbol,
			endpointSpan,
			level+1,
			endpointRelation,
			facts.CombineConfidence(parentConfidence, endpointEvidence),
		))
	}
	return node
}

// endpointNode 构造一个 endpoint 终端节点，并把命中的端点记入本棵树的端点摘要。
//
// 端点按 "method\x00path" 去重；同一棵树中多次命中同一端点不会重复出现在摘要里。
// relation 与 confidence 由调用方按端点来源（注解/路由/被删除路由）传入。
func (b *treeBuilder) endpointNode(
	method, path, annotationID string,
	handler facts.SymbolID,
	span facts.SourceSpan,
	level int,
	relation string,
	confidence facts.Confidence,
) Node {
	id := fmt.Sprintf("endpoint:%s:%s", method, path)
	key := method + "\x00" + path
	b.endpoints[key] = EndpointImpact{
		ID:            id,
		Method:        method,
		Path:          path,
		AnnotationID:  annotationID,
		HandlerSymbol: handler,
		Routes:        b.resolvedRoutesForHandler(handler),
	}
	return Node{
		ID:         id,
		Kind:       "endpoint",
		Name:       method + " " + path,
		File:       span.File,
		Relation:   relation,
		Span:       span,
		Confidence: confidence,
		Level:      level,
		Method:     method,
		Path:       path,
		Children:   []Node{},
	}
}

// resolvedRoutesForHandler 收集指定 handler 已静态解析出的路由候选，作为 endpoint
// 的辅助证据（ARCHITECTURE 第 5 节）。优先取 resolved path，缺失时退回 local path；
// 无 method 或无任何 path 的路由跳过。顺序去重以保证输出稳定。
func (b *treeBuilder) resolvedRoutesForHandler(handler facts.SymbolID) []EndpointRoute {
	if handler == "" {
		return nil
	}
	var out []EndpointRoute
	seen := map[string]bool{}
	for _, route := range b.routes.RoutesForHandler(handler) {
		path := route.ResolvedPath
		if path == "" {
			path = route.LocalPath
		}
		if route.Method == "" || path == "" {
			continue
		}
		candidate := EndpointRoute{Method: strings.ToUpper(route.Method), Path: path}
		key := candidate.Method + "\x00" + candidate.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, candidate)
	}
	return out
}

// referenceRelation 把引用边 kind 转成树中展示的 relation 字符串。
func referenceRelation(kind facts.ReferenceKind) string {
	switch kind {
	case facts.ReferenceKindType:
		return "type_ref"
	case facts.ReferenceKindValue:
		return "value_ref"
	default:
		return "call"
	}
}

// mergeAndSortChildren 合并并稳定排序同一节点的子节点。
//
// 合并规则：ID 与 relation 都相同的子节点视为同一个，把其孙节点合并进第一个出现的位置，
// 并保留 Cycle 标记。这样在多路径汇聚（例如多个引用都到达同一符号）时不会重复展开。
// 排序键依次为 Level、Kind、File、Package、ID、Relation，保证输出对 golden/consumer 稳定。
func mergeAndSortChildren(children []Node) []Node {
	merged := make([]Node, 0, len(children))
	byKey := map[string]int{}
	for _, child := range children {
		key := child.ID + "\x00" + child.Relation
		if index, ok := byKey[key]; ok {
			// 同 ID+relation 的节点合并：把后者的孙节点并入前者，并保留任一 Cycle 标记。
			merged[index].Children = mergeAndSortChildren(append(merged[index].Children, child.Children...))
			merged[index].Cycle = merged[index].Cycle || child.Cycle
			continue
		}
		byKey[key] = len(merged)
		merged = append(merged, child)
	}
	sort.Slice(merged, func(i, j int) bool {
		left, right := merged[i], merged[j]
		if left.Level != right.Level {
			return left.Level < right.Level
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Relation < right.Relation
	})
	return merged
}

// symbolKindFromID 从符号 ID（形如 "func:pkg::Name"）中拆出类型前缀。
// 符号在 facts 中缺失时仍能通过 ID 推断展示用的 kind。
func symbolKindFromID(id facts.SymbolID) string {
	raw := string(id)
	if index := strings.Index(raw, ":"); index > 0 {
		return raw[:index]
	}
	return "symbol"
}

// symbolNameFromID 从符号 ID 中拆出末尾的名字部分。
// 符号在 facts 中缺失时仍能通过 ID 推断展示用的 name。
func symbolNameFromID(id facts.SymbolID) string {
	raw := string(id)
	if index := strings.LastIndex(raw, ":"); index >= 0 && index+1 < len(raw) {
		return raw[index+1:]
	}
	return raw
}
