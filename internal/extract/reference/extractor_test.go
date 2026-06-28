package reference

import (
	"os"
	"path/filepath"
	"strings"
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
		facts.ReferenceKindCall,
	)
}

func TestExtractPackageVarMethodCallReference(t *testing.T) {
	store := extractReferenceFixture(t)

	assertReference(t, store,
		"func:example.com/reference-chain/controller::Update",
		"method:example.com/reference-chain/service:merchantSettingService:UpdateSubMerchantSettingByCode",
		facts.ReferenceKindCall,
	)
}

func TestExtractTypeReferences(t *testing.T) {
	store := extractFixture(t, "type-impact")

	assertReference(t, store,
		"type:example.com/type-impact/model::CreateOrderRequest",
		"type:example.com/type-impact/model::Address",
		facts.ReferenceKindType,
	)
	assertReference(t, store,
		"method:example.com/type-impact/controller:OrderAPI:Create",
		"type:example.com/type-impact/model::CreateOrderRequest",
		facts.ReferenceKindType,
	)
	assertReference(t, store,
		"method:example.com/type-impact/controller:OrderAPI:Create",
		"type:example.com/type-impact/model::CreateOrderResponse",
		facts.ReferenceKindType,
	)
	assertReference(t, store,
		"method:example.com/type-impact/controller:OrderAPI:Create",
		"type:example.com/type-impact/controller::OrderAPI",
		facts.ReferenceKindType,
	)
}

func TestTypeConversionIsNotExtractedAsCall(t *testing.T) {
	store := extractFixture(t, "type-impact")
	from := facts.SymbolID("method:example.com/type-impact/controller:OrderAPI:Create")
	typeID := facts.SymbolID("type:example.com/type-impact/controller::OrderID")
	assertReference(t, store, from, typeID, facts.ReferenceKindType)
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToRaw == "OrderID" && ref.Kind == facts.ReferenceKindCall {
			t.Fatalf("type conversion extracted as call: %#v", ref)
		}
	}
}

func TestExtractValueReferences(t *testing.T) {
	store := extractFixture(t, "type-impact")
	from := facts.SymbolID("func:example.com/type-impact/controller::Build")

	assertReference(t, store,
		from,
		"const:example.com/type-impact/controller::Timeout",
		facts.ReferenceKindValue,
	)
	assertReference(t, store,
		from,
		"var:example.com/type-impact/controller::DefaultRequest",
		facts.ReferenceKindValue,
	)
}

func TestReferenceIDUsesSemanticIdentityAndSourceLocation(t *testing.T) {
	span := facts.SourceSpan{File: "service/service.go", StartLine: 10, StartCol: 2, EndLine: 10, EndCol: 12}
	got := referenceID(
		"func:example.com/project/controller::Build",
		"func:example.com/project/service::Load",
		facts.ReferenceKindCall,
		span,
	)
	want := "ref:call:func:example.com/project/controller::Build:func:example.com/project/service::Load:service/service.go:10:2:10:12"
	if got != want {
		t.Fatalf("reference ID = %q, want %q", got, want)
	}
}

func TestExtractUnresolvedProjectReferencesDoesNotAbortResolvedReferences(t *testing.T) {
	store := extractReferenceFixture(t)

	assertReference(t, store,
		"func:example.com/reference-chain/controller::CheckIn",
		"func:example.com/reference-chain/service::WebApiForwardGray",
		facts.ReferenceKindCall,
	)
	assertReferenceDiagnostic(t, store, "symbol_reference_unresolved")
	assertReferenceDiagnostic(t, store, "type_reference_unresolved")
}

func TestExtractGenericFunctionCallSeparatesCalleeAndTypeArguments(t *testing.T) {
	store := extractReferenceFixture(t)
	from := facts.SymbolID("func:example.com/reference-chain/controller::CheckIn")

	assertReference(t, store,
		from,
		"func:example.com/reference-chain/service::Fetch",
		facts.ReferenceKindCall,
	)
	assertReference(t, store,
		from,
		"type:example.com/reference-chain/service::Response",
		facts.ReferenceKindType,
	)
	for _, diagnostic := range store.Diagnostics {
		if strings.Contains(diagnostic.Message, "Fetch") {
			t.Fatalf("generic function diagnosed as unresolved type: %#v", diagnostic)
		}
	}
}

func TestExtractValueReferenceBeforeLocalShadowing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/shadow\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

var DefaultRequest = 1

func Build() int {
	_ = DefaultRequest
	DefaultRequest := 2
	return DefaultRequest
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	assertReference(t, store,
		"func:example.com/shadow::Build",
		"var:example.com/shadow::DefaultRequest",
		facts.ReferenceKindValue,
	)
}

func TestExtractConstructorInferredMethodCallUsesMediumConfidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/constructor-confidence\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(`package service

type Client struct{}

func NewClient() *Client { return &Client{} }

func (c *Client) Query() {}

var Default = NewClient()
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package service

func Handle() {
	Default.Query()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	ref := findReference(t, store,
		"func:example.com/constructor-confidence::Handle",
		"method:example.com/constructor-confidence:Client:Query",
		facts.ReferenceKindCall,
	)
	if ref.Confidence != facts.ConfidenceMedium {
		t.Fatalf("confidence = %q, want medium: %#v", ref.Confidence, ref)
	}
}

func extractReferenceFixture(t *testing.T) *facts.Store {
	t.Helper()
	return extractFixture(t, "reference-chain")
}

func extractFixture(t *testing.T, fixture string) *facts.Store {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", fixture)
	return extractFixtureRoot(t, root)
}

func extractFixtureRoot(t *testing.T, root string) *facts.Store {
	t.Helper()
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

func assertReference(t *testing.T, store *facts.Store, from, to facts.SymbolID, kind facts.ReferenceKind) {
	t.Helper()
	_ = findReference(t, store, from, to, kind)
}

func findReference(t *testing.T, store *facts.Store, from, to facts.SymbolID, kind facts.ReferenceKind) facts.ReferenceFact {
	t.Helper()
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == to && ref.Kind == kind {
			return ref
		}
	}
	t.Fatalf("reference %s -[%s]-> %s not found: %#v", from, kind, to, store.References)
	return facts.ReferenceFact{}
}

func assertReferenceDiagnostic(t *testing.T, store *facts.Store, code string) {
	t.Helper()
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %q not found: %#v", code, store.Diagnostics)
}
