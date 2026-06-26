package facts

type LinkKind string

const (
	LinkKindRouteToHandler      LinkKind = "route_to_handler"
	LinkKindHandlerToAnnotation LinkKind = "handler_to_annotation"
)

type LinkFact struct {
	ID         string     `json:"id"`
	Kind       LinkKind   `json:"kind"`
	FromID     string     `json:"from_id"`
	ToID       string     `json:"to_id"`
	Confidence Confidence `json:"confidence"`
}
