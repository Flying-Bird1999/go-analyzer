// reference.go 实现代码依赖边事实类型 ReferenceFact，以及跨事实复用的 confidence 与 reference kind 定义。
// 由 reference extractor 产出；internal/graph 会把它转成 ToSymbol -> []FromSymbol 的反向查询视图。

package facts

// ReferenceKind 枚举代码依赖边的种类。
type ReferenceKind string

const (
	// ReferenceKindCall 表示函数/方法调用边。
	ReferenceKindCall ReferenceKind = "call"
	// ReferenceKindType 表示类型引用边（参数、返回值、字段、组合字面量、泛型参数等）。
	ReferenceKindType ReferenceKind = "type"
	// ReferenceKindValue 表示 var/const/function value 引用边。
	ReferenceKindValue ReferenceKind = "value"
)

// Confidence 表示 analyzer 对某个 fact、change root 或传播节点的静态证据强度，
// 不是概率分数；impact 不会按 confidence 自动截断已建立的传播边。
type Confidence string

const (
	// ConfidenceHigh 表示来自明确 AST/fact 证据，例如 diff 命中现存 symbol/route/annotation、
	// reference/link 精确解析。
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium 表示来自定向 fallback 或推断，例如 deletion anchor 命中 surviving declaration、
	// deleted route 用 method/path fallback endpoint、go.mod usage 降级到 importing file declarations。
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow 表示只能保留弱 fallback，例如无法映射到语义 fact 的 file-level root。
	ConfidenceLow Confidence = "low"
)

// CombineConfidence 返回沿传播链路合并后的置信度——取链路上最弱的一跳。
// parent 为空时取 edge，edge 为空时取 parent，两者都为空时返回空串。
// 这保证弱根（如 file_changed/low）经 high 边到达 endpoint 后，结论仍为 low，
// 不会被最后一跳的高置信度静默升级。
func CombineConfidence(parent, edge Confidence) Confidence {
	rank := func(c Confidence) int {
		switch c {
		case ConfidenceLow:
			return 1
		case ConfidenceMedium:
			return 2
		case ConfidenceHigh:
			return 3
		default:
			return 0
		}
	}
	pr, er := rank(parent), rank(edge)
	if pr == 0 {
		return edge
	}
	if er == 0 {
		return parent
	}
	if pr <= er {
		return parent
	}
	return edge
}

// ReferenceFact 描述一条“FromSymbol 依赖 ToSymbol”的代码依赖边。
// 边方向为 FromSymbol depends on ToSymbol，graph 阶段构造反向索引供 impact 传播使用。
type ReferenceFact struct {
	// ID 是该引用事实的唯一标识。
	ID string `json:"id"`
	// Kind 是依赖边种类（call/type/value）。
	Kind ReferenceKind `json:"kind"`
	// FromSymbol 是发起引用的声明 symbol ID（当前正在扫描的 function/method/type/var/const）。
	FromSymbol SymbolID `json:"from_symbol"`
	// ToSymbol 是被引用的目标 symbol ID；无法解析为项目内 symbol 时留空不输出。
	ToSymbol SymbolID `json:"to_symbol,omitempty"`
	// ToRaw 是无法解析时的目标原始表达式文本，供调试；已解析时留空不输出。
	ToRaw string `json:"to_raw,omitempty"`
	// Confidence 是该引用边的静态证据强度。
	Confidence Confidence `json:"confidence"`
	// Span 是该引用表达式的位置区间。
	Span SourceSpan `json:"span"`
	// Evidence 记录关键 AST 表达式的证据，供 facts 调试与解释能力复用，缺失时不输出。
	Evidence []EvidenceFact `json:"evidence,omitempty"`
}
