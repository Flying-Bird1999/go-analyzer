package route

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func unwrapHandler(expr ast.Expr) (string, []facts.WrapperFact) {
	return unwrapHandlerDepth(expr, 0)
}

func unwrapHandlerDepth(expr ast.Expr, depth int) (string, []facts.WrapperFact) {
	if depth > 16 {
		return exprString(expr), nil
	}
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
	handlerArg, ok := handlerArgument(call)
	if !ok {
		return exprString(expr), nil
	}
	handlerRaw, wrappers := unwrapHandlerDepth(handlerArg, depth+1)
	return handlerRaw, append([]facts.WrapperFact{{Name: name, Raw: exprString(call)}}, wrappers...)
}

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

func shortCallName(call *ast.CallExpr) string {
	name := callName(call)
	if name == "" {
		return ""
	}
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}
