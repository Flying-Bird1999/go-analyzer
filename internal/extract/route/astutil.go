// astutil.go 实现 route 提取所需的 AST 辅助函数：表达式转字符串、
// 字符串字面量解析、选择器拆分、调用名提取、路径拼接。
package route

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
)

// exprString 将任意 AST 表达式格式化为源码字符串，用于事实记录中的原始文本。
func exprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var b bytes.Buffer
	_ = printer.Fprint(&b, token.NewFileSet(), expr)
	return b.String()
}

// stringLiteral 尝试把表达式解析为字符串字面量，返回去引号后的值。
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// callName 返回调用的全限定名（含包前缀的点分形式）；非标识符/选择器调用返回空串。
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		parts := astindex.SelectorParts(fn)
		return strings.Join(parts, ".")
	default:
		return ""
	}
}

// joinPath 按 URL 路径规则拼接前缀与子路径，保证以 "/" 开头并去除重复斜杠。
func joinPath(prefix, path string) string {
	if prefix == "" {
		prefix = "/"
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	out := strings.TrimRight(prefix, "/") + path
	if out == "" {
		return "/"
	}
	out = strings.ReplaceAll(out, "//", "/")
	if len(out) > 1 {
		out = strings.TrimRight(out, "/")
	}
	return out
}
