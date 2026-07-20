// handler.go 实现 route handler 原始表达式（handler_raw）到 handler 符号的解析。
package link

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// ResolveHandlerSymbol 解析 route 的 handler 原始表达式并返回稳定符号 ID。ok 为 false 表示无法解析。
//
// 解析策略按 "." 分段数决定：
//
//   - 单段（如 "List"）：当前包内同名函数。
//   - 两段（如 "controller.List" 或 "live_view.LiveViewRedirect"）：优先按 import 把首段映射为包路径，
//     尝试包级函数，再尝试包级 var；首段不是 import 名（如本包变量 HandlerAPI.List）时，退回 astindex 的 selector 方法解析。
//   - 三段及以上（如 "provider.Default.Middleware" 或更深的 struct field 链）：直接交给 astindex 复用值类型解析，
//     可跨包解析一层或多层已索引 struct field。
func ResolveHandlerSymbol(idx *astindex.Index, route facts.RouteRegistrationFact) (facts.SymbolID, bool) {
	if idx == nil || idx.Project == nil || route.HandlerRaw == "" {
		return "", false
	}
	// route 里只存了相对路径，需要先定位到对应的源文件，才能拿到所属包路径与 import 别名表。
	file := idx.FileByRelativePath(route.File)
	if file == nil {
		return "", false
	}
	// 用 "." 切分原始表达式，按段数选择不同解析路径。
	parts := strings.Split(route.HandlerRaw, ".")
	if len(parts) == 1 {
		// 单段：handler 是当前包里的普通函数（func:<pkg>::<name>）。
		id := astindex.FunctionSymbolID(file.Package.Path, parts[0])
		_, ok := idx.Symbols[id]
		return id, ok
	}
	// 两段及以上时，首段可能是 import 别名，查 import 表把别名还原成真实包路径。
	importPath := file.Imports[parts[0]]
	if len(parts) == 2 {
		if importPath != "" {
			// 两段且首段是 import 别名：优先尝试目标包里的普通函数。
			id := astindex.FunctionSymbolID(importPath, parts[1])
			if _, ok := idx.Symbols[id]; ok {
				return id, true
			}
			// 不是函数则尝试包级变量（var:<pkg>:<name>），如直接引用某个导出变量。
			id = astindex.ValueSymbolID("var", importPath, parts[1])
			_, ok := idx.Symbols[id]
			return id, ok
		}
		// 首段不是 import 别名：说明是当前包内的 selector 表达式（如 HandlerAPI.List），交给 astindex 解析。
		resolved, ok := idx.ResolveSelectorMethod(file, parts)
		return resolved.ID, ok
	}
	if len(parts) >= 3 {
		// 三段及以上：一定是 pkg.Var.Field...Method 形式，统一交给 astindex 走值类型链解析。
		if resolved, ok := idx.ResolveSelectorMethod(file, parts); ok {
			return resolved.ID, true
		}
		if receiver, ok := routeFunctionReceiverType(idx, route, parts[0]); ok {
			resolved, ok := idx.ResolveValueTypeMethod(receiver, parts[1:])
			return resolved.ID, ok
		}
	}
	return "", false
}

func routeFunctionReceiverType(idx *astindex.Index, route facts.RouteRegistrationFact, receiverName string) (astindex.ValueType, bool) {
	symbol, ok := idx.Symbols[route.RouteFunc]
	if !ok || symbol.Receiver == "" {
		return astindex.ValueType{}, false
	}
	file := idx.FileByRelativePath(route.File)
	if file == nil {
		return astindex.ValueType{}, false
	}
	for _, decl := range file.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != symbol.Name || fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
			continue
		}
		if fn.Recv.List[0].Names[0].Name != receiverName {
			return astindex.ValueType{}, false
		}
		return astindex.ValueType{PackagePath: symbol.PackagePath, TypeName: symbol.Receiver, Resolved: true}, true
	}
	return astindex.ValueType{}, false
}
