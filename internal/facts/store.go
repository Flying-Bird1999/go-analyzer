package facts

type ProjectFact struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
}

type ModuleFact struct{}
type DiagnosticFact struct{}

type Store struct {
	Project     ProjectFact             `json:"project"`
	Symbols     []SymbolFact            `json:"symbols"`
	Annotations []AnnotationFact        `json:"annotations"`
	RouteGroups []RouteGroupFact        `json:"route_groups"`
	Routes      []RouteRegistrationFact `json:"routes"`
	Middleware  []MiddlewareBindingFact `json:"middleware"`
	References  []ReferenceFact         `json:"references"`
	Modules     []ModuleFact            `json:"modules"`
	Links       []LinkFact              `json:"links"`
	Diagnostics []DiagnosticFact        `json:"diagnostics"`
}

func NewStore(root, modulePath string) *Store {
	return &Store{
		Project: ProjectFact{
			Root:       root,
			ModulePath: modulePath,
		},
		Symbols:     []SymbolFact{},
		Annotations: []AnnotationFact{},
		RouteGroups: []RouteGroupFact{},
		Routes:      []RouteRegistrationFact{},
		Middleware:  []MiddlewareBindingFact{},
		References:  []ReferenceFact{},
		Modules:     []ModuleFact{},
		Links:       []LinkFact{},
		Diagnostics: []DiagnosticFact{},
	}
}

func (s *Store) AddSymbol(symbol SymbolFact) {
	s.Symbols = append(s.Symbols, symbol)
}
