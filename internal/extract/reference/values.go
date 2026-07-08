// values.go 实现函数体内 value 引用边的提取：解析 Ident 与 selector 表达式指向的
// 包级 var/const/func 值符号并写出 value 引用事实。
package reference

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// extractValueReferences 遍历函数体，提取其中的 value 引用边。
// 与初始化表达式不同，这里需要排除局部变量；调用位置的选择器走接收者解析路径。
func extractValueReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	// ignored 标记不应作为 value 引用的位置（组合字面量类型、键值对键）。
	ignored := ignoredValuePositions(fn.Body)
	// callFuns 标记作为被调函数的选择器位置，需走接收者解析路径。
	callFuns := callFunPositions(fn.Body)
	resolver := newResolver(file, idx, scopedValueTypes{})

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.SelectorExpr:
			if ignored[x.Pos()] {
				return false
			}
			var targets []facts.SymbolID
			if callFuns[x.Pos()] {
				targets = resolver.ResolveReceiverValueIDs(x)
			} else {
				targets = resolver.ResolveValueIDs(x)
			}
			addValueReferenceFacts(p, file, store, from, x, targets)
			// 选择器整体解析完毕，不再下钻以避免重复解析根 Ident。
			return false
		case *ast.Ident:
			// 跳过被忽略位置、调用函数位置以及局部变量。
			if ignored[x.Pos()] || callFuns[x.Pos()] || isLocalIdentifier(idx, x) {
				return true
			}
			addValueReferenceFacts(p, file, store, from, x, resolver.ResolveValueIDs(x))
		}
		return true
	})
}

// ResolveValueIDs 将一个值表达式解析为目标 value 符号列表。
// 处理 Ident、导入包 value 选择器、本包局部变量上的方法调用以及 pkg.var.Method 三段选择器。
func (r resolver) ResolveValueIDs(expr ast.Expr) []facts.SymbolID {
	switch x := expr.(type) {
	case *ast.Ident:
		if id, ok := r.idx.PackageValueSymbol(x.Obj); ok {
			return []facts.SymbolID{id}
		}
		if isLocalIdentifier(r.idx, x) {
			// 局部变量不属于 value 引用边目标。
			return nil
		}
		return existingValueIDs(r.idx, r.file.Package.Path, x.Name)
	case *ast.SelectorExpr:
		parts := astindex.SelectorParts(x)
		if len(parts) == 2 {
			// pkg.Name：导入包级 value。
			if importPath := r.file.Imports[parts[0]]; importPath != "" {
				return existingValueIDs(r.idx, importPath, parts[1])
			}
			// 根部是局部变量：尝试在该变量类型上解析方法作为 value 引用。
			if isLocalIdentifier(r.idx, selectorRootIdent(x)) {
				return nil
			}
			return r.resolveLocalVarMethod(parts)
		}
		if len(parts) >= 3 {
			// pkg.var.Method：包级变量上的方法调用。
			importPath := r.file.Imports[parts[0]]
			if importPath == "" {
				return nil
			}
			varID := astindex.ValueSymbolID("var", importPath, parts[1])
			out := existingIDs(r.idx, varID)
			if methodID, ok := r.idx.ResolveSelectorMethod(r.file, parts); ok {
				out = appendExistingID(out, r.idx, methodID)
			}
			return out
		}
	}
	return nil
}

// isLocalIdentifier 判断 Ident 是否为函数内局部变量或常量（排除包级 value）。
func isLocalIdentifier(idx *astindex.Index, ident *ast.Ident) bool {
	if ident == nil || ident.Obj == nil {
		return false
	}
	if _, ok := idx.PackageValueSymbol(ident.Obj); ok {
		return false
	}
	return ident.Obj.Kind == ast.Var || ident.Obj.Kind == ast.Con
}

// ResolveReceiverValueIDs 解析调用接收者选择器的 value 目标，仅取变量本身而非方法。
// 例如 pkg.var.Method 解析为 pkg.var；本包 var.Method 解析为 var。
func (r resolver) ResolveReceiverValueIDs(selector *ast.SelectorExpr) []facts.SymbolID {
	parts := astindex.SelectorParts(selector)
	if len(parts) == 3 {
		if importPath := r.file.Imports[parts[0]]; importPath != "" {
			return existingIDs(r.idx, astindex.ValueSymbolID("var", importPath, parts[1]))
		}
	}
	if len(parts) == 2 {
		return existingIDs(r.idx, astindex.ValueSymbolID("var", r.file.Package.Path, parts[0]))
	}
	return nil
}

// existingValueIDs 返回给定包与名字下存在的 var/const/func 符号候选。
func existingValueIDs(idx *astindex.Index, pkgPath, name string) []facts.SymbolID {
	var out []facts.SymbolID
	out = appendExistingID(out, idx, astindex.ValueSymbolID("var", pkgPath, name))
	out = appendExistingID(out, idx, astindex.ValueSymbolID("const", pkgPath, name))
	out = appendExistingID(out, idx, astindex.FunctionSymbolID(pkgPath, name))
	return out
}

// resolveLocalVarMethod 解析本包同名局部变量上的方法调用：返回变量符号与方法符号候选。
func (r resolver) resolveLocalVarMethod(parts []string) []facts.SymbolID {
	if len(parts) < 2 {
		return nil
	}
	varID := astindex.ValueSymbolID("var", r.file.Package.Path, parts[0])
	var out []facts.SymbolID
	out = appendExistingID(out, r.idx, varID)
	if methodID, ok := r.idx.ResolveSelectorMethod(r.file, parts); ok {
		out = appendExistingID(out, r.idx, methodID)
	}
	return out
}

// existingIDs 过滤出在索引中真实存在的符号，保持入参顺序。
func existingIDs(idx *astindex.Index, ids ...facts.SymbolID) []facts.SymbolID {
	var out []facts.SymbolID
	for _, id := range ids {
		out = appendExistingID(out, idx, id)
	}
	return out
}

// appendExistingID 将存在的符号去重追加到 out；不存在或已存在则跳过。
func appendExistingID(out []facts.SymbolID, idx *astindex.Index, id facts.SymbolID) []facts.SymbolID {
	if _, ok := idx.Symbols[id]; !ok {
		return out
	}
	for _, existing := range out {
		if existing == id {
			return out
		}
	}
	return append(out, id)
}

// addValueReferenceFacts 为每个目标符号写出一条 value 引用事实，跳过自引用与空目标。
func addValueReferenceFacts(p *project.Project, file *project.File, store *facts.Store, from facts.SymbolID, expr ast.Expr, targets []facts.SymbolID) {
	for _, target := range targets {
		if target == "" || target == from {
			continue
		}
		span := spanFor(p, file, expr.Pos(), expr.End())
		raw := typeExprString(file, expr)
		store.References = append(store.References, facts.ReferenceFact{
			ID:         referenceID(from, target, facts.ReferenceKindValue, span),
			Kind:       facts.ReferenceKindValue,
			FromSymbol: from,
			ToSymbol:   target,
			ToRaw:      raw,
			Confidence: facts.ConfidenceHigh,
			Span:       span,
			Evidence: []facts.EvidenceFact{{
				Kind:       "value_expr",
				Raw:        raw,
				Span:       span,
				Confidence: facts.ConfidenceHigh,
			}},
		})
	}
}

// ignoredValuePositions 标记不应作为 value 引用处理的节点位置：
// 组合字面量的类型部分（含其全部子节点）与键值表达式中的字段键。
func ignoredValuePositions(root ast.Node) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CompositeLit:
			markExprPositions(out, x.Type)
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok {
				out[id.Pos()] = true
			}
		}
		return true
	})
	return out
}

// markExprPositions 将 expr 子树所有节点位置标记为忽略。
func markExprPositions(out map[token.Pos]bool, expr ast.Expr) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		if node != nil {
			out[node.Pos()] = true
		}
		return true
	})
}

// callFunPositions 收集所有作为调用函数表达式的位置，用于区分调用接收者解析路径。
func callFunPositions(root ast.Node) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(root, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			out[call.Fun.Pos()] = true
		}
		return true
	})
	return out
}
