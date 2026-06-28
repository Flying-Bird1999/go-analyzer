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
		linkRoute(idx, store, &store.Routes[i], byHandler)
	}
	return nil
}

func LinkRoute(idx *astindex.Index, store *facts.Store, route *facts.RouteRegistrationFact) bool {
	return linkRoute(idx, store, route, annotationsByHandler(store))
}

func linkRoute(
	idx *astindex.Index,
	store *facts.Store,
	route *facts.RouteRegistrationFact,
	byHandler map[facts.SymbolID][]facts.AnnotationFact,
) bool {
	handler, ok := ResolveHandlerSymbolWithConfidence(idx, *route)
	if !ok {
		return false
	}
	route.HandlerSymbol = handler.ID
	store.Links = append(store.Links, facts.LinkFact{
		ID:         linkID(facts.LinkKindRouteToHandler, route.ID, string(handler.ID)),
		Kind:       facts.LinkKindRouteToHandler,
		FromID:     route.ID,
		ToID:       string(handler.ID),
		Confidence: handler.Confidence,
	})
	for _, annotation := range byHandler[handler.ID] {
		store.Links = append(store.Links, facts.LinkFact{
			ID:         linkID(facts.LinkKindHandlerToAnnotation, string(handler.ID), annotation.ID),
			Kind:       facts.LinkKindHandlerToAnnotation,
			FromID:     string(handler.ID),
			ToID:       annotation.ID,
			Confidence: facts.ConfidenceHigh,
		})
	}
	return true
}

func linkID(kind facts.LinkKind, from, to string) string {
	return fmt.Sprintf("link:%s:%s:%s", kind, from, to)
}
