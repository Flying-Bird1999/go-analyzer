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

func TestRunImpactMapsGoModDiffToEndpoint(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", `module example.com/gomod-impact

go 1.24

require gopkg.inshopline.com/sc1/commons/utils v1.0.1
`)
	writeTestFile(t, root, "service/common.go", `package service

import jsonx "gopkg.inshopline.com/sc1/commons/utils/jsonx"

func CheckIn(v any) string {
	return jsonx.String(v)
}
`)
	writeTestFile(t, root, "controller/common.go", `package controller

import "example.com/gomod-impact/service"

// @Get /api/checkIn
func CheckIn() string {
	return service.CheckIn("ok")
}
`)
	writeTestFile(t, root, "router/router.go", `package router

import common "example.com/gomod-impact/controller"

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	g.GET("/checkIn", common.CheckIn)
}
`)
	diffPath := filepath.Join(t.TempDir(), "gomod.diff")
	patch := []byte("diff --git a/go.mod b/go.mod\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/go.mod\n" +
		"+++ b/go.mod\n" +
		"@@ -2,4 +2,4 @@ module example.com/gomod-impact\n" +
		" \n" +
		" go 1.24\n" +
		" \n" +
		"-require gopkg.inshopline.com/sc1/commons/utils v1.0.0\n" +
		"+require gopkg.inshopline.com/sc1/commons/utils v1.0.1\n")
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
	if len(doc.ModuleChanges) != 1 || doc.ModuleChanges[0].Path != "gopkg.inshopline.com/sc1/commons/utils" {
		t.Fatalf("module changes = %#v", doc.ModuleChanges)
	}
	if len(doc.ModuleUsages) == 0 {
		t.Fatalf("module usages = %#v", doc.ModuleUsages)
	}
	assertEndpointSummary(t, doc, "GET", "/api/checkIn")
}

func TestRunImpactReportsUnresolvedGoModDiff(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example.com/gomod-unresolved\n\ngo 1.24\n")
	writeTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	diffPath := filepath.Join(t.TempDir(), "gomod.diff")
	patch := []byte("diff --git a/go.mod b/go.mod\n" +
		"--- a/go.mod\n" +
		"+++ b/go.mod\n" +
		"@@ -2,3 +2,3 @@ module example.com/gomod-unresolved\n" +
		" \n" +
		"-go 1.23\n" +
		"+go 1.24\n")
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
		if diagnostic.Code == "module_diff_unresolved" {
			return
		}
	}
	t.Fatalf("module_diff_unresolved diagnostic not found: %#v", doc.Meta.Diagnostics)
}

func TestRunImpactMapsMiddlewareMethodDiffToEndpoint(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example.com/middleware-impact\n\ngo 1.24\n")
	writeTestFile(t, root, "auth/auth.go", `package auth

var Default = NewAuth()

type Auth struct{}

func NewAuth() *Auth {
	return &Auth{}
}

func (a *Auth) Middleware() {
	_ = "old"
}
`)
	writeTestFile(t, root, "router/router.go", `package router

import auth "example.com/middleware-impact/auth"

type RouterGroup struct{}

func (g *RouterGroup) Use(middleware any) {}
func (g *RouterGroup) GET(path string, handler any) {}

func Handler() {}

func InitRouter(g *RouterGroup) {
	g.Use(auth.Default.Middleware)
	g.GET("/x", Handler)
}
`)
	diffPath := filepath.Join(t.TempDir(), "middleware.diff")
	patch := []byte("diff --git a/auth/auth.go b/auth/auth.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/auth/auth.go\n" +
		"+++ b/auth/auth.go\n" +
		"@@ -8,5 +8,5 @@ func NewAuth() *Auth {\n" +
		" }\n" +
		" \n" +
		" func (a *Auth) Middleware() {\n" +
		"-\t_ = \"old\"\n" +
		"+\t_ = \"new\"\n" +
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
	assertEndpointSummary(t, doc, "GET", "/x")
}

func TestRunImpactRecoversMultilineDeletedRouteAndHandlerAnnotation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module example.com/deleted-route\n\ngo 1.24\n")
	writeTestFile(t, root, "controller/order.go", `package controller

// @Post /public/orders
func Create() {}
`)
	writeTestFile(t, root, "router/router.go", `package router

import "example.com/deleted-route/controller"

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}
func (g *RouterGroup) POST(path string, handler any) {}

func Health() {}

func Init(g *RouterGroup) {
	g.GET("/health", Health)
	_ = controller.Create
}
`)
	diffPath := filepath.Join(t.TempDir(), "deleted-route.diff")
	patch := []byte("diff --git a/router/router.go b/router/router.go\n" +
		"--- a/router/router.go\n" +
		"+++ b/router/router.go\n" +
		"@@ -12,8 +12,4 @@ func Init(g *RouterGroup) {\n" +
		"-\tg.POST(\n" +
		"-\t\t\"/internal/orders\",\n" +
		"-\t\tcontroller.Create,\n" +
		"-\t)\n" +
		" \tg.GET(\"/health\", Health)\n" +
		" \t_ = controller.Create\n" +
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
	assertEndpointSummary(t, doc, "POST", "/public/orders")
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

func writeTestFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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
