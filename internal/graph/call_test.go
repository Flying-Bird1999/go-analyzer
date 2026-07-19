package graph

import (
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"testing"
)

func TestCallGraphOnlyIncludesExecutableReferences(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.References = []facts.ReferenceFact{
		{Kind: facts.ReferenceKindCall, FromSymbol: "func:a", ToSymbol: "func:b"},
		{Kind: facts.ReferenceKindType, FromSymbol: "func:a", ToSymbol: "func:c"},
		{Kind: facts.ReferenceKindValue, FromSymbol: "func:a", ToSymbol: "func:d"},
	}
	store.GrpcCalls = []facts.GrpcCallFact{{ID: "grpc_call:a", CallerSymbol: "func:a"}}
	g := NewCallGraph(store)
	if got := g.Callees("func:a"); len(got) != 1 || got[0] != "func:b" {
		t.Fatalf("callees=%#v", got)
	}
	if got := g.Callers("func:b"); len(got) != 1 || got[0] != "func:a" {
		t.Fatalf("callers=%#v", got)
	}
	if got := g.Callees("func:c"); len(got) != 0 {
		t.Fatalf("type edge leaked=%#v", got)
	}
	if got := g.GrpcCalls("func:a"); len(got) != 1 {
		t.Fatalf("grpc calls=%#v", got)
	}
}

// TestCallGraphDedupesGrpcCallsByID 验证 grpcByCaller 与 forward/reverse 一致地按 ID
// 去重：同一 GrpcCallFact.ID 若因任何原因在 store.GrpcCalls 中出现多次，同一 caller
// 下也只记一条，不产生重复调用图边。
func TestCallGraphDedupesGrpcCallsByID(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.GrpcCalls = []facts.GrpcCallFact{
		{ID: "grpc_call:a", CallerSymbol: "func:a"},
		{ID: "grpc_call:a", CallerSymbol: "func:a"},
	}
	g := NewCallGraph(store)
	if got := g.GrpcCalls("func:a"); len(got) != 1 {
		t.Fatalf("grpc calls = %#v, want exactly 1 deduped entry", got)
	}
}
