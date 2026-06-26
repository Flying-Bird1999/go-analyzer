package facts

type AnnotationFact struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Method        string     `json:"method"`
	Path          string     `json:"path"`
	Raw           string     `json:"raw"`
	HandlerSymbol SymbolID   `json:"handler_symbol"`
	Span          SourceSpan `json:"span"`
}
