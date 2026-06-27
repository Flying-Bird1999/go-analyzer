package link

import (
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func Run(idx *astindex.Index, store *facts.Store) error {
	linkMiddlewareSymbols(idx, store)
	byHandler := annotationsByHandler(store)
	for i := range store.Routes {
		handler, ok := ResolveHandlerSymbol(idx, store.Routes[i])
		if !ok {
			continue
		}
		store.Routes[i].HandlerSymbol = handler
		store.Links = append(store.Links, facts.LinkFact{
			ID:         linkID(facts.LinkKindRouteToHandler, store.Routes[i].ID, string(handler)),
			Kind:       facts.LinkKindRouteToHandler,
			FromID:     store.Routes[i].ID,
			ToID:       string(handler),
			Confidence: facts.ConfidenceHigh,
		})
		for _, annotation := range byHandler[handler] {
			store.Links = append(store.Links, facts.LinkFact{
				ID:         linkID(facts.LinkKindHandlerToAnnotation, string(handler), annotation.ID),
				Kind:       facts.LinkKindHandlerToAnnotation,
				FromID:     string(handler),
				ToID:       annotation.ID,
				Confidence: facts.ConfidenceHigh,
			})
		}
	}
	return nil
}

func linkID(kind facts.LinkKind, from, to string) string {
	return fmt.Sprintf("link:%s:%s:%s", kind, from, to)
}
