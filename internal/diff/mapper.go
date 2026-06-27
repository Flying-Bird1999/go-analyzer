package diff

import (
	"fmt"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func MapChanges(changes []FileChange, store *facts.Store, source string) []facts.ChangeFact {
	var out []facts.ChangeFact
	for _, fileChange := range changes {
		file := filepath.ToSlash(fileChange.NewPath)
		if file == "" {
			file = filepath.ToSlash(fileChange.OldPath)
		}
		for _, r := range fileChange.Ranges {
			changeRange := facts.ChangeRange{StartLine: r.StartLine, EndLine: r.EndLine}
			confidence := facts.ConfidenceHigh
			if r.Kind == RangeKindDeletionAnchor {
				confidence = facts.ConfidenceMedium
			}
			mapped := mapRange(file, changeRange, store, source, len(out), confidence)
			out = append(out, mapped)
			if r.Kind == RangeKindDeletionAnchor && mapped.Kind == facts.ChangeKindFileChanged {
				diagnostics.AddFact(store, diagnostics.Diagnostic{
					Code:     diagnostics.CodeDeletedSymbolUnresolved,
					Severity: diagnostics.SeverityWarning,
					Message:  "deleted lines could not be mapped to a surviving symbol",
					Span: facts.SourceSpan{
						File:      file,
						StartLine: r.StartLine,
						EndLine:   r.EndLine,
					},
					RelatedFactIDs: []string{mapped.ID},
				})
			}
		}
	}
	return out
}

func mapRange(file string, r facts.ChangeRange, store *facts.Store, source string, index int, confidence facts.Confidence) facts.ChangeFact {
	for _, annotation := range store.Annotations {
		if spanContains(annotation.Span, file, r) {
			return changeFact(index, facts.ChangeKindAnnotationChanged, annotation.ID, annotation.HandlerSymbol, file, r, source, facts.ConfidenceHigh)
		}
	}
	for _, group := range store.RouteGroups {
		if spanContains(group.Span, file, r) {
			return changeFact(index, facts.ChangeKindRouteGroupChanged, group.ID, group.RouteFunc, file, r, source, facts.ConfidenceHigh)
		}
	}
	for _, route := range store.Routes {
		if spanContains(route.Span, file, r) {
			return changeFact(index, facts.ChangeKindRouteChanged, route.ID, route.HandlerSymbol, file, r, source, facts.ConfidenceHigh)
		}
	}
	for _, binding := range store.Middleware {
		if spanContains(binding.Span, file, r) {
			return changeFact(index, facts.ChangeKindMiddlewareChanged, binding.ID, "", file, r, source, facts.ConfidenceHigh)
		}
	}
	var selected *facts.SymbolFact
	for _, symbol := range store.Symbols {
		if spanContains(symbol.Span, file, r) {
			if selected == nil || spanSize(symbol.Span) < spanSize(selected.Span) ||
				(spanSize(symbol.Span) == spanSize(selected.Span) && symbol.ID < selected.ID) {
				candidate := symbol
				selected = &candidate
			}
		}
	}
	if selected != nil {
		return changeFact(index, facts.ChangeKindSymbolChanged, string(selected.ID), selected.ID, file, r, source, confidence)
	}
	return changeFact(index, facts.ChangeKindFileChanged, file, "", file, r, source, facts.ConfidenceLow)
}

func spanSize(span facts.SourceSpan) int {
	return span.EndLine - span.StartLine
}

func spanContains(span facts.SourceSpan, file string, r facts.ChangeRange) bool {
	if filepath.ToSlash(span.File) != filepath.ToSlash(file) {
		return false
	}
	return r.StartLine >= span.StartLine && r.EndLine <= span.EndLine
}

func changeFact(index int, kind facts.ChangeKind, targetID string, symbolID facts.SymbolID, file string, r facts.ChangeRange, source string, confidence facts.Confidence) facts.ChangeFact {
	return facts.ChangeFact{
		ID:         fmt.Sprintf("change:%s:%s:%d:%d:%d", kind, file, r.StartLine, r.EndLine, index),
		Kind:       kind,
		TargetID:   targetID,
		SymbolID:   symbolID,
		File:       file,
		Ranges:     []facts.ChangeRange{r},
		Source:     source,
		Confidence: confidence,
	}
}
