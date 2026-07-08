// middleware.go 实现 middleware 绑定的原始表达式到 middleware 函数/方法符号的解析与写回。
package link

import (
	"go/ast"
	"go/parser"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// linkMiddlewareSymbols 遍历所有 middleware 绑定，把每条 MiddlewareRaw 表达式解析为符号并追加到 MiddlewareSymbols。
// middleware 表达式可能形如 "Auth"、"h1"、"Audit()"（函数调用结果作为 middleware），
// 也可能是 "provider.Default.Middleware"（跨包包级变量的方法，或经多层 struct field）。
// 解析依赖 astindex 的值类型解析能力，与 handler 共用同一条 selector 解析路径。
func linkMiddlewareSymbols(idx *astindex.Index, store *facts.Store) {
	if idx == nil || idx.Project == nil {
		return
	}
	for i := range store.Middleware {
		// 取地址写回 binding.MiddlewareSymbols，因此使用索引遍历。
		binding := &store.Middleware[i]
		// middleware 表达式记录在哪个文件里，需要定位回源文件以获取 import 表与包路径。
		file := fileByRelativePath(idx.Project, binding.Span.File)
		if file == nil {
			continue
		}
		// MiddlewareRaw 只是表达式文本片段，单条表达式可能不是合法的 Go 表达式
		// （例如含逗号的多 middleware 写法）。这里包一层 "[]any{...}" 把它当作切片字面量解析，
		// 既能复用 parser，又能一次性处理多个 middleware。
		expr, err := parser.ParseExpr("[]any{" + binding.MiddlewareRaw + "}")
		if err != nil {
			continue
		}
		// seen 用于对同一 binding 内重复出现的同一符号去重，避免一条 middleware 解析出多个相同符号。
		seen := map[facts.SymbolID]bool{}
		addSymbol := func(candidate ast.Expr) {
			if symbol, ok := resolveCallable(idx, file, candidate); ok && !seen[symbol] {
				seen[symbol] = true
				binding.MiddlewareSymbols = append(binding.MiddlewareSymbols, symbol)
			}
		}
		composite, ok := expr.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, middlewareExpr := range composite.Elts {
			// 先把整个元素表达式当作 middleware 候选，覆盖 "Auth"、"provider.Default.Middleware" 等直接引用。
			addSymbol(middlewareExpr)
			// 再递归扫描子节点，提取形如 "Audit()" 这种“调用结果作为 middleware”的真实目标函数（call.Fun），
			// 因为影响范围分析关心的是被调用的 middleware 函数本身，而不是其返回值。
			ast.Inspect(middlewareExpr, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if ok {
					addSymbol(call.Fun)
				}
				return true
			})
		}
	}
}

// resolveCallable 把单个 AST 表达式解析为可调用的符号 ID。
// 支持两种形态：
//   - *ast.Ident：当前包内的普通函数；
//   - *ast.SelectorExpr：x.Sel 形式，可能形如 pkg.Func、pkg.Var.Method 或本包 var.Method，
//     两段且首段为 import 别名时优先按包级函数解析，其余交给 astindex 走 selector/值类型链解析。
func resolveCallable(idx *astindex.Index, file *project.File, expr ast.Expr) (facts.SymbolID, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		// 当前包内裸名函数：func:<pkg>::<name>。
		id := astindex.FunctionSymbolID(file.Package.Path, x.Name)
		_, ok := idx.Symbols[id]
		return id, ok
	case *ast.SelectorExpr:
		// 把嵌套 selector 拍平成 ["pkg", "Var", ..., "Method"] 的段序列。
		parts := astindex.SelectorParts(x)
		if len(parts) == 2 {
			// 两段且首段是 import 别名：优先尝试目标包里的普通函数（如 pkg.Middleware）。
			if importPath := file.Imports[parts[0]]; importPath != "" {
				id := astindex.FunctionSymbolID(importPath, parts[1])
				if _, ok := idx.Symbols[id]; ok {
					return id, true
				}
			}
		}
		// 其余情况（本包变量方法、跨包变量方法、多层 field 链）交给 astindex 解析。
		return idx.ResolveSelectorMethod(file, parts)
	}
	return "", false
}
