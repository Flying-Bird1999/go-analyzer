package grpc

import (
	"os"
	"path/filepath"
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
