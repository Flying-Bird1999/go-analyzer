package diagnostics

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestCollectorDedupesDiagnostics(t *testing.T) {
	collector := NewCollector()
	span := facts.SourceSpan{File: "router/router.go", StartLine: 10, EndLine: 10}
	collector.Add(Diagnostic{
		Code:           CodeRouteDynamicPath,
		Severity:       SeverityWarning,
		Message:        "dynamic route path cannot be resolved",
		Span:           span,
		RelatedFactIDs: []string{"route:a"},
	})
	collector.Add(Diagnostic{
		Code:           CodeRouteDynamicPath,
		Severity:       SeverityWarning,
		Message:        "dynamic route path cannot be resolved",
		Span:           span,
		RelatedFactIDs: []string{"route:a"},
	})

	got := collector.List()
	if len(got) != 1 {
		t.Fatalf("diagnostics = %d", len(got))
	}
	if got[0].Code != CodeRouteDynamicPath {
		t.Fatalf("code = %q", got[0].Code)
	}
	if got[0].Severity != SeverityWarning {
		t.Fatalf("severity = %q", got[0].Severity)
	}
	if got[0].Span.File != "router/router.go" {
		t.Fatalf("span = %#v", got[0].Span)
	}
	if len(got[0].RelatedFactIDs) != 1 || got[0].RelatedFactIDs[0] != "route:a" {
		t.Fatalf("related facts = %#v", got[0].RelatedFactIDs)
	}
}
