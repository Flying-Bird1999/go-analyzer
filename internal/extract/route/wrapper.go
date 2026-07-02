package route

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func groupForExpr(file *project.File, funcs map[facts.SymbolID]routeFunction, groups map[string]groupContext, expr ast.Expr) (groupContext, []facts.WrapperFact, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		group, ok := groups[x.Name]
		return group, nil, ok
	case *ast.CallExpr:
		name := shortCallName(x)
		if len(x.Args) == 0 {
			return groupContext{}, nil, false
		}
		if !isRouteGroupWrapper(name) {
			return groupContext{}, nil, false
		}
		if unresolvedSelectorRouteFunction(file, funcs, x.Fun) {
			return groupContext{}, nil, false
		}
		if callee, resolved := resolveRouteFunctionCall(file, x.Fun); resolved {
			if target, projectFunction := funcs[callee]; projectFunction && !target.returnsGroup {
				return groupContext{}, nil, false
			}
		}
		group, wrappers, ok := groupForExpr(file, funcs, groups, x.Args[0])
		if !ok {
			return groupContext{}, nil, false
		}
		if name != "" {
			wrappers = append([]facts.WrapperFact{{Name: name, Raw: exprString(x)}}, wrappers...)
		}
		return group, wrappers, true
	default:
		return groupContext{}, nil, false
	}
}

func unresolvedSelectorRouteFunction(file *project.File, funcs map[facts.SymbolID]routeFunction, expr ast.Expr) bool {
	if _, ok := expr.(*ast.SelectorExpr); !ok {
		return false
	}
	callee, resolved := resolveRouteFunctionCall(file, expr)
	if !resolved {
		return false
	}
	_, ok := funcs[callee]
	return !ok
}
