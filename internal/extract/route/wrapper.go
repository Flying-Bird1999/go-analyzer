// wrapper.go 实现对路由组包装器表达式的解析：递归剥离子路由组上的包装器调用，
// 返回底层组上下文与按外到内顺序的包装器事实。
package route

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// groupForExpr 把一个路由组表达式解析为组上下文加包装器栈：
// 标识符直接查表；调用则递归处理首参，并把当前包装器前置入栈。
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
		// 非路由组包装器调用不视为合法组表达式。
		if !isRouteGroupWrapper(name) {
			return groupContext{}, nil, false
		}
		// 选择器形式的调用若指向项目外或无法解析的函数，保守跳过。
		if unresolvedSelectorRouteFunction(file, funcs, x.Fun) {
			return groupContext{}, nil, false
		}
		// 若解析到项目内函数但其返回类型不是路由组，说明并非真正的组包装器。
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

// unresolvedSelectorRouteFunction 判断选择器调用是否指向项目外（无法在 funcs 中解析到）的函数。
// 项目外的函数保持乐观接受；只有能解析但不在 funcs 中时才视为未解析。
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
