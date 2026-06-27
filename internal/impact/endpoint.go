package impact

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type EndpointImpact struct {
	ID            string         `json:"id"`
	Method        string         `json:"method"`
	Path          string         `json:"path"`
	AnnotationID  string         `json:"annotation_id"`
	HandlerSymbol facts.SymbolID `json:"handler_symbol"`
}
