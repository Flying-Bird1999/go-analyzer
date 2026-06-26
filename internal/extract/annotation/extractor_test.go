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

func TestExtractAnnotationFacts(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "annotation-only")
	p, err := project.Load(root, project.Options{})
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
}
