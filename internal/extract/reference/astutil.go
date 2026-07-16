// astutil.go 提供 reference 包共用的 AST 小工具：选择器分段、泛型被调者剥离与类型实参提取。
package reference

import "go/ast"

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

// containsCallExpr 判断表达式子树内是否存在函数调用。用于识别链式调用的接收者
// 表达式（如 Helper(g).Method(...) 中的 Helper(g)），以便在选择器整体解析后仍
// 单独遍历接收者子树、补齐被剪枝的调用引用。
func containsCallExpr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		if _, ok := node.(*ast.CallExpr); ok {
			found = true
			return false
		}
		return true
	})
	return found
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
