// handler.go 实现 handler 表达式的包装器栈拆解：递归剥离 handler 包装器，
// 返回最内层 handler 的原始文本与按外到内顺序排列的包装器事实。
package route

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// unwrapHandler 拆解 handler 包装器栈，返回最内层 handler 原始文本与包装器列表。
func unwrapHandler(expr ast.Expr) (string, []facts.WrapperFact) {
	return unwrapHandlerDepth(expr, 0)
}

// unwrapHandlerDepth 在限定递归深度内拆解包装器栈，避免无限递归。
func unwrapHandlerDepth(expr ast.Expr, depth int) (string, []facts.WrapperFact) {
	if depth > 16 {
		return exprString(expr), nil
	}
	// 跳过多余的括号包裹。
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return unwrapHandlerDepth(paren.X, depth)
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return exprString(expr), nil
	}
	name := shortCallName(call)
	if name == "" || len(call.Args) == 0 {
		return exprString(expr), nil
	}
	// 定位当前调用中被视为 handler 的参数，继续向内拆解。
	handlerArg, ok := handlerArgument(call)
	if !ok {
		return exprString(expr), nil
	}
	handlerRaw, wrappers := unwrapHandlerDepth(handlerArg, depth+1)
	// 当前层包装器置于栈顶，保证顺序为外到内。
	return handlerRaw, append([]facts.WrapperFact{{Name: name, Raw: exprString(call)}}, wrappers...)
}

// handlerArgument 从一个调用表达式中找出代表 handler 的参数：
// 若调用名是已知 handler 包装器则取最后一个参数，否则从后向前找首个 handler 风格表达式。
func handlerArgument(call *ast.CallExpr) (ast.Expr, bool) {
	if len(call.Args) == 0 {
		return nil, false
	}
	if isHandlerWrapper(shortCallName(call)) {
		return call.Args[len(call.Args)-1], true
	}
	for i := len(call.Args) - 1; i >= 0; i-- {
		if isHandlerLikeExpr(call.Args[i]) {
			return call.Args[i], true
		}
	}
	return nil, false
}

// isHandlerLikeExpr 判断表达式是否"长得像" handler（标识符/选择器/带参调用/括号包裹的上述形式）。
func isHandlerLikeExpr(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return true
	case *ast.CallExpr:
		return len(x.Args) > 0
	case *ast.ParenExpr:
		return isHandlerLikeExpr(x.X)
	default:
		return false
	}
}

// shortCallName 返回调用全名中最后一个点之后的短名，例如 a.b.Foo -> Foo。
func shortCallName(call *ast.CallExpr) string {
	name := callName(call)
	if name == "" {
		return ""
	}
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}
