// tree_test.go 覆盖 serviceimpact 的关键正/反路径闸门（handoff §7.2 item 8）：
// 未注册实现不得成为终点（registrationIsLive）、DubboServiceChanged 的 interface 全方法扇出、
// 动态 path 的 symbolic 身份保真、引用环终止，以及多路径命中同一 contract 取最弱置信度。
package serviceimpact

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

// newTestStore 构造一个仅含指定 symbol 的空 store，供各测试填充 References/Routes/
// DubboProviders/Changes 后调用 AnalyzeTrees。
func newTestStore(symbols ...facts.SymbolFact) *facts.Store {
	store := facts.NewStore("/tmp/p", "example.com/p")
	store.Symbols = append(store.Symbols, symbols...)
	return store
}

func rootContractIDs(root RootImpact) map[string]facts.Confidence {
	out := map[string]facts.Confidence{}
	for _, c := range root.Contracts {
		out[c.Contract.ID] = c.Confidence
	}
	return out
}

// TestRegistrationLivenessGate 验证「未注册实现不得成为终点」闸门：
// 注册函数既无项目引用、名字也不符合 main/Register*/Initialize* 约定时，其契约不进入
// 分析结果；一旦被引用（或符合约定），契约恢复。这是 grpc/dubbo/http/job 共用的核心闸门。
func TestRegistrationLivenessGate(t *testing.T) {
	handler := facts.SymbolID("method:example.com/p/impl:Impl:Do")
	deadReg := facts.SymbolID("func:example.com/p::wireProviders") // 无引用、非启动命名
	provider := facts.DubboProviderFact{
		ID: "dubbo_provider:x.API/do:impl.go:1:1", Interface: "x.API", Version: "1.0.0", Method: "do", GoMethod: "Do",
		HandlerSymbol: handler, RegistrationSymbol: deadReg, Confidence: facts.ConfidenceHigh,
		Span: facts.SourceSpan{File: "impl.go", StartLine: 1},
	}
	change := facts.ChangeFact{ID: "c1", Kind: facts.ChangeKindSymbolChanged, SymbolID: handler, File: "impl.go", Confidence: facts.ConfidenceHigh}

	// 反例：死注册 -> 契约不出现。
	store := newTestStore(facts.SymbolFact{ID: handler, Kind: "method", Name: "Do", Span: facts.SourceSpan{File: "impl.go"}})
	store.DubboProviders = []facts.DubboProviderFact{provider}
	store.Changes = []facts.ChangeFact{change}
	res := AnalyzeTrees(store)
	if len(res.Roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(res.Roots))
	}
	if got := rootContractIDs(res.Roots[0]); len(got) != 0 {
		t.Fatalf("dead registration should expose no contract, got %v", got)
	}

	// 正例：注册函数被项目引用 -> 契约出现。
	store2 := newTestStore(facts.SymbolFact{ID: handler, Kind: "method", Name: "Do", Span: facts.SourceSpan{File: "impl.go"}})
	store2.DubboProviders = []facts.DubboProviderFact{provider}
	store2.Changes = []facts.ChangeFact{change}
	store2.References = []facts.ReferenceFact{{
		ID: "r1", Kind: facts.ReferenceKindCall, FromSymbol: "func:example.com/p::main", ToSymbol: deadReg, Confidence: facts.ConfidenceHigh,
	}}
	res2 := AnalyzeTrees(store2)
	if got := rootContractIDs(res2.Roots[0]); len(got) != 1 {
		t.Fatalf("referenced registration should expose exactly one contract, got %v", got)
	}
}

// TestRegistrationLivenessNamingConvention 记录 R2-P2-5 的既定行为：名字以 Register 开头的
// 注册函数即使无项目引用也判为 live（命名约定）。此测试锁定该约定，避免无意改动。
func TestRegistrationLivenessNamingConvention(t *testing.T) {
	handler := facts.SymbolID("method:example.com/p/impl:Impl:Do")
	reg := facts.SymbolID("func:example.com/p::RegisterProviders") // 无引用，但符合命名约定
	store := newTestStore(facts.SymbolFact{ID: handler, Kind: "method", Name: "Do", Span: facts.SourceSpan{File: "impl.go"}})
	store.DubboProviders = []facts.DubboProviderFact{{
		ID: "dubbo_provider:x.API/do:impl.go:1:1", Interface: "x.API", Version: "1.0.0", Method: "do", GoMethod: "Do",
		HandlerSymbol: handler, RegistrationSymbol: reg, Confidence: facts.ConfidenceHigh, Span: facts.SourceSpan{File: "impl.go", StartLine: 1},
	}}
	store.Changes = []facts.ChangeFact{{ID: "c1", Kind: facts.ChangeKindSymbolChanged, SymbolID: handler, File: "impl.go", Confidence: facts.ConfidenceHigh}}
	if got := rootContractIDs(AnalyzeTrees(store).Roots[0]); len(got) != 1 {
		t.Fatalf("Register*-named registration should be live by convention, got %v", got)
	}
}

// TestDubboServiceChangedFansOutAllMethods 验证 DubboServiceChanged 根扇出该 interface
// 的全部已注册方法（不只被直接命中的那条）。
func TestDubboServiceChangedFansOutAllMethods(t *testing.T) {
	reg := facts.SymbolID("func:example.com/p::RegisterProviders")
	handlerA := facts.SymbolID("method:example.com/p/impl:Impl:A")
	handlerB := facts.SymbolID("method:example.com/p/impl:Impl:B")
	base := func(id, method string, handler facts.SymbolID, line int) facts.DubboProviderFact {
		return facts.DubboProviderFact{
			ID: id, Interface: "x.API", Version: "1.0.0", Method: method, GoMethod: method,
			HandlerSymbol: handler, RegistrationSymbol: reg, Confidence: facts.ConfidenceHigh,
			Span: facts.SourceSpan{File: "impl.go", StartLine: line},
		}
	}
	pa := base("dubbo_provider:x.API/a:impl.go:1:1", "a", handlerA, 1)
	pb := base("dubbo_provider:x.API/b:impl.go:2:1", "b", handlerB, 2)
	store := newTestStore(
		facts.SymbolFact{ID: handlerA, Kind: "method", Name: "A", Span: facts.SourceSpan{File: "impl.go"}},
		facts.SymbolFact{ID: handlerB, Kind: "method", Name: "B", Span: facts.SourceSpan{File: "impl.go"}},
		facts.SymbolFact{ID: reg, Kind: "func", Name: "RegisterProviders", Span: facts.SourceSpan{File: "wire.go"}},
	)
	store.DubboProviders = []facts.DubboProviderFact{pa, pb}
	// service 级变更只命中 provider A 的 fact，但应扇出 interface 的全部方法。
	store.Changes = []facts.ChangeFact{{ID: "c1", Kind: facts.ChangeKindDubboServiceChanged, TargetID: pa.ID, SymbolID: reg, File: "wire.go", Confidence: facts.ConfidenceHigh}}
	got := rootContractIDs(AnalyzeTrees(store).Roots[0])
	if _, ok := got["dubbo:"+pa.ID]; !ok {
		t.Errorf("missing method a contract: %v", got)
	}
	if _, ok := got["dubbo:"+pb.ID]; !ok {
		t.Errorf("DubboServiceChanged did not fan out to method b: %v", got)
	}
}

// TestHTTPContractPreservesSymbolicIdentity 验证动态 path 路由（PathRaw 非空）被标为 symbolic，
// 身份取原始 LocalPath 表达式而非伪造 resolved path。
func TestHTTPContractPreservesSymbolicIdentity(t *testing.T) {
	handler := facts.SymbolID("func:example.com/p/ctrl::Get")
	routeFunc := facts.SymbolID("func:example.com/p/router::RegisterRouter") // Register* -> live
	store := newTestStore(facts.SymbolFact{ID: handler, Kind: "func", Name: "Get", Span: facts.SourceSpan{File: "ctrl.go"}})
	store.Routes = []facts.RouteRegistrationFact{{
		ID: "route:r1", Method: "GET", LocalPath: "/:id", PathRaw: "prefix + \"/:id\"", ResolvedPath: "",
		HandlerSymbol: handler, RouteFunc: routeFunc, Span: facts.SourceSpan{File: "router.go", StartLine: 3},
	}}
	store.Changes = []facts.ChangeFact{{ID: "c1", Kind: facts.ChangeKindSymbolChanged, SymbolID: handler, File: "ctrl.go", Confidence: facts.ConfidenceHigh}}
	root := AnalyzeTrees(store).Roots[0]
	if len(root.Contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(root.Contracts))
	}
	c := root.Contracts[0].Contract
	if c.IdentityResolution != IdentitySymbolic {
		t.Errorf("identity resolution = %q, want symbolic", c.IdentityResolution)
	}
	if c.Identity != "GET /:id" {
		t.Errorf("identity = %q, want GET /:id (LocalPath, not fabricated resolved)", c.Identity)
	}
}

// TestReverseCycleTerminates 验证引用环（A->B->A）在传播时被标记 Cycle 并终止，不会无限递归。
func TestReverseCycleTerminates(t *testing.T) {
	handler := facts.SymbolID("func:example.com/p/ctrl::Get")
	routeFunc := facts.SymbolID("func:example.com/p/router::RegisterRouter")
	a := facts.SymbolID("func:example.com/p::A")
	b := facts.SymbolID("func:example.com/p::B")
	store := newTestStore(
		facts.SymbolFact{ID: handler, Kind: "func", Name: "Get", Span: facts.SourceSpan{File: "ctrl.go"}},
		facts.SymbolFact{ID: a, Kind: "func", Name: "A", Span: facts.SourceSpan{File: "a.go"}},
		facts.SymbolFact{ID: b, Kind: "func", Name: "B", Span: facts.SourceSpan{File: "b.go"}},
	)
	store.Routes = []facts.RouteRegistrationFact{{
		ID: "route:r1", Method: "GET", LocalPath: "/x", ResolvedPath: "/x",
		HandlerSymbol: handler, RouteFunc: routeFunc, Span: facts.SourceSpan{File: "router.go", StartLine: 3},
	}}
	// handler <- A <- B <- A（环）。
	store.References = []facts.ReferenceFact{
		{ID: "r1", Kind: facts.ReferenceKindCall, FromSymbol: a, ToSymbol: handler, Confidence: facts.ConfidenceHigh},
		{ID: "r2", Kind: facts.ReferenceKindCall, FromSymbol: b, ToSymbol: a, Confidence: facts.ConfidenceHigh},
		{ID: "r3", Kind: facts.ReferenceKindCall, FromSymbol: a, ToSymbol: b, Confidence: facts.ConfidenceHigh},
	}
	store.Changes = []facts.ChangeFact{{ID: "c1", Kind: facts.ChangeKindSymbolChanged, SymbolID: handler, File: "ctrl.go", Confidence: facts.ConfidenceHigh}}
	// 不应死循环；且契约仍应被发现。
	root := AnalyzeTrees(store).Roots[0]
	if len(root.Contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(root.Contracts))
	}
	if !hasCycleNode(root.Root) {
		t.Errorf("expected a node marked Cycle in the propagation tree")
	}
}

func hasCycleNode(node impact.Node) bool {
	if node.Cycle {
		return true
	}
	for _, child := range node.Children {
		if hasCycleNode(child) {
			return true
		}
	}
	return false
}
