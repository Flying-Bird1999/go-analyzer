// source.go 实现全包复用的源码位置区间类型 SourceSpan。

package facts

// SourceSpan 描述一段源码的位置区间，所有 fact 用它定位证据位置。
// File 在 facts/output 中统一为项目相对路径。
type SourceSpan struct {
	// File 是该区间所在文件（项目相对路径）。
	File string `json:"file"`
	// StartLine 是区间起始行号。
	StartLine int `json:"start_line"`
	// StartCol 是区间起始列号。
	StartCol int `json:"start_col"`
	// EndLine 是区间结束行号。
	EndLine int `json:"end_line"`
	// EndCol 是区间结束列号。
	EndCol int `json:"end_col"`
}
