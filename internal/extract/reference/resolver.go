// resolver.go 实现 reference 包的解析边界：把 selector/call/value 表达式解析为项目内目标符号，
// 并对未解析的项目内调用给出接口绑定诊断与 value/method 候选解析。
package reference

import (
	"fmt"
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// resolver 是单文件解析上下文，绑定当前文件、索引以及该函数/初始化式的局部作用域类型推断。
type resolver struct {
	file        *project.File
	idx         *astindex.Index
	scopedTypes scopedValueTypes
}

// newResolver 构造一个绑定到指定文件、索引与作用域推断的解析器。
func newResolver(file *project.File, idx *astindex.Index, scopedTypes scopedValueTypes) resolver {
	return resolver{
		file:        file,
		idx:         idx,
		scopedTypes: scopedTypes,
	}
}

// UnresolvedProjectCallDiagnostic 判断 expr 是否属于"项目内部但未能解析"的调用，
// 若是则返回对应的诊断码与信息；否则返回 ok=false。
// 它优先尝试给出更具体的接口绑定诊断，再回退到通用的未解析诊断。
func (r resolver) UnresolvedProjectCallDiagnostic(expr ast.Expr) (diagnostics.Code, string, bool) {
	if !r.isUnresolvedProjectCall(expr) {
		return "", "", false
	}
	raw := typeExprString(r.file, expr)
	if code, message, ok := r.interfaceBindingDiagnostic(expr, raw); ok {
		return code, message, true
	}
	return diagnostics.CodeSymbolReferenceUnresolved,
		fmt.Sprintf("project symbol reference %q could not be resolved", raw),
		true
}

// interfaceBindingDiagnostic 针对"通过接口变量调用方法"的情形给出更精确的诊断：
// 当变量存在未知的具体赋值或多于一个具体类型时，分别返回 unknown/ambiguous 诊断。
func (r resolver) interfaceBindingDiagnostic(expr ast.Expr, raw string) (diagnostics.Code, string, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	parts := astindex.SelectorParts(selector)
	if len(parts) < 2 {
		return "", "", false
	}
	// 默认认为变量属于当前包；若首段是导入别名则改用导入包路径。
	packagePath := r.file.Package.Path
	varName := parts[0]
	if importPath := r.file.Imports[parts[0]]; importPath != "" {
		if len(parts) < 3 {
			return "", "", false
		}
		packagePath = importPath
		varName = parts[1]
	}
	if !r.idx.IsProjectPackage(packagePath) {
		// 非项目包的接口变量不在此诊断范围内。
		return "", "", false
	}
	valueID := astindex.ValueSymbolID("var", packagePath, varName)
	binding := r.idx.InterfaceBindings[valueID]
	if binding == nil {
		return "", "", false
	}
	if binding.HasUnknownBinding || len(binding.ConcreteTypes) == 0 {
		return diagnostics.CodeSymbolReferenceUnknownInterfaceBinding,
			fmt.Sprintf("project interface variable %q has unknown concrete assignments; method reference %q could not be resolved", valueID, raw),
			true
	}
	if len(binding.ConcreteTypes) > 1 {
		return diagnostics.CodeSymbolReferenceAmbiguousInterface,
			fmt.Sprintf("project interface variable %q has %d concrete assignments; method reference %q is ambiguous", valueID, len(binding.ConcreteTypes), raw),
			true
	}
	return "", "", false
}

// isUnresolvedProjectCall 判断 selector 表达式是否指向项目内部接收者却无法静态解析。
// 优先用局部作用域推断接收者类型，其次回退到索引的包级 selector 接收者解析。
func (r resolver) isUnresolvedProjectCall(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	parts := astindex.SelectorParts(selector)
	if len(parts) < 2 {
		return false
	}
	importPath := r.file.Imports[parts[0]]
	if !r.idx.IsProjectPackage(importPath) {
		return false
	}
	if receiverType, ok := r.scopedTypes.resolve(selectorRootIdent(selector), selector.Pos()); ok {
		return r.idx.IsProjectPackage(receiverType.PackagePath)
	}
	if receiverType, ok := r.idx.ResolveSelectorReceiverType(r.file, parts); ok {
		return r.idx.IsProjectPackage(receiverType.PackagePath)
	}
	return true
}
