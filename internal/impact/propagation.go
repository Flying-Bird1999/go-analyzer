package impact

import (
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

type analyzer struct {
	store   *facts.Store
	reverse *graph.ReverseGraph
	routes  *graph.RouteGraph
	result  Result
	seen    map[string]bool
}

func newAnalyzer(s *facts.Store) *analyzer {
	return &analyzer{
		store:   s,
		reverse: graph.NewReverseGraph(s),
		routes:  graph.NewRouteGraph(s),
		result: Result{
			ImpactedEndpoints: []EndpointImpact{},
			EvidenceChains:    []graph.EvidenceChain{},
			ModuleImpacts:     []ModuleImpact{},
			Diagnostics:       []string{},
		},
		seen: map[string]bool{},
	}
}

func (a *analyzer) run() Result {
	for _, change := range a.store.Changes {
		if change.SymbolID != "" {
			a.propagateSymbolChange(change, change.SymbolID)
			continue
		}
		if change.TargetID != "" {
			a.propagateTargetChange(change)
		}
	}
	for _, usage := range a.store.ModuleUsages {
		a.propagateModuleUsage(usage)
	}
	return a.result
}

func (a *analyzer) propagateModuleUsage(usage facts.ModuleUsageFact) {
	a.result.ModuleImpacts = append(a.result.ModuleImpacts, ModuleImpact{
		ModulePath: usage.ModulePath,
		Basis:      usage.Basis,
		SymbolID:   usage.SymbolID,
	})
	if usage.Basis == facts.ModuleUsageUnreferenced || usage.SymbolID == "" {
		return
	}
	change := facts.ChangeFact{
		ID:       "module_change:" + usage.ModulePath,
		Kind:     facts.ChangeKindMethodBodyChanged,
		SymbolID: usage.SymbolID,
		File:     usage.File,
	}
	a.propagateSymbolChange(change, usage.SymbolID)
}

func (a *analyzer) propagateTargetChange(change facts.ChangeFact) {
	if route, ok := a.routes.RoutesByID[change.TargetID]; ok {
		a.addEndpointsForRoute(change, route, "changed route registration")
		return
	}
	if _, ok := a.routes.GroupsByID[change.TargetID]; ok {
		for _, route := range a.routes.RoutesForGroup(change.TargetID) {
			a.addEndpointsForRoute(change, route, "changed route group")
		}
		return
	}
	if _, ok := a.routes.MiddlewareByID[change.TargetID]; ok {
		for _, route := range a.routes.RoutesAffectedByMiddleware(change.TargetID) {
			a.addEndpointsForRoute(change, route, "changed middleware binding")
		}
	}
}

func (a *analyzer) propagateSymbolChange(change facts.ChangeFact, start facts.SymbolID) {
	queue := []facts.SymbolID{start}
	visited := map[facts.SymbolID]bool{start: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		a.addEndpointsForHandler(change, start, current, "symbol propagation")
		for _, ref := range a.reverse.ReferencesTo(current) {
			if visited[ref.FromSymbol] {
				continue
			}
			visited[ref.FromSymbol] = true
			queue = append(queue, ref.FromSymbol)
		}
	}
}

func (a *analyzer) addEndpointsForHandler(change facts.ChangeFact, trigger facts.SymbolID, handler facts.SymbolID, reason string) {
	for _, route := range a.routes.RoutesForHandler(handler) {
		a.addEndpointsForRoute(change, route, reason)
	}
}

func (a *analyzer) addEndpointsForRoute(change facts.ChangeFact, route facts.RouteRegistrationFact, reason string) {
	if route.HandlerSymbol == "" {
		return
	}
	for _, annotation := range a.routes.AnnotationsForHandler(route.HandlerSymbol) {
		chain := graph.NewEvidenceChain(fmt.Sprintf("evidence:%s:%s", change.ID, annotation.ID))
		chain.AddNode(change.ID, "changed fact", facts.SourceSpan{})
		chain.AddNode(route.ID, reason, route.Span)
		chain.AddNode(string(route.HandlerSymbol), "route handler", facts.SourceSpan{})
		chain.AddNode(annotation.ID, "annotation endpoint", annotation.Span)
		chain.AddEdge(change.ID, route.ID, "affects_route")
		chain.AddEdge(route.ID, string(route.HandlerSymbol), "registered_handler")
		chain.AddEdge(string(route.HandlerSymbol), annotation.ID, "handler_annotation")
		a.addEndpoint(change, annotation, route.HandlerSymbol, chain)
	}
}

func (a *analyzer) addEndpoint(change facts.ChangeFact, annotation facts.AnnotationFact, handler facts.SymbolID, chain graph.EvidenceChain) {
	key := annotation.Method + " " + annotation.Path + " " + change.ID
	if a.seen[key] {
		return
	}
	a.seen[key] = true
	a.result.ImpactedEndpoints = append(a.result.ImpactedEndpoints, EndpointImpact{
		ID:              fmt.Sprintf("endpoint:%s:%s:%s", change.ID, annotation.Method, annotation.Path),
		Method:          annotation.Method,
		Path:            annotation.Path,
		AnnotationID:    annotation.ID,
		HandlerSymbol:   handler,
		TriggerChangeID: change.ID,
		EvidenceChainID: chain.ID,
	})
	a.result.EvidenceChains = append(a.result.EvidenceChains, chain)
}
