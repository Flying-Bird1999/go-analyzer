// Package endpoint centralizes the HTTP endpoint identity rules shared by
// impact propagation and dependency queries.
package endpoint

import (
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

// Key is the canonical HTTP endpoint identity.
type Key struct {
	Method string
	Path   string
}

// Route is one statically resolved registration candidate for an endpoint.
type Route struct {
	Method string
	Path   string
}

// Resolution records why a handler, route or annotation resolves to an
// endpoint. AnnotationID is empty only for route fallback, i.e. handlers that
// carry no annotation at all.
type Resolution struct {
	Endpoint     Key
	AnnotationID string
	Handler      facts.SymbolID
	Routes       []Route
}

// Entry is the merged catalog record for one canonical endpoint.
type Entry struct {
	Endpoint Key
	Routes   []Route
	Handlers []facts.SymbolID
}

// Catalog is immutable after construction. Every returned slice is a copy.
type Catalog struct {
	entries      map[Key]Entry
	byHandler    map[facts.SymbolID][]Resolution
	byRoute      map[string][]Resolution
	byAnnotation map[string][]Resolution
}

// Build derives a catalog from a frozen fact snapshot.
func Build(snapshot facts.Snapshot) *Catalog {
	routes := graph.NewRouteGraph(snapshot)
	catalog := &Catalog{
		entries:      map[Key]Entry{},
		byHandler:    map[facts.SymbolID][]Resolution{},
		byRoute:      map[string][]Resolution{},
		byAnnotation: map[string][]Resolution{},
	}

	handlers := make([]facts.SymbolID, 0, len(routes.RoutesByHandler)+len(routes.AnnotationsByHandler))
	seenHandlers := map[facts.SymbolID]bool{}
	for handler := range routes.RoutesByHandler {
		seenHandlers[handler] = true
		handlers = append(handlers, handler)
	}
	for handler := range routes.AnnotationsByHandler {
		if !seenHandlers[handler] {
			handlers = append(handlers, handler)
		}
	}
	sort.Slice(handlers, func(i, j int) bool { return handlers[i] < handlers[j] })

	for _, handler := range handlers {
		registered := routes.RoutesForHandler(handler)
		annotations := routes.AnnotationsForHandler(handler)
		candidates := resolvedRoutes(registered)
		if len(registered) == 0 {
			for _, annotation := range annotations {
				resolution, ok := annotationResolution(annotation, facts.RouteRegistrationFact{}, candidates)
				if ok {
					catalog.addResolution(resolution, "", annotation.ID)
				}
			}
			continue
		}
		for _, route := range registered {
			// 接口身份以接口注释为准：handler 上但凡有一条注释，注释就是它唯一的对外身份
			// 来源，注释没覆盖到的路由不自成接口，只作为注册证据进入 Routes。
			// 只有完全没有注释时才按路由兜底。
			if len(annotations) == 0 {
				if resolution, ok := routeResolution(route, candidates); ok {
					catalog.addResolution(resolution, route.ID, "")
				}
				continue
			}
			for _, annotation := range annotations {
				resolution, ok := annotationResolution(annotation, route, candidates)
				if ok {
					catalog.addResolution(resolution, route.ID, annotation.ID)
				}
			}
		}
	}
	routeIDs := make([]string, 0, len(routes.RoutesByID))
	for routeID := range routes.RoutesByID {
		routeIDs = append(routeIDs, routeID)
	}
	sort.Strings(routeIDs)
	for _, routeID := range routeIDs {
		if len(catalog.byRoute[routeID]) > 0 {
			continue
		}
		route := routes.RoutesByID[routeID]
		if resolution, ok := routeResolution(route, resolvedRoutes([]facts.RouteRegistrationFact{route})); ok {
			catalog.addResolution(resolution, route.ID, "")
		}
	}
	catalog.normalize()
	return catalog
}

// Entries returns all endpoints in stable method/path order.
func (c *Catalog) Entries() []Entry {
	if c == nil {
		return []Entry{}
	}
	out := make([]Entry, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, cloneEntry(entry))
	}
	sort.Slice(out, func(i, j int) bool { return lessKey(out[i].Endpoint, out[j].Endpoint) })
	return out
}

// Lookup returns one endpoint entry.
//
// 查询 key 先按接口身份匹配；匹配不到时按注册证据回退，返回该路由所属的正式接口。
// 回退存在的原因：接口身份以注释为准，注释没覆盖到的注册路径不是接口身份，但调用方
// 手上往往只有真实 URL（取自前端代码或网关日志），仍需要查得到它属于哪个接口。
func (c *Catalog) Lookup(key Key) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	normalized := normalizeKey(key)
	if entry, ok := c.entries[normalized]; ok {
		return cloneEntry(entry), true
	}
	// 按 Entries() 的稳定顺序遍历，保证同一条路由被多个接口列为证据时回退结果确定。
	for _, entry := range c.Entries() {
		for _, route := range entry.Routes {
			if Key(route) == normalized {
				return entry, true
			}
		}
	}
	return Entry{}, false
}

// ForHandler returns all endpoint resolutions for a handler.
func (c *Catalog) ForHandler(handler facts.SymbolID) []Resolution {
	return cloneResolutions(c.byHandler[handler])
}

// ForRoute returns endpoint resolutions carried by one route registration.
func (c *Catalog) ForRoute(routeID string) []Resolution {
	return cloneResolutions(c.byRoute[routeID])
}

// ForAnnotation returns endpoint resolutions carried by one annotation.
func (c *Catalog) ForAnnotation(annotationID string) []Resolution {
	return cloneResolutions(c.byAnnotation[annotationID])
}

func (c *Catalog) addResolution(resolution Resolution, routeID, annotationID string) {
	resolution.Endpoint = normalizeKey(resolution.Endpoint)
	if resolution.Endpoint.Method == "" || resolution.Endpoint.Path == "" {
		return
	}
	resolution.Routes = uniqueRoutes(resolution.Routes)
	c.byHandler[resolution.Handler] = appendResolution(c.byHandler[resolution.Handler], resolution)
	if routeID != "" {
		c.byRoute[routeID] = appendResolution(c.byRoute[routeID], resolution)
	}
	if annotationID != "" {
		c.byAnnotation[annotationID] = appendResolution(c.byAnnotation[annotationID], resolution)
	}
	entry := c.entries[resolution.Endpoint]
	entry.Endpoint = resolution.Endpoint
	entry.Routes = uniqueRoutes(append(entry.Routes, resolution.Routes...))
	entry.Handlers = appendSymbol(entry.Handlers, resolution.Handler)
	c.entries[resolution.Endpoint] = entry
}

func (c *Catalog) normalize() {
	for handler, values := range c.byHandler {
		c.byHandler[handler] = sortedResolutions(values)
	}
	for routeID, values := range c.byRoute {
		c.byRoute[routeID] = sortedResolutions(values)
	}
	for annotationID, values := range c.byAnnotation {
		c.byAnnotation[annotationID] = sortedResolutions(values)
	}
	for key, entry := range c.entries {
		entry.Routes = uniqueRoutes(entry.Routes)
		sort.Slice(entry.Handlers, func(i, j int) bool { return entry.Handlers[i] < entry.Handlers[j] })
		c.entries[key] = entry
	}
}

func routeResolution(route facts.RouteRegistrationFact, candidates []Route) (Resolution, bool) {
	path := route.ResolvedPath
	if path == "" {
		path = route.LocalPath
	}
	key := normalizeKey(Key{Method: route.Method, Path: path})
	return Resolution{Endpoint: key, Handler: route.HandlerSymbol, Routes: candidates}, key.Method != "" && key.Path != ""
}

func annotationResolution(annotation facts.AnnotationFact, route facts.RouteRegistrationFact, candidates []Route) (Resolution, bool) {
	method := annotation.Method
	path := annotation.Path
	if method == "" {
		method = route.Method
	}
	if path == "" {
		path = route.ResolvedPath
		if path == "" {
			path = route.LocalPath
		}
	}
	key := normalizeKey(Key{Method: method, Path: path})
	return Resolution{Endpoint: key, AnnotationID: annotation.ID, Handler: annotation.HandlerSymbol, Routes: candidates}, key.Method != "" && key.Path != ""
}

func resolvedRoutes(routes []facts.RouteRegistrationFact) []Route {
	out := make([]Route, 0, len(routes))
	for _, route := range routes {
		path := route.ResolvedPath
		if path == "" {
			path = route.LocalPath
		}
		key := normalizeKey(Key{Method: route.Method, Path: path})
		if key.Method != "" && key.Path != "" {
			out = append(out, Route(key))
		}
	}
	return uniqueRoutes(out)
}

func normalizeKey(key Key) Key {
	key.Method = strings.ToUpper(strings.TrimSpace(key.Method))
	key.Path = strings.TrimSpace(key.Path)
	return key
}

func lessKey(left, right Key) bool {
	if left.Method != right.Method {
		return left.Method < right.Method
	}
	return left.Path < right.Path
}

func appendResolution(values []Resolution, value Resolution) []Resolution {
	for i, existing := range values {
		if existing.Endpoint == value.Endpoint && existing.AnnotationID == value.AnnotationID && existing.Handler == value.Handler {
			values[i].Routes = uniqueRoutes(append(values[i].Routes, value.Routes...))
			return values
		}
	}
	return append(values, value)
}

func sortedResolutions(values []Resolution) []Resolution {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Endpoint != values[j].Endpoint {
			return lessKey(values[i].Endpoint, values[j].Endpoint)
		}
		if values[i].AnnotationID != values[j].AnnotationID {
			return values[i].AnnotationID < values[j].AnnotationID
		}
		return values[i].Handler < values[j].Handler
	})
	return values
}

func appendSymbol(values []facts.SymbolID, value facts.SymbolID) []facts.SymbolID {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueRoutes(values []Route) []Route {
	for i := range values {
		values[i].Method = strings.ToUpper(values[i].Method)
	}
	sort.Slice(values, func(i, j int) bool { return lessKey(Key(values[i]), Key(values[j])) })
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	if out == nil {
		return []Route{}
	}
	return out
}

func cloneResolutions(values []Resolution) []Resolution {
	out := append([]Resolution(nil), values...)
	for i := range out {
		out[i].Routes = append([]Route(nil), out[i].Routes...)
	}
	if out == nil {
		return []Resolution{}
	}
	return out
}

func cloneEntry(entry Entry) Entry {
	entry.Routes = append([]Route(nil), entry.Routes...)
	entry.Handlers = append([]facts.SymbolID(nil), entry.Handlers...)
	return entry
}
