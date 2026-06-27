package facts

type WrapperFact struct {
	Name string `json:"name"`
	Raw  string `json:"raw"`
}

type RouteGroupFact struct {
	ID             string     `json:"id"`
	GroupVar       string     `json:"group_var"`
	ParentGroupID  string     `json:"parent_group_id,omitempty"`
	ParentGroupVar string     `json:"parent_group_var,omitempty"`
	Prefix         string     `json:"prefix"`
	RouteFunc      SymbolID   `json:"route_func"`
	StatementIndex int        `json:"statement_index"`
	Span           SourceSpan `json:"span"`
}

type RouteRegistrationFact struct {
	ID             string        `json:"id"`
	Method         string        `json:"method"`
	LocalPath      string        `json:"local_path"`
	PathRaw        string        `json:"path_raw,omitempty"`
	ResolvedPath   string        `json:"resolved_path,omitempty"`
	GroupID        string        `json:"group_id"`
	GroupVar       string        `json:"group_var"`
	HandlerRaw     string        `json:"handler_raw"`
	HandlerSymbol  SymbolID      `json:"handler_symbol,omitempty"`
	Wrappers       []WrapperFact `json:"wrappers,omitempty"`
	RouteFunc      SymbolID      `json:"route_func"`
	StatementIndex int           `json:"statement_index"`
	SourceFamily   string        `json:"source_family,omitempty"`
	File           string        `json:"file"`
	Span           SourceSpan    `json:"span"`
}

type MiddlewareBindingFact struct {
	ID                string     `json:"id"`
	GroupID           string     `json:"group_id"`
	GroupVar          string     `json:"group_var"`
	MiddlewareRaw     string     `json:"middleware_raw"`
	MiddlewareSymbols []SymbolID `json:"middleware_symbols,omitempty"`
	RouteFunc         SymbolID   `json:"route_func"`
	StatementIndex    int        `json:"statement_index"`
	Span              SourceSpan `json:"span"`
}
