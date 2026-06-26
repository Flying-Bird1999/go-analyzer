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
	assertChangeKind(t, got, facts.ChangeKindMethodBodyChanged)
	assertChangeKind(t, got, facts.ChangeKindRouteRegistrationChanged)
	assertChangeKind(t, got, facts.ChangeKindMiddlewareBindingChanged)
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
	assertChangeKind(t, got, facts.ChangeKindMiddlewareBindingChanged)
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
