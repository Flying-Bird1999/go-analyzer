package reference

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func Extract(p *project.Project, idx *astindex.Index, store *facts.Store) error {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				from := functionSymbol(pkg.Path, fn)
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					to, raw, ok := resolveCall(file, idx, call)
					if !ok || to == "" || to == from {
						return true
					}
					store.References = append(store.References, facts.ReferenceFact{
						ID:         referenceID(from, to, len(store.References)),
						Kind:       facts.ReferenceKindCall,
						FromSymbol: from,
						ToSymbol:   to,
						ToRaw:      raw,
						Confidence: facts.ConfidenceHigh,
						Span:       spanFor(p, file, call.Pos(), call.End()),
					})
					return true
				})
			}
		}
	}
	return nil
}

func functionSymbol(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, receiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func referenceID(from, to facts.SymbolID, index int) string {
	return fmt.Sprintf("ref:%s:%s:%d", from, to, index)
}

func spanFor(p *project.Project, file *project.File, start, end token.Pos) facts.SourceSpan {
	span := astindex.SourceSpanFor(file.FileSet, start, end)
	if rel, err := filepath.Rel(p.Root, span.File); err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return span
}
