package link

import (
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func ResolveHandlerSymbol(idx *astindex.Index, route facts.RouteRegistrationFact) (facts.SymbolID, bool) {
	if idx == nil || idx.Project == nil || route.HandlerRaw == "" {
		return "", false
	}
	file := fileByRelativePath(idx.Project, route.File)
	if file == nil {
		return "", false
	}
	parts := strings.Split(route.HandlerRaw, ".")
	if len(parts) == 1 {
		id := astindex.FunctionSymbolID(file.Package.Path, parts[0])
		_, ok := idx.Symbols[id]
		return id, ok
	}
	importPath := file.Imports[parts[0]]
	if importPath == "" {
		return "", false
	}
	if len(parts) == 2 {
		id := astindex.FunctionSymbolID(importPath, parts[1])
		if _, ok := idx.Symbols[id]; ok {
			return id, true
		}
		id = astindex.ValueSymbolID("var", importPath, parts[1])
		_, ok := idx.Symbols[id]
		return id, ok
	}
	if len(parts) == 3 {
		varID := astindex.ValueSymbolID("var", importPath, parts[1])
		receiver := idx.VarReceiverTypes[string(varID)]
		if receiver == "" {
			return "", false
		}
		id := astindex.MethodSymbolID(importPath, receiver, parts[2])
		_, ok := idx.Symbols[id]
		return id, ok
	}
	return "", false
}
