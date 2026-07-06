package im

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
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
	if engine.iterationCapped {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:     diagnostics.CodeIMSummaryIterationCapped,
			Severity: diagnostics.SeverityWarning,
			Message:  "IM summary propagation hit the iteration ceiling; results may be incomplete",
		})
	}
	reportSDKArgumentMismatches(p, store)
	return nil
}

// reportSDKArgumentMismatches surfaces calls that resolve to a known common IM
// SDK function by exact import path and name, but whose argument count is too
// small to carry the expected event/payload positions. That combination means
// the SDK signature drifted from the built-in adapter, which would otherwise
// silently drop a real outbound IM send. Emitting a diagnostic makes the drift
// visible instead of turning it into a missed event.
func reportSDKArgumentMismatches(p *project.Project, store *facts.Store) {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				fn, ok := rawDecl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					name, args, ok := sdkCandidate(file, call)
					if !ok {
						return true
					}
					if args.EventArg < len(call.Args) && args.PayloadArg < len(call.Args) {
						return true
					}
					span := spanForNode(p, file, call)
					diagnostics.AddFact(store, diagnostics.Diagnostic{
						Code:     diagnostics.CodeIMSDKArgumentMismatch,
						Severity: diagnostics.SeverityWarning,
						Message: fmt.Sprintf(
							"IM SDK call %q has %d arguments; adapter expects event at index %d and payload at index %d, so this send is not analyzed",
							name, len(call.Args), args.EventArg, args.PayloadArg,
						),
						Span: span,
					})
					return true
				})
			}
		}
	}
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
