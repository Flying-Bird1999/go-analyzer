package impact

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

type treeBuilder struct {
	*treeContext
	endpoints map[string]EndpointImpact
	change    facts.ChangeFact
}

type treeContext struct {
	store       *facts.Store
	reverse     *graph.ReverseGraph
	routes      *graph.RouteGraph
	symbols     map[facts.SymbolID]facts.SymbolFact
	annotations map[string]facts.AnnotationFact
}

func AnalyzeTrees(store *facts.Store) TreeResult {
	result := TreeResult{
		Roots: []RootImpact{},
	}
	context := newTreeContext(store)
	changes := append([]facts.ChangeFact(nil), store.Changes...)
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].ID < changes[j].ID
	})
	for _, change := range changes {
		builder := newTreeBuilder(context, change)
		root := builder.buildRoot()
		endpoints := make([]EndpointImpact, 0, len(builder.endpoints))
		for _, endpoint := range builder.endpoints {
			endpoints = append(endpoints, endpoint)
		}
		sort.Slice(endpoints, func(i, j int) bool {
			if endpoints[i].Method != endpoints[j].Method {
				return endpoints[i].Method < endpoints[j].Method
			}
			return endpoints[i].Path < endpoints[j].Path
		})
		result.Roots = append(result.Roots, RootImpact{
			Change:    change,
			Root:      root,
			Endpoints: endpoints,
		})
	}
	return result
}

func newTreeContext(store *facts.Store) *treeContext {
	context := &treeContext{
		store:       store,
		reverse:     graph.NewReverseGraph(store),
		routes:      graph.NewRouteGraph(store),
		symbols:     map[facts.SymbolID]facts.SymbolFact{},
		annotations: map[string]facts.AnnotationFact{},
	}
	for _, symbol := range store.Symbols {
		context.symbols[symbol.ID] = symbol
	}
	for _, annotation := range store.Annotations {
		context.annotations[annotation.ID] = annotation
	}
	return context
}

func newTreeBuilder(context *treeContext, change facts.ChangeFact) *treeBuilder {
	return &treeBuilder{
		treeContext: context,
		endpoints:   map[string]EndpointImpact{},
		change:      change,
	}
}

func (b *treeBuilder) buildRoot() Node {
	if route, ok := b.routes.RoutesByID[b.change.TargetID]; ok {
		return b.routeNode(route, 0, "")
	}
	if group, ok := b.routes.GroupsByID[b.change.TargetID]; ok {
		root := Node{
			ID:         group.ID,
			Kind:       "route_group",
			Name:       group.GroupVar,
			File:       group.Span.File,
			Relation:   "",
			Span:       group.Span,
			Confidence: b.change.Confidence,
			Level:      0,
			Children:   []Node{},
		}
		for _, route := range b.routes.RoutesForGroup(group.ID) {
			root.Children = append(root.Children, b.routeNode(route, 1, "route_group_contains"))
		}
		root.Children = mergeAndSortChildren(root.Children)
		return root
	}
	if middleware, ok := b.routes.MiddlewareByID[b.change.TargetID]; ok {
		root := b.middlewareNode(middleware, 0, "")
		root.Confidence = b.change.Confidence
		return root
	}
	if annotation, ok := b.annotations[b.change.TargetID]; ok {
		return b.annotationNode(annotation, facts.RouteRegistrationFact{}, 0, "")
	}
	if b.change.SymbolID != "" {
		root := b.symbolNode(b.change.SymbolID, 0)
		root.Confidence = b.change.Confidence
		path := map[facts.SymbolID]bool{b.change.SymbolID: true}
		b.expandSymbol(&root, path)
		return root
	}
	return Node{
		ID:         b.change.File,
		Kind:       "file",
		Name:       b.change.File,
		File:       b.change.File,
		Confidence: b.change.Confidence,
		Level:      0,
		Children:   []Node{},
	}
}

func (b *treeBuilder) expandSymbol(node *Node, path map[facts.SymbolID]bool) {
	symbolID := facts.SymbolID(node.ID)
	references := b.reverse.ReferencesTo(symbolID)
	routes := b.routes.RoutesForHandler(symbolID)
	dependencyRoutes := b.routes.RoutesForDependency(symbolID)
	middlewareBindings := b.middlewareBindingsForSymbol(symbolID)
	for _, ref := range references {
		child := b.symbolNode(ref.FromSymbol, node.Level+1)
		child.Relation = referenceRelation(ref.Kind)
		child.Raw = ref.ToRaw
		child.Span = ref.Span
		child.File = b.symbolFile(ref.FromSymbol, child.File)
		child.Confidence = ref.Confidence
		if path[ref.FromSymbol] {
			child.Cycle = true
		} else {
			nextPath := copySymbolPath(path)
			nextPath[ref.FromSymbol] = true
			b.expandSymbol(&child, nextPath)
		}
		node.Children = append(node.Children, child)
	}
	for _, route := range routes {
		node.Children = append(node.Children, b.routeNode(route, node.Level+1, "registered_handler"))
	}
	for _, route := range dependencyRoutes {
		node.Children = append(node.Children, b.routeNode(route, node.Level+1, "route_dependency"))
	}
	for _, middleware := range middlewareBindings {
		node.Children = append(node.Children, b.middlewareNode(middleware, node.Level+1, "middleware_symbol"))
	}
	node.Children = mergeAndSortChildren(node.Children)
}

func (b *treeBuilder) middlewareBindingsForSymbol(symbolID facts.SymbolID) []facts.MiddlewareBindingFact {
	var out []facts.MiddlewareBindingFact
	for _, binding := range b.store.Middleware {
		for _, candidate := range binding.MiddlewareSymbols {
			if candidate == symbolID {
				out = append(out, binding)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Span.File != out[j].Span.File {
			return out[i].Span.File < out[j].Span.File
		}
		if out[i].StatementIndex != out[j].StatementIndex {
			return out[i].StatementIndex < out[j].StatementIndex
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (b *treeBuilder) symbolNode(id facts.SymbolID, level int) Node {
	symbol, ok := b.symbols[id]
	if !ok {
		return Node{
			ID:       string(id),
			Kind:     symbolKindFromID(id),
			Name:     symbolNameFromID(id),
			Level:    level,
			Children: []Node{},
		}
	}
	return Node{
		ID:       string(symbol.ID),
		Kind:     symbol.Kind,
		Name:     symbol.Name,
		File:     symbol.Span.File,
		Package:  symbol.PackagePath,
		Span:     symbol.Span,
		Level:    level,
		Children: []Node{},
	}
}

func (b *treeBuilder) symbolFile(id facts.SymbolID, fallback string) string {
	if symbol, ok := b.symbols[id]; ok {
		return symbol.Span.File
	}
	return fallback
}

func (b *treeBuilder) routeNode(route facts.RouteRegistrationFact, level int, relation string) Node {
	path := route.ResolvedPath
	if path == "" {
		path = route.LocalPath
	}
	node := Node{
		ID:         route.ID,
		Kind:       "route",
		Name:       strings.TrimSpace(route.Method + " " + path),
		File:       route.Span.File,
		Relation:   relation,
		Raw:        route.HandlerRaw,
		Span:       route.Span,
		Confidence: facts.ConfidenceHigh,
		Level:      level,
		Method:     route.Method,
		Path:       path,
		Children:   []Node{},
	}
	annotations := b.routes.AnnotationsForHandler(route.HandlerSymbol)
	if len(annotations) == 0 {
		if route.Method != "" && path != "" {
			relation := "route_endpoint"
			if route.RecoveredFromDiff {
				relation = "deleted_route_endpoint"
			}
			node.Children = append(node.Children, b.endpointNode(
				route.Method,
				path,
				"",
				route.HandlerSymbol,
				route.Span,
				level+1,
				relation,
				facts.ConfidenceMedium,
			))
		}
		return node
	}
	for _, annotation := range annotations {
		node.Children = append(node.Children, b.annotationNode(annotation, route, level+1, "handler_annotation"))
	}
	node.Children = mergeAndSortChildren(node.Children)
	return node
}

func (b *treeBuilder) middlewareNode(middleware facts.MiddlewareBindingFact, level int, relation string) Node {
	node := Node{
		ID:         middleware.ID,
		Kind:       "middleware",
		Name:       middleware.MiddlewareRaw,
		File:       middleware.Span.File,
		Relation:   relation,
		Raw:        middleware.MiddlewareRaw,
		Span:       middleware.Span,
		Confidence: facts.ConfidenceHigh,
		Level:      level,
		Children:   []Node{},
	}
	routes := b.routes.RoutesAffectedByMiddleware(middleware.ID)
	for _, route := range routes {
		node.Children = append(node.Children, b.routeNode(route, level+1, "middleware_applies_to"))
	}
	node.Children = mergeAndSortChildren(node.Children)
	return node
}

func (b *treeBuilder) annotationNode(annotation facts.AnnotationFact, route facts.RouteRegistrationFact, level int, relation string) Node {
	routePath := route.ResolvedPath
	routePathAuthoritative := routePath != "" && (routePath != route.LocalPath || isLegacyPathGroup(route.GroupVar))
	if route.RecoveredFromDiff && route.LocalPath != "" {
		routePath = route.LocalPath
		routePathAuthoritative = true
	}
	if annotation.Path == "" && routePath == "" {
		routePath = route.LocalPath
		routePathAuthoritative = routePath != ""
	}
	if routePathAuthoritative && annotationExtendsRoutePath(annotation.Path, routePath) && !isLegacyPathGroup(route.GroupVar) {
		routePathAuthoritative = false
	}
	method := annotation.Method
	path := annotation.Path
	endpointRelation := "annotation_endpoint"
	endpointSpan := annotation.Span
	endpointConfidence := facts.ConfidenceHigh
	if routePathAuthoritative {
		path = routePath
		if route.Method != "" {
			method = route.Method
		}
		endpointRelation = "route_endpoint"
		endpointSpan = route.Span
		if route.RecoveredFromDiff {
			endpointRelation = "deleted_route_endpoint"
		}
	}
	if method == "" {
		method = route.Method
	}
	if path == "" {
		path = route.LocalPath
	}
	node := Node{
		ID:         annotation.ID,
		Kind:       "annotation",
		Name:       strings.TrimSpace(method + " " + path),
		File:       annotation.Span.File,
		Relation:   relation,
		Raw:        annotation.Raw,
		Span:       annotation.Span,
		Confidence: facts.ConfidenceHigh,
		Level:      level,
		Method:     method,
		Path:       path,
		Children:   []Node{},
	}
	if method != "" && path != "" {
		node.Children = append(node.Children, b.endpointNode(
			method,
			path,
			annotation.ID,
			annotation.HandlerSymbol,
			endpointSpan,
			level+1,
			endpointRelation,
			endpointConfidence,
		))
	}
	return node
}

func isLegacyPathGroup(groupVar string) bool {
	return strings.Contains(strings.ToLower(groupVar), "oldpath")
}

func annotationExtendsRoutePath(annotationPath, routePath string) bool {
	return annotationPath != "" &&
		routePath != "" &&
		annotationPath != routePath &&
		routePathSegmentCount(routePath) >= 2 &&
		strings.HasSuffix(annotationPath, routePath)
}

func routePathSegmentCount(routePath string) int {
	trimmed := strings.Trim(routePath, "/")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "/"))
}

func (b *treeBuilder) endpointNode(
	method, path, annotationID string,
	handler facts.SymbolID,
	span facts.SourceSpan,
	level int,
	relation string,
	confidence facts.Confidence,
) Node {
	id := fmt.Sprintf("endpoint:%s:%s", method, path)
	key := method + "\x00" + path
	b.endpoints[key] = EndpointImpact{
		ID:            id,
		Method:        method,
		Path:          path,
		AnnotationID:  annotationID,
		HandlerSymbol: handler,
	}
	return Node{
		ID:         id,
		Kind:       "endpoint",
		Name:       method + " " + path,
		File:       span.File,
		Relation:   relation,
		Span:       span,
		Confidence: confidence,
		Level:      level,
		Method:     method,
		Path:       path,
		Children:   []Node{},
	}
}

func referenceRelation(kind facts.ReferenceKind) string {
	switch kind {
	case facts.ReferenceKindType:
		return "type_ref"
	case facts.ReferenceKindSelector:
		return "selector_ref"
	case facts.ReferenceKindValue:
		return "value_ref"
	default:
		return "call"
	}
}

func copySymbolPath(path map[facts.SymbolID]bool) map[facts.SymbolID]bool {
	out := make(map[facts.SymbolID]bool, len(path)+1)
	for id, present := range path {
		out[id] = present
	}
	return out
}

func mergeAndSortChildren(children []Node) []Node {
	merged := make([]Node, 0, len(children))
	byKey := map[string]int{}
	for _, child := range children {
		key := child.ID + "\x00" + child.Relation
		if index, ok := byKey[key]; ok {
			merged[index].Children = mergeAndSortChildren(append(merged[index].Children, child.Children...))
			merged[index].Cycle = merged[index].Cycle || child.Cycle
			continue
		}
		byKey[key] = len(merged)
		merged = append(merged, child)
	}
	sort.Slice(merged, func(i, j int) bool {
		left, right := merged[i], merged[j]
		if left.Level != right.Level {
			return left.Level < right.Level
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Relation < right.Relation
	})
	return merged
}

func symbolKindFromID(id facts.SymbolID) string {
	raw := string(id)
	if index := strings.Index(raw, ":"); index > 0 {
		return raw[:index]
	}
	return "symbol"
}

func symbolNameFromID(id facts.SymbolID) string {
	raw := string(id)
	if index := strings.LastIndex(raw, ":"); index >= 0 && index+1 < len(raw) {
		return raw[index+1:]
	}
	return raw
}
