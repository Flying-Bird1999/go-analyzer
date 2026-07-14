package dubbo

import (
	"path/filepath"
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
	if len(store.DubboProviders) != 2 {
		t.Fatalf("dubbo providers = %#v", store.DubboProviders)
	}
	for _, provider := range store.DubboProviders {
		if provider.Method == "reply" {
			if provider.Interface != "example.reply.ReplyAPI" || provider.Version != "1.0.0" ||
				provider.GoMethod != "Reply" || provider.HandlerSymbol != "method:example.com/grpcservice/provider:ReplyAPI:Reply" {
				t.Fatalf("reply provider = %#v", provider)
			}
			return
		}
	}
	t.Fatal("reply Dubbo provider missing")
}
