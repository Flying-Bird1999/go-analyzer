package facts

type ChangeKind string

const (
	ChangeKindSymbolChanged     ChangeKind = "symbol_changed"
	ChangeKindRouteGroupChanged ChangeKind = "route_group_changed"
	ChangeKindRouteChanged      ChangeKind = "route_changed"
	ChangeKindRouteDeleted      ChangeKind = "route_deleted"
	ChangeKindMiddlewareChanged ChangeKind = "middleware_changed"
	ChangeKindAnnotationChanged ChangeKind = "annotation_changed"
	ChangeKindFileChanged       ChangeKind = "file_changed"
)

type ChangeRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type ChangeFact struct {
	ID           string        `json:"id"`
	Kind         ChangeKind    `json:"kind"`
	TargetID     string        `json:"target_id,omitempty"`
	SymbolID     SymbolID      `json:"symbol_id,omitempty"`
	File         string        `json:"file"`
	Ranges       []ChangeRange `json:"ranges"`
	Source       string        `json:"source"`
	SourceFactID string        `json:"source_fact_id,omitempty"`
	Confidence   Confidence    `json:"confidence"`
}
