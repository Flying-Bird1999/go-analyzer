// facts.go 提供诊断模型与 facts.DiagnosticFact 之间的转换，以及把诊断
// 经去重后写入 facts.Store 的便捷入口。
package diagnostics

import (
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// ToFact 把内部 Diagnostic 转为可序列化的 facts.DiagnosticFact。
// 转换时把强类型的 Code/Severity 降级为字符串，并对 RelatedFactIDs 做防御性拷贝，
// 避免外部修改影响原始切片。
func ToFact(d Diagnostic) facts.DiagnosticFact {
	var span *facts.SourceSpan
	if d.Span.File != "" || d.Span.StartLine != 0 {
		span = &d.Span
	}
	return facts.DiagnosticFact{
		ID:       d.ID,
		Code:     string(d.Code),
		Severity: string(d.Severity),
		Message:  d.Message,
		Span:     span,
		// 拷贝 RelatedFactIDs，避免与传入诊断共享底层数组。
		RelatedFactIDs: append([]string(nil), d.RelatedFactIDs...),
	}
}

// AddFact 把一条新诊断加入 store，保证同一去重键（码+位置+消息）不重复写入。
//
// 早期实现每次调用都把 store 中全部已有诊断重新转换、塞进一个临时 Collector 再整体
// 替换、重排，插入 N 条诊断的总代价是 O(N²)（extractor 在循环中逐条调用 AddFact 是
// 常见模式，如 reference/route 抽取遇到未解析引用时逐条上报）。这里改为用
// Store.DiagnosticIndex()（去重 key -> 下标的增量索引）判断是否已存在：已存在直接
// 跳过；不存在则纯追加，均摊 O(1) 每条，整体 O(N)。按 ID 排序不再由 AddFact 维护——
// 唯一的公开渲染入口 output.RenderJSON 已经在渲染前统一按 ID 排序 Diagnostics，
// AddFact 维护顺序纯属重复劳动，去掉后不改变任何对外可见行为。
func AddFact(store *facts.Store, d Diagnostic) {
	key := dedupeKey(d)
	if d.ID == "" {
		d.ID = "diagnostic:" + key
	}
	index := store.DiagnosticIndex()
	if _, exists := index[key]; exists {
		return
	}
	index[key] = len(store.Diagnostics)
	store.Diagnostics = append(store.Diagnostics, ToFact(d))
}

// dedupeKey 计算诊断的去重键：由诊断码、位置区间（文件+起止行）与消息拼接而成，
// 与原 Collector.key 语义一致——同一位置上同一码+同一消息视为同一条诊断。
func dedupeKey(d Diagnostic) string {
	return fmt.Sprintf("%s:%s:%d:%d:%s", d.Code, d.Span.File, d.Span.StartLine, d.Span.EndLine, d.Message)
}
