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
			g.grpcByCaller[call.CallerSymbol] = append(g.grpcByCaller[call.CallerSymbol], call)
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
