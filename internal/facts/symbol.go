// symbol.go 实现声明符号事实类型 SymbolFact，由 astindex 产出。

package facts

// SymbolFact 描述项目内一个 declaration symbol，由 astindex.Build 产出。
// 当前粒度覆盖 function、receiver method、type、package-level var/const。
type SymbolFact struct {
	// ID 是稳定 symbol ID，形式如 func:<package>::<name>、method:<package>:<receiver>:<name> 等。
	ID SymbolID `json:"id"`
	// Kind 是符号种类：function、method、type、var、const。
	Kind string `json:"kind"`
	// PackagePath 是符号所属 package path。
	PackagePath string `json:"package_path"`
	// Receiver 仅对 method 有效，记录 receiver type 名称；其他种类留空不输出。
	Receiver string `json:"receiver,omitempty"`
	// Name 是符号名称，与声明中的标识符一致。
	Name string `json:"name"`
	// Span 是该声明在源码中的位置区间。
	Span SourceSpan `json:"span"`
}
