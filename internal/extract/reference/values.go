package reference

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func extractValueReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	ignored := ignoredValuePositions(fn.Body)
	callFuns := callFunPositions(fn.Body)

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.SelectorExpr:
			if ignored[x.Pos()] {
				return false
			}
			var targets []facts.SymbolID
			if callFuns[x.Pos()] {
				targets = resolveReceiverValueIDs(file, idx, x)
			} else {
				targets = resolveValueIDs(file, idx, x)
			}
			addValueReferenceFacts(p, file, store, from, x, targets)
			return false
		case *ast.Ident:
			if ignored[x.Pos()] || callFuns[x.Pos()] || isLocalIdentifier(idx, x) {
				return true
			}
			addValueReferenceFacts(p, file, store, from, x, resolveValueIDs(file, idx, x))
		}
		return true
	})
}

func resolveValueIDs(file *project.File, idx *astindex.Index, expr ast.Expr) []facts.SymbolID {
	switch x := expr.(type) {
	case *ast.Ident:
		if id, ok := idx.PackageValueSymbol(x.Obj); ok {
			return []facts.SymbolID{id}
		}
		if isLocalIdentifier(idx, x) {
			return nil
		}
		return existingValueIDs(idx, file.Package.Path, x.Name)
	case *ast.SelectorExpr:
		parts := selectorParts(x)
		if len(parts) == 2 {
			if importPath := file.Imports[parts[0]]; importPath != "" {
				return existingValueIDs(idx, importPath, parts[1])
			}
			if isLocalIdentifier(idx, selectorRootIdent(x)) {
				return nil
			}
			return resolveLocalVarMethod(idx, file, parts)
		}
		if len(parts) >= 3 {
			importPath := file.Imports[parts[0]]
			if importPath == "" {
				return nil
			}
			varID := astindex.ValueSymbolID("var", importPath, parts[1])
			out := existingIDs(idx, varID)
			if methodID, ok := idx.ResolveSelectorMethod(file, parts); ok {
				out = appendExistingID(out, idx, methodID)
			}
			return out
		}
	}
	return nil
}

func isLocalIdentifier(idx *astindex.Index, ident *ast.Ident) bool {
	if ident == nil || ident.Obj == nil {
		return false
	}
	if _, ok := idx.PackageValueSymbol(ident.Obj); ok {
		return false
	}
	return ident.Obj.Kind == ast.Var || ident.Obj.Kind == ast.Con
}

func resolveReceiverValueIDs(file *project.File, idx *astindex.Index, selector *ast.SelectorExpr) []facts.SymbolID {
	parts := selectorParts(selector)
	if len(parts) == 3 {
		if importPath := file.Imports[parts[0]]; importPath != "" {
			return existingIDs(idx, astindex.ValueSymbolID("var", importPath, parts[1]))
		}
	}
	if len(parts) == 2 {
		return existingIDs(idx, astindex.ValueSymbolID("var", file.Package.Path, parts[0]))
	}
	return nil
}

func existingValueIDs(idx *astindex.Index, pkgPath, name string) []facts.SymbolID {
	var out []facts.SymbolID
	out = appendExistingID(out, idx, astindex.ValueSymbolID("var", pkgPath, name))
	out = appendExistingID(out, idx, astindex.ValueSymbolID("const", pkgPath, name))
	out = appendExistingID(out, idx, astindex.FunctionSymbolID(pkgPath, name))
	return out
}

func resolveLocalVarMethod(idx *astindex.Index, file *project.File, parts []string) []facts.SymbolID {
	if len(parts) < 2 {
		return nil
	}
	varID := astindex.ValueSymbolID("var", file.Package.Path, parts[0])
	var out []facts.SymbolID
	out = appendExistingID(out, idx, varID)
	if methodID, ok := idx.ResolveSelectorMethod(file, parts); ok {
		out = appendExistingID(out, idx, methodID)
	}
	return out
}

func existingIDs(idx *astindex.Index, ids ...facts.SymbolID) []facts.SymbolID {
	var out []facts.SymbolID
	for _, id := range ids {
		out = appendExistingID(out, idx, id)
	}
	return out
}

func appendExistingID(out []facts.SymbolID, idx *astindex.Index, id facts.SymbolID) []facts.SymbolID {
	if _, ok := idx.Symbols[id]; !ok {
		return out
	}
	for _, existing := range out {
		if existing == id {
			return out
		}
	}
	return append(out, id)
}

func addValueReferenceFacts(p *project.Project, file *project.File, store *facts.Store, from facts.SymbolID, expr ast.Expr, targets []facts.SymbolID) {
	for _, target := range targets {
		if target == "" || target == from {
			continue
		}
		span := spanFor(p, file, expr.Pos(), expr.End())
		store.References = append(store.References, facts.ReferenceFact{
			ID:         referenceID(from, target, facts.ReferenceKindValue, span),
			Kind:       facts.ReferenceKindValue,
			FromSymbol: from,
			ToSymbol:   target,
			ToRaw:      typeExprString(file, expr),
			Confidence: facts.ConfidenceHigh,
			Span:       span,
		})
	}
}

func ignoredValuePositions(root ast.Node) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CompositeLit:
			markExprPositions(out, x.Type)
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok {
				out[id.Pos()] = true
			}
		}
		return true
	})
	return out
}

func markExprPositions(out map[token.Pos]bool, expr ast.Expr) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		if node != nil {
			out[node.Pos()] = true
		}
		return true
	})
}

func callFunPositions(root ast.Node) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			out[call.Fun.Pos()] = true
		}
		return true
	})
	return out
}
