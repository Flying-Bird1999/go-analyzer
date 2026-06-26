package astindex

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

func FunctionSymbolID(pkgPath, name string) facts.SymbolID {
	return facts.SymbolID("func:" + pkgPath + "::" + name)
}

func MethodSymbolID(pkgPath, receiver, name string) facts.SymbolID {
	return facts.SymbolID("method:" + pkgPath + ":" + receiver + ":" + name)
}

func TypeSymbolID(pkgPath, name string) facts.SymbolID {
	return facts.SymbolID("type:" + pkgPath + "::" + name)
}

func ValueSymbolID(kind, pkgPath, name string) facts.SymbolID {
	return facts.SymbolID(kind + ":" + pkgPath + "::" + name)
}
