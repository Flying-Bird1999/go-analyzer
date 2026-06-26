package impact

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestAnalyzeServiceSymbolChangeImpactsEndpoint(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:service",
		Kind:     facts.ChangeKindMethodBodyChanged,
		SymbolID: serviceSymbol,
		File:     "service/common.go",
	})

	result := Analyze(store)
	assertEndpoint(t, result, "GET", "/api/bff-web/common/checkIn")
	if len(result.EvidenceChains) == 0 {
		t.Fatal("expected evidence chains")
	}
}

func TestAnalyzeControllerSymbolChangeImpactsEndpoint(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:controller",
		Kind:     facts.ChangeKindMethodBodyChanged,
		SymbolID: controllerSymbol,
		File:     "controller/common.go",
	})

	result := Analyze(store)
	assertEndpoint(t, result, "GET", "/api/bff-web/common/checkIn")
}

func TestAnalyzeRouteRegistrationChangeImpactsEndpoint(t *testing.T) {
	store := referenceImpactStore()
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:route",
		Kind:     facts.ChangeKindRouteRegistrationChanged,
		TargetID: "route:checkIn",
		File:     "router/router.go",
	})

	result := Analyze(store)
	assertEndpoint(t, result, "GET", "/api/bff-web/common/checkIn")
}

func TestAnalyzeRouteGroupChangeImpactsGroupRoutes(t *testing.T) {
	store := referenceImpactStore()
	store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
		ID:       "group:root",
		GroupVar: "g",
		Prefix:   "/api",
	})
	store.Routes[0].GroupVar = "g"
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:group",
		Kind:     facts.ChangeKindRouteRegistrationChanged,
		TargetID: "group:root",
		File:     "router/router.go",
	})

	result := Analyze(store)
	assertEndpoint(t, result, "GET", "/api/bff-web/common/checkIn")
}

func TestAnalyzeMiddlewareBindingChangeImpactsOnlyLaterRoutes(t *testing.T) {
	store := referenceImpactStore()
	laterHandler := facts.SymbolID("func:example.com/project/controller::Later")
	store.Symbols = append(store.Symbols, facts.SymbolFact{ID: laterHandler, Kind: "func"})
	store.Routes[0].GroupVar = "g"
	store.Routes[0].StatementIndex = 1
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:             "route:later",
		Method:         "GET",
		LocalPath:      "/later",
		GroupVar:       "g",
		HandlerSymbol:  laterHandler,
		StatementIndex: 3,
	})
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:later",
		Method:        "GET",
		Path:          "/api/bff-web/common/later",
		HandlerSymbol: laterHandler,
	})
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:             "middleware:auth",
		GroupVar:       "g",
		StatementIndex: 2,
	})
	store.Changes = append(store.Changes, facts.ChangeFact{
		ID:       "change:middleware",
		Kind:     facts.ChangeKindMiddlewareBindingChanged,
		TargetID: "middleware:auth",
		File:     "router/router.go",
	})

	result := Analyze(store)
	assertEndpoint(t, result, "GET", "/api/bff-web/common/later")
	assertNoEndpoint(t, result, "GET", "/api/bff-web/common/checkIn")
}

func TestAnalyzePreciseModuleUsageImpactsEndpoint(t *testing.T) {
	store := referenceImpactStore()
	store.ModuleUsages = append(store.ModuleUsages, facts.ModuleUsageFact{
		ID:         "module_usage:service",
		ModulePath: "example.com/external/module",
		Basis:      facts.ModuleUsagePrecise,
		SymbolID:   serviceSymbol,
		Confidence: facts.ConfidenceHigh,
	})

	result := Analyze(store)
	assertEndpoint(t, result, "GET", "/api/bff-web/common/checkIn")
	if len(result.ModuleImpacts) != 1 {
		t.Fatalf("module impacts = %d", len(result.ModuleImpacts))
	}
}

func TestAnalyzeUnreferencedModuleUsageProducesNoEndpoint(t *testing.T) {
	store := referenceImpactStore()
	store.ModuleUsages = append(store.ModuleUsages, facts.ModuleUsageFact{
		ID:         "module_usage:unreferenced",
		ModulePath: "example.com/external/module",
		Basis:      facts.ModuleUsageUnreferenced,
		Confidence: facts.ConfidenceHigh,
	})

	result := Analyze(store)
	if len(result.ImpactedEndpoints) != 0 {
		t.Fatalf("impacted endpoints = %#v", result.ImpactedEndpoints)
	}
	if len(result.ModuleImpacts) != 1 {
		t.Fatalf("module impacts = %d", len(result.ModuleImpacts))
	}
	if result.ModuleImpacts[0].Basis != facts.ModuleUsageUnreferenced {
		t.Fatalf("basis = %q", result.ModuleImpacts[0].Basis)
	}
}

const (
	serviceSymbol    facts.SymbolID = "func:example.com/project/service::CheckIn"
	controllerSymbol facts.SymbolID = "func:example.com/project/controller::CheckIn"
)

func referenceImpactStore() *facts.Store {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols,
		facts.SymbolFact{ID: serviceSymbol, Kind: "func", Span: facts.SourceSpan{File: "service/common.go", StartLine: 1, EndLine: 3}},
		facts.SymbolFact{ID: controllerSymbol, Kind: "func", Span: facts.SourceSpan{File: "controller/common.go", StartLine: 10, EndLine: 14}},
	)
	store.References = append(store.References, facts.ReferenceFact{
		ID:         "ref:controller-service",
		Kind:       facts.ReferenceKindCall,
		FromSymbol: controllerSymbol,
		ToSymbol:   serviceSymbol,
		Confidence: facts.ConfidenceHigh,
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:            "route:checkIn",
		Method:        "GET",
		LocalPath:     "/checkIn",
		HandlerSymbol: controllerSymbol,
		Span:          facts.SourceSpan{File: "router/router.go", StartLine: 20, EndLine: 20},
	})
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:checkIn",
		Method:        "GET",
		Path:          "/api/bff-web/common/checkIn",
		HandlerSymbol: controllerSymbol,
		Span:          facts.SourceSpan{File: "controller/common.go", StartLine: 9, EndLine: 9},
	})
	return store
}

func assertEndpoint(t *testing.T, result Result, method, path string) {
	t.Helper()
	for _, endpoint := range result.ImpactedEndpoints {
		if endpoint.Method == method && endpoint.Path == path {
			return
		}
	}
	t.Fatalf("endpoint %s %s not found: %#v", method, path, result.ImpactedEndpoints)
}

func assertNoEndpoint(t *testing.T, result Result, method, path string) {
	t.Helper()
	for _, endpoint := range result.ImpactedEndpoints {
		if endpoint.Method == method && endpoint.Path == path {
			t.Fatalf("endpoint %s %s should not be impacted: %#v", method, path, result.ImpactedEndpoints)
		}
	}
}
