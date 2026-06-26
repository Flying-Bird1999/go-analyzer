package astindex

import (
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

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
