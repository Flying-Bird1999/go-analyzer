// diagnostics.go 定义可恢复不确定性的标准诊断模型。
//
// Package diagnostics 定义 go-analyzer 各阶段共用的诊断模型。诊断表达“可恢复的
// 不确定性”——例如动态路由路径、无法精确解析的符号、严格证据下的接口分发歧义等——
// 它不等于程序失败：发生诊断时分析仍会继续，只是在结果或诊断列表中标注需要人工复核。
// 影响范围报告（impact JSON）不输出诊断，因此 diagnostics 主要服务于 facts 调试与
// 项目级不确定性观察。
package diagnostics

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

// Diagnostic 是一条诊断的内部表示。它与 facts.DiagnosticFact 同构，区别在于
// Code/Severity 使用本包的强类型（Code/Severity），便于在生成阶段做类型校验；
// 进入 facts.Store 前由 ToFact 转为字符串形式。
type Diagnostic struct {
	// ID 是诊断的唯一标识。未显式设置时，Collector.Add 会基于 key 自动生成。
	ID string `json:"id"`
	// Code 是诊断码，定义见 codes.go，标识诊断的种类。
	Code Code `json:"code"`
	// Severity 是严重级别（info/warning/error）。
	Severity Severity `json:"severity"`
	// Message 是面向人类的诊断说明。
	Message string `json:"message"`
	// Span 是诊断关联的源码位置区间；无具体位置时不输出。
	Span facts.SourceSpan `json:"span,omitempty"`
	// RelatedFactIDs 列出与诊断相关的其他事实 ID，便于追溯；为空时不输出。
	RelatedFactIDs []string `json:"related_fact_ids,omitempty"`
}
