package route

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

func exprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var b bytes.Buffer
	_ = printer.Fprint(&b, token.NewFileSet(), expr)
	return b.String()
}

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

func selectorParts(expr ast.Expr) []string {
	switch x := expr.(type) {
	case *ast.Ident:
		return []string{x.Name}
	case *ast.SelectorExpr:
		return append(selectorParts(x.X), x.Sel.Name)
	default:
		return nil
	}
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		parts := selectorParts(fn)
		return strings.Join(parts, ".")
	default:
		return ""
	}
}

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
