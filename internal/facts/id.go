// id.go 定义全包复用的稳定符号 ID 类型 SymbolID。

package facts

// SymbolID 是声明符号的稳定标识类型，形式如 func:<package>::<name>、
// method:<package>:<receiver>:<name>、type:<package>::<name>、var/const:<package>::<name>，
// 由 astindex 生成并在各 fact 之间流转。
type SymbolID string
