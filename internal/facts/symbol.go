package facts

type SymbolFact struct {
	ID          SymbolID   `json:"id"`
	Kind        string     `json:"kind"`
	PackagePath string     `json:"package_path"`
	Receiver    string     `json:"receiver,omitempty"`
	Name        string     `json:"name"`
	Span        SourceSpan `json:"span"`
}
