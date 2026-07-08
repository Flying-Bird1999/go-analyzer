// impact_tree_test.go 校验 BuildImpactDocument 与 RenderImpactTreeJSON 的来源聚合、
// 去重、稳定排序与 review 证据保留行为。
package output

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

// 场景：同文件多个 change root 按来源聚合，并去重端点。
func TestBuildImpactDocumentGroupsRootsBySourceFile(t *testing.T) {
	fileChanges := []diff.FileChange{{
		NewPath: "model/model.go",
		Raw:     "diff --git a/model/model.go b/model/model.go\n+changed\n",
	}}
	result := impact.TreeResult{Roots: []impact.RootImpact{
		testRootImpact("change:address", "type:example.com/project/model::Address", "model/model.go", "Address", "POST", "/orders"),
		testRootImpact("change:request", "type:example.com/project/model::CreateOrderRequest", "model/model.go", "CreateOrderRequest", "POST", "/orders"),
	}}

	doc := BuildImpactDocument(fileChanges, result, ImpactDocumentOptions{})
	if len(doc.FileSources) != 1 {
		t.Fatalf("fileSources = %d", len(doc.FileSources))
	}
	if doc.Summary.ImpactedEndpointCount != 1 || len(doc.Summary.ImpactedEndpoints) != 1 {
		t.Fatalf("summary = %#v", doc.Summary)
	}
	source := doc.FileSources[0]
	if len(source.Symbols) != 2 {
		t.Fatalf("symbols = %d", len(source.Symbols))
	}
	if !strings.Contains(source.Diff, "diff --git") {
		t.Fatalf("diff missing: %q", source.Diff)
	}
	if len(source.ImpactedEndpoints) != 1 {
		t.Fatalf("impacted endpoints = %#v", source.ImpactedEndpoints)
	}
}

// 场景：顶层 endpointSourcesSummary 可以从 fileSources 的递归树反查 endpoint 来源文件、root symbol 与最短链。
func TestBuildImpactDocumentAddsEndpointSourcesSummaryForFileSources(t *testing.T) {
	fileChanges := []diff.FileChange{{
		NewPath: "service/order.go",
		Raw:     "diff --git a/service/order.go b/service/order.go\n",
	}}
	root := impact.RootImpact{
		Change: facts.ChangeFact{
			File:     "service/order.go",
			SymbolID: "func:example.com/app/service::UpdateOrder",
		},
		Root: impact.Node{
			ID:         "func:example.com/app/service::UpdateOrder",
			Kind:       "func",
			Name:       "UpdateOrder",
			File:       "service/order.go",
			Confidence: facts.ConfidenceHigh,
			Children: []impact.Node{{
				ID:         "func:example.com/app/controller::UpdateOrder",
				Kind:       "func",
				Name:       "UpdateOrder",
				File:       "controller/order.go",
				Relation:   "call",
				Confidence: facts.ConfidenceHigh,
				Children: []impact.Node{{
					ID:         "endpoint:POST:/orders",
					Kind:       "endpoint",
					Name:       "POST /orders",
					File:       "router/order.go",
					Relation:   "route_endpoint",
					Confidence: facts.ConfidenceHigh,
					Method:     "POST",
					Path:       "/orders",
				}},
			}},
		},
		Endpoints: []impact.EndpointImpact{{Method: "POST", Path: "/orders"}},
	}

	doc := BuildImpactDocument(fileChanges, impact.TreeResult{Roots: []impact.RootImpact{root}}, ImpactDocumentOptions{})

	if len(doc.EndpointSourcesSummary) != 1 {
		t.Fatalf("endpointSourcesSummary = %#v", doc.EndpointSourcesSummary)
	}
	got := doc.EndpointSourcesSummary[0]
	if got.Method != "POST" || got.Path != "/orders" {
		t.Fatalf("endpoint summary = %#v", got)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %#v", got.Sources)
	}
	source := got.Sources[0]
	if source.SourceType != "file" || source.SourceFile != "service/order.go" {
		t.Fatalf("source = %#v", source)
	}
	if len(source.RootSymbols) != 1 || source.RootSymbols[0].ID != "func:example.com/app/service::UpdateOrder" {
		t.Fatalf("root symbols = %#v", source.RootSymbols)
	}
	wantChain := []string{"func UpdateOrder", "func UpdateOrder", "POST /orders"}
	if len(source.Chains) != 1 || !reflect.DeepEqual(source.Chains[0], wantChain) {
		t.Fatalf("chains = %#v, want %#v", source.Chains, wantChain)
	}
	if source.Confidence != facts.ConfidenceHigh {
		t.Fatalf("confidence = %q", source.Confidence)
	}
}

// 场景：颠倒 fileChanges 与 roots 输入顺序，最终 JSON 应字节级一致（确定性输出）。
func TestRenderImpactTreeJSONIsDeterministic(t *testing.T) {
	changeA := diff.FileChange{NewPath: "a.go", Raw: "diff --git a/a.go b/a.go\n"}
	changeB := diff.FileChange{NewPath: "b.go", Raw: "diff --git a/b.go b/b.go\n"}
	rootA := testRootImpact("change:a", "func:example.com/project::A", "a.go", "A", "GET", "/a")
	rootB := testRootImpact("change:b", "func:example.com/project::B", "b.go", "B", "POST", "/b")

	first := BuildImpactDocument([]diff.FileChange{changeB, changeA}, impact.TreeResult{
		Roots: []impact.RootImpact{rootB, rootA},
	}, ImpactDocumentOptions{})
	second := BuildImpactDocument([]diff.FileChange{changeA, changeB}, impact.TreeResult{
		Roots: []impact.RootImpact{rootA, rootB},
	}, ImpactDocumentOptions{})

	firstJSON, err := RenderImpactTreeJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := RenderImpactTreeJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("rendering is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
}

// 场景：无 endpoint / IM 事件的 root 仍保留在来源 symbols 中，但摘要与 IM 列表为非 nil 空数组。
func TestBuildImpactDocumentKeepsRootWithNoEndpoint(t *testing.T) {
	root := testRootImpact("change:orphan", "func:example.com/project::Orphan", "orphan.go", "Orphan", "", "")
	root.Endpoints = nil

	doc := BuildImpactDocument(nil, impact.TreeResult{
		Roots: []impact.RootImpact{root},
	}, ImpactDocumentOptions{})
	if len(doc.FileSources) != 1 {
		t.Fatalf("fileSources = %#v", doc.FileSources)
	}
	if len(doc.FileSources[0].Symbols) != 1 || len(doc.FileSources[0].ImpactedEndpoints) != 0 {
		t.Fatalf("source = %#v", doc.FileSources[0])
	}
	if doc.Summary.ImpactedEndpointCount != 0 || len(doc.Summary.ImpactedEndpoints) != 0 {
		t.Fatalf("summary = %#v", doc.Summary)
	}
	if doc.Summary.ImpactedIMCount != 0 || len(doc.Summary.ImpactedIMEvents) != 0 {
		t.Fatalf("IM summary = %#v", doc.Summary)
	}
	if doc.FileSources[0].ImpactedIMEvents == nil {
		t.Fatal("source IM events must be a non-nil empty array")
	}
	if doc.EndpointSourcesSummary == nil || len(doc.EndpointSourcesSummary) != 0 {
		t.Fatalf("endpointSourcesSummary must be a non-nil empty array: %#v", doc.EndpointSourcesSummary)
	}
}

// 场景：IM 事件按来源去重并汇入全局 summary，跨来源重复事件只计一次。
func TestBuildImpactDocumentSummarizesIMEventsBySource(t *testing.T) {
	rootA := testRootImpact("change:a", "func:example.com/project::A", "a.go", "A", "GET", "/a")
	rootA.IMEvents = []impact.IMEventImpact{{Event: "inbox_msg"}, {Event: "inbox_customer_msg"}}
	rootB := testRootImpact("change:b", "func:example.com/project::B", "b.go", "B", "POST", "/b")
	rootB.IMEvents = []impact.IMEventImpact{{Event: "inbox_msg"}}

	doc := BuildImpactDocument(
		[]diff.FileChange{{NewPath: "b.go"}, {NewPath: "a.go"}},
		impact.TreeResult{Roots: []impact.RootImpact{rootB, rootA}},
		ImpactDocumentOptions{},
	)

	wantGlobal := []string{"inbox_customer_msg", "inbox_msg"}
	if doc.Summary.ImpactedIMCount != len(wantGlobal) ||
		!reflect.DeepEqual(doc.Summary.ImpactedIMEvents, wantGlobal) {
		t.Fatalf("summary = %#v", doc.Summary)
	}
	if len(doc.FileSources) != 2 {
		t.Fatalf("fileSources = %#v", doc.FileSources)
	}
	if !reflect.DeepEqual(doc.FileSources[0].ImpactedIMEvents, wantGlobal) {
		t.Fatalf("a.go events = %#v", doc.FileSources[0].ImpactedIMEvents)
	}
	if !reflect.DeepEqual(doc.FileSources[1].ImpactedIMEvents, []string{"inbox_msg"}) {
		t.Fatalf("b.go events = %#v", doc.FileSources[1].ImpactedIMEvents)
	}
}

// 场景：普通文件来源与 go.mod 模块来源分离，模块来源按 usage 入口聚合传播树并强化 basis。
func TestBuildImpactDocumentSeparatesFileAndModuleSources(t *testing.T) {
	fileChanges := []diff.FileChange{
		{NewPath: "controller/checkin.go", Raw: "diff --git a/controller/checkin.go b/controller/checkin.go\n"},
		{NewPath: "go.mod", Raw: "diff --git a/go.mod b/go.mod\n"},
	}
	fileRoot := testRootImpact(
		"change:file",
		"func:example.com/project/controller::CheckIn",
		"controller/checkin.go",
		"CheckIn",
		"POST",
		"/checkIn",
	)
	fileRoot.Change.Source = "git_diff"
	moduleRoot := testRootImpact(
		"change:module",
		"func:example.com/project/util::ParsePrice",
		"util/price.go",
		"ParsePrice",
		"GET",
		"/products",
	)
	moduleRoot.Change.Source = "go_mod_diff"
	moduleRoot.Change.SourceFactID = "module_usage:decimal"
	moduleRoot.Root.Children = []impact.Node{{
		ID:         "func:example.com/project/model::ConvertPrice",
		Kind:       "func",
		Name:       "ConvertPrice",
		File:       "model/price.go",
		Relation:   "call",
		Raw:        "transform.ParsePrice(value)",
		Confidence: facts.ConfidenceHigh,
		Level:      1,
		Children: []impact.Node{{
			ID:         "endpoint:GET:/products",
			Kind:       "endpoint",
			Name:       "GET /products",
			Method:     "GET",
			Path:       "/products",
			Relation:   "resolved_endpoint",
			Confidence: facts.ConfidenceHigh,
			Level:      2,
			Children:   []impact.Node{},
		}},
	}}
	fallbackRoot := testRootImpact(
		"change:module-fallback",
		"func:example.com/project/util::FormatPrice",
		"util/format.go",
		"FormatPrice",
		"",
		"",
	)
	fallbackRoot.Change.Source = "go_mod_diff"
	fallbackRoot.Change.SourceFactID = "module_usage:decimal-fallback"

	doc := BuildImpactDocument(fileChanges, impact.TreeResult{
		Roots: []impact.RootImpact{fileRoot, moduleRoot, fallbackRoot},
	}, ImpactDocumentOptions{
		ModuleChanges: []facts.ModuleChangeFact{{
			ID:         "module_change:decimal",
			Path:       "github.com/shopspring/decimal",
			Kind:       facts.ModuleChangeUpgraded,
			OldVersion: "v1.3.1",
			NewVersion: "v1.4.0",
		}},
		ModuleUsages: []facts.ModuleUsageFact{{
			ID:         "module_usage:decimal",
			ModulePath: "github.com/shopspring/decimal",
			ImportPath: "github.com/shopspring/decimal",
			Basis:      facts.ModuleUsagePrecise,
			SymbolID:   "func:example.com/project/util::ParsePrice",
			File:       "util/price.go",
			Confidence: facts.ConfidenceHigh,
		}, {
			ID:         "module_usage:decimal-fallback",
			ModulePath: "github.com/shopspring/decimal",
			ImportPath: "github.com/shopspring/decimal",
			Basis:      facts.ModuleUsageFileFallback,
			SymbolID:   "func:example.com/project/util::FormatPrice",
			File:       "util/format.go",
			Confidence: facts.ConfidenceMedium,
		}},
	})

	if len(doc.FileSources) != 1 || doc.FileSources[0].SourceFile != "controller/checkin.go" {
		t.Fatalf("fileSources = %#v", doc.FileSources)
	}
	if len(doc.ModuleSources) != 1 {
		t.Fatalf("moduleSources = %#v", doc.ModuleSources)
	}
	moduleSource := doc.ModuleSources[0]
	if moduleSource.ModulePath != "github.com/shopspring/decimal" ||
		moduleSource.ChangeType != facts.ModuleChangeUpgraded ||
		moduleSource.VersionBefore != "v1.3.1" ||
		moduleSource.VersionAfter != "v1.4.0" {
		t.Fatalf("module source = %#v", moduleSource)
	}
	if moduleSource.Basis != "matched_import_usage" {
		t.Fatalf("basis = %q", moduleSource.Basis)
	}
	if len(moduleSource.SourceFiles) != 2 ||
		moduleSource.SourceFiles[1].SourceFile != "util/price.go" ||
		len(moduleSource.SourceFiles[1].Symbols) != 1 ||
		len(moduleSource.SourceFiles[1].ImpactedEndpoints) != 1 {
		t.Fatalf("module source files = %#v", moduleSource.SourceFiles)
	}
	moduleTree := moduleSource.SourceFiles[1].Symbols["func:example.com/project/util::ParsePrice"]
	if len(moduleTree.Children) != 1 ||
		moduleTree.Children[0].Name != "ConvertPrice" ||
		len(moduleTree.Children[0].Children) != 1 ||
		moduleTree.Children[0].Children[0].Kind != "endpoint" {
		t.Fatalf("module propagation tree = %#v", moduleTree)
	}
	if doc.Summary.ImpactedEndpointCount != 2 {
		t.Fatalf("summary = %#v", doc.Summary)
	}

	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"module_changes"`)) ||
		bytes.Contains(payload, []byte(`"module_usages"`)) {
		t.Fatalf("retired module fact arrays remain in impact output: %s", payload)
	}
}

// 场景：moduleSources 中的 usage 入口也会汇总到 endpointSourcesSummary，并携带 module 版本元数据。
func TestBuildImpactDocumentAddsEndpointSourcesSummaryForModuleSources(t *testing.T) {
	moduleChange := facts.ModuleChangeFact{
		Path:       "example.com/lib",
		Kind:       facts.ModuleChangeUpgraded,
		OldVersion: "v1.0.0",
		NewVersion: "v1.1.0",
	}
	moduleUsage := facts.ModuleUsageFact{
		ID:         "module_usage:example.com/lib:service/payment.go",
		ModulePath: "example.com/lib",
		File:       "service/payment.go",
		SymbolID:   "func:example.com/app/service::Pay",
		Basis:      facts.ModuleUsagePrecise,
		Confidence: facts.ConfidenceMedium,
	}
	root := impact.RootImpact{
		Change: facts.ChangeFact{
			File:         moduleUsage.File,
			SymbolID:     moduleUsage.SymbolID,
			Source:       "go_mod_diff",
			SourceFactID: moduleUsage.ID,
			Confidence:   facts.ConfidenceMedium,
		},
		Root: impact.Node{
			ID:         string(moduleUsage.SymbolID),
			Kind:       "func",
			Name:       "Pay",
			File:       "service/payment.go",
			Confidence: facts.ConfidenceMedium,
			Children: []impact.Node{{
				ID:         "endpoint:POST:/pay",
				Kind:       "endpoint",
				Name:       "POST /pay",
				Relation:   "route_endpoint",
				Confidence: facts.ConfidenceMedium,
				Method:     "POST",
				Path:       "/pay",
			}},
		},
		Endpoints: []impact.EndpointImpact{{Method: "POST", Path: "/pay"}},
	}

	doc := BuildImpactDocument(nil, impact.TreeResult{Roots: []impact.RootImpact{root}}, ImpactDocumentOptions{
		ModuleChanges: []facts.ModuleChangeFact{moduleChange},
		ModuleUsages:  []facts.ModuleUsageFact{moduleUsage},
	})

	if len(doc.EndpointSourcesSummary) != 1 || len(doc.EndpointSourcesSummary[0].Sources) != 1 {
		t.Fatalf("endpointSourcesSummary = %#v", doc.EndpointSourcesSummary)
	}
	got := doc.EndpointSourcesSummary[0].Sources[0]
	if got.SourceType != "module" || got.ModulePath != "example.com/lib" || got.SourceFile != "service/payment.go" {
		t.Fatalf("module endpoint source = %#v", got)
	}
	if got.ChangeType != facts.ModuleChangeUpgraded || got.VersionBefore != "v1.0.0" || got.VersionAfter != "v1.1.0" {
		t.Fatalf("module metadata = %#v", got)
	}
	if got.Confidence != facts.ConfidenceMedium {
		t.Fatalf("confidence = %q", got.Confidence)
	}
}

// 场景：JSON 顶层字段顺序把 endpointSourcesSummary 放在 fileSources/moduleSources 之后，便于人工阅读。
func TestRenderImpactTreeJSONPlacesEndpointSourcesSummaryLast(t *testing.T) {
	doc := ImpactDocument{
		Summary: ImpactSummary{ImpactedEndpoints: []EndpointSummary{{Method: "GET", Path: "/x"}}},
		FileSources: []FileSourceImpact{{
			SourceFile:        "a.go",
			Symbols:           map[string]ImpactNode{},
			ImpactedEndpoints: []EndpointSummary{{Method: "GET", Path: "/x"}},
			ImpactedIMEvents:  []string{},
		}},
		ModuleSources: []ModuleSourceImpact{{
			ModulePath: "example.com/lib",
			ChangeType: facts.ModuleChangeUpgraded,
			Basis:      string(facts.ModuleUsagePrecise),
		}},
		EndpointSourcesSummary: []EndpointSourceSummary{{Method: "GET", Path: "/x"}},
	}
	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	fileIdx := strings.Index(text, `"fileSources"`)
	moduleIdx := strings.Index(text, `"moduleSources"`)
	summaryIdx := strings.Index(text, `"endpointSourcesSummary"`)
	if !(fileIdx >= 0 && moduleIdx > fileIdx && summaryIdx > moduleIdx) {
		t.Fatalf("unexpected field order: %s", text)
	}
}

// 场景：replace 变更的 before/after 替换目标 path/version 正确投影到 moduleSources。
func TestBuildImpactDocumentPreservesModuleReplacements(t *testing.T) {
	doc := BuildImpactDocument(
		[]diff.FileChange{{NewPath: "go.mod"}},
		impact.TreeResult{},
		ImpactDocumentOptions{
			ModuleChanges: []facts.ModuleChangeFact{{
				Path:              "example.com/sdk",
				Kind:              facts.ModuleChangeReplaced,
				OldReplacePath:    "example.com/sdk-fork",
				OldReplaceVersion: "v1.0.0",
				NewReplacePath:    "../sdk",
			}},
		},
	)

	if len(doc.ModuleSources) != 1 {
		t.Fatalf("moduleSources = %#v", doc.ModuleSources)
	}
	source := doc.ModuleSources[0]
	if source.ReplacementBefore == nil ||
		source.ReplacementBefore.Path != "example.com/sdk-fork" ||
		source.ReplacementBefore.Version != "v1.0.0" {
		t.Fatalf("replacementBefore = %#v", source.ReplacementBefore)
	}
	if source.ReplacementAfter == nil || source.ReplacementAfter.Path != "../sdk" {
		t.Fatalf("replacementAfter = %#v", source.ReplacementAfter)
	}
}

// 场景：多个 root 共享的递归子树内嵌在各自所属来源的 symbols 下，无需顶层 nodes 字段。
func TestBuildImpactDocumentEmbedsRecursiveTreesInOwningSource(t *testing.T) {
	shared := impact.Node{
		ID:         "func:example.com/project/service::Shared",
		Kind:       "func",
		Name:       "Shared",
		File:       "service/shared.go",
		Relation:   "call",
		Confidence: facts.ConfidenceMedium,
		Children: []impact.Node{{
			ID:         "endpoint:GET:/shared",
			Kind:       "endpoint",
			Name:       "GET /shared",
			Method:     "GET",
			Path:       "/shared",
			Relation:   "resolved_endpoint",
			Confidence: facts.ConfidenceHigh,
			Level:      2,
			Children:   []impact.Node{},
		}},
	}
	rootA := rawTestRoot("change:a", "func:example.com/project/controller::A", "controller/a.go", "A", shared)
	rootB := rawTestRoot("change:b", "func:example.com/project/controller::B", "controller/a.go", "B", shared)

	doc := BuildImpactDocument([]diff.FileChange{{
		NewPath: "controller/a.go",
		Raw:     "diff --git a/controller/a.go b/controller/a.go\n+changed\n",
	}}, impact.TreeResult{Roots: []impact.RootImpact{rootA, rootB}}, ImpactDocumentOptions{})

	if len(doc.FileSources) != 1 {
		t.Fatalf("fileSources = %#v", doc.FileSources)
	}
	source := doc.FileSources[0]
	if len(source.Symbols) != 2 {
		t.Fatalf("symbols = %#v", source.Symbols)
	}
	if source.Diff == "" {
		t.Fatal("ordinary source diff should be retained")
	}
	for _, rootID := range []string{
		"func:example.com/project/controller::A",
		"func:example.com/project/controller::B",
	} {
		node := source.Symbols[rootID]
		if len(node.Children) != 1 || node.Children[0].ID != "func:example.com/project/service::Shared" {
			t.Fatalf("root node %s = %#v", rootID, node)
		}
		sharedNode := node.Children[0]
		if sharedNode.Relation != "call" || sharedNode.Confidence != facts.ConfidenceMedium {
			t.Fatalf("shared node evidence = %#v", sharedNode)
		}
		if len(sharedNode.Children) != 1 ||
			sharedNode.Children[0].Kind != "endpoint" ||
			sharedNode.Children[0].Method != "GET" ||
			sharedNode.Children[0].Path != "/shared" {
			t.Fatalf("endpoint chain missing from %s: %#v", rootID, sharedNode.Children)
		}
	}

	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"nodes"`)) {
		t.Fatalf("top-level nodes should not be required to read source chains: %s", payload)
	}
}

// 场景：对外 JSON 保留 raw/relation/level/confidence 等 review 证据，但省略 span/meta/nodes。
func TestRenderRawImpactTreeKeepsReviewEvidenceButOmitsSpan(t *testing.T) {
	root := rawTestRoot(
		"change:a",
		"func:example.com/project/controller::A",
		"controller/a.go",
		"A",
		impact.Node{
			ID:         "func:example.com/project/service::Shared",
			Kind:       "func",
			Name:       "Shared",
			File:       "service/shared.go",
			Package:    "example.com/project/service",
			Relation:   "call",
			Raw:        "service.Shared()",
			Span:       facts.SourceSpan{File: "service/shared.go", StartLine: 10, StartCol: 2, EndLine: 10, EndCol: 18},
			Confidence: facts.ConfidenceHigh,
			Level:      1,
			Children:   []impact.Node{},
		},
	)
	doc := BuildImpactDocument(
		[]diff.FileChange{{NewPath: "controller/a.go", Raw: "diff --git a/controller/a.go b/controller/a.go\n"}},
		impact.TreeResult{Roots: []impact.RootImpact{root}},
		ImpactDocumentOptions{},
	)

	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"meta"`,
		`"projectRoot"`,
		`"span"`,
		`"nodes"`,
	} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("forbidden field %s remains: %s", forbidden, payload)
		}
	}
	if !bytes.Contains(payload, []byte(`"diff": "diff --git`)) {
		t.Fatalf("source diff missing: %s", payload)
	}
	for _, required := range []string{
		`"symbols"`,
		`"raw": "service.Shared()"`,
		`"relation": "call"`,
		`"level": 1`,
		`"confidence": "high"`,
	} {
		if !bytes.Contains(payload, []byte(required)) {
			t.Fatalf("review evidence %s missing: %s", required, payload)
		}
	}
}

// 场景：递归子节点的 confidence 独立保留，不被父节点 confidence 覆盖。
func TestRawImpactTreePreservesConfidenceOnRecursiveNodes(t *testing.T) {
	root := rawTestRoot(
		"change:a",
		"func:example.com/project/controller::A",
		"controller/a.go",
		"A",
		impact.Node{
			ID:         "func:example.com/project/service::Fallback",
			Kind:       "func",
			Name:       "Fallback",
			File:       "service/fallback.go",
			Relation:   "call",
			Confidence: facts.ConfidenceMedium,
			Children:   []impact.Node{},
		},
	)
	root.Change.Confidence = facts.ConfidenceLow
	doc := BuildImpactDocument(
		[]diff.FileChange{{NewPath: "controller/a.go"}},
		impact.TreeResult{Roots: []impact.RootImpact{root}},
		ImpactDocumentOptions{},
	)

	rootNode := doc.FileSources[0].Symbols["func:example.com/project/controller::A"]
	if rootNode.Confidence != facts.ConfidenceHigh {
		t.Fatalf("root confidence = %#v", rootNode)
	}
	if rootNode.Children[0].Confidence != facts.ConfidenceMedium {
		t.Fatalf("child confidence = %#v", rootNode.Children[0])
	}
}

// rawTestRoot 构造一个带单个子节点与端点的测试 RootImpact，使用真实 impact.Node。
func rawTestRoot(changeID, rootID, file, name string, child impact.Node) impact.RootImpact {
	return impact.RootImpact{
		Change: facts.ChangeFact{
			ID:         changeID,
			File:       file,
			SymbolID:   facts.SymbolID(rootID),
			Confidence: facts.ConfidenceHigh,
		},
		Root: impact.Node{
			ID:         rootID,
			Kind:       "func",
			Name:       name,
			File:       file,
			Confidence: facts.ConfidenceHigh,
			Children:   []impact.Node{child},
		},
		Endpoints: []impact.EndpointImpact{{
			ID:     "endpoint:GET:/shared",
			Method: "GET",
			Path:   "/shared",
		}},
	}
}

// testRootImpact 构造一个简化的测试 RootImpact，根节点为 type，可附带端点。
func testRootImpact(changeID, symbolID, file, name, method, path string) impact.RootImpact {
	return impact.RootImpact{
		Change: facts.ChangeFact{ID: changeID, File: file, SymbolID: facts.SymbolID(symbolID)},
		Root: impact.Node{
			ID:       symbolID,
			Kind:     "type",
			Name:     name,
			File:     file,
			Children: []impact.Node{},
		},
		Endpoints: []impact.EndpointImpact{{
			ID:     "endpoint:" + method + ":" + path,
			Method: method,
			Path:   path,
		}},
	}
}
