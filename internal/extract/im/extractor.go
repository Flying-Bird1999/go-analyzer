package im

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func Extract(p *project.Project, idx *astindex.Index, store *facts.Store) error {
	engine := newSummaryEngine(p, idx)
	events := engine.extract()
	store.IMEvents = append(store.IMEvents, events...)
	sort.Slice(store.IMEvents, func(i, j int) bool {
		return store.IMEvents[i].ID < store.IMEvents[j].ID
	})
	return nil
}

func spanForNode(p *project.Project, file *project.File, node ast.Node) facts.SourceSpan {
	if node == nil {
		return facts.SourceSpan{}
	}
	start := file.FileSet.Position(node.Pos())
	end := file.FileSet.Position(node.End())
	rel, err := filepath.Rel(p.Root, file.Path)
	if err != nil {
		rel = file.Path
	}
	return facts.SourceSpan{
		File:      filepath.ToSlash(rel),
		StartLine: start.Line,
		StartCol:  start.Column,
		EndLine:   end.Line,
		EndCol:    end.Column,
	}
}

func eventFactID(sender facts.SymbolID, event string, span facts.SourceSpan) string {
	if event == "" {
		event = "unresolved"
	}
	return fmt.Sprintf("im_event:%s:%s:%s:%d:%d", sender, event, span.File, span.StartLine, span.StartCol)
}
