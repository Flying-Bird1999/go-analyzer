package facts

type ProjectFact struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
}

type DiagnosticFact struct{}

type Store struct {
	Project       ProjectFact             `json:"project"`
	Symbols       []SymbolFact            `json:"symbols"`
	Annotations   []AnnotationFact        `json:"annotations"`
	RouteGroups   []RouteGroupFact        `json:"route_groups"`
	Routes        []RouteRegistrationFact `json:"routes"`
	Middleware    []MiddlewareBindingFact `json:"middleware"`
	Changes       []ChangeFact            `json:"changes"`
	References    []ReferenceFact         `json:"references"`
	Modules       []ModuleDependencyFact  `json:"modules"`
	ModuleChanges []ModuleChangeFact      `json:"module_changes"`
	ModuleUsages  []ModuleUsageFact       `json:"module_usages"`
	Links         []LinkFact              `json:"links"`
	Diagnostics   []DiagnosticFact        `json:"diagnostics"`
}

func NewStore(root, modulePath string) *Store {
	return &Store{
		Project: ProjectFact{
			Root:       root,
			ModulePath: modulePath,
		},
		Symbols:       []SymbolFact{},
		Annotations:   []AnnotationFact{},
		RouteGroups:   []RouteGroupFact{},
		Routes:        []RouteRegistrationFact{},
		Middleware:    []MiddlewareBindingFact{},
		Changes:       []ChangeFact{},
		References:    []ReferenceFact{},
		Modules:       []ModuleDependencyFact{},
		ModuleChanges: []ModuleChangeFact{},
		ModuleUsages:  []ModuleUsageFact{},
		Links:         []LinkFact{},
		Diagnostics:   []DiagnosticFact{},
	}
}

func (s *Store) AddSymbol(symbol SymbolFact) {
	s.Symbols = append(s.Symbols, symbol)
}
