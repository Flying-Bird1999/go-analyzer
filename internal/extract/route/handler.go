package route

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func unwrapHandler(expr ast.Expr, cfg config.Config) (string, []facts.WrapperFact) {
	return unwrapHandlerDepth(expr, cfg, 0)
}

func unwrapHandlerDepth(expr ast.Expr, cfg config.Config, depth int) (string, []facts.WrapperFact) {
	if depth > 16 {
		return exprString(expr), nil
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return exprString(expr), nil
	}
	name := shortCallName(call)
	if name == "" || len(call.Args) == 0 {
		return exprString(expr), nil
	}
	handlerArg, ok := handlerArgument(call, cfg)
	if !ok {
		return exprString(expr), nil
	}
	handlerRaw, wrappers := unwrapHandlerDepth(handlerArg, cfg, depth+1)
	return handlerRaw, append([]facts.WrapperFact{{Name: name, Raw: exprString(call)}}, wrappers...)
}

func handlerArgument(call *ast.CallExpr, cfg config.Config) (ast.Expr, bool) {
	if len(call.Args) == 0 {
		return nil, false
	}
	if cfg.IsHandlerWrapper(shortCallName(call)) {
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
