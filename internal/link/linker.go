// linker.go 实现 link 包的入口：把 route 文件里的 handler 原始表达式、controller 里的 handler 声明、handler 注解里的 endpoint 归并到同一个 handler 符号，并解析 middleware 符号，复用 astindex 值类型解析。
//
// Package link 负责把 route 领域里散落在不同 extractor 中的事实接到同一条 endpoint 链路上。
//
// route extractor 只能从路由注册语法里抽出形如 "controller.GetOrder" 这样的 handler 原始表达式（raw expression）字符串，
// annotation extractor 只能从 handler 注释里抽出 "@Get /path -> handler symbol"，
// reference extractor 只负责代码 symbol 之间的依赖边。这些事实如果直接使用，无法稳定地在 route、handler、annotation 之间互相传播。
//
// 本包借助 astindex 已建立的符号索引和值类型（value type）解析能力，完成三件事：
//
//   - 把 route 里 handler_raw 原始表达式解析为稳定的 handler 符号（支持跨包 import、包级变量方法、多层 struct field 的 selector）；
//   - 生成 route -> handler 的关联事实（route_to_handler）；
//   - 生成 handler -> annotation 的关联事实（handler_to_annotation），使 route 注册能找到对应 endpoint 注解；
//   - 把 middleware 绑定里的 middleware 原始表达式解析为 middleware 函数/方法符号。
//
// 完成关联后，下游 RouteGraph 才能回答“handler 变了影响哪些 route”“middleware 变了影响哪些挂载它的 route”等问题。
package link

import (
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// Run 是 link 阶段的入口：先解析并补全 middleware 符号，再逐条 route 解析其 handler 符号、生成 route-handler 与 handler-annotation 关联事实。
// handler_to_annotation 按 handler 去重：多个 route 共享同一 handler 时只生成一次该 handler 的注解关联。
// 始终返回 nil；解析失败的单条 route/middleware 仅被跳过，不会中断整体流程。
func Run(idx *astindex.Index, store *facts.Store) error {
	// 先处理 middleware：因为 middleware 绑定与 route 是独立的两类事实，互不依赖，先做后做均可。
	linkMiddlewareSymbols(idx, store)
	// 预先把注解按 handler symbol 分桶，避免每条 route 都全量扫描 store.Annotations。
	byHandler := annotationsByHandler(store)
	// linkedHandlers 记录已生成 handler_to_annotation 的 handler，避免多 route 共享 handler 时产生重复链接。
	linkedHandlers := map[facts.SymbolID]bool{}
	for i := range store.Routes {
		// 取地址写回 route.HandlerSymbol，因此使用索引遍历而非值拷贝。
		linkRoute(idx, store, &store.Routes[i], byHandler, linkedHandlers)
	}
	return nil
}

// LinkRoute 对单条 route 解析 handler 并生成关联事实，供外部（如增量分析）按需调用。
// 内部会重新构建一次按 handler 分桶的注解索引，再委托给 linkRoute。返回是否解析成功。
func LinkRoute(idx *astindex.Index, store *facts.Store, route *facts.RouteRegistrationFact) bool {
	linkedHandlers := map[facts.SymbolID]bool{}
	return linkRoute(idx, store, route, annotationsByHandler(store), linkedHandlers)
}

// linkRoute 是 route 关联的核心实现：解析 handler 符号、写回 route，并生成 route->handler 与 handler->annotation 两条关联事实。
// byHandler 为按 handler symbol 预先分桶的注解集合，由调用方传入以便复用。
// linkedHandlers 记录已生成 handler_to_annotation 的 handler，避免多 route 共享 handler 时产生重复链接。
func linkRoute(
	idx *astindex.Index,
	store *facts.Store,
	route *facts.RouteRegistrationFact,
	byHandler map[facts.SymbolID][]facts.AnnotationFact,
	linkedHandlers map[facts.SymbolID]bool,
) bool {
	// 解析 handler 原始表达式为带置信度的符号；解析失败直接放弃此 route，不产生关联。
	handler, ok := ResolveHandlerSymbolWithConfidence(idx, *route)
	if !ok {
		return false
	}
	// 把解析到的稳定符号写回 route，使后续 RouteGraph 可直接读取，无需再次解析。
	route.HandlerSymbol = handler.ID
	// 生成 route -> handler 关联事实，置信度继承自 handler 解析结果（如多层 field 推断会降到 medium）。
	store.Links = append(store.Links, facts.LinkFact{
		ID:         linkID(facts.LinkKindRouteToHandler, route.ID, string(handler.ID)),
		Kind:       facts.LinkKindRouteToHandler,
		FromID:     route.ID,
		ToID:       string(handler.ID),
		Confidence: handler.Confidence,
	})
	// handler_to_annotation 是 per-handler 关系：同一 handler 被多 route 注册时只生成一次。
	if !linkedHandlers[handler.ID] {
		linkedHandlers[handler.ID] = true
		// 同一个 handler 可能有多条注解（多个 endpoint），逐条建立 handler -> annotation 关联。
		// 这里 handler 已被索引精确解析，注解归属无歧义，因此置信度固定为 high。
		for _, annotation := range byHandler[handler.ID] {
			store.Links = append(store.Links, facts.LinkFact{
				ID:         linkID(facts.LinkKindHandlerToAnnotation, string(handler.ID), annotation.ID),
				Kind:       facts.LinkKindHandlerToAnnotation,
				FromID:     string(handler.ID),
				ToID:       annotation.ID,
				Confidence: facts.ConfidenceHigh,
			})
		}
	}
	return true
}

// linkID 按固定模板拼接关联事实的稳定 ID，格式为 "link:<kind>:<from>:<to>"，便于去重与外部按 ID 引用。
func linkID(kind facts.LinkKind, from, to string) string {
	return fmt.Sprintf("link:%s:%s:%s", kind, from, to)
}
