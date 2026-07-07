// reverse.go 实现反向引用图视图：把 facts.Store 中的引用按被依赖 symbol 聚合，
// 供影响传播阶段从被改动的 symbol 反向查找所有引用它的 symbol。
//
// Package graph 基于 facts.Store 临时构造三个运行时查询视图，本包不生产业务事实，
// 也不会写回 Store，仅为影响传播提供高效查询：
//
//   - 反向引用图（ReverseGraph）：被依赖 symbol -> 引用它的 symbol references。
//   - 路由图（RouteGraph）：handler/group/middleware -> routes/annotations。
//   - IM 图（IMGraph）：sender -> IM facts，并按当前传播 path 上的
//     payload/event/control 依赖精确匹配。
//
// 三类视图拆分是因为各自查询语义不同，详见 ARCHITECTURE.md 5.13 节。
package graph

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// ReverseGraph 是反向引用图：以被依赖 symbol（ToSymbol）为键，聚合所有引用它的
// ReferenceFact。构造后可 O(1) 查询某个 symbol 的全部引用者。
type ReverseGraph struct {
	// ByTarget 按 ToSymbol 聚合的引用列表，已按 FromSymbol、起始行排序。
	ByTarget map[facts.SymbolID][]facts.ReferenceFact
}

// NewReverseGraph 扫描 store 中全部 ReferenceFact，按 ToSymbol 聚合并排序后
// 构造反向引用图。ToSymbol 为空的引用会被跳过。
func NewReverseGraph(store *facts.Store) *ReverseGraph {
	g := &ReverseGraph{ByTarget: map[facts.SymbolID][]facts.ReferenceFact{}}
	for _, ref := range store.References {
		if ref.ToSymbol == "" {
			continue
		}
		g.ByTarget[ref.ToSymbol] = append(g.ByTarget[ref.ToSymbol], ref)
	}
	// 对每个 target 的引用列表排序：先按引用者 symbol，再按位置区间起始行，
	// 保证查询输出稳定且可复现。
	for target := range g.ByTarget {
		sort.Slice(g.ByTarget[target], func(i, j int) bool {
			if g.ByTarget[target][i].FromSymbol != g.ByTarget[target][j].FromSymbol {
				return g.ByTarget[target][i].FromSymbol < g.ByTarget[target][j].FromSymbol
			}
			return g.ByTarget[target][i].Span.StartLine < g.ByTarget[target][j].Span.StartLine
		})
	}
	return g
}

// ReferencesTo 返回引用了 target 的全部 ReferenceFact。返回的是副本切片，
// 调用方可安全修改而不影响内部索引。
func (g *ReverseGraph) ReferencesTo(target facts.SymbolID) []facts.ReferenceFact {
	return append([]facts.ReferenceFact(nil), g.ByTarget[target]...)
}
