// im.go 实现出站 IM 事件事实类型及其依赖与证据，由 IM extractor 产出。
// IMGraph 在传播时按 path 上的 payload/event/control 依赖精确匹配 sender 下的 event，
// 避免同一 sender 多 event 产生误报。

package facts

// IMEventRelation 枚举 IM 事件依赖的关系种类，用于精确区分同一 sender 下的多个 event。
type IMEventRelation string

const (
	// IMRelationPayload 表示载荷依赖（直接类型、selector 字段、泛型结果、converter 返回值、producer）。
	IMRelationPayload IMEventRelation = "im_payload"
	// IMRelationEventValue 表示事件值依赖（event 常量、拼接、iota+String() 字符串表等）。
	IMRelationEventValue IMEventRelation = "im_event_value"
	// IMRelationControl 表示控制依赖（wrapper、条件分支等影响是否发送的因素）。
	IMRelationControl IMEventRelation = "im_control"
)

// IMEventDependency 描述一个 IM 事件对某 symbol 的依赖，IMGraph 按此与传播 path 精确相交。
type IMEventDependency struct {
	// SymbolID 是被依赖的 symbol ID。
	SymbolID SymbolID `json:"symbol_id"`
	// Relation 是依赖关系种类（payload / event_value / control）。
	Relation IMEventRelation `json:"relation"`
	// Confidence 是该依赖的静态证据强度。
	Confidence Confidence `json:"confidence"`
	// Span 是该依赖表达式的位置区间，缺失时不输出。
	Span SourceSpan `json:"span,omitempty"`
}

// IMEventEvidence 记录 IM 事件某条依赖关系对应的源码证据位置。
type IMEventEvidence struct {
	// Relation 是该证据对应的依赖关系种类。
	Relation IMEventRelation `json:"relation"`
	// Span 是该证据在源码中的位置区间。
	Span SourceSpan `json:"span"`
}

// IMEventFact 描述一个出站 IM 事件，包含 sender、event 值、依赖与证据。
// 动态 event（无法静态求值）保留 Resolved=false，投影为 im_event_unresolved 终端，不计入 IM 摘要。
type IMEventFact struct {
	// ID 是该 IM 事件事实的唯一标识。
	ID string `json:"id"`
	// Event 是静态求值成功的 event 字符串，求值失败时留空不输出。
	Event string `json:"event,omitempty"`
	// EventRaw 是无法静态求值时的原始表达式文本，供调试与人工 review。
	EventRaw string `json:"event_raw,omitempty"`
	// SenderSymbol 是发送该事件的 sender symbol ID。
	SenderSymbol SymbolID `json:"sender_symbol"`
	// Dependencies 是该事件的 payload/event/control 依赖列表，IMGraph 据此精确匹配传播 path。
	Dependencies []IMEventDependency `json:"dependencies"`
	// Evidence 是该事件各依赖关系对应的源码证据列表。
	Evidence []IMEventEvidence `json:"evidence"`
	// Confidence 是该 IM 事件的静态证据强度。
	Confidence Confidence `json:"confidence"`
	// Span 是 sender 调用表达式的位置区间。指针类型以便 omitempty 在 nil 时省略；
	// 当前 summary 投影总是赋非零值，nil 仅为防御性语义。
	Span *SourceSpan `json:"span,omitempty"`
	// Resolved 指示 event 是否能静态求值为确定字符串；false 表示动态 event（im_event_unresolved）。
	Resolved bool `json:"resolved"`
}
