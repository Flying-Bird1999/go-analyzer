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
}

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
