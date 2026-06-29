package output

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

func TestBuildImpactDocumentGroupsRootsBySourceFile(t *testing.T) {
	project := facts.ProjectFact{Root: "/tmp/project", ModulePath: "example.com/project"}
	fileChanges := []diff.FileChange{{
		NewPath: "model/model.go",
		Raw:     "diff --git a/model/model.go b/model/model.go\n+changed\n",
	}}
	result := impact.TreeResult{Roots: []impact.RootImpact{
		testRootImpact("change:address", "type:example.com/project/model::Address", "model/model.go", "Address", "POST", "/orders"),
		testRootImpact("change:request", "type:example.com/project/model::CreateOrderRequest", "model/model.go", "CreateOrderRequest", "POST", "/orders"),
	}}

	doc := BuildImpactDocument(project, fileChanges, result, ImpactDocumentOptions{})
	if len(doc.FileSources) != 1 {
		t.Fatalf("fileSources = %d", len(doc.FileSources))
	}
	if doc.Summary.ImpactedEndpointCount != 1 || len(doc.Summary.ImpactedEndpoints) != 1 {
		t.Fatalf("summary = %#v", doc.Summary)
	}
	source := doc.FileSources[0]
	if len(source.Roots) != 2 {
		t.Fatalf("roots = %d", len(source.Roots))
	}
	if !strings.Contains(source.Diff, "diff --git") {
		t.Fatalf("diff missing: %q", source.Diff)
	}
	if len(source.ImpactedEndpoints) != 1 {
		t.Fatalf("impacted endpoints = %#v", source.ImpactedEndpoints)
	}
}

func TestRenderImpactTreeJSONIsDeterministic(t *testing.T) {
	project := facts.ProjectFact{Root: "/tmp/project", ModulePath: "example.com/project"}
	changeA := diff.FileChange{NewPath: "a.go", Raw: "diff --git a/a.go b/a.go\n"}
	changeB := diff.FileChange{NewPath: "b.go", Raw: "diff --git a/b.go b/b.go\n"}
	rootA := testRootImpact("change:a", "func:example.com/project::A", "a.go", "A", "GET", "/a")
	rootB := testRootImpact("change:b", "func:example.com/project::B", "b.go", "B", "POST", "/b")

	first := BuildImpactDocument(project, []diff.FileChange{changeB, changeA}, impact.TreeResult{
		Roots:       []impact.RootImpact{rootB, rootA},
		Diagnostics: []facts.DiagnosticFact{{ID: "diagnostic:b"}, {ID: "diagnostic:a"}},
	}, ImpactDocumentOptions{})
	second := BuildImpactDocument(project, []diff.FileChange{changeA, changeB}, impact.TreeResult{
		Roots:       []impact.RootImpact{rootA, rootB},
		Diagnostics: []facts.DiagnosticFact{{ID: "diagnostic:a"}, {ID: "diagnostic:b"}},
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

func TestBuildImpactDocumentKeepsRootWithNoEndpointAndDedupesDiagnostics(t *testing.T) {
	project := facts.ProjectFact{Root: "/tmp/project", ModulePath: "example.com/project"}
	root := testRootImpact("change:orphan", "func:example.com/project::Orphan", "orphan.go", "Orphan", "", "")
	root.Endpoints = nil
	diagnostic := facts.DiagnosticFact{ID: "diagnostic:unresolved", Code: "symbol_reference_unresolved"}

	doc := BuildImpactDocument(project, nil, impact.TreeResult{
		Roots:       []impact.RootImpact{root},
		Diagnostics: []facts.DiagnosticFact{diagnostic, diagnostic},
	}, ImpactDocumentOptions{})
	if len(doc.FileSources) != 1 {
		t.Fatalf("fileSources = %#v", doc.FileSources)
	}
	if len(doc.FileSources[0].Roots) != 1 || len(doc.FileSources[0].ImpactedEndpoints) != 0 {
		t.Fatalf("source = %#v", doc.FileSources[0])
	}
	if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Code != "symbol_reference_unresolved" {
		t.Fatalf("diagnostics = %#v", doc.Diagnostics)
	}
	if doc.Summary.ImpactedEndpointCount != 0 || len(doc.Summary.ImpactedEndpoints) != 0 {
		t.Fatalf("summary = %#v", doc.Summary)
	}
}

func TestBuildImpactDocumentSeparatesFileAndModuleSources(t *testing.T) {
	project := facts.ProjectFact{Root: "/tmp/project", ModulePath: "example.com/project"}
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

	doc := BuildImpactDocument(project, fileChanges, impact.TreeResult{
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
		len(moduleSource.SourceFiles[1].ImpactedEndpoints) != 1 {
		t.Fatalf("module source files = %#v", moduleSource.SourceFiles)
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

func TestRenderImpactTreeJSONOmitsEmptyDiagnostics(t *testing.T) {
	doc := ImpactDocument{
		Summary:     ImpactSummary{ImpactedEndpoints: []EndpointSummary{}},
		FileSources: []FileSourceImpact{},
		Nodes:       map[string]ImpactGraphNode{},
	}

	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"diagnostics"`)) {
		t.Fatalf("empty diagnostics should be omitted: %s", payload)
	}
}

func TestBuildImpactDocumentPreservesModuleReplacements(t *testing.T) {
	doc := BuildImpactDocument(
		facts.ProjectFact{Root: "/tmp/project"},
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

func TestBuildImpactDocumentDeduplicatesNodesAndReferencesRoots(t *testing.T) {
	project := facts.ProjectFact{Root: "/tmp/project"}
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
			Confidence: facts.ConfidenceHigh,
			Children:   []impact.Node{},
		}},
	}
	rootA := compactTestRoot("change:a", "func:example.com/project/controller::A", "controller/a.go", "A", shared)
	rootB := compactTestRoot("change:b", "func:example.com/project/controller::B", "controller/a.go", "B", shared)

	doc := BuildImpactDocument(project, []diff.FileChange{{
		NewPath: "controller/a.go",
		Raw:     "diff --git a/controller/a.go b/controller/a.go\n+changed\n",
	}}, impact.TreeResult{Roots: []impact.RootImpact{rootA, rootB}}, ImpactDocumentOptions{})

	if len(doc.FileSources) != 1 {
		t.Fatalf("fileSources = %#v", doc.FileSources)
	}
	source := doc.FileSources[0]
	if len(source.Roots) != 2 {
		t.Fatalf("roots = %#v", source.Roots)
	}
	if source.Diff == "" {
		t.Fatal("ordinary source diff should be retained")
	}
	if len(doc.Nodes) != 3 {
		t.Fatalf("nodes = %#v", doc.Nodes)
	}
	if _, ok := doc.Nodes["endpoint:GET:/shared"]; ok {
		t.Fatalf("endpoint should not be projected as a graph node: %#v", doc.Nodes)
	}
	sharedNode := doc.Nodes["func:example.com/project/service::Shared"]
	if len(sharedNode.Children) != 0 {
		t.Fatalf("endpoint edge should be omitted: %#v", sharedNode.Children)
	}
	for _, rootID := range []string{
		"func:example.com/project/controller::A",
		"func:example.com/project/controller::B",
	} {
		node := doc.Nodes[rootID]
		if len(node.Children) != 1 ||
			node.Children[0].To != "func:example.com/project/service::Shared" ||
			node.Children[0].Relation != "call" {
			t.Fatalf("root node %s = %#v", rootID, node)
		}
	}
}

func TestRenderCompactImpactJSONOmitsSpanAndDebugFields(t *testing.T) {
	root := compactTestRoot(
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
		facts.ProjectFact{Root: "/absolute/project"},
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
		`"raw"`,
		`"package"`,
		`"level"`,
		`"confidence": "high"`,
	} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("forbidden field %s remains: %s", forbidden, payload)
		}
	}
	if !bytes.Contains(payload, []byte(`"diff": "diff --git`)) {
		t.Fatalf("source diff missing: %s", payload)
	}
}

func TestCompactImpactKeepsOnlyNonHighConfidence(t *testing.T) {
	root := compactTestRoot(
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
		facts.ProjectFact{},
		[]diff.FileChange{{NewPath: "controller/a.go"}},
		impact.TreeResult{Roots: []impact.RootImpact{root}},
		ImpactDocumentOptions{},
	)

	if doc.FileSources[0].Roots[0].Confidence != facts.ConfidenceLow {
		t.Fatalf("root confidence = %#v", doc.FileSources[0].Roots)
	}
	edge := doc.Nodes["func:example.com/project/controller::A"].Children[0]
	if edge.Confidence != facts.ConfidenceMedium {
		t.Fatalf("edge confidence = %#v", edge)
	}
}

func TestCompactImpactProjectsDiagnosticsWithoutSpan(t *testing.T) {
	doc := BuildImpactDocument(
		facts.ProjectFact{},
		nil,
		impact.TreeResult{Diagnostics: []facts.DiagnosticFact{{
			ID:       "diagnostic:unresolved",
			Code:     "symbol_reference_unresolved",
			Severity: "warning",
			Message:  "reference could not be resolved",
			Span:     facts.SourceSpan{File: "controller/a.go", StartLine: 10, StartCol: 2, EndLine: 10, EndCol: 8},
		}}},
		ImpactDocumentOptions{},
	)

	if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].File != "controller/a.go" {
		t.Fatalf("diagnostics = %#v", doc.Diagnostics)
	}
	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"span"`)) {
		t.Fatalf("diagnostic span should be omitted: %s", payload)
	}
}

func compactTestRoot(changeID, rootID, file, name string, child impact.Node) impact.RootImpact {
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
