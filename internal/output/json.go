package output

import (
	"encoding/json"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func RenderJSON(store *facts.Store) ([]byte, error) {
	doc := Document{
		Project:     store.Project,
		Symbols:     append([]facts.SymbolFact(nil), store.Symbols...),
		Annotations: append([]facts.AnnotationFact(nil), store.Annotations...),
		RouteGroups: append([]facts.RouteGroupFact(nil), store.RouteGroups...),
		Routes:      append([]facts.RouteRegistrationFact(nil), store.Routes...),
		Middleware:  append([]facts.MiddlewareBindingFact(nil), store.Middleware...),
		References:  append([]facts.ReferenceFact(nil), store.References...),
		Modules:     append([]facts.ModuleDependencyFact(nil), store.Modules...),
		Links:       append([]facts.LinkFact(nil), store.Links...),
		Diagnostics: append([]facts.DiagnosticFact(nil), store.Diagnostics...),
	}
	sort.Slice(doc.Symbols, func(i, j int) bool {
		return doc.Symbols[i].ID < doc.Symbols[j].ID
	})
	sort.Slice(doc.Annotations, func(i, j int) bool {
		return doc.Annotations[i].ID < doc.Annotations[j].ID
	})
	sort.Slice(doc.RouteGroups, func(i, j int) bool {
		return doc.RouteGroups[i].ID < doc.RouteGroups[j].ID
	})
	sort.Slice(doc.Routes, func(i, j int) bool {
		return doc.Routes[i].ID < doc.Routes[j].ID
	})
	sort.Slice(doc.Middleware, func(i, j int) bool {
		return doc.Middleware[i].ID < doc.Middleware[j].ID
	})
	sort.Slice(doc.References, func(i, j int) bool {
		return doc.References[i].ID < doc.References[j].ID
	})
	sort.Slice(doc.Modules, func(i, j int) bool {
		return doc.Modules[i].ID < doc.Modules[j].ID
	})
	sort.Slice(doc.Links, func(i, j int) bool {
		return doc.Links[i].ID < doc.Links[j].ID
	})
	sort.Slice(doc.Diagnostics, func(i, j int) bool {
		return doc.Diagnostics[i].ID < doc.Diagnostics[j].ID
	})
	ensureNonNilSlices(&doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func ensureNonNilSlices(doc *Document) {
	if doc.Symbols == nil {
		doc.Symbols = []facts.SymbolFact{}
	}
	if doc.Annotations == nil {
		doc.Annotations = []facts.AnnotationFact{}
	}
	if doc.RouteGroups == nil {
		doc.RouteGroups = []facts.RouteGroupFact{}
	}
	if doc.Routes == nil {
		doc.Routes = []facts.RouteRegistrationFact{}
	}
	if doc.Middleware == nil {
		doc.Middleware = []facts.MiddlewareBindingFact{}
	}
	if doc.References == nil {
		doc.References = []facts.ReferenceFact{}
	}
	if doc.Modules == nil {
		doc.Modules = []facts.ModuleDependencyFact{}
	}
	if doc.Links == nil {
		doc.Links = []facts.LinkFact{}
	}
	if doc.Diagnostics == nil {
		doc.Diagnostics = []facts.DiagnosticFact{}
	}
}
