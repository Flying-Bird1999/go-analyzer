// mapper_test.go 验证 diff 行范围到语义根的映射：按领域事实优先级（注解→路由组→路由→中间件→最小包含符号）
// 选择最精确的根、跨重叠符号拆分范围、删除锚点降级置信度，以及无法恢复删除符号时的诊断。

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

// TestMapRangesToSemanticFacts 验证同一文件不同行能分别映射到注解、符号、路由、中间件和 Job 注册语义根。
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
	store.JobRegistrations = append(store.JobRegistrations, facts.JobRegistrationFact{
		ID: "job_registration:refresh", HandlerSymbol: "func:example.com/project/jobs::Refresh",
		Span: facts.SourceSpan{File: "jobs/jobs.go", StartLine: 22, EndLine: 22},
	})

	changes := []FileChange{
		{NewPath: "controller/common.go", Ranges: []LineRange{{StartLine: 11, EndLine: 11}}},
		{NewPath: "controller/common.go", Ranges: []LineRange{{StartLine: 25, EndLine: 25}}},
		{NewPath: "router/router.go", Ranges: []LineRange{{StartLine: 20, EndLine: 20}}},
		{NewPath: "router/router.go", Ranges: []LineRange{{StartLine: 21, EndLine: 21}}},
		{NewPath: "jobs/jobs.go", Ranges: []LineRange{{StartLine: 22, EndLine: 22}}},
	}

	got := MapChanges(changes, store, "git_diff")
	assertChangeKind(t, got, facts.ChangeKindAnnotationChanged)
	assertChangeKind(t, got, facts.ChangeKindSymbolChanged)
	assertChangeKind(t, got, facts.ChangeKindRouteChanged)
	assertChangeKind(t, got, facts.ChangeKindMiddlewareChanged)
	assertChangeKind(t, got, facts.ChangeKindJobRegistrationChanged)
}

// TestMapRealRouteFixtureRange 验证基于 middleware-order 真实 fixture 的中间件行能正确映射。
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

// TestMapAnnotatedFunctionBodyToSymbolInsteadOfAnnotation 验证注解函数的函数体行映射到符号而非注解（注解 span 仅限注释行）。
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

// TestMapChangesSelectsSmallestContainingSymbol 验证多重包含时优先选行跨度最小的符号。
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

// TestMapChangesSplitsRangeAcrossOverlappingSymbols 验证跨两个相邻符号的范围会被拆分映射到各自符号。
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

// TestMapChangesMapsRouteGroupBeforeEnclosingSymbol 验证路由组优先于包裹它的路由函数符号被命中。
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

func TestMapChangesMapsSingleDubboMethodRegistration(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Symbols = append(store.Symbols, facts.SymbolFact{
		ID: "func:example.com/project/provider::Export", Kind: "func",
		Span: facts.SourceSpan{File: "provider/api.go", StartLine: 10, EndLine: 40},
	})
	store.DubboProviders = append(store.DubboProviders,
		facts.DubboProviderFact{ID: "dubbo:first", HandlerSymbol: "method:example.com/project/provider:API:First", Span: facts.SourceSpan{File: "provider/api.go", StartLine: 20, EndLine: 22}},
		facts.DubboProviderFact{ID: "dubbo:second", HandlerSymbol: "method:example.com/project/provider:API:Second", Span: facts.SourceSpan{File: "provider/api.go", StartLine: 23, EndLine: 25}},
	)

	got := MapChanges([]FileChange{{NewPath: "provider/api.go", Ranges: []LineRange{{StartLine: 24, EndLine: 24}}}}, store, "git_diff")
	if len(got) != 1 || got[0].Kind != facts.ChangeKindDubboProviderChanged || got[0].TargetID != "dubbo:second" {
		t.Fatalf("mapped change = %#v", got)
	}
}

// TestMapChangesUsesMediumConfidenceForDeletionAnchor 验证删除锚点命中的符号根使用 medium 置信度。
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

// TestMapChangesUsesMediumConfidenceForDeletedDomainFactAnchors 验证注解/路由组/路由/中间件的删除锚点均降级为 medium 置信度。
func TestMapChangesUsesMediumConfidenceForDeletedDomainFactAnchors(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.Annotations = append(store.Annotations, facts.AnnotationFact{
		ID:            "annotation:orders",
		HandlerSymbol: "func:example.com/project/controller::Orders",
		Span:          facts.SourceSpan{File: "controller/orders.go", StartLine: 3, EndLine: 3},
	})
	store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
		ID:        "route_group:orders",
		RouteFunc: "func:example.com/project/router::Init",
		Span:      facts.SourceSpan{File: "router/router.go", StartLine: 10, EndLine: 10},
	})
	store.Routes = append(store.Routes, facts.RouteRegistrationFact{
		ID:            "route:orders",
		HandlerSymbol: "func:example.com/project/controller::Orders",
		Span:          facts.SourceSpan{File: "router/router.go", StartLine: 11, EndLine: 11},
	})
	store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
		ID:   "middleware:auth",
		Span: facts.SourceSpan{File: "router/router.go", StartLine: 12, EndLine: 12},
	})

	got := MapChanges([]FileChange{
		{NewPath: "controller/orders.go", Ranges: []LineRange{{StartLine: 3, EndLine: 3, Kind: RangeKindDeletionAnchor}}},
		{NewPath: "router/router.go", Ranges: []LineRange{{StartLine: 10, EndLine: 12, Kind: RangeKindDeletionAnchor}}},
	}, store, "git_diff")

	for _, kind := range []facts.ChangeKind{
		facts.ChangeKindAnnotationChanged,
		facts.ChangeKindRouteGroupChanged,
		facts.ChangeKindRouteChanged,
		facts.ChangeKindMiddlewareChanged,
	} {
		change := findChangeKind(t, got, kind)
		if change.Confidence != facts.ConfidenceMedium {
			t.Fatalf("%s confidence = %s, want medium; changes=%#v", kind, change.Confidence, got)
		}
	}
}

// TestMapChangesDiagnosesUnresolvedDeletedSymbol 验证删除文件无法恢复符号时降级为 file 根并输出 deleted_symbol_unresolved 诊断。
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

// loadFactsForDiff 加载 fixture 项目并构建到含符号/注解/路由的 facts.Store，供映射测试使用。
func loadFactsForDiff(t *testing.T, root string) *facts.Store {
	t.Helper()
	p, err := project.Load(root)
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

// assertChangeKind 断言变更列表中存在指定 kind 的根。
func assertChangeKind(t *testing.T, changes []facts.ChangeFact, kind facts.ChangeKind) {
	t.Helper()
	_ = findChangeKind(t, changes, kind)
}

// findChangeKind 在变更列表中查找指定 kind 的根，找不到则测试失败。
func findChangeKind(t *testing.T, changes []facts.ChangeFact, kind facts.ChangeKind) facts.ChangeFact {
	t.Helper()
	for _, change := range changes {
		if change.Kind == kind {
			return change
		}
	}
	t.Fatalf("change kind %s not found: %#v", kind, changes)
	return facts.ChangeFact{}
}

// assertChangeSymbol 断言变更列表中存在指向指定符号的根。
func assertChangeSymbol(t *testing.T, changes []facts.ChangeFact, symbol facts.SymbolID) {
	t.Helper()
	for _, change := range changes {
		if change.SymbolID == symbol {
			return
		}
	}
	t.Fatalf("change symbol %s not found: %#v", symbol, changes)
}
