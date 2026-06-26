package diagnostics

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type Diagnostic struct {
	ID             string           `json:"id"`
	Code           Code             `json:"code"`
	Severity       Severity         `json:"severity"`
	Message        string           `json:"message"`
	Span           facts.SourceSpan `json:"span,omitempty"`
	RelatedFactIDs []string         `json:"related_fact_ids,omitempty"`
}
