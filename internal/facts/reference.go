package facts

type ReferenceKind string

const (
	ReferenceKindCall  ReferenceKind = "call"
	ReferenceKindType  ReferenceKind = "type"
	ReferenceKindValue ReferenceKind = "value"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

type ReferenceFact struct {
	ID         string        `json:"id"`
	Kind       ReferenceKind `json:"kind"`
	FromSymbol SymbolID      `json:"from_symbol"`
	ToSymbol   SymbolID      `json:"to_symbol,omitempty"`
	ToRaw      string        `json:"to_raw,omitempty"`
	Confidence Confidence    `json:"confidence"`
	Span       SourceSpan    `json:"span"`
}
