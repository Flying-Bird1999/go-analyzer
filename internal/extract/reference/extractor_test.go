// extractor_test.go 校验 reference 包对 call/type/value 三类引用边的提取与解析行为。
package reference

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

// 场景：controller 方法调用 service 函数，应产生 call 引用边并保留调用表达式证据。
func TestExtractFunctionCallReference(t *testing.T) {
	store := extractReferenceFixture(t)

	ref := findReference(t, store,
		"func:example.com/reference-chain/controller::CheckIn",
		"func:example.com/reference-chain/service::WebApiForwardGray",
		facts.ReferenceKindCall,
	)
	if len(ref.Evidence) != 1 {
		t.Fatalf("reference evidence = %#v", ref.Evidence)
	}
	if ref.Evidence[0].Kind != "call_expr" || ref.Evidence[0].Raw != "svc.WebApiForwardGray" {
		t.Fatalf("reference evidence = %#v", ref.Evidence)
	}
}

// 场景：包级 var 被赋值为某函数值，再通过该变量发起调用，应分别产生 value 与 call 边。
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

// 场景：包级变量上的方法调用应解析为该方法符号并产生 call 边。
func TestExtractPackageVarMethodCallReference(t *testing.T) {
	store := extractReferenceFixture(t)

	assertReference(t, store,
		"func:example.com/reference-chain/controller::Update",
		"method:example.com/reference-chain/service:merchantSettingService:UpdateSubMerchantSettingByCode",
		facts.ReferenceKindCall,
	)
}

// 场景：接口变量有唯一具体实现时，其方法调用应高置信度解析到具体方法。
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

// 场景：接口变量被多次赋值且包含无法静态确定的实现时，不应产生具体 call 边，只报未知绑定诊断。
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
	assertReferenceDiagnostic(t, store, "symbol_reference_unknown_interface_binding")
}

// 场景：未知接口绑定的调用应解析失败，并由解析器给出 unknown_interface_binding 诊断。
func TestResolverExplainsUnknownInterfaceBinding(t *testing.T) {
	root := writeStrictInterfaceFixture(t, `func buildClient() Client {
	return new(client)
}

func Init() {
	Client = buildClient()
}
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	file := findReferenceTestFile(t, p, "controller/controller.go")
	call := firstCallInFile(t, file)

	resolver := newResolver(file, idx, scopedValueTypes{})
	resolved, raw, ok := resolver.ResolveCall(call)
	if ok || len(resolved) != 0 {
		t.Fatalf("unknown interface binding resolved %q to %#v", raw, resolved)
	}
	code, _, diagnosticOK := resolver.UnresolvedProjectCallDiagnostic(unwrapGenericCallee(call.Fun))
	if !diagnosticOK || code != "symbol_reference_unknown_interface_binding" {
		t.Fatalf("diagnostic = %q ok=%v", code, diagnosticOK)
	}
}

// 场景：接口变量在跨包被额外赋值导致具体实现多于一个时，应报歧义诊断而非产生 call 边。
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
	assertReferenceDiagnostic(t, store, "symbol_reference_ambiguous_interface")
}

// writeStrictInterfaceFixture 构造 strict-interface 测试夹具：定义接口、唯一实现与可注入的赋值源，
// 并组装 controller 调用 remote.ClientValue.Fetch 的最小场景。
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

// 场景：项目包变量持有外部 SDK 类型，调用其外部方法不应被误报为项目内未解析符号。
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

// 场景：导入包名被同名的函数参数遮蔽，调用外部类型方法不应误报为项目内未解析符号。
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

// 场景：导入包名被局部变量遮蔽且变量类型未知时，不应将其方法误解析为导入包的方法。
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

// 场景：map[string]interface 的元素调用方法时，应枚举静态可见的具体实现作为分发候选。
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

// 场景：对未导入项目包的本地参数（如内置 error）调用方法，不应误报为项目内未解析符号。
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

// 场景：类型字段、方法签名中的参数/返回值、接收者类型都应产生 type 引用边。
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

// 场景：类型转换（如 OrderID(x)）应作为 type 引用，而不是 call 边。
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

// 场景：函数体引用本包 const 与 var，应产生 value 引用边。
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

// 场景：引用 ID 由边类型、起止符号与源码位置共同决定，确保可去重且稳定。
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

// 场景：存在未解析引用时不应中断已可解析引用的提取，且应同时上报未解析诊断。
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

// 场景：泛型函数调用的被调者（call 边）与显式类型实参（type 边）应分别提取，互不干扰。
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

// 场景：包级变量在函数内被局部 := 遮蔽前已被引用，该引用应解析到包级变量。
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

// 场景：嵌套块内遮蔽同名变量后，遮蔽前后的引用都应解析到包级变量，共产生两条 value 边。
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

// 场景：嵌套块遮蔽同名变量类型时，内层调用应解析到内层类型方法，外层调用解析到外层类型方法。
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

// 场景：包级 var 由构造函数初始化时，对其方法的调用以中等置信度解析到构造函数返回类型的方法。
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

// 场景：构造函数返回类型与名字暗示不同时，应以声明的返回类型为准解析方法，避免名字启发式误导。
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

// 场景：构造函数返回接口类型但实际返回具体类型时，方法调用应解析到具体类型方法且不报未解析。
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

// 场景：同一接收者类型内部的方法互调应高置信度解析为目标方法。
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

// 场景：局部变量由构造函数初始化时，其方法调用以中等置信度解析到返回类型方法。
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

// 场景：局部变量由内置 new(T) 构造时，其方法调用以高置信度解析到 T 的方法。
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

// 场景：自定义基础类型的常量调用其方法，应以声明的具名类型解析方法且高置信度。
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

// 场景：map[string]接口 的元素调用方法，应静态枚举所有具名实现并产生对应方法调用边。
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

// 场景：map 值中存在非组合字面量（未知实现）时，应拒绝静态分发，不产生部分方法调用边。
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

// 场景：多返回值构造函数赋值（c, err := ...）仅对首参推断类型，不应为 err 错误地产生方法调用边。
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

// extractReferenceFixture 加载 reference-chain 测试夹具并执行 Extract。
func extractReferenceFixture(t *testing.T) *facts.Store {
	t.Helper()
	return extractFixture(t, "reference-chain")
}

// extractFixture 加载 testdata 下的指定夹具并执行 Extract。
func extractFixture(t *testing.T, fixture string) *facts.Store {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", fixture)
	return extractFixtureRoot(t, root)
}

// extractFixtureRoot 从给定根目录加载项目、构建索引并执行 Extract，返回 facts 存储。
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

// findReferenceTestFile 在项目中按相对路径查找测试目标文件。
func findReferenceTestFile(t *testing.T, p *project.Project, rel string) *project.File {
	t.Helper()
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			fileRel, err := filepath.Rel(p.Root, file.Path)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.ToSlash(fileRel) == rel {
				return file
			}
		}
	}
	t.Fatalf("file %s not found", rel)
	return nil
}

// firstCallInFile 返回文件 AST 中首个出现的调用表达式。
func firstCallInFile(t *testing.T, file *project.File) *ast.CallExpr {
	t.Helper()
	var out *ast.CallExpr
	ast.Inspect(file.AST, func(node ast.Node) bool {
		if out != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if ok {
			out = call
			return false
		}
		return true
	})
	if out == nil {
		t.Fatalf("call expression not found in %s", file.Path)
	}
	return out
}

// assertReference 断言给定 from/to/kind 的引用边存在。
func assertReference(t *testing.T, store *facts.Store, from, to facts.SymbolID, kind facts.ReferenceKind) {
	t.Helper()
	_ = findReference(t, store, from, to, kind)
}

// findReference 查找并返回指定 from/to/kind 的引用事实，不存在则失败。
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

// assertReferenceDiagnostic 断言存储中存在指定诊断码的引用相关诊断。
func assertReferenceDiagnostic(t *testing.T, store *facts.Store, code string) {
	t.Helper()
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostic %q not found: %#v", code, store.Diagnostics)
}
