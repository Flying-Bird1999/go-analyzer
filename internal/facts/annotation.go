// annotation.go 实现 handler HTTP 注解事实类型 AnnotationFact，由 annotation extractor 产出。

package facts

// AnnotationFact 描述从 handler 注释中提取的 HTTP method/path 注解，例如 @Get /api/foo。
// span 精确到注释行而非整个函数体，保证“改注释”归为 annotation_changed、改函数体归为符号本身。
type AnnotationFact struct {
	// ID 是注解事实的唯一标识。
	ID string `json:"id"`
	// Kind 固定为 annotation，用于区分事实类型。
	Kind string `json:"kind"`
	// Method 是 HTTP 方法，如 GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS。
	Method string `json:"method"`
	// Path 是注解中声明的 endpoint 路径。
	Path string `json:"path"`
	// Raw 是注解注释行的原始文本，供调试与人工 review。
	Raw string `json:"raw"`
	// HandlerSymbol 是该注解所属 handler 的稳定 symbol ID。
	HandlerSymbol SymbolID `json:"handler_symbol"`
	// Span 是注解所在注释行的位置区间。
	Span SourceSpan `json:"span"`
}
