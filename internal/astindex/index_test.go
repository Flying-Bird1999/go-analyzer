// index_test.go 测试声明符号索引与轻量 value-type 推断的核心行为。
package astindex

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestBuildIndexesDeclarationSymbols 验证 mini-bff fixture 下 func/method/type/var/const 五类声明都能建出符号 ID 且 span 携带源码文件。
func TestBuildIndexesDeclarationSymbols(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"const:example.com/mini-bff/controller::DefaultChannel",
		"func:example.com/mini-bff/service::CheckIn",
		"func:example.com/mini-bff/router::Register",
		"method:example.com/mini-bff/controller:CommonController:CheckIn",
		"type:example.com/mini-bff/controller::CommonController",
		"var:example.com/mini-bff/controller::Common",
	}
	for _, id := range want {
		symbolID := facts.SymbolID(id)
		if _, ok := idx.Symbols[symbolID]; !ok {
			t.Fatalf("symbol %s not found; got %#v", id, idx.Symbols)
		}
		if idx.Symbols[symbolID].Span.File == "" {
			t.Fatalf("symbol %s has empty source file", id)
		}
	}
}

// TestBuildDisambiguatesDuplicateInitSymbols 回归 P2-3：同包多个 func init()（Go 唯一允许
// 同名的声明）必须各自保留独立符号与 span，而不是共用 func:<pkg>::init 相互覆盖。否则命中
// 被覆盖 init 函数体的 diff 会丢失 symbol 级根、降级为 file_changed。
func TestBuildDisambiguatesDuplicateInitSymbols(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/initpkg\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc init() { _ = 1 }\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z.go"), []byte("package main\n\nfunc init() { _ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	inits := 0
	for id, symbol := range idx.Symbols {
		if symbol.Name == "init" {
			inits++
			if symbol.Span.File == "" {
				t.Fatalf("init symbol %q has empty file", id)
			}
		}
	}
	if inits != 2 {
		t.Fatalf("expected 2 distinct init symbols, got %d: %#v", inits, idx.Symbols)
	}
}

func TestIndexIsProjectPackage(t *testing.T) {
	idx := &Index{Project: &project.Project{ModulePath: "example.com/app"}}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "module root", path: "example.com/app", want: true},
		{name: "child package", path: "example.com/app/service", want: true},
		{name: "similar prefix", path: "example.com/application", want: false},
		{name: "external", path: "example.com/other", want: false},
		{name: "empty", path: "", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := idx.IsProjectPackage(tt.path); got != tt.want {
				t.Fatalf("IsProjectPackage(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestBuildUsesCompleteDeclarationSpans 验证 type 与 var 声明的 span 覆盖完整声明体，而非仅首行。
func TestBuildUsesCompleteDeclarationSpans(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "declaration-spans")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}

	typeSymbol := mustSymbol(t, idx, "type:example.com/declaration-spans::Request")
	if typeSymbol.Span.EndLine <= typeSymbol.Span.StartLine {
		t.Fatalf("type span does not cover body: %#v", typeSymbol.Span)
	}

	valueSymbol := mustSymbol(t, idx, "var:example.com/declaration-spans::DefaultRequest")
	if valueSymbol.Span.EndLine <= valueSymbol.Span.StartLine {
		t.Fatalf("value span does not cover declaration: %#v", valueSymbol.Span)
	}
}

// TestBuildIndexesNewBuiltinReceiverType 验证 new(T) 初始化的包级 var 能解析到 receiver 类型上的方法。
func TestBuildIndexesNewBuiltinReceiverType(t *testing.T) {
	idx, file := buildValueTypeFixture(t)

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"DefaultCache", "Read"})
	if !ok {
		t.Fatal("DefaultCache.Read was not resolved")
	}
	want := facts.SymbolID("method:example.com/value-types:Cache:Read")
	if resolved.ID != want || resolved.Confidence != facts.ConfidenceHigh {
		t.Fatalf("DefaultCache.Read = %#v, want %s with high confidence", resolved, want)
	}
}

// TestBuildIndexesTypedConstReceiverType 验证显式 typed const 能解析到其基础类型上的方法。
func TestBuildIndexesTypedConstReceiverType(t *testing.T) {
	idx, file := buildValueTypeFixture(t)

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"DefaultCode", "String"})
	if !ok {
		t.Fatal("DefaultCode.String was not resolved")
	}
	want := facts.SymbolID("method:example.com/value-types:Code:String")
	if resolved.ID != want || resolved.Confidence != facts.ConfidenceHigh {
		t.Fatalf("DefaultCode.String = %#v, want %s with high confidence", resolved, want)
	}
}

// buildValueTypeFixture 构造一个包含 new(T) 与 typed const 的最小 fixture，并返回索引与对应文件供测试使用。
func buildValueTypeFixture(t *testing.T) (*Index, *project.File) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/value-types\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package valuetypes

type Cache struct{}

func (*Cache) Read() {}

type Code string

func (Code) String() string { return "" }

var DefaultCache = new(Cache)

const DefaultCode Code = "default"
`
	if err := os.WriteFile(filepath.Join(root, "values.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	pkg := p.Packages[p.ModulePath]
	if pkg == nil || len(pkg.Files) != 1 {
		t.Fatalf("fixture files = %#v", pkg)
	}
	return idx, pkg.Files[0]
}

// mustSymbol 断言索引中存在指定符号并返回该 SymbolFact，缺失则失败。
func mustSymbol(t *testing.T, idx *Index, id facts.SymbolID) facts.SymbolFact {
	t.Helper()
	symbol, ok := idx.Symbols[id]
	if !ok {
		t.Fatalf("symbol %s not found", id)
	}
	return symbol
}
