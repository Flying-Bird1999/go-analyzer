package app

import (
	"encoding/json"
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
