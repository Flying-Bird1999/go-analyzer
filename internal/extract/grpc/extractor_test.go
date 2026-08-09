package grpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestExtractRecognizesClientSuffixedFieldReceiver 覆盖最基础的形态：
// receiver 是本包内某个 struct 字段，字段类型来自另一个包且以 Client 结尾，
// 且该包 import 路径落在公司内网生成包域名 gopkg.inshopline.com/ 下。
// 不读取该包的任何源码——这个包在测试里根本不存在。
func TestExtractRecognizesClientSuffixedFieldReceiver(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	writeProjectFile(t, root, "controller/order.go", `package controller
import pb "gopkg.inshopline.com/proto"
type API struct { client pb.OrderClient }
func (a *API) Get() { a.client.Get() }
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := "gopkg.inshopline.com/proto.Order/Get"
	if len(operations) != 1 || operations[0].Identity != wantIdentity {
		t.Fatalf("operations = %#v, want identity %q", operations, wantIdentity)
	}
	if operations[0].GoPackage != "gopkg.inshopline.com/proto" || operations[0].Service != "Order" || operations[0].GoMethod != "Get" {
		t.Fatalf("operation fields = %#v", operations[0])
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v", calls)
	}
	if calls[0].OperationID != operations[0].ID || calls[0].Span.File != "controller/order.go" {
		t.Fatalf("call = %#v", calls[0])
	}
	if len(calls[0].Evidence) != 1 || calls[0].Evidence[0].Kind != "grpc_call_expression" {
		t.Fatalf("evidence = %#v", calls[0].Evidence)
	}
}

// TestExtractIgnoresClientSuffixedTypeOutsideGeneratedDomain 覆盖真实项目里验证过的
// 误判模式：Redis/HTTP client 封装同样以 Client 结尾、同样来自外部包，但 import 路径
// 不在公司内网生成包域名 gopkg.inshopline.com/ 下，必须被排除。
func TestExtractIgnoresClientSuffixedTypeOutsideGeneratedDomain(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	writeProjectFile(t, root, "controller/cache.go", `package controller
import redis "github.com/go-redis/redis"
type API struct { client redis.UniversalClient }
func (a *API) Get() { a.client.Get() }
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 0 || len(calls) != 0 {
		t.Fatalf("operations = %#v, calls = %#v, want none (type outside generated package domain)", operations, calls)
	}
}

// TestExtractIgnoresClientSuffixedTypeFromOwnModule 覆盖真实项目里验证过的另一种
// 误判模式：项目自己 module 内的手写包装类型，恰好落在 gopkg.inshopline.com 域名下
// （如 sc1-server 内部的 ConversationOnlineClient，import 路径形如
// gopkg.inshopline.com/<module>/internal/grpc/consumer/conv），必须被排除。
func TestExtractIgnoresClientSuffixedTypeFromOwnModule(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module gopkg.inshopline.com/sc1/app\n\ngo 1.24\n")
	writeProjectFile(t, root, "internal/conv/client.go", `package conv
type ConversationOnlineClient struct{}
func (ConversationOnlineClient) Get() {}
`)
	writeProjectFile(t, root, "controller/order.go", `package controller
import "gopkg.inshopline.com/sc1/app/internal/conv"
type API struct { client conv.ConversationOnlineClient }
func (a *API) Get() { a.client.Get() }
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 0 || len(calls) != 0 {
		t.Fatalf("operations = %#v, calls = %#v, want none (type belongs to the analyzed project's own module)", operations, calls)
	}
}

// TestExtractIgnoresNonClientSuffixedType 确认命名契约是必要条件：字段类型来自
// 另一个包，但类型名不以 Client 结尾时，不产生任何 gRPC 事实——这类调用在 AST
// 上和真正的 gRPC 调用没有区别，唯一的判据就是这条命名契约。
func TestExtractIgnoresNonClientSuffixedType(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	writeProjectFile(t, root, "controller/order.go", `package controller
import pb "example.com/proto"
type API struct { helper pb.OrderHelper }
func (a *API) Get() { a.helper.Get() }
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 0 || len(calls) != 0 {
		t.Fatalf("operations = %#v, calls = %#v, want none", operations, calls)
	}
}

// TestExtractIgnoresSamePackageClientSuffixedType 确认第二个必要条件：类型必须
// 来自调用方所在包之外的另一个包。generated client 接口从不与消费它的业务代码
// 同包声明；项目自己在同一个包里手写一个恰好以 Client 结尾的类型，不应被误判。
func TestExtractIgnoresSamePackageClientSuffixedType(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	writeProjectFile(t, root, "controller/order.go", `package controller
type OrderClient struct{}
func (OrderClient) Get() {}
type API struct { client OrderClient }
func (a *API) Get() { a.client.Get() }
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 0 || len(calls) != 0 {
		t.Fatalf("operations = %#v, calls = %#v, want none (same-package type)", operations, calls)
	}
}

// TestExtractResolvesPackageQualifiedClientVar 覆盖真实 BFF 里最常见的 client 持有方式：
// generated client 声明为某个包的包级变量，业务代码跨包以 `pkg.Client.Method(...)` 调用。
// 此前 functionScope.resolve 只把 selector 的 X 当值解析（找 struct 字段），
// 从不考虑 X 是 import 别名，导致这类调用点被整体漏掉。
func TestExtractResolvesPackageQualifiedClientVar(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	// client 声明在 remote 包的包级变量上。
	writeProjectFile(t, root, "remote/client.go", `package remote
import pb "gopkg.inshopline.com/proto"
var OrderClient pb.OrderClient
`)
	// 业务代码跨包调用：receiver 是 remote.OrderClient 这个包限定变量。
	writeProjectFile(t, root, "service/order.go", `package service
import (
	"example.com/bff/remote"
	pb "gopkg.inshopline.com/proto"
)
func Load() { remote.OrderClient.Get(&pb.GetRequest{}) }
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := "gopkg.inshopline.com/proto.Order/Get"
	if len(operations) != 1 || operations[0].Identity != wantIdentity {
		t.Fatalf("operations = %#v, want identity %q", operations, wantIdentity)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v, want 1 cross-package call site", calls)
	}
	if calls[0].CallerSymbol != "func:example.com/bff/service::Load" {
		t.Errorf("callerSymbol = %q", calls[0].CallerSymbol)
	}
	if calls[0].OperationID != operations[0].ID || calls[0].Span.File != "service/order.go" {
		t.Errorf("call = %#v", calls[0])
	}
}

// TestExtractRejectsShadowedPackageAlias 确认包限定解析不会越过局部遮蔽：
// 局部变量与 import 别名同名时，receiver 指的是局部变量，不能再按包级变量解析。
func TestExtractRejectsShadowedPackageAlias(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	writeProjectFile(t, root, "remote/client.go", `package remote
import pb "example.com/proto"
var OrderClient pb.OrderClient
`)
	writeProjectFile(t, root, "service/order.go", `package service
import "example.com/bff/remote"
type stub struct{ OrderClient otherClient }
type otherClient struct{}
func (otherClient) Get(_ ...any) {}
func Load() {
	remote := stub{}
	remote.OrderClient.Get()
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

	operations, calls, err := Extract(p, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 0 || len(calls) != 0 {
		t.Fatalf("operations = %#v, calls = %#v, want none (local variable shadows the import alias)", operations, calls)
	}
}

func writeProjectFile(t *testing.T, root, name, source string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCallAmbiguityErrorFormatting 验证 CallAmbiguityError 类型可正确构造并格式化。
//
// 注意：这不是一个能调用 Extract 触发歧义的端到端测试。extractor.go 的 len(types) > 1
// 分支是防御性代码——当前 functionScope.resolve 在单一标识符上最多返回 1 个 ValueType
// （interface 多实现被 resolveUniqueInterfaceBinding 拒绝，map 索引分发无 IndexExpr 分支），
// 故该分支在现有架构下不可达。本测试仅保证错误类型可用、格式稳定，为未来 resolve 能力
// 扩展（使分支可达）保留 surface 契约。若未来让分支可达，应补一个调用 Extract 的
// 端到端 fixture 测试。
func TestCallAmbiguityErrorFormatting(t *testing.T) {
	err := &CallAmbiguityError{
		Caller: "func:example.com/bff/controller::Get",
		Span:   facts.SourceSpan{File: "controller/order.go", StartLine: 10, StartCol: 3},
	}
	msg := err.Error()
	for _, want := range []string{"ambiguous", "controller/order.go", "10"} {
		if !strings.Contains(msg, want) {
			t.Errorf("CallAmbiguityError() = %q, missing %q", msg, want)
		}
	}
}
