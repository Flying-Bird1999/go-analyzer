// facts_test.go 验证 AddFact 的去重语义与插入性能，覆盖从 O(n²) 重建改为增量索引追加
// 后的正确性与摊还复杂度。
package diagnostics

import (
	"fmt"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// TestAddFactDedupesAcrossCalls 验证跨多次 AddFact 调用（而非同一个 Collector 内）
// 同一去重键（码+位置+消息）只保留一条，且保留完整字段。这是 AddFact 从"每次调用
// 重建全部诊断"改为"增量索引追加"后必须保持的对外行为。
func TestAddFactDedupesAcrossCalls(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	span := facts.SourceSpan{File: "router/router.go", StartLine: 10, EndLine: 10}
	AddFact(store, Diagnostic{
		Code:           CodeRouteDynamicPath,
		Severity:       SeverityWarning,
		Message:        "dynamic route path cannot be resolved",
		Span:           span,
		RelatedFactIDs: []string{"route:a"},
	})
	// 与上一条完全相同的诊断（同码、同位置、同消息）应被去重，即使来自不同调用。
	AddFact(store, Diagnostic{
		Code:           CodeRouteDynamicPath,
		Severity:       SeverityWarning,
		Message:        "dynamic route path cannot be resolved",
		Span:           span,
		RelatedFactIDs: []string{"route:a"},
	})
	// 不同消息的诊断应被视为新的一条。
	AddFact(store, Diagnostic{
		Code:    CodeRouteDynamicPath,
		Message: "a different message",
		Span:    span,
	})

	if len(store.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %d: %#v", len(store.Diagnostics), store.Diagnostics)
	}
	first := store.Diagnostics[0]
	if first.Code != string(CodeRouteDynamicPath) || first.Severity != string(SeverityWarning) {
		t.Fatalf("first diagnostic = %#v", first)
	}
	if len(first.RelatedFactIDs) != 1 || first.RelatedFactIDs[0] != "route:a" {
		t.Fatalf("related facts = %#v", first.RelatedFactIDs)
	}
}

// TestAddFactAssignsStableIDsAcrossCalls 验证跨调用产生的诊断仍能拿到基于去重键派生
// 的稳定 ID（未显式设置 ID 时）。
func TestAddFactAssignsStableIDsAcrossCalls(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	AddFact(store, Diagnostic{Code: CodeRouteDynamicPath, Message: "m1", Span: facts.SourceSpan{File: "a.go", StartLine: 1}})
	AddFact(store, Diagnostic{Code: CodeRouteDynamicPath, Message: "m2", Span: facts.SourceSpan{File: "a.go", StartLine: 2}})
	if len(store.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %d", len(store.Diagnostics))
	}
	for _, d := range store.Diagnostics {
		if d.ID == "" {
			t.Fatalf("diagnostic missing auto-derived ID: %#v", d)
		}
	}
	if store.Diagnostics[0].ID == store.Diagnostics[1].ID {
		t.Fatalf("distinct diagnostics got the same ID: %q", store.Diagnostics[0].ID)
	}
}

// BenchmarkAddFactManyDistinctDiagnostics 度量插入 N 条互不相同诊断的总耗时。
// 修复前的实现（每次调用重建全部已存诊断）是 O(N²)：N=20000 时基准耗时应随 N 近似
// 线性增长，而非平方增长——可通过对比不同 N 下 ns/op 是否大致随 N 线性放大来判断
// （平方增长时 N 翻倍、ns/op 应翻 4 倍；线性增长时 ns/op 大致不变或仅小幅变化，
// 因为 b.N 已把总耗时归一到单次操作）。
func BenchmarkAddFactManyDistinctDiagnostics(b *testing.B) {
	const n = 20000
	for i := 0; i < b.N; i++ {
		store := facts.NewStore("/tmp/project", "example.com/project")
		for j := 0; j < n; j++ {
			AddFact(store, Diagnostic{
				Code:    CodeSymbolReferenceUnresolved,
				Message: fmt.Sprintf("unresolved reference #%d", j),
				Span:    facts.SourceSpan{File: "pkg/file.go", StartLine: j + 1},
			})
		}
		if len(store.Diagnostics) != n {
			b.Fatalf("diagnostics = %d, want %d", len(store.Diagnostics), n)
		}
	}
}
