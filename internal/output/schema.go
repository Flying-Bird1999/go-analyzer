package output

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type Document struct {
	Project       facts.ProjectFact             `json:"project"`
	Symbols       []facts.SymbolFact            `json:"symbols"`
	Annotations   []facts.AnnotationFact        `json:"annotations"`
	RouteGroups   []facts.RouteGroupFact        `json:"route_groups"`
	Routes        []facts.RouteRegistrationFact `json:"routes"`
	Middleware    []facts.MiddlewareBindingFact `json:"middleware"`
	Changes       []facts.ChangeFact            `json:"changes"`
	References    []facts.ReferenceFact         `json:"references"`
	Modules       []facts.ModuleDependencyFact  `json:"modules"`
	ModuleChanges []facts.ModuleChangeFact      `json:"module_changes"`
	ModuleUsages  []facts.ModuleUsageFact       `json:"module_usages"`
	Links         []facts.LinkFact              `json:"links"`
	Diagnostics   []facts.DiagnosticFact        `json:"diagnostics"`
}
