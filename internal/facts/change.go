package facts

type ChangeKind string

const (
	ChangeKindMethodBodyChanged        ChangeKind = "method_body_changed"
	ChangeKindRouteRegistrationChanged ChangeKind = "route_registration_changed"
	ChangeKindMiddlewareBindingChanged ChangeKind = "middleware_binding_changed"
	ChangeKindAnnotationChanged        ChangeKind = "annotation_changed"
	ChangeKindFileChanged              ChangeKind = "file_changed"
)

type ChangeRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type ChangeFact struct {
	ID         string        `json:"id"`
	Kind       ChangeKind    `json:"kind"`
	TargetID   string        `json:"target_id,omitempty"`
	SymbolID   SymbolID      `json:"symbol_id,omitempty"`
	File       string        `json:"file"`
	Ranges     []ChangeRange `json:"ranges"`
	Source     string        `json:"source"`
	Confidence Confidence    `json:"confidence"`
}
