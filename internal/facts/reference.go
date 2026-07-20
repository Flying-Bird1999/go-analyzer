// reference.go 实现代码依赖边事实类型 ReferenceFact，以及跨事实复用的 reference kind 定义。
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
	// Span 是该引用表达式的位置区间。
	Span SourceSpan `json:"span"`
	// Evidence 记录关键 AST 表达式的证据，供 facts 调试与解释能力复用，缺失时不输出。
	Evidence []EvidenceFact `json:"evidence,omitempty"`
}
