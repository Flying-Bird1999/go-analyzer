package link

import (
	"go/ast"
	"go/parser"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func linkMiddlewareSymbols(idx *astindex.Index, store *facts.Store) {
	if idx == nil || idx.Project == nil {
		return
	}
	for i := range store.Middleware {
		binding := &store.Middleware[i]
		file := fileByRelativePath(idx.Project, binding.Span.File)
		if file == nil {
			continue
		}
		expr, err := parser.ParseExpr("[]any{" + binding.MiddlewareRaw + "}")
		if err != nil {
			continue
		}
		seen := map[facts.SymbolID]bool{}
		addSymbol := func(candidate ast.Expr) {
			if symbol, ok := resolveCallable(idx, file, candidate); ok && !seen[symbol] {
				seen[symbol] = true
				binding.MiddlewareSymbols = append(binding.MiddlewareSymbols, symbol)
			}
		}
		composite, ok := expr.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, middlewareExpr := range composite.Elts {
			addSymbol(middlewareExpr)
			ast.Inspect(middlewareExpr, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if ok {
					addSymbol(call.Fun)
				}
				return true
			})
		}
	}
}

func resolveCallable(idx *astindex.Index, file *project.File, expr ast.Expr) (facts.SymbolID, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		id := astindex.FunctionSymbolID(file.Package.Path, x.Name)
		_, ok := idx.Symbols[id]
		return id, ok
	case *ast.SelectorExpr:
		parts := selectorParts(x)
		if len(parts) == 2 {
			if importPath := file.Imports[parts[0]]; importPath != "" {
				id := astindex.FunctionSymbolID(importPath, parts[1])
				if _, ok := idx.Symbols[id]; ok {
					return id, true
				}
			}
		}
		return idx.ResolveSelectorMethod(file, parts)
	}
	return "", false
}

func selectorParts(expr ast.Expr) []string {
	switch x := expr.(type) {
	case *ast.Ident:
		return []string{x.Name}
	case *ast.SelectorExpr:
		return append(selectorParts(x.X), x.Sel.Name)
	default:
		return nil
	}
}
