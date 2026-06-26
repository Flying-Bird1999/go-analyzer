package graph

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type ReverseGraph struct {
	ByTarget map[facts.SymbolID][]facts.ReferenceFact
}

func NewReverseGraph(store *facts.Store) *ReverseGraph {
	g := &ReverseGraph{ByTarget: map[facts.SymbolID][]facts.ReferenceFact{}}
	for _, ref := range store.References {
		if ref.ToSymbol == "" {
			continue
		}
		g.ByTarget[ref.ToSymbol] = append(g.ByTarget[ref.ToSymbol], ref)
	}
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

func (g *ReverseGraph) ReferencesTo(target facts.SymbolID) []facts.ReferenceFact {
	return append([]facts.ReferenceFact(nil), g.ByTarget[target]...)
}
