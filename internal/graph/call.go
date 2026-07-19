// call.go 提供只包含项目内执行调用与 gRPC terminal 的查询图。
package graph

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// CallGraph 排除 type/value reference，避免把数据模型引用误当成可执行链路。
type CallGraph struct {
	forward      map[facts.SymbolID][]facts.SymbolID
	reverse      map[facts.SymbolID][]facts.SymbolID
	grpcByCaller map[facts.SymbolID][]facts.GrpcCallFact
}

func NewCallGraph(store *facts.Store) *CallGraph {
	g := &CallGraph{forward: map[facts.SymbolID][]facts.SymbolID{}, reverse: map[facts.SymbolID][]facts.SymbolID{}, grpcByCaller: map[facts.SymbolID][]facts.GrpcCallFact{}}
	for _, ref := range store.References {
		if ref.Kind != facts.ReferenceKindCall || ref.FromSymbol == "" || ref.ToSymbol == "" {
			continue
		}
		g.forward[ref.FromSymbol] = appendSymbolOnce(g.forward[ref.FromSymbol], ref.ToSymbol)
		g.reverse[ref.ToSymbol] = appendSymbolOnce(g.reverse[ref.ToSymbol], ref.FromSymbol)
	}
	for _, call := range store.GrpcCalls {
		if call.CallerSymbol != "" {
			// 与 forward/reverse 一致使用 appendOnce 去重：GrpcCallFact.ID 按调用点构造
			// 理论上唯一，但若上游因任何原因（如未来新增的抽取路径）产生同 ID 重复
			// 记录，这里也不应放大成重复的调用图边，保持三张表去重语义一致。
			g.grpcByCaller[call.CallerSymbol] = appendGrpcCallOnce(g.grpcByCaller[call.CallerSymbol], call)
		}
	}
	for symbol := range g.forward {
		sort.Slice(g.forward[symbol], func(i, j int) bool { return g.forward[symbol][i] < g.forward[symbol][j] })
	}
	for symbol := range g.reverse {
		sort.Slice(g.reverse[symbol], func(i, j int) bool { return g.reverse[symbol][i] < g.reverse[symbol][j] })
	}
	for symbol := range g.grpcByCaller {
		sort.Slice(g.grpcByCaller[symbol], func(i, j int) bool { return g.grpcByCaller[symbol][i].ID < g.grpcByCaller[symbol][j].ID })
	}
	return g
}

func (g *CallGraph) Callees(symbol facts.SymbolID) []facts.SymbolID {
	return append([]facts.SymbolID(nil), g.forward[symbol]...)
}
func (g *CallGraph) Callers(symbol facts.SymbolID) []facts.SymbolID {
	return append([]facts.SymbolID(nil), g.reverse[symbol]...)
}
func (g *CallGraph) GrpcCalls(symbol facts.SymbolID) []facts.GrpcCallFact {
	return append([]facts.GrpcCallFact(nil), g.grpcByCaller[symbol]...)
}

func appendSymbolOnce(items []facts.SymbolID, item facts.SymbolID) []facts.SymbolID {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

// appendGrpcCallOnce 把 call 追加到 calls（仅当其 ID 尚未存在），保持与 forward/reverse
// 相同的去重语义。
func appendGrpcCallOnce(calls []facts.GrpcCallFact, call facts.GrpcCallFact) []facts.GrpcCallFact {
	for _, existing := range calls {
		if existing.ID == call.ID {
			return calls
		}
	}
	return append(calls, call)
}
