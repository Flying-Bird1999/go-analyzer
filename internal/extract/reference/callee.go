// callee.go 实现调用表达式到目标符号的解析：包内 Ident 调用、导入包函数选择器调用，
// 以及基于局部变量推断类型的方法分发。
package reference

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// ResolveCall 将一次调用表达式解析为候选目标符号列表，返回候选、原始文本与是否解析成功。
// 先剥去泛型实参，再按被调表达式是 Ident 还是 SelectorExpr 分别处理。
func (r resolver) ResolveCall(call *ast.CallExpr) ([]astindex.ResolvedSymbol, string, bool) {
	switch fun := unwrapGenericCallee(call.Fun).(type) {
	case *ast.Ident:
		// 1) 同包函数；2) 索引能识别的包级 value；3) 同包 var（函数值变量等）。
		id := astindex.FunctionSymbolID(r.file.Package.Path, fun.Name)
		if _, ok := r.idx.Symbols[id]; ok {
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, fun.Name, true
		}
		if id, ok := r.idx.PackageValueSymbol(fun.Obj); ok {
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, fun.Name, true
		}
		id = astindex.ValueSymbolID("var", r.file.Package.Path, fun.Name)
		if _, ok := r.idx.Symbols[id]; ok {
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, fun.Name, true
		}
		return nil, fun.Name, false
	case *ast.SelectorExpr:
		return r.resolveSelectorCandidates(fun)
	default:
		return nil, "", false
	}
}

// resolveSelectorCandidates 解析形如 pkg.Foo / recv.Method / pkg.var.Method 的选择器调用。
// 优先级：导入包函数 > 局部变量推断类型上的方法 > 索引的包级选择器方法解析。
func (r resolver) resolveSelectorCandidates(selector *ast.SelectorExpr) ([]astindex.ResolvedSymbol, string, bool) {
	parts := selectorParts(selector)
	raw := strings.Join(parts, ".")
	if len(parts) == 2 {
		// 形如 pkg.Func：当首段是导入别名且对应函数存在时直接命中。
		if importPath := r.file.Imports[parts[0]]; importPath != "" {
			id := astindex.FunctionSymbolID(importPath, parts[1])
			_, ok := r.idx.Symbols[id]
			if !ok {
				return nil, raw, false
			}
			return []astindex.ResolvedSymbol{{ID: id, Confidence: facts.ConfidenceHigh}}, raw, true
		}
	}
	if len(parts) >= 2 {
		// 当选择器根部能从局部作用域推断出类型时，按方法分发；多候选时枚举所有类型。
		if valueTypes, ok := r.scopedTypes.resolveAll(selectorRootIdent(selector), selector.Pos()); ok {
			if len(valueTypes) != 1 {
				return resolveValueTypeMethodCandidates(r.idx, valueTypes, parts[1:], raw)
			}
			valueType := valueTypes[0]
			if resolved, ok := r.idx.ResolveValueTypeMethod(valueType, parts[1:]); ok {
				return []astindex.ResolvedSymbol{resolved}, raw, true
			}
			return nil, raw, false
		}
	}
	// 回退路径：交由索引按包级 selector 解析（含接收者方法），并带上置信度。
	if resolved, ok := r.idx.ResolveSelectorMethodWithConfidence(r.file, parts); ok {
		return []astindex.ResolvedSymbol{resolved}, raw, true
	}
	return nil, raw, false
}

// selectorRootIdent 取选择器链最左边的 Ident，用于在局部作用域中查其推断类型。
func selectorRootIdent(selector *ast.SelectorExpr) *ast.Ident {
	var expr ast.Expr = selector
	for {
		switch current := expr.(type) {
		case *ast.SelectorExpr:
			expr = current.X
		case *ast.Ident:
			return current
		default:
			return nil
		}
	}
}

// resolveValueTypeMethodCandidates 针对多个候选类型枚举方法，去重后返回所有可解析的候选。
// 用于 map 索引分发的接口方法等存在多种具体类型的场景。
func resolveValueTypeMethodCandidates(idx *astindex.Index, valueTypes []astindex.ValueType, selectors []string, raw string) ([]astindex.ResolvedSymbol, string, bool) {
	if len(valueTypes) == 0 {
		return nil, raw, false
	}
	seen := map[facts.SymbolID]bool{}
	out := make([]astindex.ResolvedSymbol, 0, len(valueTypes))
	for _, valueType := range valueTypes {
		if valueType.TypeName == "" {
			continue
		}
		resolved, ok := idx.ResolveValueTypeMethod(valueType, selectors)
		if !ok {
			continue
		}
		if seen[resolved.ID] {
			continue
		}
		seen[resolved.ID] = true
		out = append(out, resolved)
	}
	return out, raw, len(out) > 0
}
