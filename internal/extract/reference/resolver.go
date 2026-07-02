package reference

import (
	"fmt"
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type resolver struct {
	file        *project.File
	idx         *astindex.Index
	scopedTypes scopedValueTypes
}

func newResolver(file *project.File, idx *astindex.Index, scopedTypes scopedValueTypes) resolver {
	return resolver{
		file:        file,
		idx:         idx,
		scopedTypes: scopedTypes,
	}
}

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

func (r resolver) interfaceBindingDiagnostic(expr ast.Expr, raw string) (diagnostics.Code, string, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	parts := selectorParts(selector)
	if len(parts) < 2 {
		return "", "", false
	}
	packagePath := r.file.Package.Path
	varName := parts[0]
	if importPath := r.file.Imports[parts[0]]; importPath != "" {
		if len(parts) < 3 {
			return "", "", false
		}
		packagePath = importPath
		varName = parts[1]
	}
	if !isProjectPackage(r.idx.Project.ModulePath, packagePath) {
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

func (r resolver) isUnresolvedProjectCall(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	parts := selectorParts(selector)
	if len(parts) < 2 {
		return false
	}
	importPath := r.file.Imports[parts[0]]
	if !isProjectPackage(r.idx.Project.ModulePath, importPath) {
		return false
	}
	if receiverType, ok := r.scopedTypes.resolve(selectorRootIdent(selector), selector.Pos()); ok {
		return isProjectPackage(r.idx.Project.ModulePath, receiverType.PackagePath)
	}
	if receiverType, ok := r.idx.ResolveSelectorReceiverType(r.file, parts); ok {
		return isProjectPackage(r.idx.Project.ModulePath, receiverType.PackagePath)
	}
	return true
}

func isProjectPackage(modulePath, packagePath string) bool {
	return packagePath == modulePath || strings.HasPrefix(packagePath, modulePath+"/")
}
