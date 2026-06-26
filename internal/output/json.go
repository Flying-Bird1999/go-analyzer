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
		Modules:     append([]facts.ModuleFact(nil), store.Modules...),
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
	sort.Slice(doc.Links, func(i, j int) bool {
		return doc.Links[i].ID < doc.Links[j].ID
	})
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
