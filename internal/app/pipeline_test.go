package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
)

func TestRunFactsRequiresProjectPath(t *testing.T) {
	_, err := RunFacts(Options{Format: "json"})
	if err == nil {
		t.Fatal("expected error when project path is empty")
	}
}

func TestRunFactsOnMiniBFFReturnsProjectMetadata(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	got, err := RunFacts(Options{ProjectPath: root, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}

	var doc output.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Project.ModulePath != "example.com/mini-bff" {
		t.Fatalf("module path = %q", doc.Project.ModulePath)
	}
	if len(doc.Symbols) == 0 {
		t.Fatal("expected symbols in facts output")
	}
}

func TestRunFactsIncludesAnnotationFacts(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "annotation-only")
	got, err := RunFacts(Options{ProjectPath: root, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}

	var doc output.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Annotations) != 2 {
		t.Fatalf("annotations = %d", len(doc.Annotations))
	}
}

func TestRunFactsIncludesRouteFacts(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "controller-wrapper")
	got, err := RunFacts(Options{ProjectPath: root, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}

	var doc output.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RouteGroups) != 1 {
		t.Fatalf("route groups = %d", len(doc.RouteGroups))
	}
	if len(doc.Routes) != 1 {
		t.Fatalf("routes = %d", len(doc.Routes))
	}
}

func TestRunFactsIncludesLinksAndReferences(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "utility-fanout")
	got, err := RunFacts(Options{ProjectPath: root, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}

	var doc output.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Routes) != 1 {
		t.Fatalf("routes = %d", len(doc.Routes))
	}
	if doc.Routes[0].HandlerSymbol == "" {
		t.Fatal("route handler symbol is empty")
	}
	if len(doc.Links) == 0 {
		t.Fatal("expected links")
	}
	if len(doc.References) == 0 {
		t.Fatal("expected references")
	}
}

func TestRunFactsIncludesModuleDependencyFacts(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "gomod-change")
	got, err := RunFacts(Options{ProjectPath: root, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}

	var doc output.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Modules) != 2 {
		t.Fatalf("modules = %#v", doc.Modules)
	}
	for _, module := range doc.Modules {
		if module.Path == "github.com/gin-gonic/gin" && module.ReplaceVersion == "v1.10.1" {
			return
		}
	}
	t.Fatalf("replaced gin module not found: %#v", doc.Modules)
}

func TestRunFactsUsesConfigFile(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "configurable-rules"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "go-analyzer.json")
	configBody := []byte(`{
  "route": {
    "httpMethods": ["SEARCH"],
    "handlerWrappers": ["CustomController"],
    "routeGroupWrappers": [{"contains": "Shield"}]
  },
  "annotation": {
    "methods": ["Search"]
  }
}`)
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RunFacts(Options{ProjectPath: root, ConfigPath: configPath, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var doc output.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Annotations) != 1 || doc.Annotations[0].Method != "SEARCH" {
		t.Fatalf("annotations = %#v", doc.Annotations)
	}
	if len(doc.Routes) != 1 || doc.Routes[0].Method != "SEARCH" {
		t.Fatalf("routes = %#v", doc.Routes)
	}
}

func TestRunImpactMapsDiffToEndpoint(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "utility-fanout"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	diff := []byte(`diff --git a/service/common.go b/service/common.go
index 1111111..2222222 100644
--- a/service/common.go
+++ b/service/common.go
@@ -2,3 +2,4 @@ package service
 func CheckIn() string {
+	_ = "changed"
     return "ok"
 }
`)
	if err := os.WriteFile(diffPath, diff, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RunImpact(ImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var doc output.ImpactDocument
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	assertEndpointSummary(t, doc, "GET", "/api/bff-web/common/checkIn")
}

func TestRunImpactMapsStructChangeToEndpointTree(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "type-impact"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	patch := []byte("diff --git a/model/model.go b/model/model.go\n" +
		"--- a/model/model.go\n" +
		"+++ b/model/model.go\n" +
		"@@ -1,5 +1,5 @@\n" +
		" package model\n" +
		" \n" +
		" type Address struct {\n" +
		"-\tCity string `json:\"city_name\"`\n" +
		"+\tCity string `json:\"city\"`\n" +
		" }\n")
	if err := os.WriteFile(diffPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RunImpact(ImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var doc output.ImpactDocument
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	assertSourceRoot(t, doc, "model/model.go", "type:example.com/type-impact/model::Address")
	assertEndpointSummary(t, doc, "POST", "/orders")
}

func TestRunImpactIncludesRecoverableProjectLoadDiagnostics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/partial\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "valid.go"), []byte("package partial\n\nfunc Valid() {\n\t_ = 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package partial\n\nfunc Broken( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	patch := []byte("diff --git a/valid.go b/valid.go\n" +
		"--- a/valid.go\n" +
		"+++ b/valid.go\n" +
		"@@ -2,4 +2,4 @@ package partial\n" +
		" \n" +
		" func Valid() {\n" +
		"-\t_ = 0\n" +
		"+\t_ = 1\n" +
		" }\n")
	if err := os.WriteFile(diffPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RunImpact(ImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var doc output.ImpactDocument
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range doc.Meta.Diagnostics {
		if diagnostic.Code == "package_load_failed" && diagnostic.Span.File == "broken.go" {
			return
		}
	}
	t.Fatalf("package load diagnostic not found: %#v", doc.Meta.Diagnostics)
}

func assertSourceRoot(t *testing.T, doc output.ImpactDocument, sourceFile, rootID string) {
	t.Helper()
	for _, source := range doc.FileSources {
		if source.SourceFile != sourceFile {
			continue
		}
		if _, ok := source.Symbols[rootID]; ok {
			return
		}
		t.Fatalf("root %q not found in %q: %#v", rootID, sourceFile, source.Symbols)
	}
	t.Fatalf("source file %q not found: %#v", sourceFile, doc.FileSources)
}

func assertEndpointSummary(t *testing.T, doc output.ImpactDocument, method, path string) {
	t.Helper()
	for _, source := range doc.FileSources {
		for _, endpoint := range source.ImpactedEndpoints {
			if endpoint.Method == method && endpoint.Path == path {
				return
			}
		}
	}
	t.Fatalf("endpoint %s %s not found: %#v", method, path, doc.FileSources)
}
