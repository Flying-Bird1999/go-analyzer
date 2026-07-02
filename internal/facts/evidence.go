package facts

type EvidenceFact struct {
	Kind       string     `json:"kind"`
	Raw        string     `json:"raw,omitempty"`
	Span       SourceSpan `json:"span"`
	Confidence Confidence `json:"confidence,omitempty"`
}
