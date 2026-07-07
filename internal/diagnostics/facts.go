// facts.go 提供诊断模型与 facts.DiagnosticFact 之间的转换，以及把诊断
// 经去重后写入 facts.Store 的便捷入口。
package diagnostics

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

// ToFact 把内部 Diagnostic 转为可序列化的 facts.DiagnosticFact。
// 转换时把强类型的 Code/Severity 降级为字符串，并对 RelatedFactIDs 做防御性拷贝，
// 避免外部修改影响原始切片。
func ToFact(d Diagnostic) facts.DiagnosticFact {
	return facts.DiagnosticFact{
		ID:       d.ID,
		Code:     string(d.Code),
		Severity: string(d.Severity),
		Message:  d.Message,
		Span:     d.Span,
		// 拷贝 RelatedFactIDs，避免与传入诊断共享底层数组。
		RelatedFactIDs: append([]string(nil), d.RelatedFactIDs...),
	}
}

// AddFact 把一条新诊断加入 store，同时保证整个 store 的诊断集合去重且按 ID 排序。
// 实现上先把 store 中已有的诊断重建为 Collector 条目，再加入新诊断，最后用
// 去重排序后的列表整体替换 store.Diagnostics。
func AddFact(store *facts.Store, d Diagnostic) {
	collector := NewCollector()
	// 把 store 中已有的诊断（字符串形式）转回强类型并加入收集器。
	for _, existing := range store.Diagnostics {
		collector.Add(Diagnostic{
			ID:             existing.ID,
			Code:           Code(existing.Code),
			Severity:       Severity(existing.Severity),
			Message:        existing.Message,
			Span:           existing.Span,
			RelatedFactIDs: existing.RelatedFactIDs,
		})
	}
	// 加入本次新诊断。
	collector.Add(d)
	// 用去重并排序后的结果整体替换 store 的诊断列表。
	list := collector.List()
	store.Diagnostics = store.Diagnostics[:0]
	for _, item := range list {
		store.Diagnostics = append(store.Diagnostics, ToFact(item))
	}
}
