// evidence.go 实现统一的 AST 表达式证据类型 EvidenceFact，
// 供 ReferenceFact 与 RouteRegistrationFact 等记录关键表达式证据，便于 facts 调试与后续解释能力复用。

package facts

// EvidenceFact 记录一条关键 AST 表达式的证据，包含种类、原始文本、位置区间与置信度。
type EvidenceFact struct {
	// Kind 是证据对应的 AST 表达式种类标识。
	Kind string `json:"kind"`
	// Raw 是表达式的原始文本，缺失时不输出。
	Raw string `json:"raw,omitempty"`
	// Span 是该表达式的位置区间。
	Span SourceSpan `json:"span"`
	// Confidence 是该证据的静态证据强度，缺失时不输出。
	Confidence Confidence `json:"confidence,omitempty"`
}
