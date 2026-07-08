// types.go 实现类型引用边的提取：递归收集类型表达式中的项目内类型符号，
// 写出 type 引用事实，并对无法解析的项目内类型选择器上报诊断。
package reference

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// resolvedType 表示一个解析命中的类型符号，同时保留对应的 AST 表达式以便计算源码跨度。
type resolvedType struct {
	ID   facts.SymbolID
	Expr ast.Expr
}

// collectTypeIDs 递归遍历类型表达式，返回其中能命中索引的项目内类型符号。
// 覆盖指针/数组/map/chan/可变参数/泛型实参/结构体/接口/函数签名等组合形式。
func collectTypeIDs(file *project.File, idx *astindex.Index, expr ast.Expr) []resolvedType {
	if expr == nil {
		return nil
	}
	switch x := expr.(type) {
	case *ast.Ident:
		id := astindex.TypeSymbolID(file.Package.Path, x.Name)
		if _, ok := idx.Symbols[id]; ok {
			return []resolvedType{{ID: id, Expr: x}}
		}
	case *ast.SelectorExpr:
		// pkg.Type 形式：通过导入别名定位包路径后再查类型符号。
		if pkg, ok := x.X.(*ast.Ident); ok {
			if importPath := file.Imports[pkg.Name]; importPath != "" {
				id := astindex.TypeSymbolID(importPath, x.Sel.Name)
				if _, ok := idx.Symbols[id]; ok {
					return []resolvedType{{ID: id, Expr: x}}
				}
			}
		}
	case *ast.StarExpr:
		return collectTypeIDs(file, idx, x.X)
	case *ast.ArrayType:
		return collectTypeIDs(file, idx, x.Elt)
	case *ast.MapType:
		return mergeTypeIDs(
			collectTypeIDs(file, idx, x.Key),
			collectTypeIDs(file, idx, x.Value),
		)
	case *ast.ChanType:
		return collectTypeIDs(file, idx, x.Value)
	case *ast.Ellipsis:
		return collectTypeIDs(file, idx, x.Elt)
	case *ast.IndexExpr:
		// 单实参泛型：基础类型与类型实参都可能引入项目类型引用。
		return mergeTypeIDs(
			collectTypeIDs(file, idx, x.X),
			collectTypeIDs(file, idx, x.Index),
		)
	case *ast.IndexListExpr:
		// 多实参泛型：基础类型与每个类型实参合并。
		out := collectTypeIDs(file, idx, x.X)
		for _, item := range x.Indices {
			out = mergeTypeIDs(out, collectTypeIDs(file, idx, item))
		}
		return out
	case *ast.StructType:
		return collectFieldListTypeIDs(file, idx, x.Fields)
	case *ast.InterfaceType:
		return collectFieldListTypeIDs(file, idx, x.Methods)
	case *ast.FuncType:
		// 函数类型签名：类型参数、参数、返回值三者合并。
		return mergeTypeIDs(
			collectFieldListTypeIDs(file, idx, x.TypeParams),
			collectFieldListTypeIDs(file, idx, x.Params),
			collectFieldListTypeIDs(file, idx, x.Results),
		)
	case *ast.ParenExpr:
		return collectTypeIDs(file, idx, x.X)
	}
	return nil
}

// collectFieldListTypeIDs 收集 FieldList 中每个字段类型所引入的类型符号。
func collectFieldListTypeIDs(file *project.File, idx *astindex.Index, fields *ast.FieldList) []resolvedType {
	if fields == nil {
		return nil
	}
	var out []resolvedType
	for _, field := range fields.List {
		out = mergeTypeIDs(out, collectTypeIDs(file, idx, field.Type))
	}
	return out
}

// mergeTypeIDs 将多组解析结果合并去重，保持首次出现的顺序。
func mergeTypeIDs(groups ...[]resolvedType) []resolvedType {
	var out []resolvedType
	seen := map[facts.SymbolID]bool{}
	for _, group := range groups {
		for _, item := range group {
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			out = append(out, item)
		}
	}
	return out
}

// addTypeReferences 对类型表达式提取 type 引用边并写出事实；
// 同时对落在项目包内却无法解析的类型选择器上报 type_reference_unresolved 诊断。
func addTypeReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, expr ast.Expr) {
	for _, resolved := range collectTypeIDs(file, idx, expr) {
		// 跳过指向自身的类型（如递归类型定义）。
		if resolved.ID == from {
			continue
		}
		span := spanFor(p, file, resolved.Expr.Pos(), resolved.Expr.End())
		store.References = append(store.References, facts.ReferenceFact{
			ID:         referenceID(from, resolved.ID, facts.ReferenceKindType, span),
			Kind:       facts.ReferenceKindType,
			FromSymbol: from,
			ToSymbol:   resolved.ID,
			ToRaw:      typeExprString(file, resolved.Expr),
			Confidence: facts.ConfidenceHigh,
			Span:       span,
			Evidence: []facts.EvidenceFact{{
				Kind:       "type_expr",
				Raw:        typeExprString(file, resolved.Expr),
				Span:       span,
				Confidence: facts.ConfidenceHigh,
			}},
		})
	}
	for _, unresolved := range unresolvedProjectTypes(file, idx, expr) {
		span := spanFor(p, file, unresolved.Pos(), unresolved.End())
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeTypeReferenceUnresolved,
			Severity:       diagnostics.SeverityWarning,
			Message:        fmt.Sprintf("project type reference %q could not be resolved", typeExprString(file, unresolved)),
			Span:           span,
			RelatedFactIDs: []string{string(from)},
		})
	}
}

// unresolvedProjectTypes 在类型表达式中查找指向项目包但未能解析的类型选择器，
// 返回这些选择器表达式以供诊断使用。
func unresolvedProjectTypes(file *project.File, idx *astindex.Index, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return nil
	}
	var out []ast.Expr
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath := file.Imports[pkg.Name]
		// 仅当导入路径属于本项目时才视为需要诊断的项目内引用。
		if !idx.IsProjectPackage(importPath) {
			return false
		}
		id := astindex.TypeSymbolID(importPath, selector.Sel.Name)
		if _, ok := idx.Symbols[id]; !ok {
			out = append(out, selector)
		}
		return false
	})
	return out
}

// typeExprString 使用 go/printer 将类型表达式还原为源码文本，供 ToRaw 与诊断信息使用。
func typeExprString(file *project.File, expr ast.Expr) string {
	var out bytes.Buffer
	if err := printer.Fprint(&out, file.FileSet, expr); err != nil {
		return ""
	}
	return out.String()
}
