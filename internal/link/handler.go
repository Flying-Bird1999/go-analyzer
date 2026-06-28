package link

import (
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func ResolveHandlerSymbol(idx *astindex.Index, route facts.RouteRegistrationFact) (facts.SymbolID, bool) {
	resolved, ok := ResolveHandlerSymbolWithConfidence(idx, route)
	return resolved.ID, ok
}

func ResolveHandlerSymbolWithConfidence(idx *astindex.Index, route facts.RouteRegistrationFact) (astindex.ResolvedSymbol, bool) {
	if idx == nil || idx.Project == nil || route.HandlerRaw == "" {
		return astindex.ResolvedSymbol{}, false
	}
	file := fileByRelativePath(idx.Project, route.File)
	if file == nil {
		return astindex.ResolvedSymbol{}, false
	}
	parts := strings.Split(route.HandlerRaw, ".")
	if len(parts) == 1 {
		id := astindex.FunctionSymbolID(file.Package.Path, parts[0])
		_, ok := idx.Symbols[id]
		return astindex.ResolvedSymbol{ID: id, Confidence: facts.ConfidenceHigh}, ok
	}
	importPath := file.Imports[parts[0]]
	if len(parts) == 2 {
		if importPath != "" {
			id := astindex.FunctionSymbolID(importPath, parts[1])
			if _, ok := idx.Symbols[id]; ok {
				return astindex.ResolvedSymbol{ID: id, Confidence: facts.ConfidenceHigh}, true
			}
			id = astindex.ValueSymbolID("var", importPath, parts[1])
			_, ok := idx.Symbols[id]
			return astindex.ResolvedSymbol{ID: id, Confidence: facts.ConfidenceHigh}, ok
		}
		return idx.ResolveSelectorMethodWithConfidence(file, parts)
	}
	if len(parts) >= 3 {
		return idx.ResolveSelectorMethodWithConfidence(file, parts)
	}
	return astindex.ResolvedSymbol{}, false
}
