package graph

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type RouteGraph struct {
	RoutesByID           map[string]facts.RouteRegistrationFact
	GroupsByID           map[string]facts.RouteGroupFact
	MiddlewareByID       map[string]facts.MiddlewareBindingFact
	RoutesByGroupID      map[string][]facts.RouteRegistrationFact
	ChildGroupsByID      map[string][]string
	RoutesByHandler      map[facts.SymbolID][]facts.RouteRegistrationFact
	AnnotationsByHandler map[facts.SymbolID][]facts.AnnotationFact
}

func NewRouteGraph(store *facts.Store) *RouteGraph {
	g := &RouteGraph{
		RoutesByID:           map[string]facts.RouteRegistrationFact{},
		GroupsByID:           map[string]facts.RouteGroupFact{},
		MiddlewareByID:       map[string]facts.MiddlewareBindingFact{},
		RoutesByGroupID:      map[string][]facts.RouteRegistrationFact{},
		ChildGroupsByID:      map[string][]string{},
		RoutesByHandler:      map[facts.SymbolID][]facts.RouteRegistrationFact{},
		AnnotationsByHandler: map[facts.SymbolID][]facts.AnnotationFact{},
	}
	for _, group := range store.RouteGroups {
		g.GroupsByID[group.ID] = group
		if group.ParentGroupID != "" {
			g.ChildGroupsByID[group.ParentGroupID] = append(g.ChildGroupsByID[group.ParentGroupID], group.ID)
		}
	}
	for _, route := range store.Routes {
		g.RoutesByID[route.ID] = route
		groupID := effectiveGroupID(route.GroupID, route.RouteFunc, route.GroupVar)
		g.RoutesByGroupID[groupID] = append(g.RoutesByGroupID[groupID], route)
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
	for group := range g.RoutesByGroupID {
		sortRoutes(g.RoutesByGroupID[group])
	}
	for group := range g.ChildGroupsByID {
		sort.Strings(g.ChildGroupsByID[group])
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
	var routes []facts.RouteRegistrationFact
	seenGroups := map[string]bool{}
	seenRoutes := map[string]bool{}
	var collect func(string)
	collect = func(current string) {
		if seenGroups[current] {
			return
		}
		seenGroups[current] = true
		for _, route := range g.RoutesByGroupID[current] {
			if !seenRoutes[route.ID] {
				seenRoutes[route.ID] = true
				routes = append(routes, route)
			}
		}
		for _, child := range g.ChildGroupsByID[current] {
			collect(child)
		}
	}
	collect(groupID)
	if group, ok := g.GroupsByID[groupID]; ok {
		collect(effectiveGroupID("", group.RouteFunc, group.GroupVar))
	}
	sortRoutes(routes)
	return append([]facts.RouteRegistrationFact(nil), routes...)
}

func (g *RouteGraph) RoutesAffectedByMiddleware(bindingID string) []facts.RouteRegistrationFact {
	binding, ok := g.MiddlewareByID[bindingID]
	if !ok {
		return nil
	}
	var out []facts.RouteRegistrationFact
	groupID := effectiveGroupID(binding.GroupID, binding.RouteFunc, binding.GroupVar)
	for _, route := range g.RoutesForGroup(groupID) {
		if binding.StatementIndex < route.StatementIndex {
			out = append(out, route)
		}
	}
	sortRoutes(out)
	return out
}

func effectiveGroupID(groupID string, routeFunc facts.SymbolID, groupVar string) string {
	if groupID != "" {
		return groupID
	}
	return string(routeFunc) + "::" + groupVar
}

func sortRoutes(routes []facts.RouteRegistrationFact) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].StatementIndex != routes[j].StatementIndex {
			return routes[i].StatementIndex < routes[j].StatementIndex
		}
		return routes[i].ID < routes[j].ID
	})
}
