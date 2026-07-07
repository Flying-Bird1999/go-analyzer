// call.go 实现路由调用（g.GET/POST/...）的结构化解析，供正常 AST 提取与
// deleted-route recovery 共用，避免两套语法解析逻辑漂移。
package route

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// ParsedRouteCall 表示一次解析后的路由调用结果：方法、本地路径、动态路径原始文本、
// handler 原始文本，以及路由组包装器栈和 handler 包装器栈。
type ParsedRouteCall struct {
	GroupRaw        string              // 路由调用接收者（路由组）的原始文本
	Method          string              // HTTP 方法（大写）
	LocalPath       string              // 本地路径字面量，无法解析时为空
	PathRaw         string              // 动态路径的原始表达式文本（LocalPath 为空时有值）
	HandlerRaw      string              // 最内层 handler 的原始文本
	GroupWrappers   []facts.WrapperFact // 路由组上的包装器栈（外到内）
	HandlerWrappers []facts.WrapperFact // handler 上的包装器栈（外到内）
}

// ParseRouteCall 把形如 wrapper(...).METHOD(path, handler) 的调用解析为 ParsedRouteCall。
// 仅当选择器名为 HTTP 方法且至少两个参数时才视为路由调用。
func ParseRouteCall(call *ast.CallExpr) (ParsedRouteCall, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isHTTPMethod(selector.Sel.Name) || len(call.Args) < 2 {
		return ParsedRouteCall{}, false
	}
	// 解析路由组接收者（含包装器栈）。
	groupRaw, groupWrappers, ok := parseRouteGroupExpr(selector.X)
	if !ok {
		return ParsedRouteCall{}, false
	}
	// 第一参数为路径：优先按字面量解析，失败则保留原始表达式文本。
	localPath, ok := stringLiteral(call.Args[0])
	pathRaw := ""
	if !ok {
		pathRaw = exprString(call.Args[0])
	}
	// 第二参数为 handler：拆解包装器栈得到最内层 handler 与包装器列表。
	handlerRaw, handlerWrappers := unwrapHandler(call.Args[1])
	return ParsedRouteCall{
		GroupRaw:        groupRaw,
		Method:          strings.ToUpper(selector.Sel.Name),
		LocalPath:       localPath,
		PathRaw:         pathRaw,
		HandlerRaw:      handlerRaw,
		GroupWrappers:   groupWrappers,
		HandlerWrappers: handlerWrappers,
	}, true
}

// parseRouteGroupExpr 解析路由调用的接收者表达式：
// 标识符即返回其名；包装器调用则递归首参并把当前包装器前置入栈。
func parseRouteGroupExpr(expr ast.Expr) (string, []facts.WrapperFact, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name, nil, true
	case *ast.CallExpr:
		name := shortCallName(x)
		if len(x.Args) == 0 || !isRouteGroupWrapper(name) {
			return "", nil, false
		}
		groupRaw, wrappers, ok := parseRouteGroupExpr(x.Args[0])
		if !ok {
			return "", nil, false
		}
		if name != "" {
			wrappers = append([]facts.WrapperFact{{Name: name, Raw: exprString(x)}}, wrappers...)
		}
		return groupRaw, wrappers, true
	default:
		return "", nil, false
	}
}
