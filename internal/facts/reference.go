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
//
// 空串（未设置）与非空但不属于 low/medium/high 三者之一的畸形值语义不同：前者是
// "该跳没有 confidence 输入"，直接让另一跳的值透传；后者代表数据本身有问题（typo、
// 未来新增 confidence 枚举值但常量定义未同步等）。畸形值不应被当作"缺失"而静默让位
// 给另一跳——那样会悄悄掩盖数据 bug，且视另一跳的值而定，结果可能被错误地当作 high。
// 这里让畸形值的 rank 落在 low 之下（弱于任何合法值），使其始终作为"链路最弱一跳"
// 原样透传出去：既保留了"取最弱"的既有语义，又保证畸形值可见（不会被吞掉），便于
// 从最终输出中定位问题数据，而不是凭空造出一个看似合法的 low。
func CombineConfidence(parent, edge Confidence) Confidence {
	// rank 返回置信度的强弱序数；ok 为 false 表示该值为"未设置"（空串），
	// 此时按原有语义直接透传另一跳。ok 为 true 时，已知合法值 rank 1-3，
	// 非空畸形值固定 rank 0（弱于 low 的 1），确保畸形值总是被判定为最弱一跳。
	rank := func(c Confidence) (value int, ok bool) {
		switch c {
		case "":
			return 0, false
		case ConfidenceLow:
			return 1, true
		case ConfidenceMedium:
			return 2, true
		case ConfidenceHigh:
			return 3, true
		default:
			// 非空但不属于三档合法值：视为弱于 low 的畸形数据，ok=true 使其参与
			// 正常的强弱比较（而非被当作缺失让位），rank=0 保证它必然是最弱一跳。
			return 0, true
		}
	}
	pr, pOK := rank(parent)
	er, eOK := rank(edge)
	if !pOK {
		return edge
	}
	if !eOK {
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
