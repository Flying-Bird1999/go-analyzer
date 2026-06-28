package reference

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func resolveCall(file *project.File, idx *astindex.Index, call *ast.CallExpr) (facts.SymbolID, string, facts.Confidence, bool) {
	switch fun := unwrapGenericCallee(call.Fun).(type) {
	case *ast.Ident:
		id := astindex.FunctionSymbolID(file.Package.Path, fun.Name)
		_, ok := idx.Symbols[id]
		return id, fun.Name, facts.ConfidenceHigh, ok
	case *ast.SelectorExpr:
		return resolveSelector(file, idx, fun)
	default:
		return "", "", "", false
	}
}

func resolveSelector(file *project.File, idx *astindex.Index, selector *ast.SelectorExpr) (facts.SymbolID, string, facts.Confidence, bool) {
	parts := selectorParts(selector)
	raw := strings.Join(parts, ".")
	if len(parts) == 2 {
		if importPath := file.Imports[parts[0]]; importPath != "" {
			id := astindex.FunctionSymbolID(importPath, parts[1])
			_, ok := idx.Symbols[id]
			return id, raw, facts.ConfidenceHigh, ok
		}
	}
	if resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, parts); ok {
		return resolved.ID, raw, resolved.Confidence, true
	}
	return "", raw, "", false
}
