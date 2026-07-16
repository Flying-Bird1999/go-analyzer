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

func TestExtractRequiresExactGeneratedClientReceiver(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "go.mod", "module example.com/bff\n\ngo 1.24\n")
	writeProjectFile(t, root, "controller/order.go", `package controller
import pb "example.com/proto"
type API struct { client pb.OrderClient }
func (a *API) Get() { a.client.Get() }
func messageOnly(_ *pb.GetRequest) {}
`)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	operation := facts.GrpcOperationFact{ID: facts.GrpcOperationID("/shop.order.v1.OrderService/Get"), FullMethod: "/shop.order.v1.OrderService/Get"}
	binding := facts.GrpcClientBinding{GoPackage: "example.com/proto", ClientType: "OrderClient", GoMethod: "Get"}
	catalog := &Catalog{ByBinding: map[BindingKey]CatalogEntry{
		{GoPackage: binding.GoPackage, ClientType: binding.ClientType, GoMethod: binding.GoMethod}: {
			Operation: operation,
			Binding:   binding,
			Evidence:  facts.EvidenceFact{Kind: "generated_grpc_transport", Span: facts.SourceSpan{File: "dependency/example.com/proto/order.pb.go"}},
		},
	}}

	calls, err := Extract(p, idx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v", calls)
	}
	if calls[0].OperationID != operation.ID || calls[0].Span.File != "controller/order.go" {
		t.Fatalf("call = %#v", calls[0])
	}
	if len(calls[0].Evidence) != 2 || calls[0].Evidence[0].Kind != "grpc_call_expression" || calls[0].Evidence[1].Kind != "generated_grpc_transport" {
		t.Fatalf("evidence = %#v", calls[0].Evidence)
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
