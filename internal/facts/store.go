package facts

type ProjectFact struct {
	Root         string           `json:"root"`
	ModulePath   string           `json:"module_path"`
	BuildContext BuildContextFact `json:"build_context"`
}

type BuildContextFact struct {
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	Tags       []string `json:"tags"`
	CgoEnabled bool     `json:"cgo_enabled"`
}

type DiagnosticFact struct {
	ID             string     `json:"id"`
	Code           string     `json:"code"`
	Severity       string     `json:"severity"`
	Message        string     `json:"message"`
	Span           SourceSpan `json:"span,omitempty"`
	RelatedFactIDs []string   `json:"related_fact_ids,omitempty"`
}

type Store struct {
	Project         ProjectFact             `json:"project"`
	Symbols         []SymbolFact            `json:"symbols"`
	Annotations     []AnnotationFact        `json:"annotations"`
	RouteGroups     []RouteGroupFact        `json:"route_groups"`
	RouteGroupFlows []RouteGroupFlowFact    `json:"-"`
	Routes          []RouteRegistrationFact `json:"routes"`
	Middleware      []MiddlewareBindingFact `json:"middleware"`
	Changes         []ChangeFact            `json:"changes"`
	References      []ReferenceFact         `json:"references"`
	Modules         []ModuleDependencyFact  `json:"modules"`
	ModuleChanges   []ModuleChangeFact      `json:"module_changes"`
	ModuleUsages    []ModuleUsageFact       `json:"module_usages"`
	IMEvents        []IMEventFact           `json:"im_events"`
	Links           []LinkFact              `json:"links"`
	Diagnostics     []DiagnosticFact        `json:"diagnostics"`
}

func NewStore(root, modulePath string, buildContext ...BuildContextFact) *Store {
	effectiveBuildContext := BuildContextFact{Tags: []string{}}
	if len(buildContext) > 0 {
		effectiveBuildContext = buildContext[0]
		if effectiveBuildContext.Tags == nil {
			effectiveBuildContext.Tags = []string{}
		}
	}
	return &Store{
		Project: ProjectFact{
			Root:         root,
			ModulePath:   modulePath,
			BuildContext: effectiveBuildContext,
		},
		Symbols:         []SymbolFact{},
		Annotations:     []AnnotationFact{},
		RouteGroups:     []RouteGroupFact{},
		RouteGroupFlows: []RouteGroupFlowFact{},
		Routes:          []RouteRegistrationFact{},
		Middleware:      []MiddlewareBindingFact{},
		Changes:         []ChangeFact{},
		References:      []ReferenceFact{},
		Modules:         []ModuleDependencyFact{},
		ModuleChanges:   []ModuleChangeFact{},
		ModuleUsages:    []ModuleUsageFact{},
		IMEvents:        []IMEventFact{},
		Links:           []LinkFact{},
		Diagnostics:     []DiagnosticFact{},
	}
}

func (s *Store) AddSymbol(symbol SymbolFact) {
	s.Symbols = append(s.Symbols, symbol)
}
