package link

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func annotationsByHandler(store *facts.Store) map[facts.SymbolID][]facts.AnnotationFact {
	out := map[facts.SymbolID][]facts.AnnotationFact{}
	for _, annotation := range store.Annotations {
		out[annotation.HandlerSymbol] = append(out[annotation.HandlerSymbol], annotation)
	}
	for handler := range out {
		sort.Slice(out[handler], func(i, j int) bool {
			return out[handler][i].ID < out[handler][j].ID
		})
	}
	return out
}
