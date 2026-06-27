package impact

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

type treeBuilder struct {
	store       *facts.Store
	reverse     *graph.ReverseGraph
	routes      *graph.RouteGraph
	symbols     map[facts.SymbolID]facts.SymbolFact
	annotations map[string]facts.AnnotationFact
	endpoints   map[string]EndpointImpact
	change      facts.ChangeFact
	opts        TreeOptions
	diagnostics []facts.DiagnosticFact
}

func AnalyzeTrees(store *facts.Store, opts TreeOptions) TreeResult {
	result := TreeResult{
		Roots:       []RootImpact{},
		Diagnostics: append([]facts.DiagnosticFact(nil), store.Diagnostics...),
	}
	changes := append([]facts.ChangeFact(nil), store.Changes...)
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].ID < changes[j].ID
	})
	for _, change := range changes {
		builder := newTreeBuilder(store, change, opts)
		root := builder.buildRoot()
		result.Diagnostics = append(result.Diagnostics, builder.diagnostics...)
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
	result.Diagnostics = dedupeTreeDiagnostics(result.Diagnostics)
	return result
}

func newTreeBuilder(store *facts.Store, change facts.ChangeFact, opts TreeOptions) *treeBuilder {
	builder := &treeBuilder{
		store:       store,
		reverse:     graph.NewReverseGraph(store),
		routes:      graph.NewRouteGraph(store),
		symbols:     map[facts.SymbolID]facts.SymbolFact{},
		annotations: map[string]facts.AnnotationFact{},
		endpoints:   map[string]EndpointImpact{},
		change:      change,
		opts:        opts,
		diagnostics: []facts.DiagnosticFact{},
	}
	for _, symbol := range store.Symbols {
		builder.symbols[symbol.ID] = symbol
	}
	for _, annotation := range store.Annotations {
		builder.annotations[annotation.ID] = annotation
	}
	return builder
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
	middlewareBindings := b.middlewareBindingsForSymbol(symbolID)
	if b.applyStopBoundary(node) {
		return
	}
	if b.depthReached(node, len(references) > 0 || len(routes) > 0 || len(middlewareBindings) > 0) {
		return
	}
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
	if b.applyStopBoundary(&node) {
		return node
	}
	if b.depthReached(&node, len(annotations) > 0 || (route.Method != "" && path != "")) {
		return node
	}
	if len(annotations) == 0 {
		if route.Method != "" && path != "" {
			relation := "route_endpoint"
			if route.SourceFamily == "deleted_diff" {
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
	if b.applyStopBoundary(&node) {
		return node
	}
	routes := b.routes.RoutesAffectedByMiddleware(middleware.ID)
	if b.depthReached(&node, len(routes) > 0) {
		return node
	}
	for _, route := range routes {
		node.Children = append(node.Children, b.routeNode(route, level+1, "middleware_applies_to"))
	}
	node.Children = mergeAndSortChildren(node.Children)
	return node
}

func (b *treeBuilder) annotationNode(annotation facts.AnnotationFact, route facts.RouteRegistrationFact, level int, relation string) Node {
	method := annotation.Method
	if method == "" {
		method = route.Method
	}
	path := annotation.Path
	if path == "" {
		path = route.ResolvedPath
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
	if b.applyStopBoundary(&node) {
		return node
	}
	if b.depthReached(&node, method != "" && path != "") {
		return node
	}
	if method != "" && path != "" {
		node.Children = append(node.Children, b.endpointNode(
			method,
			path,
			annotation.ID,
			annotation.HandlerSymbol,
			annotation.Span,
			level+1,
			"annotation_endpoint",
			facts.ConfidenceHigh,
		))
	}
	return node
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

func (b *treeBuilder) applyStopBoundary(node *Node) bool {
	if node.File == "" {
		return false
	}
	for _, pattern := range b.opts.StopPropagation {
		if matchesStopPattern(node.File, pattern) {
			node.StopBoundary = true
			return true
		}
	}
	return false
}

func (b *treeBuilder) depthReached(node *Node, hasChildren bool) bool {
	if !hasChildren || b.opts.MaxDepth <= 0 || node.Level < b.opts.MaxDepth {
		return false
	}
	b.diagnostics = append(b.diagnostics, facts.DiagnosticFact{
		ID:       fmt.Sprintf("diagnostic:%s:%s:%s", diagnostics.CodePropagationDepthTruncated, b.change.ID, node.ID),
		Code:     string(diagnostics.CodePropagationDepthTruncated),
		Severity: string(diagnostics.SeverityWarning),
		Message:  fmt.Sprintf("impact propagation stopped at configured max depth %d", b.opts.MaxDepth),
		Span:     node.Span,
		RelatedFactIDs: []string{
			b.change.ID,
			node.ID,
		},
	})
	return true
}

func matchesStopPattern(file, pattern string) bool {
	file = strings.TrimPrefix(path.Clean(filepathSlash(file)), "./")
	pattern = strings.TrimPrefix(path.Clean(filepathSlash(pattern)), "./")
	if pattern == "." || pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return file == prefix || strings.HasPrefix(file, prefix+"/")
	}
	matched, err := path.Match(pattern, file)
	return err == nil && matched
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func dedupeTreeDiagnostics(items []facts.DiagnosticFact) []facts.DiagnosticFact {
	byID := map[string]facts.DiagnosticFact{}
	for _, item := range items {
		byID[item.ID] = item
	}
	out := make([]facts.DiagnosticFact, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}
