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
	if len(doc.FileSources[0].Symbols) != 1 || len(doc.FileSources[0].ImpactedEndpoints) != 0 {
		t.Fatalf("source = %#v", doc.FileSources[0])
	}
	if len(doc.Meta.Diagnostics) != 1 || doc.Meta.Diagnostics[0].Code != "symbol_reference_unresolved" {
		t.Fatalf("diagnostics = %#v", doc.Meta.Diagnostics)
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
