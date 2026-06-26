package route

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func unwrapHandler(expr ast.Expr, cfg config.Config) (string, []facts.WrapperFact) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return exprString(expr), nil
	}
	name := shortCallName(call)
	if name == "" || len(call.Args) == 0 || !cfg.IsHandlerWrapper(name) {
		return exprString(expr), nil
	}
	handlerArg := call.Args[len(call.Args)-1]
	handlerRaw, wrappers := unwrapHandler(handlerArg, cfg)
	return handlerRaw, append([]facts.WrapperFact{{Name: name, Raw: exprString(call)}}, wrappers...)
}

func shortCallName(call *ast.CallExpr) string {
	name := callName(call)
	if name == "" {
		return ""
	}
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}
