package reference

import (
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractFunctionCallReference(t *testing.T) {
	store := extractReferenceFixture(t)

	assertReference(t, store,
		"func:example.com/reference-chain/controller::CheckIn",
		"func:example.com/reference-chain/service::WebApiForwardGray",
	)
}

func TestExtractPackageVarMethodCallReference(t *testing.T) {
	store := extractReferenceFixture(t)

	assertReference(t, store,
		"func:example.com/reference-chain/controller::Update",
		"method:example.com/reference-chain/service:merchantSettingService:UpdateSubMerchantSettingByCode",
	)
}

func extractReferenceFixture(t *testing.T) *facts.Store {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "reference-chain")
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
	return store
}

func assertReference(t *testing.T, store *facts.Store, from, to facts.SymbolID) {
	t.Helper()
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == to {
			return
		}
	}
	t.Fatalf("reference %s -> %s not found: %#v", from, to, store.References)
}
