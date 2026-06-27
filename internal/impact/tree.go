package impact

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type Node struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Name         string           `json:"name,omitempty"`
	File         string           `json:"file,omitempty"`
	Package      string           `json:"package,omitempty"`
	Relation     string           `json:"relation,omitempty"`
	Raw          string           `json:"raw,omitempty"`
	Span         facts.SourceSpan `json:"span,omitempty"`
	Confidence   facts.Confidence `json:"confidence,omitempty"`
	Level        int              `json:"level"`
	Cycle        bool             `json:"cycle,omitempty"`
	StopBoundary bool             `json:"stop_boundary,omitempty"`
	Method       string           `json:"method,omitempty"`
	Path         string           `json:"path,omitempty"`
	Children     []Node           `json:"children"`
}

type RootImpact struct {
	Change    facts.ChangeFact `json:"change"`
	Root      Node             `json:"root"`
	Endpoints []EndpointImpact `json:"endpoints"`
}

type TreeResult struct {
	Roots       []RootImpact           `json:"roots"`
	Diagnostics []facts.DiagnosticFact `json:"diagnostics"`
}

type TreeOptions struct {
	MaxDepth        int
	StopPropagation []string
}
