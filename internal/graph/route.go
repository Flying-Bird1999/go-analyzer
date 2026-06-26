package graph

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type RouteGraph struct {
	RoutesByID           map[string]facts.RouteRegistrationFact
	GroupsByID           map[string]facts.RouteGroupFact
	MiddlewareByID       map[string]facts.MiddlewareBindingFact
	RoutesByGroup        map[string][]facts.RouteRegistrationFact
	RoutesByHandler      map[facts.SymbolID][]facts.RouteRegistrationFact
	AnnotationsByHandler map[facts.SymbolID][]facts.AnnotationFact
}

func NewRouteGraph(store *facts.Store) *RouteGraph {
	g := &RouteGraph{
		RoutesByID:           map[string]facts.RouteRegistrationFact{},
		GroupsByID:           map[string]facts.RouteGroupFact{},
		MiddlewareByID:       map[string]facts.MiddlewareBindingFact{},
		RoutesByGroup:        map[string][]facts.RouteRegistrationFact{},
		RoutesByHandler:      map[facts.SymbolID][]facts.RouteRegistrationFact{},
		AnnotationsByHandler: map[facts.SymbolID][]facts.AnnotationFact{},
	}
	for _, group := range store.RouteGroups {
		g.GroupsByID[group.ID] = group
	}
	for _, route := range store.Routes {
		g.RoutesByID[route.ID] = route
		g.RoutesByGroup[route.GroupVar] = append(g.RoutesByGroup[route.GroupVar], route)
		if route.HandlerSymbol != "" {
			g.RoutesByHandler[route.HandlerSymbol] = append(g.RoutesByHandler[route.HandlerSymbol], route)
		}
	}
	for _, binding := range store.Middleware {
		g.MiddlewareByID[binding.ID] = binding
	}
	for _, annotation := range store.Annotations {
		g.AnnotationsByHandler[annotation.HandlerSymbol] = append(g.AnnotationsByHandler[annotation.HandlerSymbol], annotation)
	}
	g.sort()
	return g
}

func (g *RouteGraph) sort() {
	for group := range g.RoutesByGroup {
		sortRoutes(g.RoutesByGroup[group])
	}
	for handler := range g.RoutesByHandler {
		sortRoutes(g.RoutesByHandler[handler])
	}
	for handler := range g.AnnotationsByHandler {
		sort.Slice(g.AnnotationsByHandler[handler], func(i, j int) bool {
			return g.AnnotationsByHandler[handler][i].ID < g.AnnotationsByHandler[handler][j].ID
		})
	}
}

func (g *RouteGraph) RoutesForHandler(handler facts.SymbolID) []facts.RouteRegistrationFact {
	return append([]facts.RouteRegistrationFact(nil), g.RoutesByHandler[handler]...)
}

func (g *RouteGraph) AnnotationsForHandler(handler facts.SymbolID) []facts.AnnotationFact {
	return append([]facts.AnnotationFact(nil), g.AnnotationsByHandler[handler]...)
}

func (g *RouteGraph) RoutesForGroup(groupID string) []facts.RouteRegistrationFact {
	group, ok := g.GroupsByID[groupID]
	if !ok {
		return nil
	}
	return append([]facts.RouteRegistrationFact(nil), g.RoutesByGroup[group.GroupVar]...)
}

func (g *RouteGraph) RoutesAffectedByMiddleware(bindingID string) []facts.RouteRegistrationFact {
	binding, ok := g.MiddlewareByID[bindingID]
	if !ok {
		return nil
	}
	var out []facts.RouteRegistrationFact
	for _, route := range g.RoutesByGroup[binding.GroupVar] {
		if binding.StatementIndex < route.StatementIndex {
			out = append(out, route)
		}
	}
	sortRoutes(out)
	return out
}

func sortRoutes(routes []facts.RouteRegistrationFact) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].StatementIndex != routes[j].StatementIndex {
			return routes[i].StatementIndex < routes[j].StatementIndex
		}
		return routes[i].ID < routes[j].ID
	})
}
