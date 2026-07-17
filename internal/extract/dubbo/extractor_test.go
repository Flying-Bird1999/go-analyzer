package dubbo

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractProviderMethods(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "grpc-service")
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
	if len(store.DubboProviders) != 3 {
		t.Fatalf("dubbo providers = %#v", store.DubboProviders)
	}
	var foundReply, foundSecond bool
	for _, provider := range store.DubboProviders {
		if provider.Method == "reply" {
			if provider.Interface != "example.reply.ReplyAPI" || provider.Version != "1.0.0" ||
				provider.GoMethod != "Reply" || provider.HandlerSymbol != "method:example.com/grpcservice/provider:ReplyAPI:Reply" {
				t.Fatalf("reply provider = %#v", provider)
			}
			foundReply = true
		}
		if provider.Interface == "example.second.SecondAPI" {
			if provider.GoMethod != "Second" || provider.ImplementationType != "SecondAPI" {
				t.Fatalf("multi-provider binding = %#v", provider)
			}
			foundSecond = true
		}
	}
	if !foundReply || !foundSecond {
		t.Fatalf("expected Dubbo providers missing: %#v", store.DubboProviders)
	}
}

// TestGroupedProviderBindingPairsInOrder 验证分组布局（config;config;call;call）下
// 每个 ServiceConfig 顺序消费下一个未占用的 SetProviderService 调用，实现一一配对。
// 修复前每个 config 都取“其后第一个” call，导致 cfgA、cfgB 都绑定到 AlphaAPI，BetaAPI 漏报。
func TestGroupedProviderBindingPairsInOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/grouped\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package export

type ServiceConfig struct {
	Interface string
	Version   string
}

func (c *ServiceConfig) SetProviderService(x any) {}

func Export() {
	cfgA := &ServiceConfig{Interface: "example.AlphaAPI", Version: "1.0.0"}
	cfgB := &ServiceConfig{Interface: "example.BetaAPI", Version: "1.0.0"}
	cfgA.SetProviderService(&AlphaAPI{})
	cfgB.SetProviderService(&BetaAPI{})
}

type AlphaAPI struct{}
type BetaAPI struct{}
`
	if err := os.WriteFile(filepath.Join(root, "export.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	var file *project.File
	for _, f := range p.Packages[p.ModulePath].Files {
		for _, decl := range f.AST.Decls {
			if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "Export" {
				fn, file = d, f
			}
		}
	}
	if fn == nil {
		t.Fatal("Export func not found")
	}
	configs := collectServiceConfigs(p.Root, file, fn)
	if len(configs) != 2 {
		t.Fatalf("configs = %d, want 2", len(configs))
	}
	calls := collectSetProviderServiceCalls(fn)
	consumed := make([]bool, len(calls))
	got := map[string]string{}
	for _, config := range configs {
		expr, ok := nextProviderService(calls, consumed, config.end)
		if !ok {
			t.Fatalf("no provider bound for %s", config.interfaceName)
		}
		got[config.interfaceName] = expression(expr)
	}
	if !strings.Contains(got["example.AlphaAPI"], "AlphaAPI") {
		t.Errorf("AlphaAPI config bound to %q, want &AlphaAPI{}", got["example.AlphaAPI"])
	}
	if !strings.Contains(got["example.BetaAPI"], "BetaAPI") {
		t.Errorf("BetaAPI config bound to %q, want &BetaAPI{} (regressed: grouped layout mis-bind)", got["example.BetaAPI"])
	}
}
