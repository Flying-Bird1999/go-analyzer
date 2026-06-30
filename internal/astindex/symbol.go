package astindex

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

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

// ReceiverTypeName returns the declared base type name for a method receiver.
func ReceiverTypeName(expr ast.Expr) string {
	switch receiver := expr.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		return ReceiverTypeName(receiver.X)
	case *ast.SelectorExpr:
		return receiver.Sel.Name
	case *ast.IndexExpr:
		return ReceiverTypeName(receiver.X)
	case *ast.IndexListExpr:
		return ReceiverTypeName(receiver.X)
	default:
		return ""
	}
}
