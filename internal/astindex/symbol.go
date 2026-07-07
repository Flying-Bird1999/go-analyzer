// symbol.go 实现稳定的声明符号 ID 拼装与接收者类型名提取。
package astindex

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// FunctionSymbolID 拼出包级 function 的符号 ID，形式为 func:<package>::<name>。
func FunctionSymbolID(pkgPath, name string) facts.SymbolID {
	return facts.SymbolID("func:" + pkgPath + "::" + name)
}

// MethodSymbolID 拼出 receiver method 的符号 ID，形式为
// method:<package>:<receiver>:<name>。receiver 用类型名表示，剥离指针/泛型包装。
func MethodSymbolID(pkgPath, receiver, name string) facts.SymbolID {
	return facts.SymbolID("method:" + pkgPath + ":" + receiver + ":" + name)
}

// TypeSymbolID 拼出 type 声明的符号 ID，形式为 type:<package>::<name>。
func TypeSymbolID(pkgPath, name string) facts.SymbolID {
	return facts.SymbolID("type:" + pkgPath + "::" + name)
}

// ValueSymbolID 拼出包级 var/const 的符号 ID，形式为
// <kind>:<package>::<name>，其中 kind 取 "var" 或 "const"。
func ValueSymbolID(kind, pkgPath, name string) facts.SymbolID {
	return facts.SymbolID(kind + ":" + pkgPath + "::" + name)
}

// ReceiverTypeName 返回 method receiver 声明的 base 类型名。
// 剥离指针（*T）、括号、限定包（pkg.T 取 T）以及泛型实例化（T[A] 取 T），
// 使同一类型上的值/指针接收者方法可以归到同一个 receiver 名。
func ReceiverTypeName(expr ast.Expr) string {
	switch receiver := expr.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		// 指针接收者 *T：递归取被指类型的名字。
		return ReceiverTypeName(receiver.X)
	case *ast.SelectorExpr:
		// 跨包限定名 pkg.T：使用类型名部分作为 receiver，跨包 method 仍可解析。
		return receiver.Sel.Name
	case *ast.IndexExpr:
		// 泛型实例化 T[A]：剥离类型实参，取左侧基础类型。
		return ReceiverTypeName(receiver.X)
	case *ast.IndexListExpr:
		// 多类型参数泛型实例化 T[A, B]：同样剥离取基础类型。
		return ReceiverTypeName(receiver.X)
	default:
		return ""
	}
}
