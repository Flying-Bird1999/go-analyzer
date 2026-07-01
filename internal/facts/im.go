package facts

type IMEventRelation string

const (
	IMRelationPayload    IMEventRelation = "im_payload"
	IMRelationEventValue IMEventRelation = "im_event_value"
	IMRelationControl    IMEventRelation = "im_control"
)

type IMEventDependency struct {
	SymbolID   SymbolID        `json:"symbol_id"`
	Relation   IMEventRelation `json:"relation"`
	Confidence Confidence      `json:"confidence"`
	Span       SourceSpan      `json:"span,omitempty"`
}

type IMEventEvidence struct {
	Relation IMEventRelation `json:"relation"`
	Span     SourceSpan      `json:"span"`
}

type IMEventFact struct {
	ID           string              `json:"id"`
	Event        string              `json:"event,omitempty"`
	EventRaw     string              `json:"event_raw,omitempty"`
	SenderSymbol SymbolID            `json:"sender_symbol"`
	Dependencies []IMEventDependency `json:"dependencies"`
	Evidence     []IMEventEvidence   `json:"evidence"`
	Confidence   Confidence          `json:"confidence"`
	Span         SourceSpan          `json:"span"`
	Resolved     bool                `json:"resolved"`
}
