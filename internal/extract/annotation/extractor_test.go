// extractor_test.go 校验 annotation 包的注解解析与提取逻辑。
package annotation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestParseAPIAnnotations 场景：同一函数多条注解应全部解析，且路径自动补齐 "/"。
func TestParseAPIAnnotations(t *testing.T) {
	src := `package p
// @Get /ready
// @Post ready
// @Refactor ignored
func CheckIn() {}
`
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	got := ParseAPIAnnotations(decl.Doc)
	if len(got) != 2 {
		t.Fatalf("annotation count = %d", len(got))
	}
	if got[0].Method != "GET" || got[0].Path != "/ready" {
		t.Fatalf("first annotation = %#v", got[0])
	}
	if got[1].Method != "POST" || got[1].Path != "/ready" {
		t.Fatalf("second annotation = %#v", got[1])
	}
	if got[0].Raw == "" {
		t.Fatal("raw comment line should be preserved")
	}
}

// TestParseAPIAnnotationsIgnoresNonHTTPMethods 场景：非内置 HTTP 方法前缀（如 @Search）应被忽略。
func TestParseAPIAnnotationsIgnoresNonHTTPMethods(t *testing.T) {
	src := `package p
// @Search /ready
// @Post /ignored
func CheckIn() {}
`
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	decl := file.Decls[0].(*ast.FuncDecl)

	got := ParseAPIAnnotations(decl.Doc)

	if len(got) != 1 {
		t.Fatalf("annotation count = %d: %#v", len(got), got)
	}
	if got[0].Method != "POST" || got[0].Path != "/ignored" {
		t.Fatalf("annotation = %#v", got[0])
	}
}

// TestExtractAnnotationFacts 场景：从 annotation-only fixture 提取注解事实，校验方法/路径、handler symbol 与精确到注释行的 span。
func TestExtractAnnotationFacts(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "annotation-only")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	if len(store.Annotations) != 2 {
		t.Fatalf("annotation facts = %d", len(store.Annotations))
	}
	first := store.Annotations[0]
	if first.Method != "GET" || first.Path != "/api/bff-web/common/checkIn" {
		t.Fatalf("first annotation = %#v", first)
	}
	if first.HandlerSymbol != "func:example.com/annotation-only/controller::CheckIn" {
		t.Fatalf("handler symbol = %q", first.HandlerSymbol)
	}
	if first.Span.File == "" {
		t.Fatal("annotation span file is empty")
	}
	if first.Span.StartLine != 4 || first.Span.EndLine != 4 {
		t.Fatalf("first annotation span = %#v", first.Span)
	}
	if store.Annotations[1].Span.StartLine != 5 || store.Annotations[1].Span.EndLine != 5 {
		t.Fatalf("second annotation span = %#v", store.Annotations[1].Span)
	}
}
