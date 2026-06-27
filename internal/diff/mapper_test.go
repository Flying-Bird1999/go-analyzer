package diff

import (
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	annotationextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestMapRangesToSemanticFacts(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols, facts.SymbolFact{
		ID:   "func:example.com/project/controller::CheckIn",
		Kind: "func",
		Span: facts.SourceSpan{File: "controller/common.go", StartLine: 10, EndLine: 30},
	})
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:checkIn",
		HandlerSymbol: "func:example.com/project/controller::CheckIn",
		Span:          facts.SourceSpan{File: "controller/common.go", StartLine: 11, EndLine: 12},
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:   "route:checkIn",
		Span: facts.SourceSpan{File: "router/router.go", StartLine: 20, EndLine: 20},
	})
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:   "middleware:auth",
		Span: facts.SourceSpan{File: "router/router.go", StartLine: 21, EndLine: 21},
	})

	changes := []FileChange{
		{NewPath: "controller/common.go", Ranges: []LineRange{{StartLine: 11, EndLine: 11}}},
		{NewPath: "controller/common.go", Ranges: []LineRange{{StartLine: 25, EndLine: 25}}},
		{NewPath: "router/router.go", Ranges: []LineRange{{StartLine: 20, EndLine: 20}}},
		{NewPath: "router/router.go", Ranges: []LineRange{{StartLine: 21, EndLine: 21}}},
	}

	got := MapChanges(changes, store, "git_diff")
	assertChangeKind(t, got, facts.ChangeKindAnnotationChanged)
	assertChangeKind(t, got, facts.ChangeKindSymbolChanged)
	assertChangeKind(t, got, facts.ChangeKindRouteChanged)
	assertChangeKind(t, got, facts.ChangeKindMiddlewareChanged)
}

func TestMapRealRouteFixtureRange(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "middleware-order")
	store := loadFactsForDiff(t, root)
	if len(store.Routes) == 0 || len(store.Middleware) == 0 {
		t.Fatalf("fixture facts missing routes/middleware: %#v", store)
	}

	got := MapChanges([]FileChange{{
		NewPath: store.Middleware[0].Span.File,
		Ranges:  []LineRange{{StartLine: store.Middleware[0].Span.StartLine, EndLine: store.Middleware[0].Span.EndLine}},
	}}, store, "git_diff")
	assertChangeKind(t, got, facts.ChangeKindMiddlewareChanged)
}

func TestMapAnnotatedFunctionBodyToSymbolInsteadOfAnnotation(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "annotation-only")
	store := loadFactsForDiff(t, root)

	got := MapChanges([]FileChange{{
		NewPath: "controller/common.go",
		Ranges:  []LineRange{{StartLine: 7, EndLine: 7}},
	}}, store, "git_diff")

	if len(got) != 1 || got[0].Kind != facts.ChangeKindSymbolChanged {
		t.Fatalf("annotated function body mapped incorrectly: %#v", got)
	}
	if got[0].SymbolID != "func:example.com/annotation-only/controller::CheckIn" {
		t.Fatalf("symbol = %q", got[0].SymbolID)
	}
}

func TestMapChangesSelectsSmallestContainingSymbol(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols,
		facts.SymbolFact{
			ID:   "type:example.com/project/model::Order",
			Kind: "type",
			Span: facts.SourceSpan{File: "model/order.go", StartLine: 10, EndLine: 30},
		},
		facts.SymbolFact{
			ID:   "var:example.com/project/model::DefaultOrder",
			Kind: "var",
			Span: facts.SourceSpan{File: "model/order.go", StartLine: 15, EndLine: 18},
		},
	)

	got := MapChanges([]FileChange{{
		NewPath: "model/order.go",
		Ranges:  []LineRange{{StartLine: 16, EndLine: 16}},
	}}, store, "git_diff")
	if len(got) != 1 {
		t.Fatalf("changes = %#v", got)
	}
	if got[0].Kind != facts.ChangeKindSymbolChanged || got[0].SymbolID != "var:example.com/project/model::DefaultOrder" {
		t.Fatalf("mapped change = %#v", got[0])
	}
}

func TestMapChangesSplitsRangeAcrossOverlappingSymbols(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols,
		facts.SymbolFact{
			ID:   "func:example.com/project/service::First",
			Kind: "func",
			Span: facts.SourceSpan{File: "service/order.go", StartLine: 10, EndLine: 12},
		},
		facts.SymbolFact{
			ID:   "func:example.com/project/service::Second",
			Kind: "func",
			Span: facts.SourceSpan{File: "service/order.go", StartLine: 13, EndLine: 16},
		},
	)

	got := MapChanges([]FileChange{{
		NewPath: "service/order.go",
		Ranges:  []LineRange{{StartLine: 12, EndLine: 13}},
	}}, store, "git_diff")

	assertChangeSymbol(t, got, "func:example.com/project/service::First")
	assertChangeSymbol(t, got, "func:example.com/project/service::Second")
}

func TestMapChangesMapsRouteGroupBeforeEnclosingSymbol(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols, facts.SymbolFact{
		ID:   "func:example.com/project/router::Init",
		Kind: "func",
		Span: facts.SourceSpan{File: "router/router.go", StartLine: 10, EndLine: 30},
	})
	store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
		ID:   "route_group:api",
		Span: facts.SourceSpan{File: "router/router.go", StartLine: 15, EndLine: 15},
	})

	got := MapChanges([]FileChange{{
		NewPath: "router/router.go",
		Ranges:  []LineRange{{StartLine: 15, EndLine: 15}},
	}}, store, "git_diff")
	if len(got) != 1 || got[0].Kind != facts.ChangeKindRouteGroupChanged || got[0].TargetID != "route_group:api" {
		t.Fatalf("mapped change = %#v", got)
	}
}

func TestMapChangesUsesMediumConfidenceForDeletionAnchor(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols, facts.SymbolFact{
		ID:   "type:example.com/project/model::Order",
		Kind: "type",
		Span: facts.SourceSpan{File: "model/order.go", StartLine: 10, EndLine: 20},
	})

	got := MapChanges([]FileChange{{
		NewPath: "model/order.go",
		Ranges: []LineRange{{
			StartLine: 12,
			EndLine:   12,
			Kind:      RangeKindDeletionAnchor,
		}},
	}}, store, "git_diff")
	if len(got) != 1 || got[0].SymbolID != "type:example.com/project/model::Order" || got[0].Confidence != facts.ConfidenceMedium {
		t.Fatalf("mapped deletion = %#v", got)
	}
}

func TestMapChangesDiagnosesUnresolvedDeletedSymbol(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")

	got := MapChanges([]FileChange{{
		OldPath: "model/deleted.go",
		Status:  StatusDeleted,
		Ranges: []LineRange{{
			StartLine: 1,
			EndLine:   1,
			Kind:      RangeKindDeletionAnchor,
		}},
	}}, store, "git_diff")
	if len(got) != 1 || got[0].Kind != facts.ChangeKindFileChanged {
		t.Fatalf("changes = %#v", got)
	}
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == "deleted_symbol_unresolved" {
			return
		}
	}
	t.Fatalf("deleted symbol diagnostic not found: %#v", store.Diagnostics)
}

func loadFactsForDiff(t *testing.T, root string) *facts.Store {
	t.Helper()
	p, err := project.Load(root, project.Options{})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	for _, symbol := range idx.Symbols {
		store.AddSymbol(symbol)
	}
	if err := annotationextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if err := routeextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	return store
}

func assertChangeKind(t *testing.T, changes []facts.ChangeFact, kind facts.ChangeKind) {
	t.Helper()
	for _, change := range changes {
		if change.Kind == kind {
			return
		}
	}
	t.Fatalf("change kind %s not found: %#v", kind, changes)
}

func assertChangeSymbol(t *testing.T, changes []facts.ChangeFact, symbol facts.SymbolID) {
	t.Helper()
	for _, change := range changes {
		if change.SymbolID == symbol {
			return
		}
	}
	t.Fatalf("change symbol %s not found: %#v", symbol, changes)
}
