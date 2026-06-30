package reference

import "go/ast"

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

func unwrapGenericCallee(expr ast.Expr) ast.Expr {
	switch x := expr.(type) {
	case *ast.IndexExpr:
		return x.X
	case *ast.IndexListExpr:
		return x.X
	default:
		return expr
	}
}

func genericTypeArguments(expr ast.Expr) []ast.Expr {
	switch x := expr.(type) {
	case *ast.IndexExpr:
		return []ast.Expr{x.Index}
	case *ast.IndexListExpr:
		return append([]ast.Expr(nil), x.Indices...)
	default:
		return nil
	}
}
