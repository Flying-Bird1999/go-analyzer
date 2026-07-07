// position.go 实现声明源码位置的 span 计算。
package astindex

import (
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// SourceSpanFor 将一对 token.Pos 转换为 facts.SourceSpan。
// 调用方传入 FileSet 以及声明/表达式的起止位置，这里利用 FileSet
// 将 token 偏移翻译回带行列号的源码区间，供后续 facts 存储和 diff 命中比较。
func SourceSpanFor(fset *token.FileSet, start, end token.Pos) facts.SourceSpan {
	startPos := fset.Position(start)
	endPos := fset.Position(end)
	return facts.SourceSpan{
		File:      startPos.Filename,
		StartLine: startPos.Line,
		StartCol:  startPos.Column,
		EndLine:   endPos.Line,
		EndCol:    endPos.Column,
	}
}
