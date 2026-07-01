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

func TestExtractPackageFunctionValueCallReference(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/function-value\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serviceDir := filepath.Join(root, "service")
	if err := os.Mkdir(serviceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "service.go"), []byte(`package service

func Query() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	controllerDir := filepath.Join(root, "controller")
	if err := os.Mkdir(controllerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerDir, "controller.go"), []byte(`package controller

import "example.com/function-value/service"

var querySvc = service.Query

func Handle() {
	querySvc()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	assertReference(t, store,
		"var:example.com/function-value/controller::querySvc",
		"func:example.com/function-value/service::Query",
		facts.ReferenceKindValue,
	)
	assertReference(t, store,
		"func:example.com/function-value/controller::Handle",
		"var:example.com/function-value/controller::querySvc",
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

func TestExtractStrictInterfaceCallReference(t *testing.T) {
	root := writeStrictInterfaceFixture(t, `func Init() {
	Client = new(client)
}
`)

	store := extractFixtureRoot(t, root)
	ref := findReference(t, store,
		"func:example.com/strict-interface/controller::Handle",
		"method:example.com/strict-interface/remote:client:Fetch",
		facts.ReferenceKindCall,
	)
	if ref.Confidence != facts.ConfidenceHigh {
		t.Fatalf("confidence = %q, want high: %#v", ref.Confidence, ref)
	}
}

func TestExtractStrictInterfaceRejectsUnknownBinding(t *testing.T) {
	root := writeStrictInterfaceFixture(t, `func buildClient() Client {
	return new(client)
}

func Init() {
	Client = new(client)
	Client = buildClient()
}
`)

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/strict-interface/controller::Handle")
	to := facts.SymbolID("method:example.com/strict-interface/remote:client:Fetch")
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == to && ref.Kind == facts.ReferenceKindCall {
			t.Fatalf("unknown interface binding emitted concrete call edge: %#v", ref)
		}
	}
	assertReferenceDiagnostic(t, store, "symbol_reference_unresolved")
}

func TestExtractStrictInterfaceRejectsCrossPackageConcreteAssignment(t *testing.T) {
	root := writeStrictInterfaceFixture(t, `func Init() {
	Client = new(client)
}
`)
	configDir := filepath.Join(root, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configSource := `package config

import "example.com/strict-interface/remote"

type otherClient struct{}

func (*otherClient) Fetch() {}

func Configure() {
	remote.ClientValue = new(otherClient)
}
`
	if err := os.WriteFile(filepath.Join(configDir, "config.go"), []byte(configSource), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/strict-interface/controller::Handle")
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.Kind == facts.ReferenceKindCall {
			t.Fatalf("cross-package ambiguous interface binding emitted call edge: %#v", ref)
		}
	}
	assertReferenceDiagnostic(t, store, "symbol_reference_unresolved")
}

func writeStrictInterfaceFixture(t *testing.T, assignmentSource string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/strict-interface\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	remoteDir := filepath.Join(root, "remote")
	if err := os.Mkdir(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteSource := `package remote

type Client interface {
	Fetch()
}

type client struct{}

func (*client) Fetch() {}

var ClientValue Client
` + strings.ReplaceAll(assignmentSource, "Client =", "ClientValue =")
	if err := os.WriteFile(filepath.Join(remoteDir, "remote.go"), []byte(remoteSource), 0o644); err != nil {
		t.Fatal(err)
	}
	controllerDir := filepath.Join(root, "controller")
	if err := os.Mkdir(controllerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	controllerSource := `package controller

import "example.com/strict-interface/remote"

func Handle() {
	remote.ClientValue.Fetch()
}
`
	if err := os.WriteFile(filepath.Join(controllerDir, "controller.go"), []byte(controllerSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestExternalMethodOnProjectPackageVarDoesNotReportUnresolvedProjectSymbol(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/external-client\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "remote"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "remote", "client.go"), []byte(`package remote

import "example.com/sdk"

var Client sdk.Client
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "controller"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller", "controller.go"), []byte(`package controller

import "example.com/external-client/remote"

func Handle() {
	remote.Client.Fetch()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	assertReference(t, store,
		"func:example.com/external-client/controller::Handle",
		"var:example.com/external-client/remote::Client",
		facts.ReferenceKindValue,
	)
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == "symbol_reference_unresolved" && strings.Contains(diagnostic.Message, "remote.Client.Fetch") {
			t.Fatalf("external method reported as unresolved project symbol: %#v", diagnostic)
		}
	}
}

func TestExternalMethodOnLocalShadowingProjectImportDoesNotReportUnresolvedProjectSymbol(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/import-shadow\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "consumer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "consumer", "consumer.go"), []byte("package consumer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

import (
	"example.com/import-shadow/consumer"
	"example.com/sdk"
)

var _ = consumer.Register

func Handle(consumer sdk.Consumer) {
	consumer.Ack()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == "symbol_reference_unresolved" && strings.Contains(diagnostic.Message, "consumer.Ack") {
			t.Fatalf("external method on shadowing local reported as unresolved project symbol: %#v", diagnostic)
		}
	}
}

func TestUnknownLocalShadowingProjectImportDoesNotResolvePackageMethod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/unknown-import-shadow\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	remoteDir := filepath.Join(root, "remote")
	if err := os.Mkdir(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "remote.go"), []byte(`package remote

type client struct{}

func (*client) Fetch() {}

var Client = new(client)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	controllerDir := filepath.Join(root, "controller")
	if err := os.Mkdir(controllerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerDir, "controller.go"), []byte(`package controller

import "example.com/unknown-import-shadow/remote"

type localClient struct{}

func (*localClient) Fetch() {}

func build() struct{ Client *localClient } {
	return struct{ Client *localClient }{Client: new(localClient)}
}

func Handle() {
	remote := build()
	remote.Client.Fetch()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/unknown-import-shadow/controller::Handle")
	to := facts.SymbolID("method:example.com/unknown-import-shadow/remote:client:Fetch")
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == to && ref.Kind == facts.ReferenceKindCall {
			t.Fatalf("unknown local shadow resolved as imported package method: %#v", ref)
		}
	}
}

func TestExtractMethodReferenceFromResolvableMapCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/map-candidates\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type handler interface{}

type fetchHandler struct{}

func (*fetchHandler) Fetch() {}

type otherHandler struct{}

var handlers = map[string]handler{
	"fetch": new(fetchHandler),
	"other": new(otherHandler),
}

func Handle() {
	h := handlers["fetch"]
	h.Fetch()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	assertReference(t, store,
		"func:example.com/map-candidates::Handle",
		"method:example.com/map-candidates:fetchHandler:Fetch",
		facts.ReferenceKindCall,
	)
}

func TestMethodOnLocalWithoutProjectImportDoesNotReportUnresolvedProjectSymbol(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/local-method\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

func Handle(err error) string {
	return err.Error()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == "symbol_reference_unresolved" {
			t.Fatalf("local method reported as unresolved project symbol: %#v", diagnostic)
		}
	}
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

func TestExtractValueReferenceAfterNestedLocalShadowing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nested-value-shadow\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

var DefaultRequest = 1

func Build() int {
	_ = DefaultRequest
	{
		DefaultRequest := 2
		_ = DefaultRequest
	}
	return DefaultRequest
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/nested-value-shadow::Build")
	to := facts.SymbolID("var:example.com/nested-value-shadow::DefaultRequest")
	var count int
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == to && ref.Kind == facts.ReferenceKindValue {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("package value references = %d, want 2: %#v", count, store.References)
	}
}

func TestExtractMethodCallUsesLexicalScopeAfterNestedShadowing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nested-type-shadow\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type outerClient struct{}
func (*outerClient) Run() {}

type innerClient struct{}
func (*innerClient) Run() {}

func Handle() {
	client := &outerClient{}
	{
		client := &innerClient{}
		client.Run()
	}
	client.Run()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/nested-type-shadow::Handle")
	assertReference(t, store, from, "method:example.com/nested-type-shadow:innerClient:Run", facts.ReferenceKindCall)
	assertReference(t, store, from, "method:example.com/nested-type-shadow:outerClient:Run", facts.ReferenceKindCall)
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

func TestExtractPackageConstructorUsesDeclaredReturnType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/package-constructor\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(`package service

type Client struct{}
func (*Client) Query() {}

type Backend struct{}
func (*Backend) Query() {}

func NewClient() *Backend { return &Backend{} }

var Default = NewClient()

func Handle() {
	Default.Query()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/package-constructor::Handle")
	assertReference(t, store, from, "method:example.com/package-constructor:Backend:Query", facts.ReferenceKindCall)
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == "method:example.com/package-constructor:Client:Query" {
			t.Fatalf("constructor name heuristic emitted wrong method reference: %#v", ref)
		}
	}
}

func TestExtractPackageInterfaceConstructorUsesConcreteReturnType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/interface-constructor\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "errors"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "errors", "errors.go"), []byte(`package errors

type Generic interface {
	WrapMsg(msg string) Generic
}

type Concrete struct{}

func (*Concrete) WrapMsg(msg string) Generic { return nil }

func NewGenericError() Generic {
	return &Concrete{}
}

var IllegalArgumentType = NewGenericError()
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "controller"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller", "controller.go"), []byte(`package controller

import bizError "example.com/interface-constructor/errors"

func Handle() {
	bizError.IllegalArgumentType.WrapMsg("invalid")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	assertReference(t, store,
		"func:example.com/interface-constructor/controller::Handle",
		"method:example.com/interface-constructor/errors:Concrete:WrapMsg",
		facts.ReferenceKindCall,
	)
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == "symbol_reference_unresolved" {
			t.Fatalf("constructor interface call should resolve without unresolved diagnostic: %#v", diagnostic)
		}
	}
}

func TestExtractReceiverMethodCallReference(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/receiver-call\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type controllerHandler struct{}

func (c *controllerHandler) executeFlow() {
	c.convert()
}

func (c *controllerHandler) convert() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	ref := findReference(t, store,
		"method:example.com/receiver-call:controllerHandler:executeFlow",
		"method:example.com/receiver-call:controllerHandler:convert",
		facts.ReferenceKindCall,
	)
	if ref.Confidence != facts.ConfidenceHigh {
		t.Fatalf("confidence = %q, want high: %#v", ref.Confidence, ref)
	}
}

func TestExtractConstructorLocalMethodCallUsesMediumConfidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/local-constructor\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type controllerHandler struct{}

func newControllerHandler() *controllerHandler {
	return &controllerHandler{}
}

func controller() {
	c := newControllerHandler()
	c.executeFlow()
}

func (c *controllerHandler) executeFlow() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	ref := findReference(t, store,
		"func:example.com/local-constructor::controller",
		"method:example.com/local-constructor:controllerHandler:executeFlow",
		facts.ReferenceKindCall,
	)
	if ref.Confidence != facts.ConfidenceMedium {
		t.Fatalf("confidence = %q, want medium: %#v", ref.Confidence, ref)
	}
}

func TestExtractNewBuiltinLocalMethodCallUsesHighConfidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/local-new\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type controllerHandler struct{}

func controller() {
	c := new(controllerHandler)
	c.executeFlow()
}

func (c *controllerHandler) executeFlow() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	ref := findReference(t, store,
		"func:example.com/local-new::controller",
		"method:example.com/local-new:controllerHandler:executeFlow",
		facts.ReferenceKindCall,
	)
	if ref.Confidence != facts.ConfidenceHigh {
		t.Fatalf("confidence = %q, want high: %#v", ref.Confidence, ref)
	}
}

func TestExtractTypedConstMethodCall(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/typed-const\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type Code string

func (Code) String() string { return "" }

const Current Code = "current"

func Handle() string {
	return Current.String()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	ref := findReference(t, store,
		"func:example.com/typed-const::Handle",
		"method:example.com/typed-const:Code:String",
		facts.ReferenceKindCall,
	)
	if ref.Confidence != facts.ConfidenceHigh {
		t.Fatalf("confidence = %q, want high: %#v", ref.Confidence, ref)
	}
}

func TestExtractStaticMapInterfaceDispatchReferencesAllConcreteMethods(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/static-map-dispatch\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(`package service

type Kind int

const (
	First Kind = iota
	Second
)

type Action interface {
	Run()
}

type firstAction struct{}
func (*firstAction) Run() {}

type secondAction struct{}
func (*secondAction) Run() {}

var actions = map[Kind]Action{
	First:  &firstAction{},
	Second: &secondAction{},
}

func Handle(kind Kind) {
	if action, ok := actions[kind]; ok {
		action.Run()
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/static-map-dispatch::Handle")
	assertReference(t, store, from, "method:example.com/static-map-dispatch:firstAction:Run", facts.ReferenceKindCall)
	assertReference(t, store, from, "method:example.com/static-map-dispatch:secondAction:Run", facts.ReferenceKindCall)
}

func TestExtractStaticMapInterfaceDispatchRejectsUnknownMapValue(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/static-map-unknown\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(`package service

type Kind int

const (
	First Kind = iota
	Second
)

type Action interface {
	Run()
}

type firstAction struct{}
func (*firstAction) Run() {}

type secondAction struct{}
func (*secondAction) Run() {}

func buildAction() Action {
	return &secondAction{}
}

var actions = map[Kind]Action{
	First:  &firstAction{},
	Second: buildAction(),
}

func Handle(kind Kind) {
	if action, ok := actions[kind]; ok {
		action.Run()
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/static-map-unknown::Handle")
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.Kind == facts.ReferenceKindCall {
			t.Fatalf("unknown static map value emitted partial dispatch edge: %#v", ref)
		}
	}
}

func TestExtractConstructorTupleOnlyInfersFirstLocal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/local-constructor-tuple\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "controller.go"), []byte(`package controller

type controllerHandler struct{}

func newControllerHandler() (*controllerHandler, error) {
	return &controllerHandler{}, nil
}

func controller() {
	c, err := newControllerHandler()
	c.executeFlow()
	err.executeFlow()
}

func (c *controllerHandler) executeFlow() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := extractFixtureRoot(t, root)
	from := facts.SymbolID("func:example.com/local-constructor-tuple::controller")
	to := facts.SymbolID("method:example.com/local-constructor-tuple:controllerHandler:executeFlow")
	var count int
	for _, ref := range store.References {
		if ref.FromSymbol == from && ref.ToSymbol == to && ref.Kind == facts.ReferenceKindCall {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("constructor method references = %d, want 1: %#v", count, store.References)
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
