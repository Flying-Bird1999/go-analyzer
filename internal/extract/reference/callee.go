package reference

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func resolveCallCandidates(file *project.File, idx *astindex.Index, scopedTypes scopedValueTypes, call *ast.CallExpr) ([]astindex.ResolvedSymbol, string, bool) {
	switch fun := unwrapGenericCallee(call.Fun).(type) {
	case *ast.Ident:
		id := astindex.FunctionSymbolID(file.Package.Path, fun.Name)
		if _, ok := idx.Symbols[id]; ok {
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, fun.Name, true
		}
		if id, ok := idx.PackageValueSymbol(fun.Obj); ok {
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, fun.Name, true
		}
		id = astindex.ValueSymbolID("var", file.Package.Path, fun.Name)
		if _, ok := idx.Symbols[id]; ok {
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, fun.Name, true
		}
		return nil, fun.Name, false
	case *ast.SelectorExpr:
		return resolveSelectorCandidates(file, idx, scopedTypes, fun)
	default:
		return nil, "", false
	}
}

func resolveSelectorCandidates(file *project.File, idx *astindex.Index, scopedTypes scopedValueTypes, selector *ast.SelectorExpr) ([]astindex.ResolvedSymbol, string, bool) {
	parts := selectorParts(selector)
	raw := strings.Join(parts, ".")
	if len(parts) == 2 {
		if importPath := file.Imports[parts[0]]; importPath != "" {
			id := astindex.FunctionSymbolID(importPath, parts[1])
			_, ok := idx.Symbols[id]
			if !ok {
				return nil, raw, false
			}
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, raw, true
		}
	}
	if len(parts) >= 2 {
		if valueTypes, ok := scopedTypes.resolveAll(selectorRootIdent(selector), selector.Pos()); ok {
			if len(valueTypes) != 1 {
				return resolveValueTypeMethodCandidates(idx, valueTypes, parts[1:], raw)
			}
			valueType := valueTypes[0]
			if resolved, ok := idx.ResolveValueTypeMethod(valueType, parts[1:]); ok {
				return []astindex.ResolvedSymbol{resolved}, raw, true
			}
			return nil, raw, false
		}
	}
	if resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, parts); ok {
		return []astindex.ResolvedSymbol{resolved}, raw, true
	}
	return nil, raw, false
}

func selectorRootIdent(selector *ast.SelectorExpr) *ast.Ident {
	var expr ast.Expr = selector
	for {
		switch current := expr.(type) {
		case *ast.SelectorExpr:
			expr = current.X
		case *ast.Ident:
			return current
		default:
			return nil
		}
	}
}

func resolveValueTypeMethodCandidates(idx *astindex.Index, valueTypes []astindex.ValueType, selectors []string, raw string) ([]astindex.ResolvedSymbol, string, bool) {
	if len(valueTypes) == 0 {
		return nil, raw, false
	}
	seen := map[facts.SymbolID]bool{}
	out := make([]astindex.ResolvedSymbol, 0, len(valueTypes))
	for _, valueType := range valueTypes {
		if valueType.TypeName == "" {
			continue
		}
		resolved, ok := idx.ResolveValueTypeMethod(valueType, selectors)
		if !ok {
			continue
		}
		if seen[resolved.ID] {
			continue
		}
		seen[resolved.ID] = true
		out = append(out, resolved)
	}
	return out, raw, len(out) > 0
}
