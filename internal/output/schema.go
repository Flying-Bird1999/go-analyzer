package output

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type Document struct {
	Project     facts.ProjectFact             `json:"project"`
	Symbols     []facts.SymbolFact            `json:"symbols"`
	Annotations []facts.AnnotationFact        `json:"annotations"`
	RouteGroups []facts.RouteGroupFact        `json:"route_groups"`
	Routes      []facts.RouteRegistrationFact `json:"routes"`
	Middleware  []facts.MiddlewareBindingFact `json:"middleware"`
	References  []facts.ReferenceFact         `json:"references"`
	Modules     []facts.ModuleDependencyFact  `json:"modules"`
	Links       []facts.LinkFact              `json:"links"`
	Diagnostics []facts.DiagnosticFact        `json:"diagnostics"`
}
