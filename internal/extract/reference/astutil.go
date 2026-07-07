// astutil.go 提供 reference 包共用的 AST 小工具：选择器分段、泛型被调者剥离与类型实参提取。
package reference

import "go/ast"

// selectorParts 将选择器链拆成按点分隔的字符串段，例如 a.b.c -> ["a","b","c"]。
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

// unwrapGenericCallee 剥去泛型调用上的类型实参（IndexExpr/IndexListExpr），返回真正的被调表达式。
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

// genericTypeArguments 提取泛型调用的显式类型实参列表；非泛型表达式返回 nil。
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
