package diagnostics

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

func ToFact(d Diagnostic) facts.DiagnosticFact {
	return facts.DiagnosticFact{
		ID:             d.ID,
		Code:           string(d.Code),
		Severity:       string(d.Severity),
		Message:        d.Message,
		Span:           d.Span,
		RelatedFactIDs: append([]string(nil), d.RelatedFactIDs...),
	}
}

func AddFact(store *facts.Store, d Diagnostic) {
	collector := NewCollector()
	for _, existing := range store.Diagnostics {
		collector.Add(Diagnostic{
			ID:             existing.ID,
			Code:           Code(existing.Code),
			Severity:       Severity(existing.Severity),
			Message:        existing.Message,
			Span:           existing.Span,
			RelatedFactIDs: existing.RelatedFactIDs,
		})
	}
	collector.Add(d)
	list := collector.List()
	store.Diagnostics = store.Diagnostics[:0]
	for _, item := range list {
		store.Diagnostics = append(store.Diagnostics, ToFact(item))
	}
}
